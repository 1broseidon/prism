// Package admin provides the Prism admin API.
package admin

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"time"

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
	oauthCallbackHandler http.Handler // optional: gateway's OAuth callback handler
	startedAt            time.Time
}

// NewAPI creates an admin API.
// agentMgr, groupMgr, and oauthCallback are optional — when nil, their endpoints return 503/404.
func NewAPI(statusFn func() any, backendMgr BackendManager, agentsFn func() []any, removeFn func(string) bool, removeStaleFn func() int, eventsFn func() []any, agentMgr AgentManager, groupMgr GroupManager, oauthCallback http.Handler) *API {
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
		startedAt:            time.Now(),
	}
}

// Handler returns the admin HTTP handler.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.handleDashboard)
	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("GET /backends", a.handleBackends)
	mux.HandleFunc("GET /info", a.handleInfo)
	mux.HandleFunc("GET /agents", a.handleAgents)
	mux.HandleFunc("GET /agents/", a.handleAgentByPrismID)
	mux.HandleFunc("PUT /agents/", a.handlePutAgent)
	mux.HandleFunc("DELETE /agents/stale", a.handleRemoveStaleAgents)
	mux.HandleFunc("DELETE /agents/", a.handleDeleteAgent)
	mux.HandleFunc("GET /events", a.handleEvents)
	mux.HandleFunc("GET /groups", a.handleListGroups)
	mux.HandleFunc("GET /groups/", a.handleGetGroup)
	mux.HandleFunc("PUT /groups/", a.handleSetGroup)
	mux.HandleFunc("DELETE /groups/", a.handleDeleteGroup)
	mux.HandleFunc("GET /defaults", a.handleDefaults)
	mux.HandleFunc("PUT /defaults", a.handleSetDefaults)
	mux.HandleFunc("POST /backends/", a.handleAddBackend)
	mux.HandleFunc("DELETE /backends/", a.handleRemoveBackend)
	mux.HandleFunc("GET /backends/", a.handleBackendSub)
	if a.oauthCallbackHandler != nil {
		mux.Handle("GET /oauth/callback", a.oauthCallbackHandler)
	}
	if metrics.Enabled() {
		mux.Handle("GET /metrics", metrics.Handler())
	}
	return mux
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

// isPolicyPath returns true if the URL path matches /agents/{id}/policy.
func isPolicyPath(path string) bool {
	// Path: /agents/{prism_id}/policy
	trimmed := path
	if trimmed != "" && trimmed[len(trimmed)-1] == '/' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return len(trimmed) > len("/agents/") && hasSuffix(trimmed, "/policy")
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) handleBackends(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.statusFn())
}

func (a *API) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":       "prism",
		"version":    "0.1.0",
		"go_version": runtime.Version(),
		"uptime":     time.Since(a.startedAt).String(),
		"goroutines": runtime.NumGoroutine(),
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
