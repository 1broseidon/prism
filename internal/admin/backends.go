package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// BackendConfig is the JSON body for adding a backend at runtime.
type BackendConfig struct {
	// Standard MCP fields
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// BackendManager is the interface the admin API uses to mutate backends.
type BackendManager interface {
	AddBackend(ctx context.Context, id string, cfg BackendConfig) error
	RemoveBackend(id string) error
}

func (a *API) handleAddBackend(w http.ResponseWriter, r *http.Request) {
	if a.backendMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backend management not available"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	// Extract backend ID from path: POST /backends/{id}
	id := strings.TrimPrefix(r.URL.Path, "/backends/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backend id is required in path: POST /backends/{id}"})
		return
	}

	var cfg BackendConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if err := a.backendMgr.AddBackend(r.Context(), id, cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok", "id": id})
}

func (a *API) handleRemoveBackend(w http.ResponseWriter, r *http.Request) {
	if a.backendMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backend management not available"})
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/backends/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backend id is required in path: DELETE /backends/{id}"})
		return
	}

	if err := a.backendMgr.RemoveBackend(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "id": id})
}
