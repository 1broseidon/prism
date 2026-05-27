package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/1broseidon/prism/internal/auth"
)

func (a *API) handleListGrantBindings(w http.ResponseWriter, r *http.Request) {
	if a.grantMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "grant management not available"})
		return
	}
	templateID := r.URL.Query().Get("template")
	group := r.URL.Query().Get("group")
	agent := r.URL.Query().Get("agent")
	out := make([]auth.GrantBinding, 0)
	for _, b := range a.grantMgr.ListGrantBindings() {
		if templateID != "" && b.TemplateID != templateID {
			continue
		}
		if group != "" && !containsString(b.Subjects.Groups, group) {
			continue
		}
		if agent != "" && !containsString(b.Subjects.AgentIDs, agent) {
			continue
		}
		out = append(out, b)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleGetGrantBinding(w http.ResponseWriter, r *http.Request) {
	if a.grantMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "grant management not available"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/grant-bindings/")
	if !isValidID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid binding id"})
		return
	}
	b, err := a.grantMgr.GetGrantBinding(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "binding not found"})
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (a *API) handleCreateGrantBinding(w http.ResponseWriter, r *http.Request) {
	a.writeGrantBinding(w, r, "")
}

func (a *API) handlePutGrantBinding(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/grant-bindings/")
	if !isValidID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid binding id"})
		return
	}
	a.writeGrantBinding(w, r, id)
}

func (a *API) writeGrantBinding(w http.ResponseWriter, r *http.Request, id string) {
	if a.grantMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "grant management not available"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var body auth.GrantBinding
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if id != "" {
		body.ID = id
	}
	latest, err := latestGrantTemplate(a.grantMgr, body.TemplateID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.TemplateHash != "" && body.TemplateHash != latest.Hash {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "template_hash does not match latest template version"})
		return
	}
	body.TemplateHash = latest.Hash
	body.TemplateID = latest.ID
	b, err := a.grantMgr.SetGrantBinding(body)
	if err != nil {
		writeJSON(w, grantWriteStatus(err), map[string]string{"error": err.Error()})
		return
	}
	// Binding writes shift the subject set + template hash for whichever
	// agents resolve through this binding; the cheapest correct policy is
	// to drop the whole policy-summary cache so the next Agents listing
	// reflects the change without waiting for the 60s TTL.
	a.invalidateAllAgentPolicySummaries()
	status := http.StatusCreated
	if id != "" {
		status = http.StatusOK
	}
	writeJSON(w, status, b)
}

func (a *API) handleDeleteGrantBinding(w http.ResponseWriter, r *http.Request) {
	if a.grantMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "grant management not available"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/grant-bindings/")
	if !isValidID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid binding id"})
		return
	}
	if err := a.grantMgr.DeleteGrantBinding(id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Removing a binding can reduce an agent's capability count; evict so
	// the Agents listing doesn't show a stale cap number for up to 60s.
	a.invalidateAllAgentPolicySummaries()
	w.WriteHeader(http.StatusNoContent)
}

func latestGrantTemplate(m GrantManager, id string) (auth.GrantTemplate, error) {
	if strings.TrimSpace(id) == "" {
		return auth.GrantTemplate{}, errors.New("template_id is required")
	}
	var latest auth.GrantTemplate
	for _, t := range m.ListGrantTemplates() {
		if t.ID == id && t.Version > latest.Version {
			latest = t
		}
	}
	if latest.Version == 0 {
		return auth.GrantTemplate{}, errors.New("template_id not found")
	}
	return latest, nil
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
