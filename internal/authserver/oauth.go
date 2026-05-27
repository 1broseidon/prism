package authserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/identity"
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
	// issuedGrants captures the RAR (Rich Authorization Request) grants
	// approved at /authorize time, frozen onto the auth code so the token
	// exchange can mint a token bound to the same set. Nil for plain
	// scope-only flows.
	issuedGrants []auth.IssuedGrant
	// authTime is the operator authentication time (Unix seconds) at code
	// issuance, propagated into the access token's auth_time claim.
	authTime int64
	// acr is the authentication context class reference captured at code
	// issuance, propagated to the access token for step-up enforcement.
	acr string
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
//
// For grant-bearing tokens, the issued grants, DPoP key thumbprint, and
// auth-time/acr ride along so the next refresh can mint a token whose
// claims match the original authorization without re-running /authorize.
type refreshToken struct {
	clientID string
	issuedAt time.Time
	grants   []auth.IssuedGrant
	dpopJKT  string
	authTime int64
	acr      string
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
	// sessions tracks step-up auth sessions issued by POST /stepup. Keyed
	// by the opaque cookie value; lookups read AuthTime / Acr to enforce
	// per-grant freshness and ACR requirements.
	sessions map[string]*authSession
	// stepupStates are single-use gating tokens minted by /authorize when
	// step-up is required and consumed by POST /stepup. Keyed by the
	// state token; values capture the return URL and creation time.
	stepupStates map[string]*stepUpState
}

func newOAuthStore() *oauthStore {
	return &oauthStore{
		codes:        make(map[string]*authCode),
		dynamics:     make(map[string]*dynamicClient),
		refresh:      make(map[string]*refreshToken),
		sessions:     make(map[string]*authSession),
		stepupStates: make(map[string]*stepUpState),
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
	// authorizationDetails is the raw JSON payload from the optional
	// authorization_details parameter (RFC 9396 / RAR). Parsed downstream
	// in validateAuthorizationDetails — kept as a string here so the
	// validator can produce the canonical OAuth error responses.
	authorizationDetails string
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
		clientID:             clientID,
		redirectURI:          redirectURI,
		state:                vals.Get("state"),
		codeChallenge:        codeChallenge,
		challengeMethod:      challengeMethod,
		authorizationDetails: vals.Get("authorization_details"),
	}
}

// issueAuthCodeWithGrants is the grant-bearing variant. Frozen grants and
// the operator session's auth_time/acr ride along on the auth code so the
// token exchange can mint a token whose claims match what was authorized.
func (s *Server) issueAuthCodeWithGrants(w http.ResponseWriter, r *http.Request, p *authorizeParams, grants []auth.IssuedGrant) {
	code, err := generateRandomString(32)
	if err != nil {
		s.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate code")
		return
	}

	var authTime int64
	var acr string
	if sess := s.authSessionFromRequest(r); sess != nil {
		authTime = sess.AuthTime
		acr = sess.Acr
	}
	if authTime == 0 {
		authTime = s.now().Unix()
	}

	s.oauth.mu.Lock()
	s.oauth.codes[code] = &authCode{
		code:         code,
		clientID:     p.clientID,
		redirectURI:  p.redirectURI,
		challenge:    p.codeChallenge,
		method:       p.challengeMethod,
		expiresAt:    time.Now().Add(10 * time.Minute),
		issuedGrants: grants,
		authTime:     authTime,
		acr:          acr,
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

	http.Redirect(w, r, redirectURL.String(), http.StatusFound) //nolint:gosec // G710: redirect_uri is validated against the client's registered RedirectURIs (exact match) in validateAuthorizeParams per OAuth 2.1 §4.1.3
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
	prismID := ""
	if hasDynamic {
		prismID = dc.PrismID
	}
	s.oauth.mu.Unlock()

	// Resolve RAR grants up front so both the consent render and the
	// auto-approve path observe the same set + step-up status.
	var grants []auth.IssuedGrant
	if p.authorizationDetails != "" {
		var ok bool
		grants, ok = s.validateAuthorizationDetails(w, r, p, prismID)
		if !ok {
			return
		}
		if s.needsStepUp(r, grants) {
			s.redirectStepUp(w, r)
			return
		}
	}

	if hasDynamic && prismID != "" && len(grants) == 0 {
		// Already consented and plain scope-only flow — auto-approve.
		s.issueAuthCodeWithGrants(w, r, p, grants)
		return
	}

	// Either first-time consent or a fresh capability-grant approval.
	clientName := ""
	if hasDynamic {
		clientName = dc.ClientName
	}
	if clientName == "" {
		clientName = p.clientID
	}

	data := &consentData{
		ClientName:           clientName,
		ClientID:             p.clientID,
		ResponseType:         "code",
		RedirectURI:          p.redirectURI,
		State:                p.state,
		CodeChallenge:        p.codeChallenge,
		ChallengeMethod:      p.challengeMethod,
		AuthorizationDetails: p.authorizationDetails,
	}
	if len(grants) > 0 {
		data.Grants = renderGrantSummaries(grants)
	}
	s.renderConsent(w, data)
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

	// Resolve existing PrismID or mint a new one.
	s.oauth.mu.Lock()
	dc, hasDynamic := s.oauth.dynamics[p.clientID]
	var prismID string
	if hasDynamic {
		prismID = dc.PrismID
	}
	s.oauth.mu.Unlock()

	if prismID == "" {
		next, err := generateAgentID()
		if err != nil {
			s.writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate agent ID")
			return
		}
		s.oauth.mu.Lock()
		dc, hasDynamic = s.oauth.dynamics[p.clientID]
		if hasDynamic {
			if dc.PrismID == "" {
				dc.PrismID = next
				dc.Label = label
				prismID = next
			} else {
				prismID = dc.PrismID
			}
		}
		s.oauth.mu.Unlock()
		s.updateClientIdentity(p.clientID, prismID, label)
		// Register the freshly-minted prism_id in the identity dispatcher so
		// the rename UX (task-51) and URL-compat layer (task-50) treat agents
		// the same as groups/roles/backends. AllocateWithID preserves the
		// prism_id as the canonical ID and stores label as display_name.
		// Existing UUID-shaped prism_ids that already pre-date this code path
		// are registered on demand by GetAgentByPrismID / the admin listing.
		if s.identityDispatcher != nil {
			if _, err := s.identityDispatcher.AllocateWithID(identity.KindAgent, prismID, label); err != nil && !errors.Is(err, identity.ErrDisplayNameInUse) {
				s.logger.Warn("register agent identity failed",
					"prism_id", prismID,
					"label", label,
					"error", err,
				)
			}
		}
		s.logger.Info("agent consented",
			"client_id", p.clientID,
			"prism_id", prismID,
			"label", label,
		)
	}

	// Resolve and freeze RAR grants on the auth code so the token endpoint
	// can mint a grant-bearing token without re-running validation.
	var grants []auth.IssuedGrant
	if p.authorizationDetails != "" {
		var ok bool
		grants, ok = s.validateAuthorizationDetails(w, r, p, prismID)
		if !ok {
			return
		}
		if s.needsStepUp(r, grants) {
			s.redirectStepUp(w, r)
			return
		}
	}

	s.issueAuthCodeWithGrants(w, r, p, grants)
}

// generateAgentID mints a fresh prism_id in ULID format, matching the
// shape used for groups/roles/backends. Existing UUID-formatted prism_ids
// stay in place — they remain opaque + valid + audit-stable — and only
// new dynamic clients pick up the ULID format.
func generateAgentID() (string, error) {
	return identityMigrationNewULID()
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

	// Peek at the code without consuming it so a missing DPoP proof on a
	// cnf-required grant doesn't burn the one-shot code. The validator
	// itself consumes the code once we know the request is well-formed.
	preview := s.peekAuthCode(code)
	requiresDPoP := preview != nil && grantsRequireCnf(preview.issuedGrants)

	// DPoP proof validation up-front. Nonce-mismatch must NOT consume the
	// code (the client needs another shot with the freshly-issued nonce);
	// any other proof failure is fatal.
	dpopJKT, dpopPresent, dpopErr := s.validateTokenDPoP(r)
	if dpopErr != nil {
		if errors.Is(dpopErr, errUseDPoPNonce) {
			s.setDPoPNonceHeader(w, s.now())
			s.writeOAuthError(w, http.StatusBadRequest, "use_dpop_nonce", "include a fresh DPoP nonce and retry")
			return
		}
		s.writeOAuthError(w, http.StatusUnauthorized, "invalid_dpop_proof", dpopErr.Error())
		return
	}
	if requiresDPoP && !dpopPresent {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "DPoP proof required for cnf-bound grants")
		return
	}

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
	s.issueTokenWithGrants(w, client, prismID, ac, dpopJKT)
}

// peekAuthCode returns the in-memory authCode without consuming it. Used
// to drive DPoP-presence checks on grants without burning the code.
func (s *Server) peekAuthCode(code string) *authCode {
	if code == "" {
		return nil
	}
	s.oauth.mu.Lock()
	defer s.oauth.mu.Unlock()
	return s.oauth.codes[code]
}

// consumeRefreshToken atomically removes and returns the in-memory entry
// for rtHash and removes the persisted record. Returns nil when the
// token is not present (single-use semantics).
func (s *Server) consumeRefreshToken(rtValue, rtHash string) *refreshToken {
	s.oauth.mu.Lock()
	rt, ok := s.oauth.refresh[rtHash]
	if ok {
		delete(s.oauth.refresh, rtHash)
	}
	s.oauth.mu.Unlock()
	s.deleteRefreshToken(rtValue)
	if !ok {
		return nil
	}
	return rt
}

// handleRefreshToken exchanges a refresh_token for a new access_token + refresh_token.
func (s *Server) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	rtValue := r.FormValue("refresh_token") //nolint:gosec // body limited by caller
	if rtValue == "" {
		s.writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}

	// Peek at the refresh token first so a DPoP nonce mismatch can return
	// a 400 use_dpop_nonce without burning the single-use refresh token.
	rtHash := sha256Hash(rtValue)
	s.oauth.mu.Lock()
	preview := s.oauth.refresh[rtHash]
	s.oauth.mu.Unlock()
	if preview != nil && preview.dpopJKT != "" {
		jkt, present, err := s.validateTokenDPoP(r)
		if err != nil {
			if errors.Is(err, errUseDPoPNonce) {
				s.setDPoPNonceHeader(w, s.now())
				s.writeOAuthError(w, http.StatusBadRequest, "use_dpop_nonce", "include a fresh DPoP nonce and retry")
				return
			}
			// Burn the token on non-nonce DPoP failures — a forged proof or
			// stolen token should not get a retry slot.
			s.consumeRefreshToken(rtValue, rtHash)
			s.writeOAuthError(w, http.StatusUnauthorized, "invalid_grant", "DPoP proof invalid on refresh: "+err.Error())
			return
		}
		if !present || jkt != preview.dpopJKT {
			s.consumeRefreshToken(rtValue, rtHash)
			s.writeOAuthError(w, http.StatusUnauthorized, "invalid_grant", "refresh requires matching DPoP key")
			return
		}
	}

	rt := s.consumeRefreshToken(rtValue, rtHash)
	if rt == nil {
		s.writeOAuthError(w, http.StatusUnauthorized, "invalid_grant", "refresh token not found or already used")
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
	// Reconstitute the auth-code shape so the grant-bearing path mints a
	// token whose claims match what the original /authorize approved.
	if len(rt.grants) > 0 || rt.dpopJKT != "" {
		ac := &authCode{
			clientID:     rt.clientID,
			issuedGrants: rt.grants,
			authTime:     rt.authTime,
			acr:          rt.acr,
		}
		s.issueTokenWithGrants(w, client, prismID, ac, rt.dpopJKT)
		return
	}
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

// issueTokenWithGrants is the grant-bearing twin of issueTokenWithRefresh.
// When the auth code carries RAR grants, the access token is minted with
// authorization_details + cnf + auth_time + acr claims and the response
// shape switches to "token_type: DPoP" if a thumbprint was provided.
//
// The refresh token captures the same metadata so the next refresh can
// reproduce the binding without a fresh /authorize trip.
func (s *Server) issueTokenWithGrants(w http.ResponseWriter, client *ClientConfig, prismID string, ac *authCode, dpopJKT string) {
	tokenType := "Bearer"
	if dpopJKT != "" {
		tokenType = "DPoP"
	}

	if len(ac.issuedGrants) == 0 && dpopJKT == "" {
		// Pure scope-only path: keep the existing shape so legacy callers
		// continue to round-trip unchanged.
		s.issueTokenWithRefresh(w, client, prismID)
		return
	}

	token, err := s.mintTokenWithOptions(tokenIssueOptions{
		ClientID: client.ClientID,
		PrismID:  prismID,
		Scopes:   client.AllowedScopes,
		Grants:   ac.issuedGrants,
		AuthTime: ac.authTime,
		Acr:      ac.acr,
		DPoPJKT:  dpopJKT,
	})
	if err != nil {
		s.logger.Error("failed to mint grant token", "client_id", client.ClientID, "error", err)
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
	s.oauth.refresh[rtHash] = &refreshToken{
		clientID: client.ClientID,
		issuedAt: issuedAt,
		grants:   ac.issuedGrants,
		dpopJKT:  dpopJKT,
		authTime: ac.authTime,
		acr:      ac.acr,
	}
	s.oauth.mu.Unlock()
	s.persistRefreshToken(rt, client.ClientID, issuedAt)

	s.writeJSON(w, http.StatusOK, TokenResponse{
		AccessToken:  token,
		TokenType:    tokenType,
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
