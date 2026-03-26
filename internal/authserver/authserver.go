// Package authserver implements a lightweight OAuth 2.1 authorization server
// purpose-built for the Prism MCP gateway.
//
// It can run as a standalone binary (cmd/prism-auth) or be embedded in the
// gateway process when the unified config format is used.
package authserver

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Config is the top-level prism-auth configuration.
type Config struct {
	// ListenAddr is the address to bind the HTTP server. Defaults to ":9100".
	ListenAddr string `json:"listen_addr"`

	// Issuer is the canonical URL of this authorization server.
	Issuer string `json:"issuer"`

	// SigningKey configures the RSA key used to sign JWTs.
	SigningKey SigningKeyConfig `json:"signing_key"`

	// Clients is the list of registered OAuth 2.1 clients (agent identities).
	Clients []ClientConfig `json:"clients"`

	// TokenTTLSeconds is the access token lifetime in seconds. Defaults to 3600.
	TokenTTLSeconds int `json:"token_ttl_seconds,omitempty"`

	// DefaultScopes are granted to dynamically registered clients (DCR).
	// Always includes mcp:connect. Empty means DCR agents can connect but see no tools.
	DefaultScopes []string `json:"-"`
}

// SigningKeyConfig specifies where to load the RSA signing key.
type SigningKeyConfig struct {
	// Path is the path to a PEM-encoded RSA private key file.
	// If empty, an ephemeral 2048-bit RSA key is generated on startup.
	Path string `json:"path,omitempty"`
}

// ClientConfig defines a registered OAuth 2.1 client (agent identity).
type ClientConfig struct {
	ClientID      string   `json:"client_id"`
	ClientSecret  string   `json:"client_secret"`
	AllowedScopes []string `json:"allowed_scopes"`
	Description   string   `json:"description,omitempty"`
}

// KeyManager holds the RSA signing key and its derived JWK metadata.
type KeyManager struct {
	privateKey *rsa.PrivateKey
	kid        string
	jwks       []byte
}

// JWK represents a JSON Web Key (RFC 7517) for an RSA public key.
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKSet is a JSON Web Key Set (RFC 7517 §5).
type JWKSet struct {
	Keys []JWK `json:"keys"`
}

// Server is the prism-auth HTTP server.
type Server struct {
	cfg     *Config
	km      *KeyManager
	logger  *slog.Logger
	mu      sync.RWMutex
	clients map[string]*ClientConfig
	scopes  []string
	oauth   *oauthStore
}

// --- Construction ---

// NewKeyManager constructs a KeyManager.
// If path is non-empty, the RSA private key is loaded from that PEM file.
// If path is empty, a 2048-bit ephemeral key is generated.
func NewKeyManager(path string) (*KeyManager, error) {
	var (
		privateKey *rsa.PrivateKey
		err        error
	)
	if path != "" {
		privateKey, err = loadRSAKey(path)
	} else {
		privateKey, err = generateRSAKey()
	}
	if err != nil {
		return nil, err
	}

	kid := computeKID(&privateKey.PublicKey)

	jwks, err := buildJWKS(&privateKey.PublicKey, kid)
	if err != nil {
		return nil, fmt.Errorf("build JWKS: %w", err)
	}

	return &KeyManager{
		privateKey: privateKey,
		kid:        kid,
		jwks:       jwks,
	}, nil
}

// JWKS returns the pre-serialized JWKS JSON for pre-seeding token validators.
func (km *KeyManager) JWKS() []byte { return km.jwks }

// NewServer constructs a Server from the provided config and key manager.
func NewServer(cfg *Config, km *KeyManager, logger *slog.Logger) *Server {
	clientMap := make(map[string]*ClientConfig, len(cfg.Clients))
	scopeSet := make(map[string]struct{})

	for i := range cfg.Clients {
		c := &cfg.Clients[i]
		clientMap[c.ClientID] = c
		for _, s := range c.AllowedScopes {
			scopeSet[s] = struct{}{}
		}
	}

	scopes := make([]string, 0, len(scopeSet))
	for s := range scopeSet {
		scopes = append(scopes, s)
	}

	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	return &Server{
		cfg:     cfg,
		km:      km,
		logger:  logger,
		clients: clientMap,
		scopes:  scopes,
		oauth:   newOAuthStore(),
	}
}

// Routes returns an http.Handler with all prism-auth endpoints registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", s.handleToken)
	mux.HandleFunc("GET /authorize", s.handleAuthorize)
	mux.HandleFunc("POST /register", s.handleRegister)
	mux.HandleFunc("GET /.well-known/jwks.json", s.handleJWKS)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleDiscovery)
	mux.HandleFunc("GET /health", s.handleHealth)
	return mux
}

// IsEphemeralKey reports whether the server is using an ephemeral signing key.
func (s *Server) IsEphemeralKey() bool {
	return s.cfg.SigningKey.Path == ""
}

// AgentInfo is a summary of an agent for the admin API.
type AgentInfo struct {
	ClientID    string   `json:"client_id"`
	Description string   `json:"description,omitempty"`
	Scopes      []string `json:"scopes"`
	Dynamic     bool     `json:"dynamic"`
}

// ListAgents returns info about all registered agents (static + DCR).
func (s *Server) ListAgents() []AgentInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agents := make([]AgentInfo, 0, len(s.clients))
	for _, c := range s.clients {
		isDynamic := false
		s.oauth.mu.Lock()
		_, isDynamic = s.oauth.dynamics[c.ClientID]
		s.oauth.mu.Unlock()

		agents = append(agents, AgentInfo{
			ClientID:    c.ClientID,
			Description: c.Description,
			Scopes:      c.AllowedScopes,
			Dynamic:     isDynamic,
		})
	}
	return agents
}

// UpdateAgentScopes replaces the allowed scopes for an agent.
// Returns false if the agent doesn't exist.
func (s *Server) UpdateAgentScopes(clientID string, scopes []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	client, ok := s.clients[clientID]
	if !ok {
		return false
	}
	client.AllowedScopes = scopes
	return true
}

// --- Token endpoint ---

// TokenResponse is the successful token endpoint JSON response (RFC 6749 §5.1).
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// OAuthError is an OAuth 2.1 error response (RFC 6749 §5.2).
type OAuthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type tokenRequest struct {
	grantType    string
	clientID     string
	clientSecret string
	scopes       []string
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := r.ParseForm(); err != nil {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "failed to parse form body")
		return
	}

	grantType := r.FormValue("grant_type")

	switch grantType {
	case "authorization_code":
		s.handleAuthCodeExchange(w, r)
	case "client_credentials":
		s.handleClientCredentials(w, r)
	case "refresh_token":
		s.handleRefreshToken(w, r)
	default:
		s.writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
			"supported: authorization_code, client_credentials, refresh_token")
	}
}

func (s *Server) handleClientCredentials(w http.ResponseWriter, r *http.Request) {
	req, err := parseTokenRequest(r)
	if err != nil {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	client, ok := s.authenticateClient(req.clientID, req.clientSecret)
	if !ok {
		s.writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "authentication failed")
		return
	}

	granted, err := resolveScopes(client, req.scopes)
	if err != nil {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}

	token, err := s.mintToken(client.ClientID, granted)
	if err != nil {
		s.logger.Error("failed to mint token", "client_id", client.ClientID, "error", err)
		s.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to issue token")
		return
	}

	s.writeJSON(w, http.StatusOK, TokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   s.cfg.TokenTTLSeconds,
		Scope:       strings.Join(granted, " "),
	})
}

func parseTokenRequest(r *http.Request) (*tokenRequest, error) {
	req := &tokenRequest{
		grantType: r.FormValue("grant_type"),
	}

	clientID, clientSecret, hasBasic := r.BasicAuth()
	if hasBasic {
		req.clientID = clientID
		req.clientSecret = clientSecret
	} else {
		req.clientID = r.FormValue("client_id")
		req.clientSecret = r.FormValue("client_secret")
	}

	if req.clientID == "" {
		return nil, fmt.Errorf("client_id is required")
	}
	if req.clientSecret == "" {
		return nil, fmt.Errorf("client_secret is required")
	}

	if scopeStr := r.FormValue("scope"); scopeStr != "" {
		req.scopes = strings.Fields(scopeStr)
	}

	return req, nil
}

func (s *Server) authenticateClient(clientID, secret string) (*ClientConfig, bool) {
	s.mu.RLock()
	client, ok := s.clients[clientID]
	s.mu.RUnlock()
	if !ok {
		_ = subtle.ConstantTimeCompare([]byte(secret), []byte(""))
		return nil, false
	}
	if subtle.ConstantTimeCompare([]byte(secret), []byte(client.ClientSecret)) != 1 {
		return nil, false
	}
	return client, true
}

func resolveScopes(client *ClientConfig, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return client.AllowedScopes, nil
	}

	allowed := make(map[string]struct{}, len(client.AllowedScopes))
	for _, s := range client.AllowedScopes {
		allowed[s] = struct{}{}
	}

	for _, req := range requested {
		if _, ok := allowed[req]; !ok {
			return nil, fmt.Errorf("scope %q is not allowed for this client", req)
		}
	}
	return requested, nil
}

func (s *Server) mintToken(clientID string, scopes []string) (string, error) {
	now := time.Now()

	jti, err := generateJTI()
	if err != nil {
		return "", err
	}

	claims := jwt.MapClaims{
		"iss":       s.cfg.Issuer,
		"sub":       clientID,
		"aud":       s.cfg.Issuer,
		"exp":       now.Add(time.Duration(s.cfg.TokenTTLSeconds) * time.Second).Unix(),
		"iat":       now.Unix(),
		"jti":       jti,
		"scope":     strings.Join(scopes, " "),
		"client_id": clientID,
	}

	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	t.Header["kid"] = s.km.kid

	return t.SignedString(s.km.privateKey)
}

func generateJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate jti: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// --- JWKS endpoint ---

func (s *Server) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(s.km.jwks)
}

// --- Discovery endpoint ---

// DiscoveryMeta is the OAuth 2.1 Authorization Server Metadata document (RFC 8414).
type DiscoveryMeta struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	JWKsURI                           string   `json:"jwks_uri"`
	ScopesSupported                   []string `json:"scopes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
}

func (s *Server) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	base := strings.TrimRight(s.cfg.Issuer, "/")
	meta := DiscoveryMeta{
		Issuer:                            s.cfg.Issuer,
		AuthorizationEndpoint:             base + "/authorize",
		TokenEndpoint:                     base + "/token",
		RegistrationEndpoint:              base + "/register",
		JWKsURI:                           base + "/.well-known/jwks.json",
		ScopesSupported:                   s.scopes,
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "client_credentials", "refresh_token"},
		TokenEndpointAuthMethodsSupported: []string{"client_secret_post", "none"},
		CodeChallengeMethodsSupported:     []string{"S256"},
	}
	s.writeJSON(w, http.StatusOK, meta)
}

// --- Health endpoint ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"clients": len(s.clients),
	})
}

// --- Config loading ---

// LoadConfig reads and parses the JSON config file at path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Config path is from CLI flag
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, ValidateConfig(&cfg)
}

// ValidateConfig applies defaults and validates required fields.
func ValidateConfig(cfg *Config) error {
	if cfg.Issuer == "" {
		return errors.New("issuer must be set")
	}
	// Zero clients is valid — agents self-register via DCR.
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":9100"
	}
	if cfg.TokenTTLSeconds == 0 {
		cfg.TokenTTLSeconds = 3600
	}
	return validateClients(cfg.Clients)
}

func validateClients(clients []ClientConfig) error {
	seen := make(map[string]struct{}, len(clients))
	for i, c := range clients {
		if c.ClientID == "" {
			return fmt.Errorf("client[%d]: client_id is required", i)
		}
		if c.ClientSecret == "" {
			return fmt.Errorf("client[%d] %q: client_secret is required", i, c.ClientID)
		}
		if _, dup := seen[c.ClientID]; dup {
			return fmt.Errorf("duplicate client_id: %q", c.ClientID)
		}
		seen[c.ClientID] = struct{}{}
	}
	return nil
}

// --- Key helpers ---

func loadRSAKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Key path is from config file
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("failed to decode PEM block from key file")
	}
	return parseRSAKeyBlock(block)
}

func parseRSAKeyBlock(block *pem.Block) (*rsa.PrivateKey, error) {
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("key file does not contain an RSA private key")
		}
		return rsaKey, nil
	}
	rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA key (tried PKCS#8 and PKCS#1): %w", err)
	}
	return rsaKey, nil
}

func generateRSAKey() (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate RSA key: %w", err)
	}
	return key, nil
}

func computeKID(pub *rsa.PublicKey) string {
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	canonical := fmt.Sprintf(`{"e":%q,"kty":"RSA","n":%q}`, e, n)
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func buildJWKS(pub *rsa.PublicKey, kid string) ([]byte, error) {
	jwk := JWK{
		Kty: "RSA",
		Kid: kid,
		Use: "sig",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
	return json.Marshal(JWKSet{Keys: []JWK{jwk}})
}

// --- JSON helpers ---

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("write JSON response", "error", err)
	}
}

func (s *Server) writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	s.writeJSON(w, status, OAuthError{Error: code, ErrorDescription: desc})
}
