package authserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
	// Identity fields — populated at consent time, restored from KV store.
	PrismID    string // Stable UUID, generated at operator consent. Policy target.
	Label      string // Operator-assigned name from consent page.
	CreatedAt  string // RFC 3339, set at DCR registration.
	LastUsedAt string // RFC 3339, updated on refresh token exchange.
}

// refreshToken maps a refresh token string to the client_id it was issued
// for, plus when it was issued so we can age out stolen tokens.
type refreshToken struct {
	clientID string
	issuedAt time.Time
}

// refreshTokenMaxAge bounds the lifetime of a refresh token regardless of
// how many times it's rotated. After this, the client must re-authenticate
// via the agent consent flow. 30 days is conservative for home-lab use;
// shorter is safer if tokens get scraped from disk.
const refreshTokenMaxAge = 30 * 24 * time.Hour

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

	now := time.Now().UTC().Format(time.RFC3339)
	dc := &dynamicClient{
		ClientID:              clientID,
		ClientSecret:          clientSecret,
		RedirectURIs:          req.RedirectURIs,
		ClientName:            clientName,
		Scopes:                scopes,
		RegistrationToken:     regToken,
		RegistrationClientURI: regURI,
		CreatedAt:             now,
	}

	// Store dynamic client and also register it as a regular client for token issuance.
	s.oauth.mu.Lock()
	s.oauth.dynamics[clientID] = dc
	s.oauth.mu.Unlock()

	// Store the hashed secret — never plaintext.
	secretHash := sha256Hash(clientSecret)
	s.mu.Lock()
	s.clients[clientID] = &ClientConfig{
		ClientID:      clientID,
		ClientSecret:  secretHash,
		AllowedScopes: scopes,
		Description:   clientName,
	}
	s.mu.Unlock()

	// Persist to KV store.
	s.persistClient(clientID, clientSecret, clientName, scopes, req.RedirectURIs)

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

// authorizeParams holds validated OAuth authorize request parameters.
type authorizeParams struct {
	clientID        string
	redirectURI     string
	state           string
	codeChallenge   string
	challengeMethod string
}

// validateAuthorizeParams validates the OAuth authorization request parameters.
// Returns nil and writes an error response if validation fails.
func (s *Server) validateAuthorizeParams(w http.ResponseWriter, vals url.Values) *authorizeParams {
	responseType := vals.Get("response_type")
	if responseType != "code" {
		s.writeOAuthError(w, http.StatusBadRequest, "unsupported_response_type",
			"only response_type=code is supported")
		return nil
	}

	clientID := vals.Get("client_id")
	if clientID == "" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id is required")
		return nil
	}

	s.mu.RLock()
	_, clientExists := s.clients[clientID]
	s.mu.RUnlock()
	if !clientExists {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "unknown client_id")
		return nil
	}

	redirectURI := vals.Get("redirect_uri")
	if redirectURI == "" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is required")
		return nil
	}

	// OAuth 2.1 §4.1.3: redirect_uri MUST exactly match a registered URI.
	s.oauth.mu.Lock()
	dc, hasDC := s.oauth.dynamics[clientID]
	s.oauth.mu.Unlock()
	if hasDC && len(dc.RedirectURIs) > 0 {
		matched := false
		for _, allowed := range dc.RedirectURIs {
			if allowed == redirectURI {
				matched = true
				break
			}
		}
		if !matched {
			s.writeOAuthError(w, http.StatusBadRequest, "invalid_request",
				"redirect_uri does not match any registered redirect URI")
			return nil
		}
	}

	codeChallenge := vals.Get("code_challenge")
	if codeChallenge == "" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code_challenge is required (PKCE)")
		return nil
	}

	challengeMethod := vals.Get("code_challenge_method")
	if challengeMethod == "" {
		challengeMethod = "S256"
	}
	if challengeMethod != "S256" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"only code_challenge_method=S256 is supported")
		return nil
	}

	return &authorizeParams{
		clientID:        clientID,
		redirectURI:     redirectURI,
		state:           vals.Get("state"),
		codeChallenge:   codeChallenge,
		challengeMethod: challengeMethod,
	}
}

// issueAuthCode generates an authorization code, stores it, and redirects.
func (s *Server) issueAuthCode(w http.ResponseWriter, r *http.Request, p *authorizeParams) {
	code, err := generateRandomString(32)
	if err != nil {
		s.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate code")
		return
	}

	s.oauth.mu.Lock()
	s.oauth.codes[code] = &authCode{
		code:        code,
		clientID:    p.clientID,
		redirectURI: p.redirectURI,
		challenge:   p.codeChallenge,
		method:      p.challengeMethod,
		expiresAt:   time.Now().Add(10 * time.Minute),
	}
	s.oauth.mu.Unlock()

	s.logger.Info("authorization code issued", "client_id", p.clientID)

	redirectURL, err := url.Parse(p.redirectURI)
	if err != nil {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid redirect_uri")
		return
	}

	rq := redirectURL.Query()
	rq.Set("code", code)
	if p.state != "" {
		rq.Set("state", p.state)
	}
	redirectURL.RawQuery = rq.Encode()

	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

// handleAuthorize handles GET /authorize.
// If the agent has already been consented (has a PrismID), auto-approve.
// Otherwise, show the consent page for the operator to name the agent.
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	p := s.validateAuthorizeParams(w, r.URL.Query())
	if p == nil {
		return
	}

	// Check if this client already has consent (PrismID set).
	s.oauth.mu.Lock()
	dc, hasDynamic := s.oauth.dynamics[p.clientID]
	s.oauth.mu.Unlock()

	if hasDynamic && dc.PrismID != "" {
		// Already consented — auto-approve silently.
		s.issueAuthCode(w, r, p)
		return
	}

	// First time — show consent page.
	clientName := ""
	if hasDynamic {
		clientName = dc.ClientName
	}
	if clientName == "" {
		clientName = p.clientID
	}

	s.renderConsent(w, &consentData{
		ClientName:      clientName,
		ClientID:        p.clientID,
		ResponseType:    "code",
		RedirectURI:     p.redirectURI,
		State:           p.state,
		CodeChallenge:   p.codeChallenge,
		ChallengeMethod: p.challengeMethod,
	})
}

// handleAuthorizePost processes the consent form submission.
// Generates a PrismID (UUID), stores the operator label, then issues the auth code.
func (s *Server) handleAuthorizePost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := r.ParseForm(); err != nil {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "failed to parse form")
		return
	}

	// CSRF: double-submit cookie. Rejects cross-origin auto-submitted POSTs
	// because SameSite=Strict on the cookie means it isn't sent on those.
	if err := verifyConsentCSRF(r); err != nil {
		s.writeOAuthError(w, http.StatusForbidden, "invalid_request", "csrf check failed: "+err.Error())
		return
	}

	p := s.validateAuthorizeParams(w, r.Form)
	if p == nil {
		return
	}

	label := r.FormValue("label")
	if label == "" {
		label = p.clientID
	}

	// Generate Prism UUID for this agent.
	prismID, err := generateUUID()
	if err != nil {
		s.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate agent ID")
		return
	}

	// Update in-memory dynamic client.
	s.oauth.mu.Lock()
	dc, ok := s.oauth.dynamics[p.clientID]
	if ok {
		// Only set if not already consented (race protection).
		if dc.PrismID == "" {
			dc.PrismID = prismID
			dc.Label = label
		} else {
			prismID = dc.PrismID // use existing
		}
	}
	s.oauth.mu.Unlock()

	// Persist identity to KV store.
	s.updateClientIdentity(p.clientID, prismID, label)

	s.logger.Info("agent consented",
		"client_id", p.clientID,
		"prism_id", prismID,
		"label", label,
	)

	s.issueAuthCode(w, r, p)
}

// generateUUID generates a UUIDv4 string using crypto/rand.
func generateUUID() (string, error) {
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return "", err
	}
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16]), nil
}

// --- Authorization code exchange in token endpoint ---

// validateAuthCode consumes and validates an authorization code, returning the
// associated authCode record or an error string suitable for an OAuth error response.
func (s *Server) validateAuthCode(code, clientID, redirectURI, codeVerifier string) (ac *authCode, errCode, errDesc string) {
	if code == "" {
		return nil, "invalid_request", "code is required"
	}

	s.oauth.mu.Lock()
	ac, ok := s.oauth.codes[code]
	if ok {
		delete(s.oauth.codes, code) // single use
	}
	s.oauth.mu.Unlock()

	if !ok {
		return nil, "invalid_grant", "authorization code not found or already used"
	}
	if time.Now().After(ac.expiresAt) {
		return nil, "invalid_grant", "authorization code expired"
	}
	if clientID != "" && clientID != ac.clientID {
		return nil, "invalid_grant", "client_id mismatch"
	}
	if redirectURI != "" && redirectURI != ac.redirectURI {
		return nil, "invalid_grant", "redirect_uri mismatch"
	}
	if codeVerifier == "" {
		return nil, "invalid_request", "code_verifier is required (PKCE)"
	}
	if !verifyPKCE(ac.challenge, ac.method, codeVerifier) {
		return nil, "invalid_grant", "PKCE verification failed"
	}
	return ac, "", ""
}

// handleAuthCodeExchange handles grant_type=authorization_code with PKCE verification.
// Body size is already limited by handleToken's MaxBytesReader.
func (s *Server) handleAuthCodeExchange(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")                  //nolint:gosec // body limited by caller
	redirectURI := r.FormValue("redirect_uri")   //nolint:gosec // body limited by caller
	codeVerifier := r.FormValue("code_verifier") //nolint:gosec // body limited by caller
	clientID := r.FormValue("client_id")         //nolint:gosec // body limited by caller

	ac, errCode, errDesc := s.validateAuthCode(code, clientID, redirectURI, codeVerifier)
	if ac == nil {
		s.writeOAuthError(w, http.StatusBadRequest, errCode, errDesc)
		return
	}

	// Lock order: s.oauth.mu before s.mu.
	// Read PrismID from dynamic client first.
	var prismID string
	s.oauth.mu.Lock()
	dc, isDynamic := s.oauth.dynamics[ac.clientID]
	if isDynamic && dc != nil {
		prismID = dc.PrismID
	}
	s.oauth.mu.Unlock()

	// Look up client to get allowed scopes.
	s.mu.RLock()
	client, clientOK := s.clients[ac.clientID]
	s.mu.RUnlock()
	if !clientOK {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client no longer exists")
		return
	}

	// Re-resolve scopes for DCR agents with a PrismID.
	if isDynamic && prismID != "" {
		resolved := s.ResolveScopesByPrismID(prismID)
		client = &ClientConfig{
			ClientID:      client.ClientID,
			ClientSecret:  client.ClientSecret,
			AllowedScopes: resolved,
			Description:   client.Description,
		}
	}

	s.updateLastUsed(ac.clientID)
	s.issueTokenWithRefresh(w, client, prismID)
}

// handleRefreshToken exchanges a refresh_token for a new access_token + refresh_token.
func (s *Server) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	rtValue := r.FormValue("refresh_token") //nolint:gosec // body limited by caller
	if rtValue == "" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}

	rtHash := sha256Hash(rtValue)
	s.oauth.mu.Lock()
	rt, ok := s.oauth.refresh[rtHash]
	if ok {
		delete(s.oauth.refresh, rtHash) // single use — rotate on each refresh
	}
	s.oauth.mu.Unlock()

	// Remove from persistent store too.
	s.deleteRefreshToken(rtValue)

	if !ok {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token not found or already used")
		return
	}
	// Enforce absolute max age so a stolen + persisted token can't be
	// replayed indefinitely. Tokens loaded from KV without an issuedAt
	// (legacy entries) get zero-value, which trips this check — operators
	// reauth those once after upgrade.
	if rt.issuedAt.IsZero() || time.Since(rt.issuedAt) > refreshTokenMaxAge {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token expired; re-authenticate")
		return
	}

	// Lock order: s.oauth.mu before s.mu.
	var prismID string
	s.oauth.mu.Lock()
	dc, isDynamic := s.oauth.dynamics[rt.clientID]
	if isDynamic && dc != nil {
		prismID = dc.PrismID
	}
	s.oauth.mu.Unlock()

	s.mu.RLock()
	client, clientOK := s.clients[rt.clientID]
	s.mu.RUnlock()
	if !clientOK {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client no longer exists")
		return
	}

	// Re-resolve scopes for DCR agents with a PrismID.
	if isDynamic && prismID != "" {
		resolved := s.ResolveScopesByPrismID(prismID)
		client = &ClientConfig{
			ClientID:      client.ClientID,
			ClientSecret:  client.ClientSecret,
			AllowedScopes: resolved,
			Description:   client.Description,
		}
	}

	s.updateLastUsed(rt.clientID)
	s.issueTokenWithRefresh(w, client, prismID)
}

// issueTokenWithRefresh mints an access_token + refresh_token for the given client.
// prismID is optional — when present, it is included in the JWT for audit enrichment.
func (s *Server) issueTokenWithRefresh(w http.ResponseWriter, client *ClientConfig, prismID ...string) {
	var pid string
	if len(prismID) > 0 {
		pid = prismID[0]
	}

	token, err := s.mintToken(client.ClientID, client.AllowedScopes, pid)
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

	issuedAt := time.Now()
	rtHash := sha256Hash(rt)
	s.oauth.mu.Lock()
	s.oauth.refresh[rtHash] = &refreshToken{clientID: client.ClientID, issuedAt: issuedAt}
	s.oauth.mu.Unlock()

	// Persist refresh token (hashed) to KV store with its issued_at.
	s.persistRefreshToken(rt, client.ClientID, issuedAt)

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
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// --- Random string helper ---

func generateRandomString(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
