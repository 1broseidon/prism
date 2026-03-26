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
	// Credential config for backend authentication
	Credential *CredentialConfig `json:"credential,omitempty"`
}

// CredentialConfig specifies how to authenticate with a backend.
// Write-only: the API accepts this on POST but never returns secret values.
type CredentialConfig struct {
	// Type: "none", "static", "env", "command"
	Type string `json:"type"`
	// Header to set. Default: "Authorization"
	Header string `json:"header,omitempty"`
	// Value is the literal secret (static type only). Write-only — never returned by the API.
	Value string `json:"value,omitempty"`
	// Env is the environment variable name (env type).
	Env string `json:"env,omitempty"`
	// Command is the shell command to execute (command type).
	Command string `json:"command,omitempty"`
}

// BackendCredentialInfo is the obfuscated credential metadata returned by GET /backends.
// Secret values are never included.
type BackendCredentialInfo struct {
	Type       string `json:"type"`                // "static", "env", "command", "none"
	Header     string `json:"header,omitempty"`    // which header is set
	Env        string `json:"env,omitempty"`       // env var name (env type only)
	Command    string `json:"command,omitempty"`   // shell command (command type only)
	Configured bool   `json:"configured"`          // true if a credential is registered
}

// BackendManager is the interface the admin API uses to mutate backends.
type BackendManager interface {
	AddBackend(ctx context.Context, id string, cfg BackendConfig) error
	RemoveBackend(id string) error
	// NotifyToolsChanged sends tools/list_changed to all MCP sessions,
	// causing clients to re-fetch their tool list with current policy.
	NotifyToolsChanged()
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
