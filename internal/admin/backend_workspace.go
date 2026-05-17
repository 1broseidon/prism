package admin

import (
	"context"
	"net/http"
	"strings"

	ws "github.com/1broseidon/prism/internal/workspace"
)

// BackendWorkspaceManager is implemented by gateways that can stage and apply
// sandbox workspace changes for bridge-managed stdio backends.
type BackendWorkspaceManager interface {
	BackendWorkspaceChanges(ctx context.Context, id string, refresh bool) (*ws.ChangeSet, error)
	ApplyBackendWorkspaceChanges(ctx context.Context, id string) (*ws.ApplyResult, error)
	DiscardBackendWorkspaceChanges(ctx context.Context, id string) error
}

func (a *API) handleBackendWorkspaceChanges(w http.ResponseWriter, r *http.Request) {
	id, ok := backendSubID(r.URL.Path, "/workspace-changes")
	if !ok {
		http.NotFound(w, r)
		return
	}
	mgr, ok := a.backendMgr.(BackendWorkspaceManager)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace changes are not available"})
		return
	}
	changes, err := mgr.BackendWorkspaceChanges(r.Context(), id, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, changes)
}

func (a *API) handleBackendWorkspaceRefresh(w http.ResponseWriter, r *http.Request) {
	id, ok := backendSubID(r.URL.Path, "/workspace-changes/refresh")
	if !ok {
		http.NotFound(w, r)
		return
	}
	mgr, ok := a.backendMgr.(BackendWorkspaceManager)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace changes are not available"})
		return
	}
	changes, err := mgr.BackendWorkspaceChanges(r.Context(), id, true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, changes)
}

func (a *API) handleBackendWorkspaceApply(w http.ResponseWriter, r *http.Request) {
	id, ok := backendSubID(r.URL.Path, "/workspace-changes/apply")
	if !ok {
		http.NotFound(w, r)
		return
	}
	mgr, ok := a.backendMgr.(BackendWorkspaceManager)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace changes are not available"})
		return
	}
	result, err := mgr.ApplyBackendWorkspaceChanges(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleBackendWorkspaceDiscard(w http.ResponseWriter, r *http.Request) {
	id, ok := backendSubID(r.URL.Path, "/workspace-changes/discard")
	if !ok {
		http.NotFound(w, r)
		return
	}
	mgr, ok := a.backendMgr.(BackendWorkspaceManager)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace changes are not available"})
		return
	}
	if err := mgr.DiscardBackendWorkspaceChanges(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func backendSubID(path, suffix string) (string, bool) {
	trimmed := strings.TrimPrefix(path, "/backends/")
	if trimmed == path || !strings.HasSuffix(trimmed, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(trimmed, suffix)
	id = strings.TrimSuffix(id, "/")
	if !isValidID(id) {
		return "", false
	}
	return id, true
}
