package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/config"
)

// readAddBackendBody buffers the POST /backends/{id} body so the handler can
// peek for an openapi-typed payload before deciding which schema to decode.
// The cap stays at 64KB for the conventional shape; once we know the payload
// declares type=="openapi" we re-arm the reader with the larger OpenAPI cap.
func readAddBackendBody(r *http.Request) ([]byte, error) {
	// First pass: small cap, enough to inspect type. If the body actually
	// fits we go straight to decode; otherwise we re-buffer with the bigger
	// cap and confirm it's openapi-typed before allowing the larger payload
	// through.
	smallLimit := int64(64 * 1024)
	probe, err := io.ReadAll(io.LimitReader(r.Body, smallLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(probe)) <= smallLimit {
		return probe, nil
	}
	// Quick sniff: only openapi-typed bodies are allowed past the 64KB cap.
	// Trailing bytes past smallLimit may not yet be valid JSON, but the
	// peek window already includes the document head, so the "type"
	// keyword (if present near the top of the JSON object) is visible.
	if !bytes.Contains(probe, []byte(`"type"`)) || !bytes.Contains(probe, []byte(`"openapi"`)) {
		return nil, errors.New("request body exceeds 64KB limit")
	}
	// Re-arm with the larger limit, then drain the rest.
	rest, err := io.ReadAll(io.LimitReader(r.Body, openAPIRequestBodyLimit-smallLimit))
	if err != nil {
		return nil, err
	}
	// Use a fresh slice rather than appending onto probe; gocritic flags the
	// in-place append here as appendAssign because probe is otherwise unused
	// after this point, and the explicit allocation is clearer.
	full := make([]byte, 0, len(probe)+len(rest))
	full = append(full, probe...)
	full = append(full, rest...)
	if int64(len(full)) > openAPIRequestBodyLimit {
		return nil, errors.New("request body exceeds openapi limit")
	}
	return full, nil
}

// BackendConfig is the JSON body for adding a backend at runtime.
type BackendConfig struct {
	// Standard MCP fields
	Command   string                  `json:"command,omitempty"`
	Args      []string                `json:"args,omitempty"`
	Env       map[string]string       `json:"env,omitempty"`
	URL       string                  `json:"url,omitempty"`
	Runtime   string                  `json:"runtime,omitempty"`
	Enabled   *bool                   `json:"enabled,omitempty"`
	Sandbox   *config.SandboxConfig   `json:"sandbox,omitempty"`
	Workspace *config.WorkspaceConfig `json:"workspace,omitempty"`
	// Credential config for backend authentication
	Credential *CredentialConfig `json:"credential,omitempty"`
	// OAuthClientID, when set, skips DCR and uses these credentials directly.
	// Required for providers that don't support Dynamic Client Registration
	// (GitHub, most identity providers without DCR enabled). The operator
	// pre-registers prism with the provider and pastes the values here.
	OAuthClientID     string `json:"oauth_client_id,omitempty"`
	OAuthClientSecret string `json:"oauth_client_secret,omitempty"`

	// Binary backend fields. BinaryHash discriminates: when non-empty the
	// gateway treats this as a prism-managed binary backend, mounts the
	// binstore read-only into the sandbox, and ignores command/url/openapi.
	// BinaryArgs is the parsed argv (shell-style split happens admin-side
	// from the operator's textbox); BinaryName is the operator's display
	// name for the binary (also used as the in-container leaf path).
	// BinarySource carries "upload" or "url" so the UI can render the right
	// re-fetch affordance later.
	BinaryHash      string   `json:"binary_hash,omitempty"`
	BinaryArgs      []string `json:"binary_args,omitempty"`
	BinaryName      string   `json:"binary_name,omitempty"`
	BinarySource    string   `json:"binary_source,omitempty"`
	BinarySourceURL string   `json:"binary_source_url,omitempty"`
}

// BackendUpdate is the PATCH /backends/{id} body for operational settings.
//
// DisabledTools uses *[]string so the handler can distinguish "field omitted"
// (nil — don't change anything) from "set to empty" (&[]string{} — enable
// every tool). A non-empty slice is the literal list of bare tool names to
// switch off.
type BackendUpdate struct {
	Enabled       *bool                   `json:"enabled,omitempty"`
	Sandbox       *config.SandboxConfig   `json:"sandbox,omitempty"`
	Workspace     *config.WorkspaceConfig `json:"workspace,omitempty"`
	DisabledTools *[]string               `json:"disabled_tools,omitempty"`
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

// BackendReconnector is implemented by backend managers that can reconnect a
// KV-persisted backend without deleting its stored config or OAuth tokens.
type BackendReconnector interface {
	ReconnectBackend(ctx context.Context, id string) error
}

// BackendSettingsUpdater is implemented by managers that can update persisted
// backend settings without deleting credentials or OAuth state.
type BackendSettingsUpdater interface {
	UpdateBackend(ctx context.Context, id string, update BackendUpdate) error
}

// callbackBaseFromRequest derives the externally-reachable base URL the OAuth
// provider should redirect to, using the inbound request. This is what makes
// the auth flow host-aware: when an operator hits the admin at
// http://172.16.30.90:9086, the callback returns to the same host instead of
// the configured fallback.
//
// X-Forwarded-Proto / X-Forwarded-Host are honored only when trustProxy is
// true AND the forwarded host is in the operator's allowlist (currently the
// host portion of admin_public_url) — otherwise a malicious client behind a
// trusted reverse proxy could still inject a foreign host header and redirect
// OAuth callbacks to an attacker-controlled domain.
func callbackBaseFromRequest(r *http.Request, trustProxy bool, allowedHosts []string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if trustProxy {
		if p := r.Header.Get("X-Forwarded-Proto"); p != "" && (p == "http" || p == "https") {
			scheme = p
		}
		if h := r.Header.Get("X-Forwarded-Host"); h != "" {
			// h may be a comma-separated list when chained; take the first.
			if i := strings.IndexByte(h, ','); i >= 0 {
				h = strings.TrimSpace(h[:i])
			}
			if isAllowedHost(h, allowedHosts) {
				host = h
			}
		}
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// isAllowedHost reports whether candidate matches one of the allowed hosts.
// Empty allowlist means "no proxy host substitution is permitted" — the
// inbound r.Host wins. Matching is case-insensitive on host only (port
// agnostic) so reverse proxies on a non-standard port still validate.
func isAllowedHost(candidate string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	cand := strings.ToLower(stripPort(candidate))
	for _, a := range allowed {
		if cand == strings.ToLower(stripPort(a)) {
			return true
		}
	}
	return false
}

func stripPort(hostport string) string {
	if i := strings.LastIndexByte(hostport, ':'); i > strings.LastIndexByte(hostport, ']') {
		return hostport[:i]
	}
	return hostport
}

// OAuthProberOptions carries optional inputs to a probe: a host-aware
// callback base derived from the inbound admin request, and operator-supplied
// client credentials used when the provider doesn't support DCR.
type OAuthProberOptions struct {
	CallbackBase string
	ClientID     string
	ClientSecret string
}

// OAuthProber is an optional interface that BackendManager may implement
// to support probing backends for OAuth authentication requirements.
type OAuthProber interface {
	// ProbeBackendOAuth probes a URL. If the backend requires OAuth, returns
	// (authURL, state, nil). If no OAuth is needed, returns ("", "", nil).
	ProbeBackendOAuth(ctx context.Context, backendID, url string, opts OAuthProberOptions) (authURL, state string, err error)
	// AuthFlowStatus returns the status of an OAuth flow for a backend.
	// Returns "pending", "connected", "failed:{reason}", or "".
	AuthFlowStatus(backendID string) string
}

// DCRUnsupportedError is returned when the backend's authorization server
// doesn't expose a registration_endpoint and no manual client credentials
// were supplied. The UI uses this to prompt the operator for a pre-registered
// client_id/client_secret. CallbackURL is what the operator should register
// with the provider.
type DCRUnsupportedError struct {
	AuthServer  string
	CallbackURL string
}

func (e *DCRUnsupportedError) Error() string {
	return "provider at " + e.AuthServer + " does not support dynamic client registration; register prism manually (callback: " + e.CallbackURL + ") and supply oauth_client_id"
}

func (a *API) handleAddBackend(w http.ResponseWriter, r *http.Request) { //nolint:gocyclo // add flow branches on transport and OAuth state
	if a.backendMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backend management not available"})
		return
	}

	// Body cap is widened in readAddBackendBody when the payload turns out to
	// be openapi-typed; stdio/HTTP backends stay on the original 64KB ceiling.
	// Extract backend ID from path: POST /backends/{id}
	id := strings.TrimPrefix(r.URL.Path, "/backends/")
	if !isValidID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backend id must be 1-64 chars of [A-Za-z0-9_.-] and cannot start with '-' or '.'"})
		return
	}

	// Check for auth-status sub-path: GET is handled separately but POST /backends/{id}/auth-status
	// should not be valid. Only strip /auth-status for the GET handler below.
	if strings.HasSuffix(id, "/auth-status") {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET for auth-status"})
		return
	}

	bodyBytes, err := readAddBackendBody(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	// Peek for an openapi-typed payload before falling through to the legacy
	// (stdio/HTTP) handling. The peek lets us share the route without
	// breaking existing AddBackend behavior: when "type" is absent the
	// payload is treated as a BackendConfig exactly like before.
	var peek struct {
		Type string `json:"type,omitempty"`
	}
	if err := json.Unmarshal(bodyBytes, &peek); err == nil && strings.EqualFold(peek.Type, "openapi") {
		var oreq openAPISaveRequest
		if err := json.Unmarshal(bodyBytes, &oreq); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		a.handleSaveOpenAPIBackend(w, r, id, oreq)
		return
	}

	var cfg BackendConfig
	if err := json.Unmarshal(bodyBytes, &cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := config.ValidateSandboxConfig(cfg.Sandbox); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := config.ValidateWorkspaceConfig(cfg.Workspace); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if cfg.BinaryHash != "" {
		// Binary mode is mutually exclusive with the existing transports.
		// We reject early so an ambiguous payload doesn't half-attach a
		// backend; the gateway re-checks the same invariants but the admin
		// trust boundary is here.
		if cfg.Command != "" || cfg.URL != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "binary_hash cannot be combined with command or url"})
			return
		}
		if !isHexHash(cfg.BinaryHash) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "binary_hash must be a 64-char hex sha256"})
			return
		}
		if a.binaryStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "binary store not configured"})
			return
		}
		entry, err := a.binaryStore.Stat(cfg.BinaryHash)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "binary_hash not found in store; upload or fetch first"})
			return
		}
		// Fill in BinaryName from the store if the operator left it blank
		// — keeps the backend record self-describing on restart.
		if strings.TrimSpace(cfg.BinaryName) == "" {
			cfg.BinaryName = entry.Name
		}
	}

	// If a URL is provided with no explicit credential, probe for OAuth.
	if cfg.URL != "" && cfg.Credential == nil {
		if prober, ok := a.backendMgr.(OAuthProber); ok {
			trustProxy := false
			var allowedHosts []string
			if np, ok := a.backendMgr.(NetworkSettingsProvider); ok {
				trustProxy = np.TrustProxyHeaders()
				allowedHosts = np.AllowedForwardedHosts()
			}
			opts := OAuthProberOptions{
				CallbackBase: callbackBaseFromRequest(r, trustProxy, allowedHosts),
				ClientID:     cfg.OAuthClientID,
				ClientSecret: cfg.OAuthClientSecret,
			}
			authURL, state, err := prober.ProbeBackendOAuth(r.Context(), id, cfg.URL, opts)
			if err != nil {
				var dcrErr *DCRUnsupportedError
				if errors.As(err, &dcrErr) {
					// Provider doesn't do DCR. Ask the operator to register
					// manually and resubmit with oauth_client_id/secret.
					writeJSON(w, http.StatusOK, map[string]any{
						"status":       "manual_oauth_required",
						"auth_server":  dcrErr.AuthServer,
						"callback_url": dcrErr.CallbackURL,
						"backend_id":   id,
					})
					return
				}
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

	if shouldAddBackendAsync(&cfg) {
		a.addBackendAsync(id, &cfg)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "connecting", "id": id})
		return
	}

	if err := a.backendMgr.AddBackend(r.Context(), id, cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok", "id": id})
}

func shouldAddBackendAsync(cfg *BackendConfig) bool {
	if cfg == nil {
		return false
	}
	if cfg.BinaryHash != "" {
		return true
	}
	return cfg.Command != "" && cfg.URL == ""
}

func (a *API) addBackendAsync(id string, cfg *BackendConfig) {
	if cfg == nil {
		return
	}
	asyncCfg := *cfg
	go func() {
		// Let the HTTP response flush before Docker creates a sandbox veth;
		// Chrome can otherwise abort the in-flight fetch with ERR_NETWORK_CHANGED.
		time.Sleep(250 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := a.backendMgr.AddBackend(ctx, id, asyncCfg); err != nil {
			slog.Warn("async backend add failed", "id", id, "error", err) //nolint:gosec // id was validated before this goroutine starts
		}
	}()
}

func (a *API) handleReconnectBackend(w http.ResponseWriter, r *http.Request) {
	if a.backendMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backend management not available"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/backends/")
	id := strings.TrimSuffix(path, "/reconnect")
	if id == path || !isValidID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid backend reconnect path"})
		return
	}

	reconnector, ok := a.backendMgr.(BackendReconnector)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backend reconnect not available"})
		return
	}
	if err := reconnector.ReconnectBackend(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "id": id})
}

func (a *API) handlePatchBackend(w http.ResponseWriter, r *http.Request) {
	if a.backendMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backend management not available"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	id := strings.TrimPrefix(r.URL.Path, "/backends/")
	if !isValidID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid backend id"})
		return
	}

	var update BackendUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if update.Enabled == nil && update.Sandbox == nil && update.Workspace == nil && update.DisabledTools == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one of enabled, sandbox, workspace, or disabled_tools is required"})
		return
	}
	if err := config.ValidateSandboxConfig(update.Sandbox); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := config.ValidateWorkspaceConfig(update.Workspace); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	updater, ok := a.backendMgr.(BackendSettingsUpdater)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backend settings update not available"})
		return
	}
	if err := updater.UpdateBackend(r.Context(), id, update); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "id": id})
}

func (a *API) handleRemoveBackend(w http.ResponseWriter, r *http.Request) {
	if a.backendMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backend management not available"})
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/backends/")
	if !isValidID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid backend id"})
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
	if id == path || !isValidID(id) {
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
