// Package admin provides the Prism admin API.
package admin

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/adminauth"
	"github.com/1broseidon/prism/internal/metrics"
)

// GroupInfo mirrors authserver.GroupInfo for the admin API boundary.
type GroupInfo struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	Source string   `json:"source"` // "config" or "dynamic"
}

// GroupManager is the interface the admin API uses to manage scope groups.
type GroupManager interface {
	// ListGroups returns all groups (config + dynamic) with source info.
	ListGroups() []GroupInfo
	// GetGroup returns a single group by name, or nil if not found.
	GetGroup(name string) *GroupInfo
	// SetGroup creates or updates a dynamic group. Returns error for config groups.
	SetGroup(name string, scopes []string) error
	// DeleteGroup removes a dynamic group. Returns error for config groups.
	DeleteGroup(name string) error
	// DefaultScopes returns the configured default scopes.
	DefaultScopes() []string
	// SetDefaultScopes updates the runtime default scopes.
	SetDefaultScopes(scopes []string) error
}

// BridgeInfoProvider is implemented by backend managers that can report
// whether a prism-bridge `manage` endpoint is configured.
type BridgeInfoProvider interface {
	BridgeURL() string
}

// NetworkSettingsProvider exposes runtime network knobs read off the gateway,
// including whether X-Forwarded-* headers should be trusted when deriving
// OAuth callbacks from the inbound request, and which host names from those
// headers are accepted.
type NetworkSettingsProvider interface {
	TrustProxyHeaders() bool
	AllowedForwardedHosts() []string
}

// API exposes admin endpoints.
type API struct {
	statusFn             func() any
	agentsFn             func() []any
	removeFn             func(string) bool
	removeStaleFn        func() int
	eventsFn             func() []any
	backendMgr           BackendManager
	agentMgr             AgentManager
	groupMgr             GroupManager
	oauthCallbackHandler http.Handler      // optional: gateway's OAuth callback handler
	auth                 *adminauth.Holder // holder for live admin auth service; zero-value = open
	adminProbeLimiter    *adminProbeRateLimiter
	startedAt            time.Time
}

// NewAPI creates an admin API.
// agentMgr, groupMgr, oauthCallback, and auth are optional —
// when nil their endpoints return 503/404 or the middleware no-ops.
func NewAPI(
	statusFn func() any,
	backendMgr BackendManager,
	agentsFn func() []any,
	removeFn func(string) bool,
	removeStaleFn func() int,
	eventsFn func() []any,
	agentMgr AgentManager,
	groupMgr GroupManager,
	oauthCallback http.Handler,
	auth *adminauth.Holder,
) *API {
	return &API{
		statusFn:             statusFn,
		agentsFn:             agentsFn,
		removeFn:             removeFn,
		removeStaleFn:        removeStaleFn,
		eventsFn:             eventsFn,
		backendMgr:           backendMgr,
		agentMgr:             agentMgr,
		groupMgr:             groupMgr,
		oauthCallbackHandler: oauthCallback,
		auth:                 auth,
		adminProbeLimiter:    newAdminProbeRateLimiter(),
		startedAt:            time.Now(),
	}
}

// Handler returns the admin HTTP handler.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public routes — no auth, always available.
	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("GET /auth/me", a.handleAuthMe)
	mux.HandleFunc("GET /auth/login", a.handleAuthLogin)
	mux.HandleFunc("GET /auth/callback", a.handleAuthCallback)
	mux.HandleFunc("POST /auth/logout", a.handleAuthLogout)

	// Read-only routes — require a session (admin or viewer). When auth is
	// nil, the middleware is a pass-through.
	mux.Handle("GET /backends", a.session(http.HandlerFunc(a.handleBackends)))
	mux.Handle("GET /info", a.session(http.HandlerFunc(a.handleInfo)))
	mux.Handle("GET /agents", a.session(http.HandlerFunc(a.handleAgents)))
	mux.Handle("GET /agents/", a.session(http.HandlerFunc(a.handleAgentByPrismID)))
	mux.Handle("GET /events", a.session(http.HandlerFunc(a.handleEvents)))
	mux.Handle("GET /groups", a.session(http.HandlerFunc(a.handleListGroups)))
	mux.Handle("GET /groups/", a.session(http.HandlerFunc(a.handleGetGroup)))
	mux.Handle("GET /defaults", a.session(http.HandlerFunc(a.handleDefaults)))
	mux.Handle("GET /backends/", a.session(http.HandlerFunc(a.handleBackendSub)))

	// Admin auth configuration — admin role required when admin auth is
	// configured; pass-through (anonymous) in open mode, consistent with
	// other mutation routes.
	mux.Handle("GET /config/admin-auth", a.admin(http.HandlerFunc(a.handleGetAdminAuth)))
	mux.Handle("PUT /config/admin-auth", a.admin(http.HandlerFunc(a.handlePutAdminAuth)))
	mux.Handle("POST /config/admin-auth/test", a.admin(http.HandlerFunc(a.handleTestAdminAuth)))
	mux.Handle("POST /config/admin-auth/enable", a.admin(http.HandlerFunc(a.handleEnableAdminAuth)))
	mux.Handle("DELETE /config/admin-auth/enable", a.admin(http.HandlerFunc(a.handleDisableAdminAuth)))

	// Runtime network settings (admin_public_url, behind-proxy toggle, ...).
	mux.Handle("GET /config/network", a.admin(http.HandlerFunc(a.handleGetNetwork)))
	mux.Handle("PUT /config/network", a.admin(http.HandlerFunc(a.handlePutNetwork)))

	// Mutation routes — admin role required.
	mux.Handle("PUT /agents/", a.admin(http.HandlerFunc(a.handlePutAgent)))
	mux.Handle("DELETE /agents/stale", a.admin(http.HandlerFunc(a.handleRemoveStaleAgents)))
	mux.Handle("DELETE /agents/", a.admin(http.HandlerFunc(a.handleDeleteAgent)))
	mux.Handle("PUT /groups/", a.admin(http.HandlerFunc(a.handleSetGroup)))
	mux.Handle("DELETE /groups/", a.admin(http.HandlerFunc(a.handleDeleteGroup)))
	mux.Handle("PUT /defaults", a.admin(http.HandlerFunc(a.handleSetDefaults)))
	mux.Handle("POST /backends/", a.admin(http.HandlerFunc(a.handleBackendPost)))
	mux.Handle("DELETE /backends/", a.admin(http.HandlerFunc(a.handleRemoveBackend)))

	if a.oauthCallbackHandler != nil {
		// Gateway's outbound-OAuth callback (backend authentication).
		// Different concept from admin auth; stays public.
		mux.Handle("GET /oauth/callback", a.oauthCallbackHandler)
	}
	if metrics.Enabled() {
		mux.Handle("GET /metrics", metrics.Handler())
	}
	// SPA catch-all. Public so the login screen can load when admin auth
	// rejects API calls. Auth gating happens inside the SPA via /auth/me.
	mux.HandleFunc("GET /", a.handleSPA)
	return mux
}

// handleBackendPost dispatches POST /backends/{id} and
// POST /backends/{id}/reconnect.
func (a *API) handleBackendPost(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/reconnect") {
		a.handleReconnectBackend(w, r)
		return
	}
	a.handleAddBackend(w, r)
}

// session wraps a handler with RequireSession when auth is configured.
// Nil-safe: when a.auth is nil, the call is a pass-through.
func (a *API) session(h http.Handler) http.Handler {
	return a.auth.RequireSession(h)
}

// admin wraps a handler with RequireAdmin when auth is configured.
func (a *API) admin(h http.Handler) http.Handler {
	return a.auth.RequireAdmin(h)
}

// handleAuthMe is exposed regardless of whether auth is configured so the
// SPA has a single contract: it sees {"auth":"open"} when running open, or
// the operator's identity when signed in.
func (a *API) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	a.auth.Get().HandleMe(w, r)
}

func (a *API) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	// Per-IP login rate limit applies even in open mode so a misbehaving
	// client can't flood /auth/login. Service-level handler then either
	// runs the OIDC redirect or 404s when auth is disabled.
	if a.auth != nil && !a.auth.LoginAllowed(r) {
		http.Error(w, "too many login attempts; try again in a moment", http.StatusTooManyRequests)
		return
	}
	a.auth.Get().HandleLogin(w, r)
}

func (a *API) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	a.auth.Get().HandleCallback(w, r)
}

func (a *API) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	a.auth.Get().HandleLogout(w, r)
}

// handlePutAgent dispatches PUT /agents/{prism_id}/policy to the policy handler.
func (a *API) handlePutAgent(w http.ResponseWriter, r *http.Request) {
	a.handleSetAgentPolicy(w, r)
}

// handleDeleteAgent dispatches DELETE /agents/{id} or DELETE /agents/{prism_id}/policy.
func (a *API) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	// Route to policy delete if path ends with /policy.
	if isPolicyPath(r.URL.Path) {
		a.handleDeleteAgentPolicy(w, r)
		return
	}
	// Otherwise fall through to the legacy remove-agent handler.
	a.handleRemoveAgent(w, r)
}

// isPolicyPath returns true iff the URL path is exactly /agents/{id}/policy
// where {id} matches isValidID. Previous suffix-based check was permissive —
// "/agents/foo/bar/policy" and "/agents/x/my-policy" both matched.
func isPolicyPath(path string) bool {
	const prefix = "/agents/"
	const suffix = "/policy"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return false
	}
	id := path[len(prefix) : len(path)-len(suffix)]
	return isValidID(id)
}

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) handleBackends(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.statusFn())
}

func (a *API) handleInfo(w http.ResponseWriter, _ *http.Request) {
	bridgeConfigured := false
	if provider, ok := a.backendMgr.(BridgeInfoProvider); ok && provider.BridgeURL() != "" {
		bridgeConfigured = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":              "prism",
		"version":           "0.1.0",
		"go_version":        runtime.Version(),
		"uptime":            time.Since(a.startedAt).String(),
		"goroutines":        runtime.NumGoroutine(),
		"bridge_configured": bridgeConfigured,
	})
}

// handleBackendSub dispatches GET /backends/{id}/auth-status.
func (a *API) handleBackendSub(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/auth-status") {
		a.handleAuthStatus(w, r)
		return
	}
	http.NotFound(w, r)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
