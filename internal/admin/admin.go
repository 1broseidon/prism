// Package admin provides the Prism admin API.
package admin

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"
)

// StatusProvider provides backend status info.
type StatusProvider interface {
	Status() []any
}

// API exposes admin endpoints.
type API struct {
	statusFn   func() any
	backendMgr BackendManager
	startedAt  time.Time
}

// NewAPI creates an admin API. statusFn returns the current backend status.
// backendMgr enables runtime backend add/remove (may be nil).
func NewAPI(statusFn func() any, backendMgr BackendManager) *API {
	return &API{
		statusFn:   statusFn,
		backendMgr: backendMgr,
		startedAt:  time.Now(),
	}
}

// Handler returns the admin HTTP handler.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("GET /backends", a.handleBackends)
	mux.HandleFunc("GET /info", a.handleInfo)
	mux.HandleFunc("POST /backends/", a.handleAddBackend)
	mux.HandleFunc("DELETE /backends/", a.handleRemoveBackend)
	return mux
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
