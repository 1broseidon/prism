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
	Runtime string            `json:"runtime,omitempty"`
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
	Type       string `json:"type"`              // "static", "env", "command", "none"
	Header     string `json:"header,omitempty"`  // which header is set
	Env        string `json:"env,omitempty"`     // env var name (env type only)
	Command    string `json:"command,omitempty"` // shell command (command type only)
	Configured bool   `json:"configured"`        // true if a credential is registered
}

// BackendManager is the interface the admin API uses to mutate backends.
type BackendManager interface {
	AddBackend(ctx context.Context, id string, cfg BackendConfig) error
	RemoveBackend(id string) error
	// NotifyToolsChanged sends tools/list_changed to all MCP sessions,
	// causing clients to re-fetch their tool list with current policy.
	NotifyToolsChanged()
}

// callbackBaseFromRequest derives the externally-reachable base URL the OAuth
// provider should redirect to, using the inbound request. This is what makes
// the auth flow host-aware: when an operator hits the admin at
// http://172.16.30.90:9086, the callback returns to the same host instead of
// the configured fallback (which defaults to localhost). Honors common
// reverse-proxy headers so deployments behind a proxy work too.
func callbackBaseFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// OAuthProber is an optional interface that BackendManager may implement
// to support probing backends for OAuth authentication requirements.
type OAuthProber interface {
	// ProbeBackendOAuth probes a URL. If the backend requires OAuth, returns
	// (authURL, state, nil). If no OAuth is needed, returns ("", "", nil).
	// callbackBase is the externally-reachable scheme+host the provider should
	// redirect to (e.g. "http://172.16.30.90:9086"); empty falls back to the
	// configured admin_public_url.
	ProbeBackendOAuth(ctx context.Context, backendID, url, callbackBase string) (authURL, state string, err error)
	// AuthFlowStatus returns the status of an OAuth flow for a backend.
	// Returns "pending", "connected", "failed:{reason}", or "".
	AuthFlowStatus(backendID string) string
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

	// Check for auth-status sub-path: GET is handled separately but POST /backends/{id}/auth-status
	// should not be valid. Only strip /auth-status for the GET handler below.
	if strings.HasSuffix(id, "/auth-status") {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET for auth-status"})
		return
	}

	var cfg BackendConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	// If a URL is provided with no explicit credential, probe for OAuth.
	if cfg.URL != "" && cfg.Credential == nil {
		if prober, ok := a.backendMgr.(OAuthProber); ok {
			authURL, state, err := prober.ProbeBackendOAuth(r.Context(), id, cfg.URL, callbackBaseFromRequest(r))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "probe failed: " + err.Error()})
				return
			}
			if authURL != "" {
				// Backend requires OAuth — return auth_required with the URL.
				writeJSON(w, http.StatusOK, map[string]any{
					"status":     "auth_required",
					"auth_url":   authURL,
					"state":      state,
					"backend_id": id,
				})
				return
			}
			// No OAuth needed, fall through to normal add.
		}
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

// handleAuthStatus returns the current OAuth flow status for a backend.
// GET /backends/{id}/auth-status
func (a *API) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	// Path: /backends/{id}/auth-status
	path := strings.TrimPrefix(r.URL.Path, "/backends/")
	id := strings.TrimSuffix(path, "/auth-status")
	if id == "" || id == path {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid path"})
		return
	}

	prober, ok := a.backendMgr.(OAuthProber)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "OAuth not available"})
		return
	}

	status := prober.AuthFlowStatus(id)
	if status == "" {
		status = "unknown"
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}
