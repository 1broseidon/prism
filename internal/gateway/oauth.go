//go:build mcp_go_client_oauth

package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/credentials"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// PendingAuthFlow tracks an in-progress OAuth authorization code flow.
type PendingAuthFlow struct {
	BackendID    string
	BackendName  string
	Config       *oauth2.Config
	CodeVerifier string
	State        string
	BackendURL   string
	ResourceURL  string
	CreatedAt    time.Time
}

// authFlowManager manages pending OAuth authorization flows.
type authFlowManager struct {
	mu          sync.Mutex
	flows       map[string]*PendingAuthFlow // keyed by state
	completed   map[string]string           // backendID -> "connected" | "failed:{reason}"
	callbackURL string
	logger      *slog.Logger
}

func newAuthFlowManager(callbackURL string, logger *slog.Logger) *authFlowManager {
	return &authFlowManager{
		flows:       make(map[string]*PendingAuthFlow),
		completed:   make(map[string]string),
		callbackURL: callbackURL,
		logger:      logger,
	}
}

// getAuthFlows returns the typed auth flow manager, or nil.
func (g *Gateway) getAuthFlows() *authFlowManager {
	if g.authFlows == nil {
		return nil
	}
	afm, _ := g.authFlows.(*authFlowManager)
	return afm
}

// generateState produces a cryptographic random state parameter.
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generateCodeVerifier produces a PKCE code verifier (RFC 7636).
func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate code verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// codeChallenge computes S256 code challenge from verifier.
func codeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ProbeBackendAuth probes a URL and initiates an OAuth flow if 401 is returned.
// Returns a PendingAuthFlow with the auth URL the operator should visit, or nil
// if the backend does not require OAuth authentication.
// ProbeAuthOptions carries per-request inputs to ProbeBackendAuth.
type ProbeAuthOptions struct {
	// CallbackOverride is the externally-reachable base URL the provider
	// should redirect to (e.g. http://172.16.30.90:9086). Empty falls back
	// to admin_public_url / localhost.
	CallbackOverride string
	// ManualClientID + Secret skip DCR. Required for providers without DCR
	// (GitHub, most IdPs without it enabled).
	ManualClientID     string
	ManualClientSecret string
}

// ProbeBackendAuth detects whether backendURL requires OAuth and, if so,
// kicks off DCR + auth flow. If DCR isn't supported by the auth server and
// no manual credentials are provided, returns an admin.DCRUnsupportedError
// so the UI can prompt the operator.
func (g *Gateway) ProbeBackendAuth(ctx context.Context, backendID, backendURL string, opts ProbeAuthOptions) (*PendingAuthFlow, error) {
	afm := g.getAuthFlows()
	if afm == nil {
		return nil, fmt.Errorf("OAuth flow manager not initialized")
	}

	// Probe the backend URL for 401.
	// MCP Streamable HTTP servers respond to POST, not GET. Some return 405 on
	// GET even if they require OAuth on POST. Send a minimal MCP initialize
	// request so the server's auth behavior is accurately detected.
	probeBody := strings.NewReader(`{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"prism-probe","version":"0.1.0"}},"id":1}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, backendURL, probeBody)
	if err != nil {
		return nil, fmt.Errorf("create probe request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("probe %s: %w", backendURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		// Not an OAuth-protected resource. Return nil to indicate normal flow.
		return nil, nil
	}

	// Parse WWW-Authenticate header for resource_metadata URL and scope.
	wwwAuth := resp.Header[http.CanonicalHeaderKey("WWW-Authenticate")]
	if len(wwwAuth) == 0 {
		return nil, fmt.Errorf("401 from %s but no WWW-Authenticate header", backendURL)
	}

	challenges, err := oauthex.ParseWWWAuthenticate(wwwAuth)
	if err != nil {
		return nil, fmt.Errorf("parse WWW-Authenticate: %w", err)
	}

	// Gap 3 (MCP auth spec §170-175): Extract scope from WWW-Authenticate challenge.
	// Prefer challenged scopes over scopes_supported from Protected Resource Metadata.
	var challengedScopes []string
	for _, c := range challenges {
		if s := c.Params["scope"]; s != "" {
			challengedScopes = strings.Fields(s)
			break
		}
	}

	// Find the resource_metadata URL from challenges.
	var metadataURL string
	for _, c := range challenges {
		if u := c.Params["resource_metadata"]; u != "" {
			metadataURL = u
			break
		}
	}
	if metadataURL == "" {
		return nil, fmt.Errorf("401 from %s: no resource_metadata in WWW-Authenticate", backendURL)
	}

	// Discover protected resource metadata (RFC 9728). The metadata resource
	// identifier is the value that must be used in RFC 8707 resource params.
	prm, err := getProtectedResourceMetadataForBackend(ctx, metadataURL, backendURL)
	if err != nil {
		return nil, fmt.Errorf("get protected resource metadata: %w", err)
	}

	if len(prm.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization servers in protected resource metadata for %s", backendURL)
	}

	// Discover auth server metadata (RFC 8414 + OIDC Discovery fallback).
	authServerIssuer := prm.AuthorizationServers[0]

	// Gap 1 (MCP auth spec §70-79): Try multiple well-known endpoints.
	// RFC 8414 first, then OIDC Discovery as fallback. The issuer value in
	// metadata must exactly match the authorization server identifier from
	// protected-resource metadata; accepting aliases here enables issuer
	// substitution during discovery.
	asm, discoveredURL, err := discoverAuthServerMeta(ctx, authServerIssuer, g.logger)
	if err != nil {
		return nil, fmt.Errorf("get auth server metadata: %w", err)
	}
	if asm == nil {
		return nil, fmt.Errorf("auth server at %s returned no metadata (tried RFC 8414 and OIDC Discovery)", authServerIssuer)
	}
	g.logger.Info("discovered auth server metadata",
		"backend", backendID,
		"url", discoveredURL,
	)

	// Gap 4 (MCP auth spec §87-155): Check for Client ID Metadata Document support.
	// This is a SHOULD requirement — detect and log, but fall back to DCR.
	if asm.ClientIDMetadataDocumentSupported {
		g.logger.Info("auth server supports Client ID Metadata Documents but Prism does not yet host one — falling back to DCR",
			"backend", backendID,
			"auth_server", authServerIssuer,
		)
	}

	// Resolve the redirect URI for this flow.
	// Precedence (high → low):
	//   1. Operator-pinned admin_public_url from the Settings page (runtime KV).
	//   2. Operator-pinned admin_public_url from the file config (static).
	//   3. Request-derived host (covers ad-hoc access).
	//   4. Fail — there's no sensible default once the operator runs prism
	//      from a non-loopback address.
	var callbackURL string
	switch {
	case g.network != nil && g.network.AdminCallbackURL() != "":
		callbackURL = g.network.AdminCallbackURL()
	case afm.callbackURL != "":
		callbackURL = afm.callbackURL
	case opts.CallbackOverride != "":
		callbackURL = strings.TrimRight(opts.CallbackOverride, "/") + "/oauth/callback"
	default:
		return nil, fmt.Errorf("no callback URL available: set admin_public_url in the Settings page or config file")
	}

	// Obtain client credentials: prefer manual when supplied, otherwise DCR.
	var clientID, clientSecret string
	switch {
	case opts.ManualClientID != "":
		clientID = opts.ManualClientID
		clientSecret = opts.ManualClientSecret
		g.logger.Info("using operator-supplied OAuth client credentials",
			"backend", backendID, "auth_server", authServerIssuer)
	case asm.RegistrationEndpoint == "":
		// Provider doesn't support DCR and operator didn't supply manual creds.
		// Surface a typed error so the admin handler can prompt for them.
		return nil, &admin.DCRUnsupportedError{
			AuthServer:  authServerIssuer,
			CallbackURL: callbackURL,
		}
	default:
		regResp, err := oauthex.RegisterClient(ctx, asm.RegistrationEndpoint, &oauthex.ClientRegistrationMetadata{
			ClientName:              "Prism Gateway",
			RedirectURIs:            []string{callbackURL},
			GrantTypes:              []string{"authorization_code"},
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: "none",
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("dynamic client registration: %w", err)
		}
		clientID = regResp.ClientID
		clientSecret = regResp.ClientSecret
	}

	// Gap 3 (MCP auth spec §170-175): Scope priority —
	// 1. scope from WWW-Authenticate challenge (parsed above)
	// 2. scopes_supported from Protected Resource Metadata
	scopes := challengedScopes
	if len(scopes) == 0 {
		scopes = prm.ScopesSupported
	}

	// Build the oauth2.Config.
	authStyle := oauth2.AuthStyleInParams
	if clientSecret != "" {
		// Confidential clients (manual creds with a real secret) typically use
		// HTTP Basic at the token endpoint. AuthStyleAutoDetect lets the lib
		// fall back if the server rejects it.
		authStyle = oauth2.AuthStyleAutoDetect
	}
	oauthCfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:   asm.AuthorizationEndpoint,
			TokenURL:  asm.TokenEndpoint,
			AuthStyle: authStyle,
		},
		RedirectURL: callbackURL,
		Scopes:      scopes,
	}

	// Generate PKCE and state.
	state, err := generateState()
	if err != nil {
		return nil, err
	}
	verifier, err := generateCodeVerifier()
	if err != nil {
		return nil, err
	}

	flow := &PendingAuthFlow{
		BackendID:    backendID,
		BackendName:  backendID,
		Config:       oauthCfg,
		CodeVerifier: verifier,
		State:        state,
		BackendURL:   backendURL,
		ResourceURL:  prm.Resource,
		CreatedAt:    time.Now(),
	}

	// Store the pending flow.
	afm.mu.Lock()
	afm.flows[state] = flow
	afm.mu.Unlock()

	g.logger.Info("initiated OAuth flow for backend",
		"backend", backendID,
		"auth_server", authServerIssuer,
		"callback_url", callbackURL,
		"state", state[:8]+"...",
	)

	return flow, nil
}

// AuthURL returns the full authorization URL the operator should visit.
// Gap 2 (MCP auth spec §183-211): Includes the resource parameter (RFC 8707)
// identifying the MCP server being accessed.
func (f *PendingAuthFlow) AuthURL() string {
	return f.Config.AuthCodeURL(
		f.State,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge(f.CodeVerifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("resource", f.ResourceURL),
	)
}

// CompleteAuthFlow exchanges an authorization code for tokens, persists them,
// registers the credential, and connects the backend.
func (g *Gateway) CompleteAuthFlow(ctx context.Context, state, code string) error {
	afm := g.getAuthFlows()
	if afm == nil {
		return fmt.Errorf("OAuth flow manager not initialized")
	}

	// Look up + validate expiry + consume under a single lock acquisition.
	// Doing the expiry check before delete keeps the state slot reusable in
	// the (theoretical) case of an attacker spamming a known state to burn
	// it before the legitimate callback arrives — they get a clean error
	// and we still serve the real one when it shows up later, until the
	// 10 min window closes.
	afm.mu.Lock()
	flow, ok := afm.flows[state]
	if !ok {
		afm.mu.Unlock()
		return fmt.Errorf("unknown or expired OAuth state")
	}
	if time.Since(flow.CreatedAt) > 10*time.Minute {
		delete(afm.flows, state)
		afm.mu.Unlock()
		g.setAuthStatus(flow.BackendID, "failed:timeout")
		return fmt.Errorf("OAuth flow expired (>10 minutes)")
	}
	delete(afm.flows, state) // single-use
	afm.mu.Unlock()

	// Exchange code for tokens with PKCE verifier.
	// Gap 2 (MCP auth spec §183-211): Include resource parameter (RFC 8707) in token request.
	token, err := flow.Config.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", flow.CodeVerifier),
		oauth2.SetAuthURLParam("resource", flow.ResourceURL),
	)
	if err != nil {
		g.setAuthStatus(flow.BackendID, "failed:"+err.Error())
		return fmt.Errorf("token exchange: %w", err)
	}

	g.logger.Info("OAuth token exchange successful",
		"backend", flow.BackendID,
		"token_type", token.TokenType,
		"expires", token.Expiry.Format(time.RFC3339),
	)

	// Persist OAuth tokens in KV.
	g.persistOAuthTokens(flow.BackendID, flow.Config, token)

	// Register credential with auto-refresh.
	ts := flow.Config.TokenSource(ctx, token)
	rts := g.persistingOAuthTokenSource(flow.BackendID, flow.Config, token, ts)
	cred := credentials.NewOAuth(rts, "")
	g.credStore.Register(flow.BackendID, cred)

	// Connect the backend.
	sc := &config.ServerConfig{
		ID:        flow.BackendID,
		Namespace: flow.BackendID,
		Enabled:   true,
		URL:       flow.BackendURL,
		Sandbox:   config.CompatSandboxConfig(),
		Timeout:   config.Duration(30 * time.Second),
	}
	if err := g.ConnectBackend(ctx, sc); err != nil {
		g.setAuthStatus(flow.BackendID, "failed:connect:"+err.Error())
		return fmt.Errorf("connect backend after OAuth: %w", err)
	}

	// Persist backend config.
	g.persistBackend(flow.BackendID, &persistedBackend{
		URL:     flow.BackendURL,
		Enabled: boolPtr(true),
		Sandbox: &sc.Sandbox,
	})

	g.setAuthStatus(flow.BackendID, "connected")

	g.logger.Info("backend connected via OAuth",
		"backend", flow.BackendID,
		"url", flow.BackendURL,
		"resource", flow.ResourceURL,
	)

	return nil
}

// AuthFlowStatus returns the status of an OAuth flow for a backend.
// Returns "pending", "connected", "failed:{reason}", or "".
func (g *Gateway) AuthFlowStatus(backendID string) string {
	afm := g.getAuthFlows()
	if afm == nil {
		return ""
	}

	afm.mu.Lock()
	defer afm.mu.Unlock()

	if status, ok := afm.completed[backendID]; ok {
		return status
	}

	// Check if there's a pending flow for this backend.
	for _, flow := range afm.flows {
		if flow.BackendID == backendID {
			return "pending"
		}
	}

	// Check if backend is already connected.
	g.mu.RLock()
	_, connected := g.backends[backendID]
	g.mu.RUnlock()
	if connected {
		return "connected"
	}

	return ""
}

// ProbeBackendOAuth probes a URL for OAuth requirements. If the backend returns
// 401 with WWW-Authenticate + resource_metadata, initiates the OAuth flow and
// returns the authorization URL and state. Returns ("", "", nil) if no OAuth needed.
// Satisfies the admin.OAuthProber interface.
func (g *Gateway) ProbeBackendOAuth(ctx context.Context, backendID, url string, opts admin.OAuthProberOptions) (authURL, state string, err error) {
	flow, err := g.ProbeBackendAuth(ctx, backendID, url, ProbeAuthOptions{
		CallbackOverride:   opts.CallbackBase,
		ManualClientID:     opts.ClientID,
		ManualClientSecret: opts.ClientSecret,
	})
	if err != nil {
		return "", "", err
	}
	if flow == nil {
		return "", "", nil
	}
	return flow.AuthURL(), flow.State, nil
}

func (g *Gateway) setAuthStatus(backendID, status string) {
	afm := g.getAuthFlows()
	if afm == nil {
		return
	}
	afm.mu.Lock()
	afm.completed[backendID] = status
	afm.mu.Unlock()
}

func getProtectedResourceMetadataForBackend(ctx context.Context, metadataURL, backendURL string) (*oauthex.ProtectedResourceMetadata, error) {
	var errs []string
	for _, candidate := range protectedResourceCandidates(backendURL) {
		prm, err := oauthex.GetProtectedResourceMetadata(ctx, metadataURL, candidate, nil)
		if err == nil {
			return prm, nil
		}
		errs = append(errs, err.Error())
	}
	return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
}

func protectedResourceCandidates(rawURL string) []string {
	candidates := []string{rawURL}

	if u, err := url.Parse(rawURL); err == nil {
		trimmed := *u
		trimmed.RawQuery = ""
		trimmed.Fragment = ""

		if trimmed.Path != "" && trimmed.Path != "/" {
			toggled := trimmed
			if strings.HasSuffix(toggled.Path, "/") {
				toggled.Path = strings.TrimRight(toggled.Path, "/")
			} else {
				toggled.Path += "/"
			}
			candidates = append(candidates, toggled.String())
		}

		base := trimmed
		base.Path = ""
		candidates = append(candidates, base.String())
		base.Path = "/"
		candidates = append(candidates, base.String())
	}

	return uniqueStrings(candidates)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

// discoverAuthServerMeta tries multiple well-known endpoints to discover auth server
// metadata, per MCP authorization spec §70-79.
//
// For issuer URLs WITH path components (e.g., https://auth.example.com/tenant1):
//  1. https://auth.example.com/.well-known/oauth-authorization-server/tenant1
//  2. https://auth.example.com/.well-known/openid-configuration/tenant1
//  3. https://auth.example.com/tenant1/.well-known/openid-configuration
//
// For issuer URLs WITHOUT path components (e.g., https://auth.example.com):
//  1. https://auth.example.com/.well-known/oauth-authorization-server
//  2. https://auth.example.com/.well-known/openid-configuration
//
// Returns the metadata, the URL that succeeded, and any error.
func validateDiscoveredIssuer(expected, got, discoveredURL string) error {
	if got == "" {
		return fmt.Errorf("metadata from %s missing issuer", discoveredURL)
	}
	if got != expected {
		return fmt.Errorf("issuer mismatch: expected %q, got %q from %q", expected, got, discoveredURL)
	}
	return nil
}

func discoverAuthServerMeta(ctx context.Context, issuer string, logger *slog.Logger) (*oauthex.AuthServerMeta, string, error) {
	issuerURL, err := url.Parse(issuer)
	if err != nil {
		return nil, "", fmt.Errorf("parse auth server issuer: %w", err)
	}

	// Build the list of discovery URLs to try in priority order.
	var discoveryURLs []string
	issuerPath := strings.TrimRight(issuerURL.Path, "/")

	if issuerPath != "" && issuerPath != "/" {
		// Issuer has a path component.
		// 1. RFC 8414: /.well-known/oauth-authorization-server/<path>
		discoveryURLs = append(discoveryURLs,
			fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server%s", issuerURL.Scheme, issuerURL.Host, issuerPath))
		// 2. OIDC Discovery: /.well-known/openid-configuration/<path>
		discoveryURLs = append(discoveryURLs,
			fmt.Sprintf("%s://%s/.well-known/openid-configuration%s", issuerURL.Scheme, issuerURL.Host, issuerPath))
		// 3. OIDC Discovery (legacy): <path>/.well-known/openid-configuration
		discoveryURLs = append(discoveryURLs,
			fmt.Sprintf("%s://%s%s/.well-known/openid-configuration", issuerURL.Scheme, issuerURL.Host, issuerPath))
	} else {
		// No path component.
		// 1. RFC 8414: /.well-known/oauth-authorization-server
		discoveryURLs = append(discoveryURLs,
			fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server", issuerURL.Scheme, issuerURL.Host))
		// 2. OIDC Discovery: /.well-known/openid-configuration
		discoveryURLs = append(discoveryURLs,
			fmt.Sprintf("%s://%s/.well-known/openid-configuration", issuerURL.Scheme, issuerURL.Host))
	}

	for _, dURL := range discoveryURLs {
		logger.Debug("trying auth server metadata discovery", "url", dURL)
		asm, err := fetchAuthServerMeta(ctx, dURL)
		if err != nil {
			logger.Debug("auth server metadata fetch failed", "url", dURL, "error", err)
			return nil, "", err
		}
		if asm != nil {
			if err := validateDiscoveredIssuer(issuer, asm.Issuer, dURL); err != nil {
				return nil, "", err
			}
			return asm, dURL, nil
		}
		// asm == nil means 4xx — try next URL.
		logger.Debug("auth server metadata not found at URL, trying next", "url", dURL)
	}

	// All URLs returned 4xx.
	return nil, "", nil
}

// fetchAuthServerMeta fetches OAuth 2.0 Authorization Server Metadata (RFC 8414).
// Issuer matching is enforced by discoverAuthServerMeta. Returns nil if the
// server returns 4xx.
func fetchAuthServerMeta(ctx context.Context, metadataURL string) (*oauthex.AuthServerMeta, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", metadataURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, metadataURL)
	}

	var asm oauthex.AuthServerMeta
	if err := json.NewDecoder(resp.Body).Decode(&asm); err != nil {
		return nil, fmt.Errorf("decode metadata from %s: %w", metadataURL, err)
	}

	if asm.AuthorizationEndpoint == "" || asm.TokenEndpoint == "" {
		return nil, fmt.Errorf("metadata from %s missing required endpoints", metadataURL)
	}

	return &asm, nil
}

// adminPublicURL is the operator-configured admin public URL, or empty.
// When non-empty it pins the OAuth redirect_uri for every flow; when empty,
// each flow uses the inbound admin request's Host header instead.
func (g *Gateway) InitAuthFlows(adminPublicURL string) {
	callbackURL := ""
	if adminPublicURL != "" {
		callbackURL = strings.TrimRight(adminPublicURL, "/") + "/oauth/callback"
	}

	g.authFlows = newAuthFlowManager(callbackURL, g.logger)

	// Start background goroutine to expire old flows.
	go g.cleanupAuthFlows()
}

// cleanupAuthFlows periodically removes expired pending flows.
func (g *Gateway) cleanupAuthFlows() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		afm := g.getAuthFlows()
		if afm == nil {
			return
		}
		afm.mu.Lock()
		now := time.Now()
		for state, flow := range afm.flows {
			if now.Sub(flow.CreatedAt) > 10*time.Minute {
				delete(afm.flows, state)
				g.logger.Info("expired OAuth flow", "backend", flow.BackendID, "state", state[:8]+"...")
			}
		}
		afm.mu.Unlock()
	}
}

// OAuthCallbackHandler returns an http.HandlerFunc for GET /oauth/callback.
func (g *Gateway) OAuthCallbackHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code := q.Get("code")
		state := q.Get("state")
		errCode := q.Get("error")
		logState := state
		if len(logState) > 8 {
			logState = logState[:8] + "..."
		}
		g.logger.Info("OAuth callback received",
			"state", logState,
			"has_code", code != "",
			"error", errCode,
		)

		// Provider returned an error instead of a code. Show it verbatim
		// plus, for the common "redirect_uri rejected because http+non-localhost"
		// case (Clerk, Auth0, many others), a concrete remediation hint.
		if errCode != "" {
			desc := q.Get("error_description")
			hint := ""
			descLower := strings.ToLower(desc)
			if strings.Contains(descLower, "insecure") ||
				strings.Contains(descLower, "redirect_uri") ||
				strings.Contains(descLower, "redirect url") {
				hint = "Most providers reject http:// redirect URIs unless the host ends in '.localhost'. " +
					"Either: (a) add an /etc/hosts entry like '" + r.Host + " prism.localhost', " +
					"set admin_public_url to 'http://prism.localhost:<port>' in the prism config, and retry; " +
					"or (b) put prism behind TLS (reverse proxy or built-in TLS) and use https://."
			}
			g.setAuthStatus(stateBackend(g, state), "failed:"+errCode+":"+desc)
			renderCallbackError(w, errCode, desc, hint)
			return
		}

		if code == "" || state == "" {
			renderCallbackError(w, "invalid_response", "Provider redirected without a code or state parameter.", "")
			return
		}

		if err := g.CompleteAuthFlow(r.Context(), state, code); err != nil {
			g.logger.Error("OAuth callback failed", "error", err)
			renderCallbackError(w, "exchange_failed", err.Error(), "")
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body style="font-family:system-ui;padding:24px;background:#050505;color:#ebebeb"><h3>Authenticated</h3><p>You can close this window.</p><script>window.close()</script></body></html>`)
	}
}

// stateBackend resolves the backendID for a given OAuth state, used to mark
// status when the provider returns an error before we've called CompleteAuthFlow.
// Returns "" if no flow matches — setAuthStatus tolerates that.
func stateBackend(g *Gateway, state string) string {
	afm := g.getAuthFlows()
	if afm == nil {
		return ""
	}
	afm.mu.Lock()
	defer afm.mu.Unlock()
	if f, ok := afm.flows[state]; ok {
		return f.BackendID
	}
	return ""
}

func renderCallbackError(w http.ResponseWriter, code, desc, hint string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	hintHTML := ""
	if hint != "" {
		hintHTML = `<p style="background:#1a1a1a;border-left:3px solid #6366f1;padding:12px 14px;border-radius:4px;line-height:1.5">` + html.EscapeString(hint) + `</p>`
	}
	fmt.Fprintf(w, `<!doctype html><html><body style="font-family:system-ui;padding:24px;background:#050505;color:#ebebeb;max-width:640px;margin:32px auto">
<h3 style="margin:0 0 8px">Authentication failed</h3>
<p style="font-family:monospace;font-size:11px;text-transform:uppercase;letter-spacing:0.15em;color:#888;margin:0 0 16px">%s</p>
<p style="line-height:1.6;margin:0 0 16px">%s</p>
%s
<p style="margin-top:24px;font-size:12px;color:#888">You can close this window and try again from the prism console.</p>
</body></html>`, html.EscapeString(code), html.EscapeString(desc), hintHTML)
}

// ─── OAuth token KV persistence ─────────────────────────────────────────────

const oauthKVPrefix = "backend/oauth/"

// persistedOAuthToken is the JSON stored in KV for OAuth tokens.
type persistedOAuthToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret,omitempty"`
	TokenURL     string    `json:"token_url"`
	AuthStyle    int       `json:"auth_style"`
	RedirectURL  string    `json:"redirect_url,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
}

type persistingTokenSource struct {
	mu        sync.Mutex
	backendID string
	cfg       *oauth2.Config
	src       oauth2.TokenSource
	last      *oauth2.Token
	persist   func(string, *oauth2.Config, *oauth2.Token)
}

func (g *Gateway) persistingOAuthTokenSource(backendID string, cfg *oauth2.Config, token *oauth2.Token, refresh oauth2.TokenSource) oauth2.TokenSource {
	return &persistingTokenSource{
		backendID: backendID,
		cfg:       cfg,
		src:       oauth2.ReuseTokenSource(token, refresh),
		last:      cloneOAuthToken(token),
		persist:   g.persistOAuthTokens,
	}
}

func (p *persistingTokenSource) Token() (*oauth2.Token, error) {
	token, err := p.src.Token()
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	changed := !sameOAuthToken(p.last, token)
	if changed {
		p.last = cloneOAuthToken(token)
	}
	p.mu.Unlock()

	if changed && p.persist != nil {
		p.persist(p.backendID, p.cfg, token)
	}
	return token, nil
}

func sameOAuthToken(a, b *oauth2.Token) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.AccessToken == b.AccessToken &&
		a.RefreshToken == b.RefreshToken &&
		a.TokenType == b.TokenType &&
		a.Expiry.Equal(b.Expiry)
}

func cloneOAuthToken(token *oauth2.Token) *oauth2.Token {
	if token == nil {
		return nil
	}
	clone := *token
	return &clone
}

// persistOAuthTokens saves OAuth tokens and client config to KV (encrypted at rest).
func (g *Gateway) persistOAuthTokens(backendID string, cfg *oauth2.Config, token *oauth2.Token) {
	if g.kvStore == nil {
		return
	}

	pt := &persistedOAuthToken{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		Expiry:       token.Expiry,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.Endpoint.TokenURL,
		AuthStyle:    int(cfg.Endpoint.AuthStyle),
		RedirectURL:  cfg.RedirectURL,
		Scopes:       cfg.Scopes,
	}

	data, err := json.Marshal(pt)
	if err != nil {
		g.logger.Warn("failed to marshal OAuth tokens for persistence", "id", backendID, "error", err)
		return
	}

	// Encrypt before writing to KV store.
	encKey, err := kvEncryptionKey()
	if err != nil {
		g.logger.Warn("failed to obtain encryption key, skipping token persistence", "id", backendID, "error", err)
		return
	}
	encrypted, err := encryptAESGCM(encKey, data)
	if err != nil {
		g.logger.Warn("failed to encrypt OAuth tokens for persistence", "id", backendID, "error", err)
		return
	}

	if err := g.kvStore.Set(oauthKVPrefix+backendID, encrypted); err != nil {
		g.logger.Warn("failed to persist OAuth tokens", "id", backendID, "error", err)
	}
}

// deletePersistedOAuthTokens removes OAuth tokens from KV.
func (g *Gateway) deletePersistedOAuthTokens(backendID string) {
	if g.kvStore == nil {
		return
	}
	if err := g.kvStore.Delete(oauthKVPrefix + backendID); err != nil {
		g.logger.Warn("failed to delete persisted OAuth tokens", "id", backendID, "error", err)
	}
}

// LoadPersistedOAuthCredentials restores OAuth credentials from KV.
// Call after SetStore and before LoadPersistedBackends.
func (g *Gateway) LoadPersistedOAuthCredentials() {
	if g.kvStore == nil {
		return
	}
	keys, err := g.kvStore.List(oauthKVPrefix)
	if err != nil {
		g.logger.Warn("failed to list persisted OAuth tokens", "error", err)
		return
	}

	encKey, encKeyErr := kvEncryptionKey()

	for _, key := range keys {
		backendID := strings.TrimPrefix(key, oauthKVPrefix)
		data, err := g.kvStore.Get(key)
		if err != nil {
			g.logger.Warn("failed to read persisted OAuth token", "key", key, "error", err)
			continue
		}

		// Decrypt if we have an encryption key.
		plaintext := data
		if encKeyErr == nil {
			decrypted, decErr := decryptAESGCM(encKey, data)
			if decErr != nil {
				// Fallback: try as unencrypted JSON (pre-encryption migration).
				if !json.Valid(data) {
					g.logger.Warn("failed to decrypt persisted OAuth token", "key", key, "error", decErr)
					continue
				}
			} else {
				plaintext = decrypted
			}
		}

		var pt persistedOAuthToken
		if err := json.Unmarshal(plaintext, &pt); err != nil {
			g.logger.Warn("failed to unmarshal persisted OAuth token", "key", key, "error", err)
			continue
		}

		// Reconstruct the oauth2.Config and TokenSource.
		oauthCfg := &oauth2.Config{
			ClientID:     pt.ClientID,
			ClientSecret: pt.ClientSecret,
			Endpoint: oauth2.Endpoint{
				TokenURL:  pt.TokenURL,
				AuthStyle: oauth2.AuthStyle(pt.AuthStyle),
			},
			RedirectURL: pt.RedirectURL,
			Scopes:      pt.Scopes,
		}

		token := &oauth2.Token{
			AccessToken:  pt.AccessToken,
			RefreshToken: pt.RefreshToken,
			TokenType:    pt.TokenType,
			Expiry:       pt.Expiry,
		}

		ts := oauthCfg.TokenSource(context.Background(), token)
		rts := g.persistingOAuthTokenSource(backendID, oauthCfg, token, ts)
		cred := credentials.NewOAuth(rts, "")
		g.credStore.Register(backendID, cred)

		g.logger.Info("restored persisted OAuth credential", "id", backendID)
	}
}
