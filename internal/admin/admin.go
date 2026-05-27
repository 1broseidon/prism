// Package admin provides the Prism admin API.
package admin

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/adminauth"
	"github.com/1broseidon/prism/internal/analytics"
	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/identity"
	"github.com/1broseidon/prism/internal/metrics"
)

// GroupInfo mirrors authserver.GroupInfo for the admin API boundary.
type GroupInfo struct {
	ID              string                        `json:"id,omitempty"`
	Name            string                        `json:"name"`
	DisplayName     string                        `json:"display_name,omitempty"`
	Scopes          []string                      `json:"scopes"`
	Source          string                        `json:"source"` // "config" or "dynamic"
	BackendPolicies map[string]auth.BackendPolicy `json:"backend_policies,omitempty"`
}

// GroupManager is the interface the admin API uses to manage scope groups.
type GroupManager interface {
	// ListGroups returns all groups (config + dynamic) with source info.
	ListGroups() []GroupInfo
	// GetGroup returns a single group by name, or nil if not found.
	GetGroup(name string) *GroupInfo
	// SetGroup creates or updates a dynamic group. Returns error for config groups.
	SetGroup(name string, scopes []string) error
	// SetGroupBackendPolicies replaces the per-backend policies for a group.
	// Empty map clears the entry. Errors for config-defined groups.
	SetGroupBackendPolicies(name string, policies map[string]auth.BackendPolicy) error
	// DeleteGroup removes a dynamic group. Returns error for config groups.
	DeleteGroup(name string) error
	// DefaultScopes returns the configured default scopes.
	DefaultScopes() []string
	// SetDefaultScopes updates the runtime default scopes.
	SetDefaultScopes(scopes []string) error
	// DefaultBackendPolicies returns the persisted default backend policies.
	DefaultBackendPolicies() map[string]auth.BackendPolicy
	// SetDefaultBackendPolicies replaces the default backend policies map.
	SetDefaultBackendPolicies(policies map[string]auth.BackendPolicy) error
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
	traceProvider        BackendPolicyTraceProvider
	workspaceLookup      WorkspaceReversePolicyLookup
	adminProbeLimiter    *adminProbeRateLimiter
	startedAt            time.Time
	// openAPIFetcher is overridable by tests so they can substitute a non-SSRF
	// HTTP client. Nil in production — handlers fall back to the default
	// SSRF-guarded fetcher.
	openAPIFetcher fetcherFactory
	// binaryStore owns the content-addressed binary directory. Nil in test
	// builds or in modes that don't enable binary backends; handlers return
	// 503 when unset.
	binaryStore BinaryStore
	// binaryFetcher is a test hook mirroring openAPIFetcher for the
	// POST /binaries/fetch path. Nil in production.
	binaryFetcher binaryFetcherFactory
	// identity is the central kind→{id, display_name} dispatcher used
	// by the /identity routes. Wired by SetIdentity from
	// cmd/prism/main.go; nil in tests that don't exercise identity
	// endpoints (the handlers return 503 when unset).
	identity identity.Dispatcher
	// grantMgr is the KV-backed grant template + binding store wired
	// by SetGrantManager. Production passes the live authserver.Server;
	// tests pass a shim. Nil-safe — every handler 503s when unset.
	grantMgr GrantManager
	// analyticsStore is the historical grant-event store; analyticsRing
	// is the in-memory ring for SSE tailing. Both wired by SetAnalytics;
	// both nil-safe — handlers 503 when the relevant capability is
	// missing.
	analyticsStore         analytics.Store
	analyticsRing          *analytics.RingBuffer
	analyticsRetentionDays int
	// policySummaryCache backs GET /agents/policy-summary with a 60s
	// TTL. Lazy-instantiated on first use; mutation paths call
	// invalidateAllAgentPolicySummaries which is nil-safe.
	policySummaryCache *agentPolicySummaryCache
}

// SetBackendPolicyTraceProvider wires the per-agent resolution trace provider
// used by GET /agents/{prism_id}/storage-resolution. Optional; when unset the
// endpoint returns 503.
func (a *API) SetBackendPolicyTraceProvider(p BackendPolicyTraceProvider) {
	a.traceProvider = p
}

// SetWorkspaceReversePolicyLookup wires the lookup that powers
// GET /workspaces/{id}. Optional; without it the endpoint omits the
// references list.
func (a *API) SetWorkspaceReversePolicyLookup(l WorkspaceReversePolicyLookup) {
	a.workspaceLookup = l
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
	adminAuth *adminauth.Holder,
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
		auth:                 adminAuth,
		adminProbeLimiter:    newAdminProbeRateLimiter(),
		startedAt:            time.Now(),
	}
}

// Handler returns the admin HTTP handler.
//
// Layout:
//   - /api/v1/*           JSON endpoints (handlers see paths without the prefix)
//   - /auth/callback      IdP redirect target (config-locked at root)
//   - /oauth/callback     gateway's outbound OAuth callback for backends
//   - /health             liveness probe (root-level convention)
//   - /metrics            Prometheus convention
//   - everything else     SPA shell (preact-iso owns client-side routing)
//
// The /api/v1/ prefix prevents collisions between SPA paths (/agents, /servers,
// /policy, ...) and API resources of the same name. The SPA fetches all JSON
// via /api/v1/... so hard refreshes of any SPA route fall through to the SPA
// shell rather than being captured by an API handler.
func (a *API) Handler() http.Handler {
	api := http.NewServeMux()
	a.registerAPIRoutes(api)

	outer := http.NewServeMux()
	outer.Handle("/api/v1/", http.StripPrefix("/api/v1", api))

	// OIDC redirect target — IdPs are configured with this exact path, so it
	// cannot move under /api/v1/. Same idea for the gateway's outbound OAuth
	// callback below.
	outer.HandleFunc("GET /auth/callback", a.handleAuthCallback)
	if a.oauthCallbackHandler != nil {
		outer.Handle("GET /oauth/callback", a.oauthCallbackHandler)
	}

	// Probe and metrics conventions live at root.
	outer.HandleFunc("GET /health", a.handleHealth)
	if metrics.Enabled() {
		outer.Handle("GET /metrics", metrics.Handler())
	}

	// SPA catch-all. Registered without a method prefix because Go's ServeMux
	// rejects "GET /" alongside the more-permissive "/api/v1/" pattern (the
	// former matches fewer methods but a more general path). handleSPA itself
	// ignores the method — non-GET requests to unknown paths get the SPA shell,
	// which is harmless for browser traffic.
	//
	// Public so the login screen can load when admin auth rejects API calls.
	// Auth gating happens inside the SPA via /api/v1/auth/me.
	outer.HandleFunc("/", a.handleSPA)
	return outer
}

// registerAPIRoutes registers JSON endpoints on the supplied mux. The mux is
// expected to be mounted at /api/v1/ via http.StripPrefix, so handlers see
// canonical paths like /agents/{id}, not /api/v1/agents/{id}.
func (a *API) registerAPIRoutes(mux *http.ServeMux) {
	// Auth — /auth/callback stays at root (IdP-configured) but the JSON
	// endpoints live here.
	mux.HandleFunc("GET /auth/me", a.handleAuthMe)
	mux.HandleFunc("GET /auth/login", a.handleAuthLogin)
	mux.HandleFunc("POST /auth/logout", a.handleAuthLogout)

	// Read-only routes — require a session (admin or viewer). When auth is
	// nil, the middleware is a pass-through.
	mux.Handle("GET /backends", a.session(http.HandlerFunc(a.handleBackends)))
	mux.Handle("GET /info", a.session(http.HandlerFunc(a.handleInfo)))
	mux.Handle("GET /agents", a.session(http.HandlerFunc(a.handleAgents)))
	mux.Handle("GET /agents/roles", a.session(http.HandlerFunc(a.handleAgentsRoles)))
	mux.Handle("GET /agents/{prism_id}/policy-resolution", a.session(http.HandlerFunc(a.handleAgentPolicyResolution)))
	mux.Handle("GET /agents/", a.session(http.HandlerFunc(a.handleAgentByPrismID)))
	mux.Handle("GET /events", a.session(http.HandlerFunc(a.handleEvents)))
	mux.Handle("GET /groups", a.session(http.HandlerFunc(a.handleListGroups)))
	mux.Handle("GET /groups/", a.session(a.identityCompat(identity.KindGroup, http.HandlerFunc(a.handleGetGroup))))
	mux.Handle("GET /defaults", a.session(http.HandlerFunc(a.handleDefaults)))
	mux.Handle("GET /workspaces", a.session(http.HandlerFunc(a.handleListWorkspaces)))
	mux.Handle("GET /workspaces/{id}", a.session(http.HandlerFunc(a.handleGetWorkspace)))
	mux.Handle("GET /backends/", a.session(a.identityCompat(identity.KindBackend, http.HandlerFunc(a.handleBackendSub))))

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
	mux.Handle("GET /config/workspace-bridge", a.admin(http.HandlerFunc(a.handleGetWorkspaceBridgeConfig)))
	mux.Handle("PUT /config/workspace-bridge", a.admin(http.HandlerFunc(a.handlePutWorkspaceBridgeConfig)))

	// Mutation routes — admin role required.
	mux.Handle("PUT /agents/{prism_id}/backend-policies", a.admin(http.HandlerFunc(a.handleSetAgentBackendPolicies)))
	mux.Handle("PUT /agents/", a.admin(http.HandlerFunc(a.handlePutAgent)))
	mux.Handle("DELETE /agents/stale", a.admin(http.HandlerFunc(a.handleRemoveStaleAgents)))
	mux.Handle("DELETE /agents/", a.admin(http.HandlerFunc(a.handleDeleteAgent)))
	mux.Handle("PUT /groups/{name}/backend-policies", a.admin(a.identityCompat(identity.KindGroup, http.HandlerFunc(a.handleSetGroupBackendPolicies))))
	mux.Handle("PUT /groups/", a.admin(http.HandlerFunc(a.handleSetGroup)))
	mux.Handle("DELETE /groups/", a.admin(a.identityCompat(identity.KindGroup, http.HandlerFunc(a.handleDeleteGroup))))
	mux.Handle("PUT /defaults/backend-policies", a.admin(http.HandlerFunc(a.handleSetDefaultBackendPolicies)))
	mux.Handle("PUT /defaults", a.admin(http.HandlerFunc(a.handleSetDefaults)))
	mux.Handle("POST /workspaces", a.admin(http.HandlerFunc(a.handleCreateWorkspace)))
	mux.Handle("DELETE /workspaces/", a.admin(http.HandlerFunc(a.handleDeleteWorkspace)))
	// OpenAPI: preview is stateless (no {id}); diff and reimport are routed
	// through handleBackendPost suffix dispatch since they share the
	// /backends/{id}/ prefix with reconnect/workspace-changes.
	mux.Handle("POST /backends/preview-openapi", a.admin(http.HandlerFunc(a.handlePreviewOpenAPI)))
	// Curl scaffold: stateless helper that turns a curl command into a
	// starter OpenAPI 3.1 YAML spec. Lives outside /backends/ because it
	// doesn't bind to a particular backend id.
	mux.Handle("POST /openapi/scaffold-from-curl", a.admin(http.HandlerFunc(a.handleOpenAPIScaffold)))
	mux.Handle("POST /backends/", a.admin(http.HandlerFunc(a.handleBackendPost)))
	mux.Handle("PATCH /backends/", a.admin(a.identityCompat(identity.KindBackend, http.HandlerFunc(a.handlePatchBackend))))
	mux.Handle("DELETE /backends/", a.admin(a.identityCompat(identity.KindBackend, http.HandlerFunc(a.handleRemoveBackend))))

	// Binary backend ingestion. Upload (multipart) and URL-fetch routes both
	// land an ELF in the binstore and return its hash; the operator then
	// passes that hash to POST /backends/{id} via binary_hash. Metadata
	// lookup (GET /binaries/{hash}) lets the UI verify a hash survives
	// restart without re-uploading.
	mux.Handle("POST /binaries/upload", a.admin(http.HandlerFunc(a.handleBinaryUpload)))
	mux.Handle("POST /binaries/fetch", a.admin(http.HandlerFunc(a.handleBinaryFetch)))
	mux.Handle("GET /binaries/", a.session(http.HandlerFunc(a.handleBinaryGet)))

	// Identity dispatcher routes. Reads are session-gated (any signed-in
	// operator); writes are admin-gated. The session and admin wrappers
	// no-op when admin auth is disabled, so the routes still work in
	// "open" mode used by integration tests.
	mux.Handle("GET /identity", a.session(http.HandlerFunc(a.handleIdentityRoot)))
	mux.Handle("POST /identity", a.admin(http.HandlerFunc(a.handleIdentityRoot)))
	mux.Handle("GET /identity/", a.session(http.HandlerFunc(a.handleIdentitySub)))
	mux.Handle("PUT /identity/", a.admin(http.HandlerFunc(a.handleIdentitySub)))
	mux.Handle("DELETE /identity/", a.admin(http.HandlerFunc(a.handleIdentitySub)))

	// Grant templates and bindings (epic-3). The handler files are
	// non-nil but the manager itself is wired separately via
	// SetGrantManager — each handler returns 503 when unwired.
	mux.Handle("GET /grant-templates", a.session(http.HandlerFunc(a.handleListGrantTemplates)))
	mux.Handle("POST /grant-templates", a.admin(http.HandlerFunc(a.handleCreateGrantTemplate)))
	mux.Handle("GET /grant-templates/", a.session(http.HandlerFunc(a.handleGetGrantTemplate)))
	mux.Handle("PUT /grant-templates/", a.admin(http.HandlerFunc(a.handlePutGrantTemplate)))
	mux.Handle("DELETE /grant-templates/", a.admin(http.HandlerFunc(a.handleDeleteGrantTemplate)))

	mux.Handle("GET /grant-bindings", a.session(http.HandlerFunc(a.handleListGrantBindings)))
	mux.Handle("POST /grant-bindings", a.admin(http.HandlerFunc(a.handleCreateGrantBinding)))
	mux.Handle("GET /grant-bindings/", a.session(http.HandlerFunc(a.handleGetGrantBinding)))
	mux.Handle("PUT /grant-bindings/", a.admin(http.HandlerFunc(a.handlePutGrantBinding)))
	mux.Handle("DELETE /grant-bindings/", a.admin(http.HandlerFunc(a.handleDeleteGrantBinding)))

	// Policy builder and verb resolution (epic-4).
	a.registerPolicyRoutes(mux)

	// Agents triage summary (epic-4).
	mux.Handle("GET /agents/policy-summary", a.session(http.HandlerFunc(a.handleAgentsPolicySummary)))

	// Analytics surface (epic-3 + epic-4).
	mux.Handle("GET /analytics/status", a.session(http.HandlerFunc(a.handleAnalyticsStatus)))
	mux.Handle("GET /analytics/events", a.session(http.HandlerFunc(a.handleAnalyticsEvents)))
	mux.Handle("GET /analytics/events/tail", a.session(http.HandlerFunc(a.handleAnalyticsTail)))
	mux.Handle("GET /analytics/templates", a.session(http.HandlerFunc(a.handleAnalyticsTemplates)))
	mux.Handle("GET /analytics/templates/", a.session(http.HandlerFunc(a.handleAnalyticsTemplate)))
}

// handleBackendPost dispatches POST /backends/{id} and
// POST /backends/{id}/reconnect.
func (a *API) handleBackendPost(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/reconnect") {
		a.handleReconnectBackend(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/workspace-changes/refresh") {
		a.handleBackendWorkspaceRefresh(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/workspace-changes/apply") {
		a.handleBackendWorkspaceApply(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/workspace-changes/discard") {
		a.handleBackendWorkspaceDiscard(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/openapi-diff") {
		a.handleOpenAPIDiff(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/reimport") {
		a.handleOpenAPIReimport(w, r)
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
	writeJSON(w, http.StatusOK, a.withBackendDisplayNames(a.statusFn()))
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

// handleBackendSub dispatches GET /backends/{id}/auth-status,
// GET /backends/{id}/workspace-changes, GET /backends/{id}/openapi-source.
func (a *API) handleBackendSub(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/auth-status") {
		a.handleAuthStatus(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/workspace-changes") {
		a.handleBackendWorkspaceChanges(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/openapi-source") {
		a.handleOpenAPISource(w, r)
		return
	}
	http.NotFound(w, r)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
