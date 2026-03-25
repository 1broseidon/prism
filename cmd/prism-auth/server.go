package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// server is the prism-auth HTTP server.
// It handles token issuance, JWKS serving, and OAuth 2.1 discovery.
type server struct {
	cfg     *Config
	km      *KeyManager
	logger  *slog.Logger
	clients map[string]*ClientConfig // keyed by client_id
	scopes  []string                 // deduplicated union of all client scopes
}

// newServer constructs a server from the provided config and key manager.
func newServer(cfg *Config, km *KeyManager, logger *slog.Logger) *server {
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

	return &server{
		cfg:     cfg,
		km:      km,
		logger:  logger,
		clients: clientMap,
		scopes:  scopes,
	}
}

// routes returns an http.Handler with all prism-auth endpoints registered.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", s.handleToken)
	mux.HandleFunc("GET /.well-known/jwks.json", s.handleJWKS)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleDiscovery)
	mux.HandleFunc("GET /health", s.handleHealth)
	return mux
}

// --- Token endpoint ---

// tokenRequest holds the parsed fields from a client credentials grant request.
type tokenRequest struct {
	grantType    string
	clientID     string
	clientSecret string
	scopes       []string
}

// tokenResponse is the successful token endpoint JSON response (RFC 6749 §5.1).
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

// oauthError is an OAuth 2.1 error response (RFC 6749 §5.2).
type oauthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// handleToken implements POST /token (OAuth 2.1 client credentials grant, RFC 6749 §4.4).
func (s *server) handleToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // guard against oversized bodies
	if err := r.ParseForm(); err != nil {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "failed to parse form body")
		return
	}

	req, err := parseTokenRequest(r)
	if err != nil {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	if req.grantType != "client_credentials" {
		s.writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
			"only client_credentials is supported")
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

	s.writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   s.cfg.TokenTTLSeconds,
		Scope:       strings.Join(granted, " "),
	})
}

// parseTokenRequest extracts client credentials and grant parameters from the request.
// Supports both HTTP Basic authentication and form-body client_id/client_secret.
func parseTokenRequest(r *http.Request) (*tokenRequest, error) {
	req := &tokenRequest{
		grantType: r.FormValue("grant_type"),
	}

	// RFC 6749 §2.3.1: Basic auth takes precedence over form body.
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

// authenticateClient looks up a client by ID and performs a constant-time secret comparison.
// Returns nil, false if the client is unknown or the secret does not match.
func (s *server) authenticateClient(clientID, secret string) (*ClientConfig, bool) {
	client, ok := s.clients[clientID]
	if !ok {
		// Perform a dummy comparison to avoid timing oracle on client existence.
		_ = subtle.ConstantTimeCompare([]byte(secret), []byte(""))
		return nil, false
	}
	if subtle.ConstantTimeCompare([]byte(secret), []byte(client.ClientSecret)) != 1 {
		return nil, false
	}
	return client, true
}

// resolveScopes returns the scopes to embed in the token.
// If no scopes are requested, all of the client's allowed scopes are granted.
// If specific scopes are requested, each must be in the client's AllowedScopes list.
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

// mintToken signs a new JWT access token for the given client and scope list.
func (s *server) mintToken(clientID string, scopes []string) (string, error) {
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

// generateJTI returns a random 16-byte base64url-encoded token ID.
func generateJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate jti: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// --- JWKS endpoint ---

// handleJWKS serves GET /.well-known/jwks.json with the server's public signing key.
func (s *server) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(s.km.jwks)
}

// --- Discovery endpoint ---

// discoveryMeta is the OAuth 2.1 Authorization Server Metadata document (RFC 8414).
type discoveryMeta struct {
	Issuer                            string   `json:"issuer"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JWKsURI                           string   `json:"jwks_uri"`
	ScopesSupported                   []string `json:"scopes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
}

// handleDiscovery serves GET /.well-known/oauth-authorization-server per RFC 8414.
func (s *server) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	base := strings.TrimRight(s.cfg.Issuer, "/")
	meta := discoveryMeta{
		Issuer:                            s.cfg.Issuer,
		TokenEndpoint:                     base + "/token",
		JWKsURI:                           base + "/.well-known/jwks.json",
		ScopesSupported:                   s.scopes,
		ResponseTypesSupported:            []string{},
		GrantTypesSupported:               []string{"client_credentials"},
		TokenEndpointAuthMethodsSupported: []string{"client_secret_basic", "client_secret_post"},
	}
	s.writeJSON(w, http.StatusOK, meta)
}

// --- Health endpoint ---

// healthResponse is the response body for GET /health.
type healthResponse struct {
	Status  string `json:"status"`
	Clients int    `json:"clients"`
}

// handleHealth serves GET /health with a simple liveness check.
func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, healthResponse{
		Status:  "ok",
		Clients: len(s.clients),
	})
}

// --- Helpers ---

// writeJSON encodes v as JSON and writes it to w with the given status code.
func (s *server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("write JSON response", "error", err)
	}
}

// writeOAuthError writes an OAuth 2.1-compliant error response.
func (s *server) writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	s.writeJSON(w, status, oauthError{Error: code, ErrorDescription: desc})
}
