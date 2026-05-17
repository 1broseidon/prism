package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/adminauth"
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

// WorkspaceCreateRequest creates a remote-only workspace registry entry.
type WorkspaceCreateRequest struct {
	ID               string   `json:"id"`
	Type             string   `json:"type"`
	Owner            string   `json:"owner,omitempty"`
	AllowedAgents    []string `json:"allowed_agents,omitempty"`
	AllowedTemplates []string `json:"allowed_templates,omitempty"`
	QuotaBytes       int64    `json:"quota_bytes,omitempty"`
	RetentionSeconds int64    `json:"retention_seconds,omitempty"`
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
	ID               string                   `json:"id"`
	Type             string                   `json:"type,omitempty"`
	Owner            string                   `json:"owner,omitempty"`
	AllowedAgents    []string                 `json:"allowed_agents,omitempty"`
	AllowedTemplates []string                 `json:"allowed_templates,omitempty"`
	QuotaBytes       int64                    `json:"quota_bytes,omitempty"`
	UsedBytes        int64                    `json:"used_bytes,omitempty"`
	RetentionSeconds int64                    `json:"retention_seconds,omitempty"`
	Hostname         string                   `json:"hostname,omitempty"`
	Root             string                   `json:"root,omitempty"`
	Version          string                   `json:"version,omitempty"`
	CreatedAt        time.Time                `json:"created_at,omitempty"`
	LastSeen         time.Time                `json:"last_seen,omitempty"`
	Connected        bool                     `json:"connected"`
	HealthStatus     string                   `json:"health_status,omitempty"`
	Backends         []WorkspaceBackendStatus `json:"backends,omitempty"`
}

// WorkspaceHealth* are the values reported in WorkspaceStatus.HealthStatus.
const (
	WorkspaceHealthOK            = "ok"
	WorkspaceHealthQuotaWarn     = "quota_warn"
	WorkspaceHealthQuotaExceeded = "quota_exceeded"
	WorkspaceHealthStale         = "stale"
)

// WorkspaceBridgeManager is implemented by the gateway.
type WorkspaceBridgeManager interface {
	WorkspaceBridgeConfig() WorkspaceBridgeConfigView
	SetWorkspaceBridgeConfig(WorkspaceBridgeUpdate) (WorkspaceBridgeConfigView, error)
	CreateWorkspace(context.Context, WorkspaceCreateRequest) (WorkspaceStatus, error)
	ListWorkspaces() []WorkspaceStatus
	DisconnectWorkspace(id string) bool
}

// WorkspaceReversePolicyLookup returns policy entries that pin to a
// workspace id via "id:<workspace-id>" selectors. Powers the admin
// workspace detail page's "selected by" section.
type WorkspaceReversePolicyLookup interface {
	WorkspacePolicyReferences(workspaceID string) []WorkspacePolicyReference
}

// WorkspacePolicyReference mirrors authserver.WorkspacePolicyReference so
// admin/web doesn't import authserver.
type WorkspacePolicyReference struct {
	Source    string `json:"source"`
	BackendID string `json:"backend_id"`
	Selector  string `json:"selector"`
}

// WorkspaceDetail bundles a single workspace's status with the policy
// entries that explicitly target it.
type WorkspaceDetail struct {
	Workspace  WorkspaceStatus            `json:"workspace"`
	References []WorkspacePolicyReference `json:"references"`
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

// handleGetWorkspace returns a single workspace's status plus the policy
// entries that explicitly pin to it via id:<workspace-id>.
func (a *API) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !workspaceDetailIDRE.MatchString(id) {
		http.Error(w, "invalid workspace id", http.StatusBadRequest)
		return
	}
	mgr, ok := a.backendMgr.(WorkspaceBridgeManager)
	if !ok {
		http.Error(w, "workspace bridge settings not available", http.StatusServiceUnavailable)
		return
	}
	var found *WorkspaceStatus
	list := mgr.ListWorkspaces()
	for i := range list {
		if list[i].ID == id {
			found = &list[i]
			break
		}
	}
	if found == nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	detail := WorkspaceDetail{Workspace: *found}
	if a.workspaceLookup != nil {
		detail.References = a.workspaceLookup.WorkspacePolicyReferences(id)
	}
	writeJSON(w, http.StatusOK, detail)
}

// workspaceDetailIDRE matches the same shape the gateway uses for ids;
// kept private here to avoid an import cycle.
var workspaceDetailIDRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func (a *API) handleListWorkspaces(w http.ResponseWriter, _ *http.Request) {
	mgr, ok := a.backendMgr.(WorkspaceBridgeManager)
	if !ok {
		http.Error(w, "workspace bridge settings not available", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, mgr.ListWorkspaces())
}

func (a *API) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	mgr, ok := a.backendMgr.(WorkspaceBridgeManager)
	if !ok {
		http.Error(w, "workspace bridge settings not available", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req WorkspaceCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Owner == "" {
		if sess := adminauth.FromContext(r.Context()); sess != nil {
			req.Owner = sess.Email
		}
	}
	status, err := mgr.CreateWorkspace(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, status)
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
