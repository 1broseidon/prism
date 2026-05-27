package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/1broseidon/prism/internal/auth"
)

func (a *API) handleListGrantTemplates(w http.ResponseWriter, r *http.Request) {
	if a.grantMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "grant management not available"})
		return
	}
	tool := r.URL.Query().Get("tool")
	backend := r.URL.Query().Get("backend")
	out := make([]auth.GrantTemplate, 0)
	for _, t := range a.grantMgr.ListGrantTemplates() {
		if tool != "" && t.Spec.Tool != tool {
			continue
		}
		if backend != "" && t.Spec.Backend != backend {
			continue
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleGetGrantTemplate(w http.ResponseWriter, r *http.Request) {
	if a.grantMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "grant management not available"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/grant-templates/by-hash/") {
		hash := strings.TrimPrefix(r.URL.Path, "/grant-templates/by-hash/")
		t, err := a.grantMgr.GetGrantTemplateByHash(hash)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
			return
		}
		writeJSON(w, http.StatusOK, t)
		return
	}
	id, version, ok := parseTemplatePath(r.URL.Path)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /grant-templates/{id} or /grant-templates/{id}/{version}"})
		return
	}
	if version == 0 {
		versions := make([]auth.GrantTemplate, 0)
		for _, t := range a.grantMgr.ListGrantTemplates() {
			if t.ID == id {
				versions = append(versions, t)
			}
		}
		for i, j := 0, len(versions)-1; i < j; i, j = i+1, j-1 {
			versions[i], versions[j] = versions[j], versions[i]
		}
		if len(versions) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
			return
		}
		writeJSON(w, http.StatusOK, versions)
		return
	}
	t, err := a.grantMgr.GetGrantTemplate(id, version)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *API) handleCreateGrantTemplate(w http.ResponseWriter, r *http.Request) {
	if a.grantMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "grant management not available"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var body auth.GrantTemplate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	body.Hash = ""
	t, err := a.grantMgr.SaveGrantTemplate(body)
	if err != nil {
		writeJSON(w, grantWriteStatus(err), map[string]string{"error": err.Error()})
		return
	}
	// Template writes change which tools/where-clauses any bound subject
	// can exercise; drop the policy-summary cache so Agents listings
	// reflect the new shape immediately.
	a.invalidateAllAgentPolicySummaries()
	writeJSON(w, http.StatusCreated, t)
}

func (a *API) handlePutGrantTemplate(w http.ResponseWriter, r *http.Request) {
	if a.grantMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "grant management not available"})
		return
	}
	id, _, ok := parseTemplatePath(r.URL.Path)
	if !ok || strings.Contains(strings.TrimPrefix(r.URL.Path, "/grant-templates/"), "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /grant-templates/{id}"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var body auth.GrantTemplate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	body.ID = id
	body.Hash = ""
	t, err := a.grantMgr.SaveGrantTemplate(body)
	if err != nil {
		writeJSON(w, grantWriteStatus(err), map[string]string{"error": err.Error()})
		return
	}
	// New template version rotates the hash bound subjects resolve through;
	// drop the policy-summary cache so the Agents listing recomputes.
	a.invalidateAllAgentPolicySummaries()
	writeJSON(w, http.StatusOK, t)
}

func (a *API) handleDeleteGrantTemplate(w http.ResponseWriter, r *http.Request) {
	if a.grantMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "grant management not available"})
		return
	}
	id, version, ok := parseTemplatePath(r.URL.Path)
	if !ok || version == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /grant-templates/{id}/{version}"})
		return
	}
	if err := a.grantMgr.DeleteGrantTemplate(id, version); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Removing a template version can revoke capabilities from any subject
	// bound to that hash; evict so Agents listings reflect the change.
	a.invalidateAllAgentPolicySummaries()
	w.WriteHeader(http.StatusNoContent)
}

func parseTemplatePath(path string) (id string, version int, ok bool) {
	rest := strings.TrimPrefix(path, "/grant-templates/")
	parts := strings.Split(rest, "/")
	if len(parts) != 1 && len(parts) != 2 {
		return "", 0, false
	}
	if !isValidID(parts[0]) {
		return "", 0, false
	}
	if len(parts) == 1 {
		return parts[0], 0, true
	}
	v, err := strconv.Atoi(parts[1])
	if err != nil || v <= 0 {
		return "", 0, false
	}
	return parts[0], v, true
}

func grantWriteStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	msg := err.Error()
	if strings.Contains(msg, "already exists") || strings.Contains(msg, "does not match latest") || strings.Contains(msg, "mismatch") {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}
