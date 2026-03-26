package admin

import (
	"embed"
	"encoding/json"
	"net/http"
	"strings"
)

//go:embed ui.html
var dashboardHTML embed.FS

func (a *API) handleDashboard(w http.ResponseWriter, _ *http.Request) {
	data, err := dashboardHTML.ReadFile("ui.html")
	if err != nil {
		http.Error(w, "dashboard not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (a *API) handleAgents(w http.ResponseWriter, _ *http.Request) {
	if a.agentsFn == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, a.agentsFn())
}

func (a *API) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	if a.updateFn == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent management not available"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	id := strings.TrimPrefix(r.URL.Path, "/agents/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent id required"})
		return
	}

	var body struct {
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if !a.updateFn(id, body.Scopes) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "agent": id})
}

func (a *API) handleEvents(w http.ResponseWriter, _ *http.Request) {
	if a.eventsFn == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, a.eventsFn())
}
