//go:build mcp_go_client_oauth

package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

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
func (g *Gateway) ProbeBackendAuth(ctx context.Context, backendID, backendURL string) (*PendingAuthFlow, error) {
	afm := g.getAuthFlows()
	if afm == nil {
		return nil, fmt.Errorf("OAuth flow manager not initialized")
	}

	// Probe the backend URL for 401.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, backendURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create probe request: %w", err)
	}
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

	// Parse WWW-Authenticate header for resource_metadata URL.
	wwwAuth := resp.Header[http.CanonicalHeaderKey("WWW-Authenticate")]
	if len(wwwAuth) == 0 {
		return nil, fmt.Errorf("401 from %s but no WWW-Authenticate header", backendURL)
	}

	challenges, err := oauthex.ParseWWWAuthenticate(wwwAuth)
	if err != nil {
		return nil, fmt.Errorf("parse WWW-Authenticate: %w", err)
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

	// Discover protected resource metadata (RFC 9728).
	prm, err := oauthex.GetProtectedResourceMetadata(ctx, metadataURL, backendURL, nil)
	if err != nil {
		return nil, fmt.Errorf("get protected resource metadata: %w", err)
	}

	if len(prm.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization servers in protected resource metadata for %s", backendURL)
	}

	// Discover auth server metadata (RFC 8414).
	authServerIssuer := prm.AuthorizationServers[0]

	// Build metadata URL from issuer.
	issuerURL, err := url.Parse(authServerIssuer)
	if err != nil {
		return nil, fmt.Errorf("parse auth server issuer: %w", err)
	}
	asmURL := fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server", issuerURL.Scheme, issuerURL.Host)

	asm, err := oauthex.GetAuthServerMeta(ctx, asmURL, authServerIssuer, nil)
	if err != nil {
		return nil, fmt.Errorf("get auth server metadata: %w", err)
	}
	if asm == nil {
		return nil, fmt.Errorf("auth server at %s returned no metadata", authServerIssuer)
	}

	// Dynamic Client Registration (RFC 7591).
	callbackURL := afm.callbackURL
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

	// Build the oauth2.Config.
	oauthCfg := &oauth2.Config{
		ClientID:     regResp.ClientID,
		ClientSecret: regResp.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:   asm.AuthorizationEndpoint,
			TokenURL:  asm.TokenEndpoint,
			AuthStyle: oauth2.AuthStyleInParams,
		},
		RedirectURL: callbackURL,
		Scopes:      prm.ScopesSupported,
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
		ResourceURL:  backendURL,
		CreatedAt:    time.Now(),
	}

	// Store the pending flow.
	afm.mu.Lock()
	afm.flows[state] = flow
	afm.mu.Unlock()

	g.logger.Info("initiated OAuth flow for backend",
		"backend", backendID,
		"auth_server", authServerIssuer,
		"state", state[:8]+"...",
	)

	return flow, nil
}

// AuthURL returns the full authorization URL the operator should visit.
func (f *PendingAuthFlow) AuthURL() string {
	return f.Config.AuthCodeURL(
		f.State,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge(f.CodeVerifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// CompleteAuthFlow exchanges an authorization code for tokens, persists them,
// registers the credential, and connects the backend.
func (g *Gateway) CompleteAuthFlow(ctx context.Context, state, code string) error {
	afm := g.getAuthFlows()
	if afm == nil {
		return fmt.Errorf("OAuth flow manager not initialized")
	}

	afm.mu.Lock()
	flow, ok := afm.flows[state]
	if ok {
		delete(afm.flows, state) // single-use
	}
	afm.mu.Unlock()

	if !ok {
		return fmt.Errorf("unknown or expired OAuth state")
	}

	// Check expiration.
	if time.Since(flow.CreatedAt) > 10*time.Minute {
		g.setAuthStatus(flow.BackendID, "failed:timeout")
		return fmt.Errorf("OAuth flow expired (>10 minutes)")
	}

	// Exchange code for tokens with PKCE verifier.
	token, err := flow.Config.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", flow.CodeVerifier),
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
	rts := oauth2.ReuseTokenSource(token, ts)
	cred := credentials.NewOAuth(rts, "")
	g.credStore.Register(flow.BackendID, cred)

	// Connect the backend.
	sc := &config.ServerConfig{
		ID:        flow.BackendID,
		Namespace: flow.BackendID,
		URL:       flow.ResourceURL,
		Timeout:   config.Duration(30 * time.Second),
	}
	if err := g.ConnectBackend(ctx, sc); err != nil {
		g.setAuthStatus(flow.BackendID, "failed:connect:"+err.Error())
		return fmt.Errorf("connect backend after OAuth: %w", err)
	}

	// Persist backend config.
	g.persistBackend(flow.BackendID, &persistedBackend{
		URL: flow.ResourceURL,
	})

	g.setAuthStatus(flow.BackendID, "connected")

	g.logger.Info("backend connected via OAuth",
		"backend", flow.BackendID,
		"url", flow.ResourceURL,
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
func (g *Gateway) ProbeBackendOAuth(ctx context.Context, backendID, url string) (authURL, state string, err error) {
	flow, err := g.ProbeBackendAuth(ctx, backendID, url)
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

// InitAuthFlows initializes the OAuth auth flow manager.
// adminAddr is the admin server listen address (e.g., ":9090").
func (g *Gateway) InitAuthFlows(adminAddr string) {
	// Build the callback URL from the admin address.
	host := "localhost"
	port := strings.TrimPrefix(adminAddr, ":")
	if !strings.HasPrefix(port, ":") && strings.Contains(adminAddr, ":") && !strings.HasPrefix(adminAddr, ":") {
		// adminAddr is "host:port"
		host = strings.Split(adminAddr, ":")[0]
		port = strings.Split(adminAddr, ":")[1]
	}
	callbackURL := fmt.Sprintf("http://%s:%s/oauth/callback", host, port)

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
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")

		if code == "" || state == "" {
			http.Error(w, "missing code or state parameter", http.StatusBadRequest)
			return
		}

		if err := g.CompleteAuthFlow(r.Context(), state, code); err != nil {
			g.logger.Error("OAuth callback failed", "error", err)
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `<html><body><h3>Authentication failed</h3><p>%s</p><script>setTimeout(function(){window.close()},3000)</script></body></html>`, err.Error())
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><h3>Authenticated</h3><p>You can close this window.</p><script>window.close()</script></body></html>`)
	}
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

// persistOAuthTokens saves OAuth tokens and client config to KV.
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
	if err := g.kvStore.Set(oauthKVPrefix+backendID, data); err != nil {
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

	for _, key := range keys {
		backendID := strings.TrimPrefix(key, oauthKVPrefix)
		data, err := g.kvStore.Get(key)
		if err != nil {
			g.logger.Warn("failed to read persisted OAuth token", "key", key, "error", err)
			continue
		}

		var pt persistedOAuthToken
		if err := json.Unmarshal(data, &pt); err != nil {
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
		rts := oauth2.ReuseTokenSource(token, ts)
		cred := credentials.NewOAuth(rts, "")
		g.credStore.Register(backendID, cred)

		g.logger.Info("restored persisted OAuth credential", "id", backendID)
	}
}
