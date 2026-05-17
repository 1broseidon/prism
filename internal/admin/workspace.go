package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// WorkspaceBridgeConfigView is the operator-facing runtime configuration for
// local workspace bridges. The token is write-only; the UI only learns whether
// one is configured.
type WorkspaceBridgeConfigView struct {
	Enabled  bool `json:"enabled"`
	TokenSet bool `json:"token_set"`
}

// WorkspaceBridgeUpdate is the write shape for /config/workspace-bridge.
// Empty Token means keep the existing token.
type WorkspaceBridgeUpdate struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token,omitempty"`
}

// WorkspaceToolStatus is a tool exposed by a connected workspace bridge.
type WorkspaceToolStatus struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// WorkspaceBackendStatus is one stdio MCP server connected through a
// workspace bridge process.
type WorkspaceBackendStatus struct {
	ID        string                `json:"id"`
	Namespace string                `json:"namespace"`
	Tools     []WorkspaceToolStatus `json:"tools,omitempty"`
}

// WorkspaceStatus is shown in the admin console.
type WorkspaceStatus struct {
	ID        string                   `json:"id"`
	Hostname  string                   `json:"hostname,omitempty"`
	Root      string                   `json:"root,omitempty"`
	Version   string                   `json:"version,omitempty"`
	LastSeen  time.Time                `json:"last_seen,omitempty"`
	Connected bool                     `json:"connected"`
	Backends  []WorkspaceBackendStatus `json:"backends,omitempty"`
}

// WorkspaceBridgeManager is implemented by the gateway.
type WorkspaceBridgeManager interface {
	WorkspaceBridgeConfig() WorkspaceBridgeConfigView
	SetWorkspaceBridgeConfig(WorkspaceBridgeUpdate) (WorkspaceBridgeConfigView, error)
	ListWorkspaces() []WorkspaceStatus
	DisconnectWorkspace(id string) bool
}

func (a *API) handleGetWorkspaceBridgeConfig(w http.ResponseWriter, _ *http.Request) {
	mgr, ok := a.backendMgr.(WorkspaceBridgeManager)
	if !ok {
		http.Error(w, "workspace bridge settings not available", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, mgr.WorkspaceBridgeConfig())
}

func (a *API) handlePutWorkspaceBridgeConfig(w http.ResponseWriter, r *http.Request) {
	mgr, ok := a.backendMgr.(WorkspaceBridgeManager)
	if !ok {
		http.Error(w, "workspace bridge settings not available", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var next WorkspaceBridgeUpdate
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	view, err := mgr.SetWorkspaceBridgeConfig(next)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) handleListWorkspaces(w http.ResponseWriter, _ *http.Request) {
	mgr, ok := a.backendMgr.(WorkspaceBridgeManager)
	if !ok {
		http.Error(w, "workspace bridge settings not available", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, mgr.ListWorkspaces())
}

func (a *API) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	mgr, ok := a.backendMgr.(WorkspaceBridgeManager)
	if !ok {
		http.Error(w, "workspace bridge settings not available", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/workspaces/")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "invalid workspace id", http.StatusBadRequest)
		return
	}
	if !mgr.DisconnectWorkspace(id) {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
