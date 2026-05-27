package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/identity"
)

// handleListGroups handles GET /groups — list all groups with source info.
func (a *API) handleListGroups(w http.ResponseWriter, _ *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}
	writeJSON(w, http.StatusOK, a.withGroupIdentities(a.groupMgr.ListGroups()))
}

func (a *API) withGroupIdentities(groups []GroupInfo) []GroupInfo {
	if a.identity == nil {
		return groups
	}
	for i := range groups {
		ent, ok := a.resolveListIdentity(identity.KindGroup, groups[i].Name)
		if !ok {
			continue
		}
		groups[i].ID = ent.ID
		groups[i].DisplayName = ent.DisplayName
	}
	return groups
}

// handleGetGroup handles GET /groups/{name} — single group details.
func (a *API) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/groups/")
	if !isValidID(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid group name"})
		return
	}

	g := a.groupMgr.GetGroup(name)
	if g == nil {
		// The URL after the compat redirect is a ULID; fall back to the
		// display name when the underlying group manager indexes by name
		// only (unit-test fakes; pre-migration legacy data).
		if alt := a.resolveSubjectName(subjectTypeGroups, name); alt != name {
			g = a.groupMgr.GetGroup(alt)
		}
	}
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
	if !isValidID(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid group name"})
		return
	}
	name = a.resolveSubjectName(subjectTypeGroups, name)

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
//
// Refuses with 409 (Conflict) when at least one agent still claims
// membership. Operators must remove every member via PUT
// /agents/{prism_id}/policy before the delete succeeds.
func (a *API) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/groups/")
	if !isValidID(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid group name"})
		return
	}
	// Translate ULID URLs back to the operator-facing name for the
	// agent-membership scan + delete path, which uses display_name keys.
	name = a.resolveSubjectName(subjectTypeGroups, name)

	if members := a.countAgentsInGroup(name); members > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   fmt.Sprintf("group %q still has %d members; remove them before deleting", name, members),
			"members": members,
		})
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

// countAgentsInGroup snapshots ListAgents and returns the count of agents
// whose policy.Groups contains name. Returns 0 when AgentManager is
// unwired so the delete is permitted in tests that don't exercise the
// guard.
func (a *API) countAgentsInGroup(name string) int {
	if a.agentMgr == nil {
		return 0
	}
	count := 0
	for _, raw := range a.agentMgr.ListAgents() {
		_, groups := agentGroupsFor(raw)
		for _, g := range groups {
			if g == name {
				count++
				break
			}
		}
	}
	return count
}

// handleDefaults handles GET /defaults — return current default scopes and
// per-backend defaults.
func (a *API) handleDefaults(w http.ResponseWriter, _ *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"default_scopes":   a.groupMgr.DefaultScopes(),
		"backend_policies": a.groupMgr.DefaultBackendPolicies(),
	})
}

// handleSetGroupBackendPolicies handles PUT /groups/{name}/backend-policies.
func (a *API) handleSetGroupBackendPolicies(w http.ResponseWriter, r *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	path := strings.TrimPrefix(r.URL.Path, "/groups/")
	name := strings.TrimSuffix(path, "/backend-policies")
	if name == path || !isValidID(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /groups/{name}/backend-policies with a valid group name"})
		return
	}
	var body map[string]auth.BackendPolicy
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := a.groupMgr.SetGroupBackendPolicies(name, body); err != nil {
		if strings.Contains(err.Error(), "config-defined") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "group": name})
}

// handleSetDefaultBackendPolicies handles PUT /defaults/backend-policies.
func (a *API) handleSetDefaultBackendPolicies(w http.ResponseWriter, r *http.Request) {
	if a.groupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "group management not available"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var body map[string]auth.BackendPolicy
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := a.groupMgr.SetDefaultBackendPolicies(body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
