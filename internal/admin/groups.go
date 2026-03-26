package admin

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleListGroups handles GET /groups — list all groups with source info.
func (a *API) handleListGroups(w http.ResponseWriter, _ *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}
	writeJSON(w, http.StatusOK, a.groupMgr.ListGroups())
}

// handleGetGroup handles GET /groups/{name} — single group details.
func (a *API) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/groups/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "group name required"})
		return
	}

	g := a.groupMgr.GetGroup(name)
	if g == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
		return
	}

	writeJSON(w, http.StatusOK, g)
}

// handleSetGroup handles PUT /groups/{name} — create or update a dynamic group.
func (a *API) handleSetGroup(w http.ResponseWriter, r *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	name := strings.TrimPrefix(r.URL.Path, "/groups/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "group name required"})
		return
	}

	var body struct {
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if err := a.groupMgr.SetGroup(name, body.Scopes); err != nil {
		if strings.Contains(err.Error(), "config-defined") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if a.backendMgr != nil {
		a.backendMgr.NotifyToolsChanged()
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "group": name})
}

// handleDeleteGroup handles DELETE /groups/{name} — delete a dynamic group.
func (a *API) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/groups/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "group name required"})
		return
	}

	if err := a.groupMgr.DeleteGroup(name); err != nil {
		if strings.Contains(err.Error(), "config-defined") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if a.backendMgr != nil {
		a.backendMgr.NotifyToolsChanged()
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "deleted": name})
}

// handleDefaults handles GET /defaults — return current default scopes.
func (a *API) handleDefaults(w http.ResponseWriter, _ *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"default_scopes": a.groupMgr.DefaultScopes()})
}

// handleSetDefaults handles PUT /defaults — update runtime default scopes.
func (a *API) handleSetDefaults(w http.ResponseWriter, r *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var body struct {
		DefaultScopes []string `json:"default_scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if err := a.groupMgr.SetDefaultScopes(body.DefaultScopes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if a.backendMgr != nil {
		a.backendMgr.NotifyToolsChanged()
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
