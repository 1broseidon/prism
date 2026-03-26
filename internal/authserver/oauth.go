package authserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// --- In-memory stores ---

// authCode is a pending authorization code.
type authCode struct {
	code        string
	clientID    string
	redirectURI string
	challenge   string
	method      string // "S256" or "plain"
	expiresAt   time.Time
}

// dynamicClient is a client registered via DCR.
type dynamicClient struct {
	ClientID              string
	ClientSecret          string
	RedirectURIs          []string
	ClientName            string
	Scopes                []string
	RegistrationToken     string
	RegistrationClientURI string
}

// refreshToken maps a refresh token string to the client_id it was issued for.
type refreshToken struct {
	clientID string
}

// oauthStore holds in-memory state for DCR, authorization codes, and refresh tokens.
type oauthStore struct {
	mu       sync.Mutex
	codes    map[string]*authCode      // keyed by code
	dynamics map[string]*dynamicClient // keyed by client_id
	refresh  map[string]*refreshToken  // keyed by refresh token string
}

func newOAuthStore() *oauthStore {
	return &oauthStore{
		codes:    make(map[string]*authCode),
		dynamics: make(map[string]*dynamicClient),
		refresh:  make(map[string]*refreshToken),
	}
}

// --- DCR endpoint (RFC 7591) ---

type dcrRequest struct {
	ClientName   string   `json:"client_name,omitempty"`
	RedirectURIs []string `json:"redirect_uris,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	GrantTypes   []string `json:"grant_types,omitempty"`
	ClientURI    string   `json:"client_uri,omitempty"`
}

type dcrResponse struct {
	ClientID              string   `json:"client_id"`
	ClientSecret          string   `json:"client_secret"`
	ClientName            string   `json:"client_name,omitempty"`
	RedirectURIs          []string `json:"redirect_uris"`
	GrantTypes            []string `json:"grant_types"`
	ResponseTypes         []string `json:"response_types"`
	TokenEndpointAuth     string   `json:"token_endpoint_auth_method"`
	RegistrationToken     string   `json:"registration_access_token"`
	RegistrationClientURI string   `json:"registration_client_uri"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var req dcrRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "failed to parse JSON body")
		return
	}

	clientID, err := generateRandomString(16)
	if err != nil {
		s.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate client_id")
		return
	}

	clientSecret, err := generateRandomString(32)
	if err != nil {
		s.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate client_secret")
		return
	}

	regToken, err := generateRandomString(32)
	if err != nil {
		s.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate registration_access_token")
		return
	}

	clientName := req.ClientName
	if clientName == "" {
		clientName = "dynamic-" + clientID[:8]
	}

	// DCR agents get the configured default scopes + mcp:connect.
	scopeSet := map[string]struct{}{"mcp:connect": {}}
	for _, ds := range s.cfg.DefaultScopes {
		scopeSet[ds] = struct{}{}
	}
	scopes := make([]string, 0, len(scopeSet))
	for sc := range scopeSet {
		scopes = append(scopes, sc)
	}

	base := strings.TrimRight(s.cfg.Issuer, "/")
	regURI := base + "/register/" + clientID

	dc := &dynamicClient{
		ClientID:              clientID,
		ClientSecret:          clientSecret,
		RedirectURIs:          req.RedirectURIs,
		ClientName:            clientName,
		Scopes:                scopes,
		RegistrationToken:     regToken,
		RegistrationClientURI: regURI,
	}

	// Store dynamic client and also register it as a regular client for token issuance.
	s.oauth.mu.Lock()
	s.oauth.dynamics[clientID] = dc
	s.oauth.mu.Unlock()

	s.mu.Lock()
	s.clients[clientID] = &ClientConfig{
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		AllowedScopes: scopes,
		Description:   clientName,
	}
	s.mu.Unlock()

	s.logger.Info("dynamic client registered", "client_id", clientID, "name", clientName)

	// Ensure redirect_uris is never null in the response.
	redirectURIs := req.RedirectURIs
	if redirectURIs == nil {
		redirectURIs = []string{}
	}

	s.writeJSON(w, http.StatusCreated, dcrResponse{
		ClientID:              clientID,
		ClientSecret:          clientSecret,
		ClientName:            clientName,
		RedirectURIs:          redirectURIs,
		GrantTypes:            []string{"authorization_code"},
		ResponseTypes:         []string{"code"},
		TokenEndpointAuth:     "none",
		RegistrationToken:     regToken,
		RegistrationClientURI: regURI,
	})
}

// --- Authorization endpoint (OAuth 2.1 Authorization Code + PKCE) ---

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	responseType := q.Get("response_type")
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	codeChallenge := q.Get("code_challenge")
	challengeMethod := q.Get("code_challenge_method")
	// RFC 8707 resource parameter — acknowledged, not enforced yet.

	if responseType != "code" {
		s.writeOAuthError(w, http.StatusBadRequest, "unsupported_response_type",
			"only response_type=code is supported")
		return
	}

	if clientID == "" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id is required")
		return
	}

	// Look up client (supports both static and dynamic clients).
	s.mu.RLock()
	_, clientExists := s.clients[clientID]
	s.mu.RUnlock()
	if !clientExists {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "unknown client_id")
		return
	}

	if redirectURI == "" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is required")
		return
	}

	if codeChallenge == "" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code_challenge is required (PKCE)")
		return
	}
	if challengeMethod == "" {
		challengeMethod = "S256"
	}
	if challengeMethod != "S256" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"only code_challenge_method=S256 is supported")
		return
	}

	// Generate authorization code.
	code, err := generateRandomString(32)
	if err != nil {
		s.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate code")
		return
	}

	// Store the code with PKCE challenge for verification at token exchange.
	s.oauth.mu.Lock()
	s.oauth.codes[code] = &authCode{
		code:        code,
		clientID:    clientID,
		redirectURI: redirectURI,
		challenge:   codeChallenge,
		method:      challengeMethod,
		expiresAt:   time.Now().Add(10 * time.Minute),
	}
	s.oauth.mu.Unlock()

	s.logger.Info("authorization code issued", "client_id", clientID)

	// Auto-approve: redirect immediately with the authorization code.
	// In production, this could show a consent page.
	redirectURL, err := url.Parse(redirectURI)
	if err != nil {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid redirect_uri")
		return
	}

	rq := redirectURL.Query()
	rq.Set("code", code)
	if state != "" {
		rq.Set("state", state)
	}
	redirectURL.RawQuery = rq.Encode()

	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

// --- Authorization code exchange in token endpoint ---

// handleAuthCodeExchange handles grant_type=authorization_code with PKCE verification.
// Body size is already limited by handleToken's MaxBytesReader.
func (s *Server) handleAuthCodeExchange(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")                  //nolint:gosec // body limited by caller
	redirectURI := r.FormValue("redirect_uri")   //nolint:gosec // body limited by caller
	codeVerifier := r.FormValue("code_verifier") //nolint:gosec // body limited by caller
	clientID := r.FormValue("client_id")         //nolint:gosec // body limited by caller

	if code == "" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code is required")
		return
	}

	// Look up and consume the authorization code.
	s.oauth.mu.Lock()
	ac, ok := s.oauth.codes[code]
	if ok {
		delete(s.oauth.codes, code) // single use
	}
	s.oauth.mu.Unlock()

	if !ok {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code not found or already used")
		return
	}

	if time.Now().After(ac.expiresAt) {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code expired")
		return
	}

	// Verify client_id matches.
	if clientID != "" && clientID != ac.clientID {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id mismatch")
		return
	}

	// Verify redirect_uri matches.
	if redirectURI != "" && redirectURI != ac.redirectURI {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}

	// Verify PKCE code_verifier.
	if codeVerifier == "" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code_verifier is required (PKCE)")
		return
	}
	if !verifyPKCE(ac.challenge, ac.method, codeVerifier) {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	// Look up client to get allowed scopes.
	s.mu.RLock()
	client, clientOK := s.clients[ac.clientID]
	s.mu.RUnlock()
	if !clientOK {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client no longer exists")
		return
	}

	s.issueTokenWithRefresh(w, client)
}

// handleRefreshToken exchanges a refresh_token for a new access_token + refresh_token.
func (s *Server) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	rtValue := r.FormValue("refresh_token") //nolint:gosec // body limited by caller
	if rtValue == "" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}

	s.oauth.mu.Lock()
	rt, ok := s.oauth.refresh[rtValue]
	if ok {
		delete(s.oauth.refresh, rtValue) // single use — rotate on each refresh
	}
	s.oauth.mu.Unlock()

	if !ok {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token not found or already used")
		return
	}

	s.mu.RLock()
	client, clientOK := s.clients[rt.clientID]
	s.mu.RUnlock()
	if !clientOK {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client no longer exists")
		return
	}

	s.issueTokenWithRefresh(w, client)
}

// issueTokenWithRefresh mints an access_token + refresh_token for the given client.
func (s *Server) issueTokenWithRefresh(w http.ResponseWriter, client *ClientConfig) {
	token, err := s.mintToken(client.ClientID, client.AllowedScopes)
	if err != nil {
		s.logger.Error("failed to mint token", "client_id", client.ClientID, "error", err)
		s.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to issue token")
		return
	}

	rt, err := generateRandomString(32)
	if err != nil {
		s.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate refresh token")
		return
	}

	s.oauth.mu.Lock()
	s.oauth.refresh[rt] = &refreshToken{clientID: client.ClientID}
	s.oauth.mu.Unlock()

	s.writeJSON(w, http.StatusOK, TokenResponse{
		AccessToken:  token,
		TokenType:    "Bearer",
		ExpiresIn:    s.cfg.TokenTTLSeconds,
		Scope:        strings.Join(client.AllowedScopes, " "),
		RefreshToken: rt,
	})
}

// --- PKCE helpers ---

// verifyPKCE checks that code_verifier matches code_challenge using the specified method.
func verifyPKCE(challenge, method, verifier string) bool {
	if method != "S256" {
		return false
	}
	h := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])
	return computed == challenge
}

// --- Random string helper ---

func generateRandomString(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
