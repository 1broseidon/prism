// Package gateway implements the MCP aggregation core.
//
// It acts as an MCP server to clients and an MCP client to each backend,
// aggregating tools, resources, and prompts under namespaced prefixes.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/audit"
	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/bridge"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/credentials"
	"github.com/1broseidon/prism/internal/metrics"
	"github.com/1broseidon/prism/internal/middleware"
	"github.com/1broseidon/prism/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
)

const namespaceSeparator = "__"

// Backend represents a connected backend. Most backends speak MCP through a
// Session; OpenAPI backends use a synthetic HTTP Dispatcher and leave
// Session nil. The Dispatcher field is the post-task-16 unified call boundary
// and is always non-nil for connected backends — Session == nil is only
// legal when Dispatcher != nil.
type Backend struct {
	Config     *config.ServerConfig
	Client     *mcp.Client
	Session    *mcp.ClientSession
	Dispatcher ToolDispatcher     // unified leaf call path (MCP, OpenAPI, ...)
	OpenAPI    *OpenAPIDispatcher // typed handle when transport == "openapi"
	CB         *middleware.CircuitBreaker
	ToolNames  []string          // namespaced tool names registered on the gateway
	Tools      []BackendToolInfo // tool metadata captured at registration
	// Transport identifies how this backend dispatches calls. "mcp" or "stdio"
	// or "openapi" — used by Status() so the admin UI can label backends
	// without re-deriving the type from URL shape.
	Transport string
	// DisabledTools is the set of bare (un-namespaced) tool names the
	// operator has switched off on this backend. tools/list filters them out
	// for callers; tools/call rejects them with a "method not found" style
	// error so a cached client can't bypass the toggle. Empty/nil = all on.
	DisabledTools map[string]struct{}
	// inflight counts in-progress tool calls so DisconnectBackend can drain
	// them before Session.Close(). Add() is called only while the gateway
	// mutex is held + the backend is still in the map, so no new Adds can
	// race with the post-delete Wait.
	inflight sync.WaitGroup
}

// BackendToolInfo is the per-tool metadata returned in BackendStatus.
// Schemas are intentionally excluded — they can be large and aren't useful
// in a list/detail UI without rendering. Name uses the namespaced form
// (e.g. "github__create_issue") so callers can map directly to audit events.
type BackendToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Disabled is true when the operator has switched this tool off on the
	// backend. The status response still lists it so the UI can re-enable
	// it; tools/list to MCP clients filters it out.
	Disabled bool `json:"disabled,omitempty"`
}

// policyResolverBox is a typed wrapper so atomic.Pointer can hold an interface
// value lock-free. The hot-path scope filter loads this on every tools/list.
type policyResolverBox struct{ r auth.PolicyResolver }

// backendPolicyResolverBox is the analogous wrapper for the layered
// per-backend resolver consulted at tool-call time.
type backendPolicyResolverBox struct{ r auth.BackendPolicyResolver }

// tokenValidatorBox lets the workspace bridge handler verify agent OAuth
// tokens lock-free. Optional; when unset, only the shared workspace token
// is accepted.
type tokenValidatorBox struct{ v *auth.TokenValidator }

// backendRateLimitEntry holds an x/time/rate limiter alongside the spec it
// was configured for, so the cache invalidates a stale bucket when policy
// changes the RPS/burst.
type backendRateLimitEntry struct {
	rps     float64
	burst   int
	limiter *rate.Limiter
}

// Gateway aggregates multiple MCP backends behind a single server.
type Gateway struct {
	mu                    sync.RWMutex
	backends              map[string]*Backend // keyed by server ID
	workspaceInst         map[string]*Backend // keyed by backend ID + workspace ID; tools are not registered
	server                *mcp.Server
	logger                *slog.Logger
	auditor               *audit.Logger
	credStore             *credentials.Store
	kvStore               store.Store // optional KV store for persisting runtime credential configs
	policyResolver        atomic.Pointer[policyResolverBox]
	backendPolicyResolver atomic.Pointer[backendPolicyResolverBox]
	tokenValidator        atomic.Pointer[tokenValidatorBox]

	rateLimitMu    sync.Mutex
	rateLimitCache map[string]*backendRateLimitEntry
	authFlows      any //nolint:unused // used by oauth.go behind mcp_go_client_oauth build tag
	bridgeURL      string
	bridgeURLs     []string
	stdioDisabled  string
	network        *networkRuntime
	workspace      *workspaceBridgeManager
}

// New creates a new Gateway.
func New(logger *slog.Logger) *Gateway {
	if logger == nil {
		logger = slog.Default()
	}
	g := &Gateway{
		backends:       make(map[string]*Backend),
		workspaceInst:  make(map[string]*Backend),
		logger:         logger,
		auditor:        audit.Noop(), // replaced via SetAuditLogger if audit is configured
		credStore:      credentials.NewStore(),
		network:        newNetworkRuntime(nil),
		rateLimitCache: make(map[string]*backendRateLimitEntry),
	}
	g.workspace = newWorkspaceBridgeManager(g)

	g.server = mcp.NewServer(
		&mcp.Implementation{
			Name:    "prism",
			Version: "0.1.0",
		},
		nil,
	)

	// Register scope-filter middleware for tools/list.
	// The middleware is stateless — it reads the policy from the per-request
	// context that the Streamable HTTP transport propagates from the HTTP layer.
	g.server.AddReceivingMiddleware(g.scopeFilterMiddleware())

	return g
}

// SetPolicyResolver sets a live policy resolver for the gateway.
// When set, scope enforcement reads policy from the resolver (live from KV)
// instead of the stale session context. This ensures policy changes take
// effect immediately without requiring MCP client reconnection.
//
// Safe for concurrent use with getPolicyResolver — the swap is atomic, so
// the scope filter on the hot path can never see a torn read.
func (g *Gateway) SetPolicyResolver(pr auth.PolicyResolver) {
	g.policyResolver.Store(&policyResolverBox{r: pr})
}

// getPolicyResolver returns the current live resolver, or nil when unset.
// Lock-free read; safe under SetPolicyResolver concurrent with hot-path reads.
func (g *Gateway) getPolicyResolver() auth.PolicyResolver {
	b := g.policyResolver.Load()
	if b == nil {
		return nil
	}
	return b.r
}

// SetBackendPolicyResolver wires the layered per-backend resolver used at
// tool-call time to stack agent → groups → defaults workspace rules.
func (g *Gateway) SetBackendPolicyResolver(r auth.BackendPolicyResolver) {
	g.backendPolicyResolver.Store(&backendPolicyResolverBox{r: r})
}

func (g *Gateway) getBackendPolicyResolver() auth.BackendPolicyResolver {
	b := g.backendPolicyResolver.Load()
	if b == nil {
		return nil
	}
	return b.r
}

// SetTokenValidator wires an OAuth token validator into the workspace
// bridge handler so agent-authenticated bridges can register without the
// shared workspace token. Optional.
func (g *Gateway) SetTokenValidator(v *auth.TokenValidator) {
	g.tokenValidator.Store(&tokenValidatorBox{v: v})
}

func (g *Gateway) getTokenValidator() *auth.TokenValidator {
	b := g.tokenValidator.Load()
	if b == nil {
		return nil
	}
	return b.v
}

// NotifyToolsChanged triggers a tools/list_changed notification to all MCP sessions.
// Clients that receive this notification re-fetch tools/list, which goes through
// the scope filter with live policy — so newly granted or denied tools appear/disappear.
func (g *Gateway) NotifyToolsChanged() {
	// The MCP SDK doesn't expose a public method to send arbitrary notifications.
	// Adding and immediately removing a sentinel tool triggers changeAndNotify
	// for notificationToolListChanged, which debounces and sends to all sessions.
	sentinel := &mcp.Tool{
		Name:        "__prism_policy_refresh",
		InputSchema: map[string]any{"type": "object"},
	}
	g.server.AddTool(sentinel, nil)
	g.server.RemoveTools(sentinel.Name)
	g.logger.Info("sent tools/list_changed notification to all sessions")
}

// SetAuditLogger replaces the audit logger used by the gateway.
// Call this before serving any requests. It is not safe to call concurrently
// with active requests.
func (g *Gateway) SetAuditLogger(al *audit.Logger) {
	if al == nil {
		al = audit.Noop()
	}
	g.auditor = al
}

// SetStore sets the KV store used for persisting runtime credential configs.
// Call this before serving any requests. When set, runtime-added credentials
// survive process restarts.
func (g *Gateway) SetStore(s store.Store) {
	g.kvStore = s
}

// SetBridgeURL configures the prism-bridge manage endpoint used for delegated command backends.
func (g *Gateway) SetBridgeURL(u string) {
	g.SetBridgeURLs([]string{u})
}

// SetBridgeURLs configures one or more prism-bridge manage endpoints used for
// delegated command backends. Backend IDs are mapped to bridges
// deterministically so delete/reconnect operations find the same bridge.
func (g *Gateway) SetBridgeURLs(urls []string) {
	normalized := normalizeBridgeURLs(urls)
	g.bridgeURLs = normalized
	if len(normalized) == 0 {
		g.bridgeURL = ""
		return
	}
	g.bridgeURL = normalized[0]
}

// BridgeURL returns the configured prism-bridge manage URL, if any.
func (g *Gateway) BridgeURL() string {
	return g.bridgeURL
}

// BridgeURLs returns the configured prism-bridge manage URLs, if any.
func (g *Gateway) BridgeURLs() []string {
	return append([]string(nil), g.bridgeURLs...)
}

// DisableProcessStdio prevents command backends from falling back to running
// directly in the Prism process when no bridge is configured.
func (g *Gateway) DisableProcessStdio(reason string) {
	g.stdioDisabled = strings.TrimSpace(reason)
}

// NetworkSettings returns the current runtime network settings.
func (g *Gateway) NetworkSettings() *admin.NetworkSettings {
	if g.network == nil {
		return &admin.NetworkSettings{}
	}
	return g.network.Get()
}

// TrustProxyHeaders is the operator's opt-in for honoring X-Forwarded-*.
// Satisfies admin.NetworkSettingsProvider.
func (g *Gateway) TrustProxyHeaders() bool {
	return g.NetworkSettings().TrustProxyHeaders
}

// AllowedForwardedHosts returns the hosts the admin handler may take from
// X-Forwarded-Host when trustProxy is on. We derive the list from the
// operator-pinned admin_public_url so attackers can't substitute an arbitrary
// host through a trusted proxy. Returns nil/empty when no admin URL is set —
// the admin handler then refuses to substitute.
func (g *Gateway) AllowedForwardedHosts() []string {
	a := g.NetworkSettings().AdminPublicURL
	if a == "" {
		return nil
	}
	u, err := url.Parse(a)
	if err != nil || u.Host == "" {
		return nil
	}
	return []string{u.Host}
}

// SetNetworkSettings atomically swaps the runtime network settings.
func (g *Gateway) SetNetworkSettings(s *admin.NetworkSettings) {
	if g.network == nil {
		g.network = newNetworkRuntime(s)
		return
	}
	g.network.Set(s)
}

// PersistNetworkSettings writes settings to KV. Satisfies admin.NetworkSettingsManager.
func (g *Gateway) PersistNetworkSettings(s *admin.NetworkSettings) error {
	return SaveNetworkSettings(g.kvStore, s)
}

// KV key prefixes for backend persistence.
const credKVPrefix = "backend/cred/" //nolint:gosec // not a credential, just a KV key prefix
const backendKVPrefix = "backend/config/"

// persistedBackend is the JSON representation of a runtime-added backend stored in KV.
type persistedBackend struct {
	Command       string                  `json:"command,omitempty"`
	Args          []string                `json:"args,omitempty"`
	Env           map[string]string       `json:"env,omitempty"`
	URL           string                  `json:"url,omitempty"`
	BridgeManaged bool                    `json:"bridge_managed,omitempty"`
	Runtime       string                  `json:"runtime,omitempty"`
	Enabled       *bool                   `json:"enabled,omitempty"`
	Sandbox       *config.SandboxConfig   `json:"sandbox,omitempty"`
	Workspace     *config.WorkspaceConfig `json:"workspace,omitempty"`
	// DisabledTools persists the operator's per-tool toggles across restarts.
	// Bare tool names (no namespace prefix).
	DisabledTools []string `json:"disabled_tools,omitempty"`
	// OpenAPI backend fields. When OpenAPISpecRaw is non-empty the gateway
	// treats this entry as an OpenAPI-typed backend and re-parses on restart;
	// the stdio/HTTP fields above are ignored in that mode.
	OpenAPISpecRaw        []byte `json:"openapi_spec,omitempty"`
	OpenAPISourceURL      string `json:"openapi_source_url,omitempty"`
	OpenAPIBaseURL        string `json:"openapi_base_url,omitempty"`
	OpenAPISecurityScheme string `json:"openapi_security_scheme,omitempty"`
}

// isOpenAPI reports whether this entry is an OpenAPI-typed backend.
func (pb *persistedBackend) isOpenAPI() bool {
	return pb != nil && len(pb.OpenAPISpecRaw) > 0
}

func boolPtr(v bool) *bool { return &v }

func (pb *persistedBackend) isEnabled() bool {
	return pb == nil || pb.Enabled == nil || *pb.Enabled
}

func (pb *persistedBackend) sandboxConfig() config.SandboxConfig {
	if pb == nil {
		return config.CompatSandboxConfig()
	}
	return config.NormalizeSandboxConfig(pb.Sandbox, config.SandboxProfileCompat)
}

func (pb *persistedBackend) workspaceConfig() *config.WorkspaceConfig {
	if pb == nil {
		return nil
	}
	return config.NormalizeWorkspaceConfig(pb.Workspace)
}

// persistBackend saves a backend config to KV for restart persistence.
func (g *Gateway) persistBackend(backendID string, pb *persistedBackend) {
	if g.kvStore == nil {
		return
	}
	data, err := json.Marshal(pb)
	if err != nil {
		g.logger.Warn("failed to marshal backend for persistence", "id", backendID, "error", err)
		return
	}
	if err := g.kvStore.Set(backendKVPrefix+backendID, data); err != nil {
		g.logger.Warn("failed to persist backend", "id", backendID, "error", err)
	}
}

// deletePersistedBackend removes a backend config from KV.
func (g *Gateway) deletePersistedBackend(backendID string) {
	if g.kvStore == nil {
		return
	}
	if err := g.kvStore.Delete(backendKVPrefix + backendID); err != nil {
		g.logger.Warn("failed to delete persisted backend", "id", backendID, "error", err)
	}
}

// persistedCredential is the JSON representation stored in KV.
type persistedCredential struct {
	Type    string `json:"type"`
	Header  string `json:"header,omitempty"`
	Value   string `json:"value,omitempty"`
	Env     string `json:"env,omitempty"`
	Command string `json:"command,omitempty"`
}

// persistCredential saves a credential config to KV for restart persistence.
func (g *Gateway) persistCredential(backendID string, pc *persistedCredential) {
	if g.kvStore == nil {
		return
	}
	data, err := json.Marshal(pc)
	if err != nil {
		g.logger.Warn("failed to marshal credential for persistence", "id", backendID, "error", err)
		return
	}
	if err := g.kvStore.Set(credKVPrefix+backendID, data); err != nil {
		g.logger.Warn("failed to persist credential", "id", backendID, "error", err)
	}
}

// deletePersistedCredential removes a credential config from KV.
func (g *Gateway) deletePersistedCredential(backendID string) {
	if g.kvStore == nil {
		return
	}
	if err := g.kvStore.Delete(credKVPrefix + backendID); err != nil {
		g.logger.Warn("failed to delete persisted credential", "id", backendID, "error", err)
	}
}

// LoadPersistedCredentials restores runtime-added credentials from KV.
// Call this after SetStore and before serving requests.
func (g *Gateway) LoadPersistedCredentials() {
	if g.kvStore == nil {
		return
	}
	keys, err := g.kvStore.List(credKVPrefix)
	if err != nil {
		g.logger.Warn("failed to list persisted credentials", "error", err)
		return
	}

	for _, key := range keys {
		backendID := strings.TrimPrefix(key, credKVPrefix)
		data, err := g.kvStore.Get(key)
		if err != nil {
			g.logger.Warn("failed to read persisted credential", "key", key, "error", err)
			continue
		}

		var pc persistedCredential
		if err := json.Unmarshal(data, &pc); err != nil {
			g.logger.Warn("failed to unmarshal persisted credential", "key", key, "error", err)
			continue
		}

		cred := buildCredentialFromPersisted(&pc)
		if cred != nil {
			g.credStore.Register(backendID, cred)
			g.logger.Info("restored persisted credential", "id", backendID, "type", pc.Type)
		}
	}
}

// LoadPersistedBackends restores runtime-added backends from KV.
// Call this after SetStore and after config backends are connected.
func (g *Gateway) LoadPersistedBackends(ctx context.Context) {
	if g.kvStore == nil {
		return
	}
	keys, err := g.kvStore.List(backendKVPrefix)
	if err != nil {
		g.logger.Warn("failed to list persisted backends", "error", err)
		return
	}

	for _, key := range keys {
		backendID := strings.TrimPrefix(key, backendKVPrefix)

		// Skip if already connected from config
		g.mu.RLock()
		_, exists := g.backends[backendID]
		g.mu.RUnlock()
		if exists {
			continue
		}

		data, err := g.kvStore.Get(key)
		if err != nil {
			g.logger.Warn("failed to read persisted backend", "key", key, "error", err)
			continue
		}

		var pb persistedBackend
		if err := json.Unmarshal(data, &pb); err != nil {
			g.logger.Warn("failed to unmarshal persisted backend", "key", key, "error", err)
			continue
		}
		if !pb.isEnabled() {
			g.logger.Info("skipping disabled persisted backend", "id", backendID)
			continue
		}

		if err := g.connectPersistedBackend(ctx, backendID, &pb); err != nil {
			g.logger.Warn("failed to reconnect persisted backend", "id", backendID, "error", err)
			continue
		}
		g.logger.Info("restored persisted backend", "id", backendID)
	}
}

func (g *Gateway) reconnectPersistedBackendsForWorkspace(ctx context.Context, workspaceID string) {
	if g.kvStore == nil || !workspaceIDRE.MatchString(workspaceID) {
		return
	}
	keys, err := g.kvStore.List(backendKVPrefix)
	if err != nil {
		g.logger.Warn("failed to list persisted backends for workspace reconnect", "workspace", workspaceID, "error", err)
		return
	}
	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}
		backendID := strings.TrimPrefix(key, backendKVPrefix)
		g.mu.RLock()
		_, exists := g.backends[backendID]
		g.mu.RUnlock()
		if exists {
			continue
		}

		data, err := g.kvStore.Get(key)
		if err != nil {
			g.logger.Warn("failed to read persisted workspace backend", "key", key, "workspace", workspaceID, "error", err)
			continue
		}
		var pb persistedBackend
		if err := json.Unmarshal(data, &pb); err != nil {
			g.logger.Warn("failed to unmarshal persisted workspace backend", "key", key, "workspace", workspaceID, "error", err)
			continue
		}
		workspaceCfg := pb.workspaceConfig()
		if !pb.isEnabled() || workspaceCfg == nil || workspaceCfg.ID != workspaceID {
			continue
		}
		if err := g.connectPersistedBackend(ctx, backendID, &pb); err != nil {
			g.logger.Warn("failed to reconnect persisted workspace backend", "id", backendID, "workspace", workspaceID, "error", err)
			continue
		}
		g.logger.Info("restored persisted workspace backend", "id", backendID, "workspace", workspaceID)
	}
}

// ReconnectBackend reconnects a backend from persisted KV state without
// deleting its stored config or OAuth tokens. It is used by the admin UI for
// backends that failed startup restore or were temporarily unreachable.
func (g *Gateway) ReconnectBackend(ctx context.Context, backendID string) error {
	if g.kvStore == nil {
		return fmt.Errorf("backend persistence is not configured")
	}
	g.mu.RLock()
	_, connected := g.backends[backendID]
	g.mu.RUnlock()
	if connected {
		return nil
	}

	data, err := g.kvStore.Get(backendKVPrefix + backendID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("backend %q is not persisted", backendID)
		}
		return fmt.Errorf("read persisted backend %q: %w", backendID, err)
	}
	var pb persistedBackend
	if err := json.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("decode persisted backend %q: %w", backendID, err)
	}
	if !pb.isEnabled() {
		return fmt.Errorf("backend %q is disabled", backendID)
	}
	return g.connectPersistedBackend(ctx, backendID, &pb)
}

func (g *Gateway) connectPersistedBackend(ctx context.Context, backendID string, pb *persistedBackend) error {
	if pb.isOpenAPI() {
		if err := g.reconnectPersistedOpenAPIBackend(ctx, backendID, pb); err != nil {
			return err
		}
		g.applyDisabledTools(backendID, pb.DisabledTools)
		g.persistBackend(backendID, pb)
		return nil
	}
	sc := &config.ServerConfig{
		ID:            backendID,
		Namespace:     backendID,
		URL:           pb.URL,
		Env:           pb.Env,
		BridgeManaged: pb.BridgeManaged,
		BridgeRuntime: pb.Runtime,
		Enabled:       pb.isEnabled(),
		Sandbox:       pb.sandboxConfig(),
		Workspace:     pb.workspaceConfig(),
		Timeout:       config.Duration(30 * time.Second),
	}
	if pb.Command != "" {
		sc.OriginalCommand = append([]string{pb.Command}, pb.Args...)
		if g.bridgeURL != "" {
			spawned, err := g.spawnBridgeBackend(ctx, backendID, pb.Command, pb.Args, pb.Env, pb.Runtime, &sc.Sandbox, sc.Workspace)
			if err != nil {
				return fmt.Errorf("delegate persisted backend to bridge: %w", err)
			}
			sc.URL = spawned.Endpoint
			sc.BridgeManaged = true
			pb.URL = sc.URL
			pb.BridgeManaged = true
			if err := g.connectBackendWithBridgeRetry(ctx, sc, &spawned, backendID, pb.Command, pb.Args, pb.Env, pb.Runtime, &sc.Sandbox, sc.Workspace); err != nil {
				return err
			}
			g.applyDisabledTools(backendID, pb.DisabledTools)
			g.persistBackend(backendID, pb)
			return nil
		}
		if err := g.stdioUnavailableError(); err != nil {
			return err
		}
		sc.Command = append([]string{pb.Command}, pb.Args...)
	}
	if err := g.ConnectBackend(ctx, sc); err != nil {
		return err
	}
	g.applyDisabledTools(backendID, pb.DisabledTools)
	g.persistBackend(backendID, pb)
	return nil
}

// applyDisabledTools sets the live disabled-tools set on a connected backend.
// Idempotent; safe to call from both connect paths. Holds the gateway lock
// for the duration so concurrent tools/list / tools/call see a consistent set.
func (g *Gateway) applyDisabledTools(backendID string, list []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	b, ok := g.backends[backendID]
	if !ok {
		return
	}
	if len(list) == 0 {
		b.DisabledTools = nil
		return
	}
	next := make(map[string]struct{}, len(list))
	for _, name := range list {
		next[name] = struct{}{}
	}
	b.DisabledTools = next
}

// HasPersistedBackends returns true if the KV store has any persisted backend configs.
func (g *Gateway) HasPersistedBackends() bool {
	if g.kvStore == nil {
		return false
	}
	keys, err := g.kvStore.List(backendKVPrefix)
	if err != nil {
		return false
	}
	return len(keys) > 0
}

// ApplyPersistedBackendSettings overlays runtime settings that are safe to
// carry for config-defined backends, such as enabled and sandbox controls.
func (g *Gateway) ApplyPersistedBackendSettings(sc *config.ServerConfig) {
	if g.kvStore == nil || sc == nil {
		return
	}
	data, err := g.kvStore.Get(backendKVPrefix + sc.ID)
	if err != nil {
		return
	}
	var pb persistedBackend
	if json.Unmarshal(data, &pb) != nil {
		return
	}
	sc.Enabled = pb.isEnabled()
	if pb.Sandbox != nil {
		sc.Sandbox = pb.sandboxConfig()
	}
	if pb.Workspace != nil {
		sc.Workspace = pb.workspaceConfig()
	}
}

// SeedBackends persists config-defined backends to KV (one-time seed on first boot).
// Does NOT connect them -- LoadPersistedBackends handles that.
func (g *Gateway) SeedBackends(_ context.Context, servers []config.ServerConfig) {
	for i := range servers {
		s := &servers[i]
		pb := &persistedBackend{
			URL:     s.URL,
			Enabled: boolPtr(s.Enabled),
		}
		sandbox := s.Sandbox
		pb.Sandbox = &sandbox
		pb.Workspace = config.NormalizeWorkspaceConfig(s.Workspace)
		if s.IsStdio() {
			pb.Command = s.Command[0]
			if len(s.Command) > 1 {
				pb.Args = s.Command[1:]
			}
			pb.Env = s.Env
		}
		g.persistBackend(s.ID, pb)

		// Also persist credentials if configured.
		if s.Credentials != nil {
			g.persistCredential(s.ID, &persistedCredential{
				Type:    s.Credentials.InferredType(),
				Header:  s.Credentials.Header,
				Value:   s.Credentials.Value,
				Env:     s.Credentials.Env,
				Command: s.Credentials.Command,
			})
		}

		g.logger.Info("seeded backend from config", "id", s.ID)
	}
}

// buildCredentialFromPersisted converts a persisted credential into a Credential.
func buildCredentialFromPersisted(pc *persistedCredential) credentials.Credential {
	header := pc.Header
	if header == "" {
		header = "Authorization"
	}
	switch pc.Type {
	case "static":
		return &credentials.Static{Header: header, Value: pc.Value}
	case "env":
		return &credentials.Env{Header: header, EnvVar: pc.Env}
	case "command":
		return &credentials.Command{Header: header, Cmd: pc.Command}
	default:
		return nil
	}
}

// disabledToolsByNamespace returns a snapshot of every backend's disabled
// tool set, keyed by namespace, so the scope filter can drop disabled tools
// in one O(1) lookup per tool rather than re-scanning the backends map.
func (g *Gateway) disabledToolsByNamespace() map[string]map[string]struct{} {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make(map[string]map[string]struct{}, len(g.backends))
	for _, b := range g.backends {
		if len(b.DisabledTools) == 0 {
			continue
		}
		copySet := make(map[string]struct{}, len(b.DisabledTools))
		for name := range b.DisabledTools {
			copySet[name] = struct{}{}
		}
		out[b.Config.Namespace] = copySet
	}
	return out
}

// scopeFilterMiddleware returns an MCP receiving middleware that intercepts
// tools/list responses and strips out tools the caller cannot access.
//
// If no policy is in context (open / unauthenticated mode) all tools are returned.
// If a policy is present, only tools whose namespace:name scope is granted pass through.
func (g *Gateway) scopeFilterMiddleware() mcp.Middleware {
	tracer := otel.Tracer("prism.gateway")

	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if method != "tools/list" || err != nil {
				return result, err
			}

			policy := auth.LivePolicy(ctx, g.getPolicyResolver())
			if policy == nil {
				// Open / unauthenticated mode: return all tools.
				return result, nil
			}

			toolsResult, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, nil
			}

			_, span := tracer.Start(ctx, "prism.gateway.scope_filter",
				trace.WithSpanKind(trace.SpanKindInternal),
			)
			defer span.End()

			toolsBefore := len(toolsResult.Tools)

			disabledByNS := g.disabledToolsByNamespace()

			filtered := make([]*mcp.Tool, 0, len(toolsResult.Tools))
			for _, t := range toolsResult.Tools {
				ns, name, ok := parseNamespacedTool(t.Name)
				if !ok {
					// Tool name doesn't follow the namespace__tool convention;
					// include it only if the superuser wildcard is granted.
					if _, hasStar := policy.AllowedScopes["*"]; hasStar {
						filtered = append(filtered, t)
					}
					continue
				}
				if !policy.CanAccessTool(ns, name) {
					continue
				}
				if d, ok := disabledByNS[ns]; ok {
					if _, off := d[name]; off {
						continue
					}
				}
				filtered = append(filtered, t)
			}

			toolsResult.Tools = filtered

			span.SetAttributes(
				attribute.Int("scope.tools_before", toolsBefore),
				attribute.Int("scope.tools_after", len(filtered)),
			)

			return toolsResult, nil
		}
	}
}

// Server returns the underlying MCP server for transport binding.
func (g *Gateway) Server() *mcp.Server {
	return g.server
}

// Handler returns an http.Handler that serves the Streamable HTTP MCP transport.
func (g *Gateway) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return g.server },
		nil,
	)
}

// ConnectBackend establishes a connection to a backend MCP server
// and registers its tools on the gateway server.
// Supports both stdio (command) and HTTP (url) backends.
func (g *Gateway) ConnectBackend(ctx context.Context, cfg *config.ServerConfig) error {
	var (
		client  *mcp.Client
		session *mcp.ClientSession
		err     error
	)

	if cfg.IsStdio() {
		// Stdio backend: spawn the process and connect via CommandTransport.
		sb, serr := bridge.ConnectStdio(ctx, cfg.Command, cfg.Env, g.logger)
		if serr != nil {
			return fmt.Errorf("connect stdio %s: %w", cfg.ID, serr)
		}
		client = sb.Client
		session = sb.Session
	} else {
		// HTTP backend: connect via StreamableClientTransport.
		client = mcp.NewClient(
			&mcp.Implementation{Name: "prism-client", Version: "0.1.0"},
			nil,
		)

		// Register credential for this backend if configured.
		if cfg.Credentials != nil {
			cred := buildCredential(cfg.Credentials)
			g.credStore.Register(cfg.ID, cred)
			g.logger.Info("registered credential for backend",
				"id", cfg.ID,
				"type", cfg.Credentials.InferredType(),
			)
		}

		httpClient := &http.Client{
			Transport: &credentials.InjectingTransport{
				Base:      http.DefaultTransport,
				Store:     g.credStore,
				BackendID: cfg.ID,
				Logger:    g.logger,
			},
		}

		transport := &mcp.StreamableClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient,
		}

		session, err = client.Connect(ctx, transport, nil)
		if err != nil {
			return fmt.Errorf("connect to %s (%s): %w", cfg.ID, cfg.URL, err)
		}
	}

	var cb *middleware.CircuitBreaker
	if cfg.CircuitBreaker != nil {
		cb = middleware.NewCircuitBreaker(middleware.CircuitBreakerConfig{
			Threshold:   cfg.CircuitBreaker.Threshold,
			Timeout:     cfg.CircuitBreaker.Timeout.Duration(),
			MaxHalfOpen: cfg.CircuitBreaker.MaxHalfOpen,
		})
	}

	ttype := "http"
	if cfg.IsStdio() {
		ttype = "stdio"
	}

	dispatcher := NewMCPSessionDispatcher(session, cfg.Namespace, nil)
	backend := &Backend{
		Config:     cfg,
		Client:     client,
		Session:    session,
		Dispatcher: dispatcher,
		CB:         cb,
		Transport:  ttype,
	}

	g.mu.Lock()
	g.backends[cfg.ID] = backend
	g.mu.Unlock()

	if err := g.registerBackendTools(ctx, backend); err != nil {
		g.logger.Warn("failed to register tools", "id", cfg.ID, "error", err)
	}
	dispatcher.setTools(backend.Tools)

	g.logger.Info("connected to backend", "id", cfg.ID, "transport", ttype, "namespace", cfg.Namespace)
	metrics.IncActiveBackends()
	return nil
}

// registerBackendTools fetches tools from a backend and registers them on the gateway server.
// Each call to AddTool triggers notifications/tools/list_changed to all connected clients.
func (g *Gateway) registerBackendTools(ctx context.Context, b *Backend) error {
	result, err := b.Session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return fmt.Errorf("list tools from %s: %w", b.Config.ID, err)
	}

	names := make([]string, 0, len(result.Tools))
	infos := make([]BackendToolInfo, 0, len(result.Tools))
	for _, tool := range result.Tools {
		namespacedName := b.Config.Namespace + namespaceSeparator + tool.Name

		// Capture for closure
		backendID := b.Config.ID
		originalName := tool.Name

		namespacedTool := &mcp.Tool{
			Name:        namespacedName,
			Description: fmt.Sprintf("[%s] %s", b.Config.Namespace, tool.Description),
			InputSchema: tool.InputSchema,
		}
		if b.Config.BridgeManaged && len(b.Config.OriginalCommand) > 0 && config.NormalizeWorkspaceConfig(b.Config.Workspace) != nil {
			namespacedTool.InputSchema = addWorkspaceSelectorToSchema(tool.InputSchema)
		}

		handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return g.routeToolCall(ctx, backendID, originalName, req)
		}

		g.server.AddTool(namespacedTool, handler)
		names = append(names, namespacedName)
		infos = append(infos, BackendToolInfo{Name: namespacedName, Description: tool.Description})
	}

	// Track tool names so we can remove them when the backend disconnects.
	b.ToolNames = names
	b.Tools = infos

	g.logger.Info("registered tools from backend", "id", b.Config.ID, "count", len(result.Tools))
	return nil
}

const prismWorkspaceArg = "_prism_workspace"

func addWorkspaceSelectorToSchema(schema any) any {
	var out map[string]any
	data, err := json.Marshal(schema)
	if err == nil {
		_ = json.Unmarshal(data, &out)
	}
	if out == nil {
		out = map[string]any{"type": "object"}
	}
	if out["type"] == nil {
		out["type"] = "object"
	}
	props, _ := out["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}
	props[prismWorkspaceArg] = map[string]any{
		"type":        "string",
		"description": "Optional Prism workspace ID to attach this stdio server call to. Prism authorizes and strips this before forwarding to the backend.",
	}
	out["properties"] = props
	return out
}

func splitWorkspaceSelector(req *mcp.CallToolRequest) (workspaceID string, forwarded *mcp.CallToolRequest, err error) {
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return "", req, nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
		return "", req, nil
	}
	raw, ok := args[prismWorkspaceArg]
	if !ok {
		return "", req, nil
	}
	if err := json.Unmarshal(raw, &workspaceID); err != nil {
		return "", nil, fmt.Errorf("%s must be a string", prismWorkspaceArg)
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "", nil, fmt.Errorf("%s must not be empty", prismWorkspaceArg)
	}
	delete(args, prismWorkspaceArg)
	nextArgs := json.RawMessage(`{}`)
	if len(args) > 0 {
		data, err := json.Marshal(args)
		if err != nil {
			return "", nil, err
		}
		nextArgs = data
	}
	clone := *req
	params := *req.Params
	params.Arguments = nextArgs
	clone.Params = &params
	return workspaceID, &clone, nil
}

// routeToolCall forwards a tool call to the correct backend.
// It enforces scope-based access control and emits a structured audit log entry
// for every call (allowed or denied).
func (g *Gateway) routeToolCall(ctx context.Context, backendID, toolName string, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) { //nolint:gocyclo // tool-call routing is the auth, workspace, circuit-breaker, and audit boundary
	tracer := otel.Tracer("prism.gateway")
	ctx, span := tracer.Start(ctx, "prism.gateway.tool_call",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	defer span.End()

	// Look up + register in-flight under the same lock so DisconnectBackend
	// can rely on "removed from map ⇒ no further Adds" to drain cleanly.
	g.mu.RLock()
	b, ok := g.backends[backendID]
	if ok {
		b.inflight.Add(1)
	}
	g.mu.RUnlock()

	if !ok {
		span.SetAttributes(
			attribute.String("tool.name", toolName),
			attribute.String("tool.backend", backendID),
			attribute.Bool("tool.allowed", false),
		)
		span.SetStatus(codes.Error, "backend not found")
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("backend %q not found", backendID)},
			},
		}, nil
	}
	defer b.inflight.Done()

	// Tool may have been switched off after a cached client already saw it
	// in tools/list. Reject before doing any auth/credential/workspace work.
	if _, off := b.DisabledTools[toolName]; off {
		span.SetAttributes(
			attribute.String("tool.namespace", b.Config.Namespace),
			attribute.String("tool.name", toolName),
			attribute.String("tool.backend", backendID),
			attribute.Bool("tool.allowed", false),
			attribute.String("tool.deny_reason", "disabled"),
		)
		span.SetStatus(codes.Error, "tool disabled")
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("tool %q is disabled on backend %q", toolName, backendID)},
			},
		}, nil
	}

	span.SetAttributes(
		attribute.String("tool.namespace", b.Config.Namespace),
		attribute.String("tool.name", toolName),
		attribute.String("tool.backend", backendID),
	)

	// credInjected tracks whether a backend credential is registered for this backend.
	// The actual injection happens in the HTTP transport; we record the fact (not the value).
	credHeader, _, _ := g.credStore.Resolve(ctx, backendID)
	credInjected := credHeader != ""
	requestedWorkspaceID, forwardedReq, selectorErr := splitWorkspaceSelector(req)
	if selectorErr != nil {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: selectorErr.Error()}},
		}, nil
	}
	if forwardedReq == nil {
		forwardedReq = req
	}

	// Workspace resolution: explicit _prism_workspace selector wins; otherwise
	// walk the policy stack (agent → groups → defaults) and fall back to the
	// backend's static workspace config as the floor.
	workspaceCfg := config.NormalizeWorkspaceConfig(b.Config.Workspace)
	resolution := BackendWorkspaceResolution{Source: "backend.static"}
	if workspaceCfg != nil {
		resolution.Selector = "static"
		resolution.WorkspaceID = workspaceCfg.ID
	}
	if requestedWorkspaceID == "" {
		callerClaims := auth.ClaimsFromContext(ctx)
		resolvedCfg, res, denyReason := g.ResolveBackendWorkspace(callerClaims, b)
		// Attach the trace early so any subsequent LogCall (including the
		// denial below) carries the policy decision in the audit event.
		ctx = audit.ContextWithPolicyTrace(ctx, auditTraceFromResolution(res))
		if denyReason != "" {
			span.SetAttributes(attribute.Bool("tool.allowed", false))
			span.SetStatus(codes.Error, "workspace policy denied")
			g.logger.Warn("tool call denied by workspace policy",
				"backend", backendID,
				"tool", toolName,
				"reason", denyReason,
				"policy_source", res.Source,
			)
			g.auditor.LogCall(ctx, b.Config.Namespace, toolName, backendID, false, credInjected, 0, nil)
			metrics.RecordScopeDenial(b.Config.Namespace, toolName)
			metrics.RecordToolCall(b.Config.Namespace, toolName, backendID, false, 0)
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: denyReason}},
			}, nil
		}
		if resolvedCfg != nil {
			workspaceCfg = resolvedCfg
		}
		resolution = res
	}
	effectiveWorkspaceID := ""
	if workspaceCfg != nil {
		effectiveWorkspaceID = workspaceCfg.ID
	}
	if requestedWorkspaceID != "" {
		if !workspaceIDRE.MatchString(requestedWorkspaceID) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "invalid workspace id"}},
			}, nil
		}
		if workspaceCfg == nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "this backend is not workspace-backed"}},
			}, nil
		}
		effectiveWorkspaceID = requestedWorkspaceID
		resolution = BackendWorkspaceResolution{
			Selector:    "request",
			Source:      "_prism_workspace",
			WorkspaceID: requestedWorkspaceID,
		}
		ctx = audit.ContextWithPolicyTrace(ctx, auditTraceFromResolution(resolution))
	}
	if effectiveWorkspaceID != "" {
		span.SetAttributes(
			attribute.String("workspace.id", effectiveWorkspaceID),
			attribute.String("workspace.policy.source", resolution.Source),
			attribute.String("workspace.policy.selector", resolution.Selector),
		)
	}

	// Scope enforcement: check if the caller has permission for this tool.
	// Uses LivePolicy to resolve from KV store (not stale session context).
	if policy := auth.LivePolicy(ctx, g.getPolicyResolver()); policy != nil {
		if !policy.CanAccessTool(b.Config.Namespace, toolName) {
			span.SetAttributes(attribute.Bool("tool.allowed", false))
			span.SetStatus(codes.Error, "access denied")
			g.logger.Warn("tool call denied by scope policy",
				"backend", backendID,
				"tool", toolName,
				"namespace", b.Config.Namespace,
			)
			// Audit: log the denial before returning.
			g.auditor.LogCall(ctx, b.Config.Namespace, toolName, backendID, false, credInjected, 0, nil)
			metrics.RecordScopeDenial(b.Config.Namespace, toolName)
			metrics.RecordToolCall(b.Config.Namespace, toolName, backendID, false, 0)
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(
						"access denied: scope %q:%q not granted",
						b.Config.Namespace, toolName,
					)},
				},
			}, nil
		}
		if effectiveWorkspaceID != "" && policy.HasWorkspaceConstraints() && !policy.CanAccessWorkspace(effectiveWorkspaceID) {
			span.SetAttributes(
				attribute.Bool("tool.allowed", false),
				attribute.String("workspace.id", effectiveWorkspaceID),
			)
			span.SetStatus(codes.Error, "workspace access denied")
			g.logger.Warn("tool call denied by workspace policy",
				"backend", backendID,
				"tool", toolName,
				"namespace", b.Config.Namespace,
				"workspace", effectiveWorkspaceID,
			)
			g.auditor.LogCall(ctx, b.Config.Namespace, toolName, backendID, false, credInjected, 0, nil)
			metrics.RecordScopeDenial(b.Config.Namespace, toolName)
			metrics.RecordToolCall(b.Config.Namespace, toolName, backendID, false, 0)
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(
						"access denied: workspace %q not granted",
						effectiveWorkspaceID,
					)},
				},
			}, nil
		}
	}
	if effectiveWorkspaceID != "" {
		if denyReason := g.authorizeWorkspaceRegistry(ctx, b, effectiveWorkspaceID); denyReason != "" {
			span.SetAttributes(
				attribute.Bool("tool.allowed", false),
				attribute.String("workspace.id", effectiveWorkspaceID),
			)
			span.SetStatus(codes.Error, "workspace registry access denied")
			g.logger.Warn("tool call denied by workspace registry",
				"backend", backendID,
				"tool", toolName,
				"namespace", b.Config.Namespace,
				"workspace", effectiveWorkspaceID,
				"reason", denyReason,
			)
			g.auditor.LogCall(ctx, b.Config.Namespace, toolName, backendID, false, credInjected, 0, nil)
			metrics.RecordScopeDenial(b.Config.Namespace, toolName)
			metrics.RecordToolCall(b.Config.Namespace, toolName, backendID, false, 0)
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: denyReason},
				},
			}, nil
		}
	}

	// Per-(agent, backend) rate-limit enforcement from policy stack.
	callerClaims := auth.ClaimsFromContext(ctx)
	if limit, rlRes := g.ResolveBackendRateLimit(callerClaims, b); limit != nil {
		if !g.allowBackendCall(callerClaims, backendID, limit) {
			span.SetAttributes(
				attribute.Bool("tool.allowed", false),
				attribute.Float64("ratelimit.rps", limit.RPS),
				attribute.Int("ratelimit.burst", limit.Burst),
				attribute.String("ratelimit.source", rlRes.Source),
			)
			span.SetStatus(codes.Error, "rate limited")
			g.logger.Warn("tool call denied by rate limit",
				"backend", backendID,
				"tool", toolName,
				"policy_source", rlRes.Source,
				"rps", limit.RPS,
				"burst", limit.Burst,
			)
			g.auditor.LogCall(ctx, b.Config.Namespace, toolName, backendID, false, credInjected, 0, nil)
			metrics.RecordScopeDenial(b.Config.Namespace, toolName)
			metrics.RecordToolCall(b.Config.Namespace, toolName, backendID, false, 0)
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(
						"rate limit exceeded for backend %q (source: %s, rps: %g)",
						backendID, rlRes.Source, limit.RPS,
					)},
				},
			}, nil
		}
	}

	span.SetAttributes(attribute.Bool("tool.allowed", true))
	if effectiveWorkspaceID != "" {
		span.SetAttributes(attribute.String("workspace.id", effectiveWorkspaceID))
	}

	target := b
	targetBackendID := backendID
	if requestedWorkspaceID != "" && workspaceCfg != nil && requestedWorkspaceID != workspaceCfg.ID {
		instance, err := g.ensureWorkspaceBackendInstance(ctx, b, requestedWorkspaceID)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		target = instance
		targetBackendID = instance.Config.ID
	}
	if target != b {
		target.inflight.Add(1)
		defer target.inflight.Done()
	}

	if b.CB != nil && !b.CB.Allow() {
		span.SetStatus(codes.Error, "circuit breaker open")
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("backend %q circuit breaker is open", backendID)},
			},
		}, nil
	}

	dispatcher := target.Dispatcher
	if dispatcher == nil {
		// Backward-compat safety net: a backend with no dispatcher should
		// never be in g.backends after task-16, but we surface the bug as a
		// soft error instead of panicking the gateway.
		span.SetStatus(codes.Error, "backend missing dispatcher")
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("backend %q has no dispatcher", backendID)},
			},
		}, nil
	}

	start := time.Now()
	result, err := dispatcher.Dispatch(ctx, toolName, forwardedReq.Params.Arguments)
	elapsed := time.Since(start)
	latencyMS := elapsed.Milliseconds()

	span.SetAttributes(attribute.Int64("tool.latency_ms", latencyMS))

	// Audit: log the outcome after the call (success or backend error).
	g.auditor.LogCall(ctx, b.Config.Namespace, toolName, targetBackendID, true, credInjected, latencyMS, err)
	metrics.RecordToolCall(b.Config.Namespace, toolName, targetBackendID, true, elapsed)

	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		if b.CB != nil {
			b.CB.RecordFailure()
		}
		return nil, fmt.Errorf("call tool %q on %s: %w", toolName, backendID, err)
	}

	if b.CB != nil {
		if result.IsError {
			b.CB.RecordFailure()
		} else {
			b.CB.RecordSuccess()
		}
	}

	g.syncWorkspaceAfterToolCall(ctx, targetBackendID, target.Config)

	return result, nil
}

func (g *Gateway) ensureWorkspaceBackendInstance(ctx context.Context, template *Backend, workspaceID string) (*Backend, error) {
	if template == nil || template.Config == nil {
		return nil, errors.New("backend template is missing")
	}
	if !template.Config.BridgeManaged || len(template.Config.OriginalCommand) == 0 {
		return nil, fmt.Errorf("backend %q cannot attach to alternate workspaces", template.Config.ID)
	}
	baseWorkspace := config.NormalizeWorkspaceConfig(template.Config.Workspace)
	if baseWorkspace == nil {
		return nil, fmt.Errorf("backend %q has no workspace template", template.Config.ID)
	}
	workspaceCfg := *baseWorkspace
	workspaceCfg.ID = workspaceID
	g.applyRegisteredWorkspaceConfig(&workspaceCfg)

	key := workspaceInstanceKey(template.Config.ID, workspaceID)
	g.mu.RLock()
	if existing := g.workspaceInst[key]; existing != nil {
		g.mu.RUnlock()
		return existing, nil
	}
	g.mu.RUnlock()

	instanceID := workspaceInstanceID(template.Config.ID, workspaceID)
	command := template.Config.OriginalCommand[0]
	args := append([]string(nil), template.Config.OriginalCommand[1:]...)
	env := cloneStringMap(template.Config.Env)
	sandbox := template.Config.Sandbox
	spawned, err := g.spawnBridgeBackend(ctx, instanceID, command, args, env, template.Config.BridgeRuntime, &sandbox, &workspaceCfg)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "prism-workspace-instance", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: spawned.Endpoint}, nil)
	if err != nil {
		_ = g.removeBridgeBackend(instanceID)
		return nil, fmt.Errorf("connect workspace backend instance %q: %w", instanceID, err)
	}

	cfg := *template.Config
	cfg.ID = instanceID
	cfg.URL = spawned.Endpoint
	cfg.Command = nil
	cfg.Env = env
	cfg.Workspace = &workspaceCfg
	cfg.BridgeManaged = true
	cfg.OriginalCommand = append([]string(nil), template.Config.OriginalCommand...)
	instance := &Backend{
		Config:     &cfg,
		Client:     client,
		Session:    session,
		Dispatcher: NewMCPSessionDispatcher(session, cfg.Namespace, nil),
		Transport:  template.Transport,
	}

	g.mu.Lock()
	if g.workspaceInst == nil {
		g.workspaceInst = make(map[string]*Backend)
	}
	if existing := g.workspaceInst[key]; existing != nil {
		g.mu.Unlock()
		_ = session.Close()
		_ = g.removeBridgeBackend(instanceID)
		return existing, nil
	}
	g.workspaceInst[key] = instance
	g.mu.Unlock()
	g.logger.Info("connected workspace backend instance", "template", template.Config.ID, "workspace", workspaceID, "id", instanceID)
	return instance, nil
}

func workspaceInstanceKey(backendID, workspaceID string) string {
	return backendID + "\x00" + workspaceID
}

func workspaceInstanceID(backendID, workspaceID string) string {
	return backendID + "-ws-" + workspaceID
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (g *Gateway) authorizeWorkspaceRegistry(ctx context.Context, b *Backend, workspaceID string) string {
	if b == nil || b.Config == nil || workspaceID == "" {
		return ""
	}
	entry, ok := g.registeredWorkspace(workspaceID)
	if !ok {
		return ""
	}
	if len(entry.AllowedTemplates) > 0 && !workspaceValueAllowed(entry.AllowedTemplates, b.Config.ID, b.Config.Namespace) {
		return fmt.Sprintf("access denied: backend %q is not allowed to attach to workspace %q", b.Config.ID, workspaceID)
	}
	if len(entry.AllowedAgents) > 0 {
		claims := auth.ClaimsFromContext(ctx)
		if claims == nil || !workspaceValueAllowed(entry.AllowedAgents, claims.ClientID, claims.PrismID, claims.Subject) {
			return fmt.Sprintf("access denied: agent is not allowed to attach to workspace %q", workspaceID)
		}
	}
	return ""
}

func (g *Gateway) applyRegisteredWorkspaceConfig(cfg *config.WorkspaceConfig) {
	if cfg == nil || cfg.ID == "" {
		return
	}
	entry, ok := g.registeredWorkspace(cfg.ID)
	if !ok {
		return
	}
	if entry.Type != "" {
		cfg.Type = entry.Type
	}
	cfg.QuotaBytes = entry.QuotaBytes
	cfg.RetentionSeconds = entry.RetentionSeconds
}

// BackendWorkspaceResolution captures the workspace decision the gateway
// made for a tool call plus the layer chain that produced it. The trace is
// emitted in audit events so operators can answer "why" without diffing
// state.
type BackendWorkspaceResolution struct {
	WorkspaceID string                 `json:"workspace_id,omitempty"`
	Selector    string                 `json:"selector"`
	Source      string                 `json:"source"` // policy layer or "backend.static"
	Layers      []ResolutionLayerTrace `json:"layers,omitempty"`
}

// ResolutionLayerTrace records what each layer of the stack contributed.
type ResolutionLayerTrace struct {
	Source   string `json:"source"`
	Selector string `json:"selector,omitempty"` // empty = layer did not have a rule for this backend
}

// ResolveBackendWorkspace walks the layered backend policy (agent → groups →
// defaults) and returns the effective workspace for a tool call to the given
// backend. The backend's static workspace config is the floor.
//
// Selector semantics:
//   - "" (no rule at any layer)       → use backend.workspace static (floor)
//   - "static"                        → explicitly select the floor
//   - "agent"                         → resolve from registry by claim ownership
//   - "id:<workspace-id>"             → pin to that registered workspace
//
// Errors are returned as values (with a trace) rather than as Go errors so
// the caller can attribute the denial to a specific policy source.
func (g *Gateway) ResolveBackendWorkspace(
	claims *auth.Claims, backend *Backend,
) (cfg *config.WorkspaceConfig, res BackendWorkspaceResolution, denyReason string) {
	if backend == nil || backend.Config == nil {
		return nil, BackendWorkspaceResolution{Source: "backend.static"}, ""
	}
	staticCfg := config.NormalizeWorkspaceConfig(backend.Config.Workspace)
	res = g.collectBackendPolicyTrace(claims, backend.Config.ID)
	if res.Selector == "" {
		res.Selector = "static"
		if staticCfg != nil {
			res.WorkspaceID = staticCfg.ID
		}
		return staticCfg, res, ""
	}
	return g.applyWorkspaceSelector(claims, staticCfg, res)
}

// collectBackendPolicyTrace walks every layer the resolver returns and
// records what each layer said for the given backend. The first non-empty
// selector wins, but the full trace is preserved for audit/UI purposes.
func (g *Gateway) collectBackendPolicyTrace(
	claims *auth.Claims, backendID string,
) BackendWorkspaceResolution {
	res := BackendWorkspaceResolution{Source: "backend.static"}
	resolver := g.getBackendPolicyResolver()
	if resolver == nil || claims == nil {
		return res
	}
	for _, layer := range resolver.ResolveBackendPolicy(claims) {
		rule, hasRule := layer.Policies[backendID]
		selector := ""
		if hasRule {
			selector = strings.TrimSpace(rule.WorkspaceSelector)
		}
		res.Layers = append(res.Layers, ResolutionLayerTrace{
			Source:   layer.Source,
			Selector: selector,
		})
		if selector == "" || res.Selector != "" {
			continue
		}
		res.Selector = selector
		res.Source = layer.Source
	}
	return res
}

// applyWorkspaceSelector turns a resolved selector value into a concrete
// WorkspaceConfig, attributing failures to the policy source for clean
// operator feedback.
func (g *Gateway) applyWorkspaceSelector(
	claims *auth.Claims,
	staticCfg *config.WorkspaceConfig,
	res BackendWorkspaceResolution,
) (*config.WorkspaceConfig, BackendWorkspaceResolution, string) {
	switch {
	case res.Selector == "static":
		if staticCfg != nil {
			res.WorkspaceID = staticCfg.ID
		}
		return staticCfg, res, ""
	case res.Selector == "agent":
		ws, reason := g.resolveAgentWorkspace(claims)
		if reason != "" {
			return nil, res, reason
		}
		res.WorkspaceID = ws.ID
		return ws, res, ""
	case strings.HasPrefix(res.Selector, "id:"):
		return g.resolveIDSelector(res)
	default:
		return nil, res, fmt.Sprintf("unknown workspace selector %q", res.Selector)
	}
}

// allowBackendCall checks the per-(caller, backend) rate limiter. Returns
// true (allowed) when no policy limit is configured or RPS=0. Buckets are
// keyed by the caller's most-stable identifier and the backend id so the
// same agent gets independent quotas across backends.
func (g *Gateway) allowBackendCall(claims *auth.Claims, backendID string, limit *auth.BackendRateLimit) bool {
	if limit == nil || limit.RPS <= 0 {
		return true
	}
	callerID := ""
	if claims != nil {
		switch {
		case claims.PrismID != "":
			callerID = claims.PrismID
		case claims.ClientID != "":
			callerID = claims.ClientID
		default:
			callerID = claims.Subject
		}
	}
	if callerID == "" {
		callerID = "anonymous"
	}
	key := callerID + "|" + backendID
	burst := limit.Burst
	if burst <= 0 {
		burst = int(limit.RPS)
		if burst < 1 {
			burst = 1
		}
	}

	g.rateLimitMu.Lock()
	entry := g.rateLimitCache[key]
	// Rebuild the limiter if the policy spec changed; otherwise reuse it
	// so we don't lose accumulated tokens between calls.
	if entry == nil || entry.rps != limit.RPS || entry.burst != burst {
		entry = &backendRateLimitEntry{
			rps:     limit.RPS,
			burst:   burst,
			limiter: rate.NewLimiter(rate.Limit(limit.RPS), burst),
		}
		g.rateLimitCache[key] = entry
	}
	limiter := entry.limiter
	g.rateLimitMu.Unlock()

	return limiter.Allow()
}

// BackendRateLimitResolution captures the rate-limit decision the gateway
// made for an (agent, backend) tuple plus the layer chain. Mirrors
// BackendWorkspaceResolution.
type BackendRateLimitResolution struct {
	Limit  *auth.BackendRateLimit `json:"limit,omitempty"`
	Source string                 `json:"source"` // policy layer or ""
	Layers []ResolutionLayerTrace `json:"layers,omitempty"`
}

// ResolveBackendRateLimit walks the layered backend policy and returns the
// effective rate limit for a tool call. Nil means "no limit applies"
// (callers should fall through to whatever global limiter exists).
//
// Per-layer override semantics: a RateLimit set anywhere wins from the
// agent layer down; a layer can intentionally clear inherited limits by
// setting RPS=0 (Burst is ignored).
func (g *Gateway) ResolveBackendRateLimit(
	claims *auth.Claims, backend *Backend,
) (limit *auth.BackendRateLimit, res BackendRateLimitResolution) {
	if backend == nil || backend.Config == nil {
		return nil, BackendRateLimitResolution{}
	}
	resolver := g.getBackendPolicyResolver()
	if resolver == nil || claims == nil {
		return nil, BackendRateLimitResolution{}
	}
	for _, layer := range resolver.ResolveBackendPolicy(claims) {
		rule, hasRule := layer.Policies[backend.Config.ID]
		var traceSelector string
		if hasRule && rule.RateLimit != nil {
			traceSelector = fmt.Sprintf("rps=%g burst=%d", rule.RateLimit.RPS, rule.RateLimit.Burst)
		}
		res.Layers = append(res.Layers, ResolutionLayerTrace{
			Source:   layer.Source,
			Selector: traceSelector,
		})
		if !hasRule || rule.RateLimit == nil || limit != nil {
			continue
		}
		limit = rule.RateLimit
		res.Limit = rule.RateLimit
		res.Source = layer.Source
	}
	return limit, res
}

// auditTraceFromResolution converts the gateway's internal trace structure
// into the audit package's shape so the audit log carries the same chain
// the policy UI does.
func auditTraceFromResolution(res BackendWorkspaceResolution) *audit.PolicyTrace {
	if res.Selector == "" && res.Source == "" && len(res.Layers) == 0 && res.WorkspaceID == "" {
		return nil
	}
	out := &audit.PolicyTrace{
		WorkspaceID: res.WorkspaceID,
		Selector:    res.Selector,
		Source:      res.Source,
	}
	if len(res.Layers) > 0 {
		out.Layers = make([]audit.PolicyTraceLayer, 0, len(res.Layers))
		for _, l := range res.Layers {
			out.Layers = append(out.Layers, audit.PolicyTraceLayer{
				Source:   l.Source,
				Selector: l.Selector,
			})
		}
	}
	return out
}

func (g *Gateway) resolveIDSelector(
	res BackendWorkspaceResolution,
) (*config.WorkspaceConfig, BackendWorkspaceResolution, string) {
	id := strings.TrimSpace(strings.TrimPrefix(res.Selector, "id:"))
	if id == "" {
		return nil, res, "policy selector 'id:' is missing a workspace id"
	}
	entry, ok := g.registeredWorkspace(id)
	if !ok {
		return nil, res, fmt.Sprintf("policy pins workspace %q but it is not registered", id)
	}
	res.WorkspaceID = entry.ID
	return &config.WorkspaceConfig{
		ID:               entry.ID,
		Type:             entry.Type,
		QuotaBytes:       entry.QuotaBytes,
		RetentionSeconds: entry.RetentionSeconds,
	}, res, ""
}

// resolveAgentWorkspace returns the single workspace registered with the
// calling agent's identity as owner. Zero or many matches are explicit
// errors so operators get clear feedback.
func (g *Gateway) resolveAgentWorkspace(
	claims *auth.Claims,
) (cfg *config.WorkspaceConfig, denyReason string) {
	if claims == nil {
		return nil, "policy requires agent workspace but call has no identity"
	}
	if g.kvStore == nil {
		return nil, "policy requires agent workspace but no workspace registry is configured"
	}
	candidates := []string{claims.PrismID, claims.ClientID, claims.Subject}
	matches := make([]*workspaceRegistryEntry, 0, 2)
	for _, entry := range g.loadAllRegisteredWorkspaceEntries() {
		if entry.Owner == "" {
			continue
		}
		for _, id := range candidates {
			if id != "" && entry.Owner == id {
				matches = append(matches, entry)
				break
			}
		}
	}
	switch len(matches) {
	case 0:
		return nil, "no workspace is registered for the calling agent — install prism-bridge with --use-agent-credentials"
	case 1:
		e := matches[0]
		return &config.WorkspaceConfig{
			ID:               e.ID,
			Type:             e.Type,
			QuotaBytes:       e.QuotaBytes,
			RetentionSeconds: e.RetentionSeconds,
		}, ""
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		sort.Strings(ids)
		return nil, fmt.Sprintf(
			"%d workspaces are registered for this agent (%s); set workspace_selector to id:<one> to disambiguate",
			len(matches), strings.Join(ids, ", "),
		)
	}
}

// loadAllRegisteredWorkspaceEntries returns every registry entry. Unlike
// loadRegisteredWorkspaces() which returns admin.WorkspaceStatus values, this
// gives the raw entries so resolution can inspect ownership.
func (g *Gateway) loadAllRegisteredWorkspaceEntries() []*workspaceRegistryEntry {
	if g.kvStore == nil {
		return nil
	}
	keys, err := g.kvStore.List(workspaceRegistryPrefix)
	if err != nil {
		return nil
	}
	out := make([]*workspaceRegistryEntry, 0, len(keys))
	for _, key := range keys {
		data, err := g.kvStore.Get(key)
		if err != nil {
			continue
		}
		var entry workspaceRegistryEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			continue
		}
		out = append(out, &entry)
	}
	return out
}

// AgentBackendResolution mirrors BackendWorkspaceResolution but is keyed by
// backend id for an agent-wide preview (admin "why" view).
type AgentBackendResolution struct {
	BackendID   string                 `json:"backend_id"`
	WorkspaceID string                 `json:"workspace_id,omitempty"`
	Selector    string                 `json:"selector"`
	Source      string                 `json:"source"`
	Layers      []ResolutionLayerTrace `json:"layers,omitempty"`
	DenyReason  string                 `json:"deny_reason,omitempty"`
}

// BackendByID returns the connected Backend for the given id, or nil. Read-
// only accessor for admin/UI machinery that needs to feed a backend into
// the resolver functions.
func (g *Gateway) BackendByID(id string) *Backend {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.backends[id]
}

// PreviewAgentBackendResolutions returns, for each currently-known backend,
// what workspace policy resolution would pick for the given agent identity.
// Used by the admin UI to render the "why" view; pure preview, no side
// effects. Groups are synthesized from the agent's persisted policy only
// (no IDP claim emulation), so the trace matches what an operator-driven
// configuration would produce.
func (g *Gateway) PreviewAgentBackendResolutions(claims *auth.Claims) []AgentBackendResolution {
	g.mu.RLock()
	backends := make([]*Backend, 0, len(g.backends))
	for _, b := range g.backends {
		backends = append(backends, b)
	}
	g.mu.RUnlock()
	sort.Slice(backends, func(i, j int) bool { return backends[i].Config.ID < backends[j].Config.ID })

	out := make([]AgentBackendResolution, 0, len(backends))
	for _, b := range backends {
		cfg, res, deny := g.ResolveBackendWorkspace(claims, b)
		entry := AgentBackendResolution{
			BackendID:  b.Config.ID,
			Selector:   res.Selector,
			Source:     res.Source,
			Layers:     res.Layers,
			DenyReason: deny,
		}
		if cfg != nil {
			entry.WorkspaceID = cfg.ID
		} else if res.WorkspaceID != "" {
			entry.WorkspaceID = res.WorkspaceID
		}
		out = append(out, entry)
	}
	return out
}

// validateBackendWorkspaceBinding rejects a backend configuration whose
// declared workspace type conflicts with an existing registry entry. Lazy
// references (workspace.id set but no registry entry yet) are allowed — the
// entry can be created later via POST /workspaces.
func (g *Gateway) validateBackendWorkspaceBinding(cfg *config.WorkspaceConfig) error {
	if cfg == nil || cfg.ID == "" || cfg.Type == "" {
		return nil
	}
	entry, ok := g.registeredWorkspace(cfg.ID)
	if !ok {
		return nil
	}
	if entry.Type != "" && entry.Type != cfg.Type {
		return fmt.Errorf(
			"workspace %q is registered as %q; cannot attach as %q",
			cfg.ID, entry.Type, cfg.Type,
		)
	}
	return nil
}

func workspaceValueAllowed(allowed []string, candidates ...string) bool {
	for _, value := range allowed {
		if value == "*" {
			return true
		}
		for _, candidate := range candidates {
			if candidate != "" && value == candidate {
				return true
			}
		}
	}
	return false
}

// DisconnectBackend closes the connection to a backend and removes its tools.
func (g *Gateway) DisconnectBackend(id string) error {
	return g.disconnectBackend(id, false)
}

func (g *Gateway) disconnectBackend(id string, preserveState bool) error {
	g.mu.Lock()
	b, ok := g.backends[id]
	if ok {
		delete(g.backends, id)
	}
	g.mu.Unlock()

	if !ok {
		return fmt.Errorf("backend %q not found", id)
	}
	instances := g.takeWorkspaceInstancesForTemplate(id)

	// Remove tools registered under this backend's namespace
	g.removeBackendTools(b)

	if !preserveState {
		// Remove credential registration and persisted configs
		g.credStore.Unregister(id)
		g.deletePersistedCredential(id)
		g.deletePersistedBackend(id)
		g.cleanupOAuthForBackend(id)
	}

	// Drain in-flight tool calls before closing the session so the SDK
	// doesn't get use-after-close. Since we already removed from the map
	// above, no new tool calls can grab this backend.
	b.inflight.Wait()
	if b.Session != nil {
		_ = b.Session.Close()
	}
	for _, inst := range instances {
		g.closeWorkspaceInstance(inst)
	}
	g.logger.Info("disconnected backend", "id", id)
	metrics.DecActiveBackends()
	return nil
}

func (g *Gateway) takeWorkspaceInstancesForTemplate(templateID string) []*Backend {
	g.mu.Lock()
	defer g.mu.Unlock()
	prefix := templateID + "\x00"
	instances := make([]*Backend, 0)
	for key, inst := range g.workspaceInst {
		if strings.HasPrefix(key, prefix) {
			instances = append(instances, inst)
			delete(g.workspaceInst, key)
		}
	}
	return instances
}

func (g *Gateway) closeWorkspaceInstance(inst *Backend) {
	if inst == nil {
		return
	}
	inst.inflight.Wait()
	if inst.Session != nil {
		_ = inst.Session.Close()
	}
	if inst.Config != nil && inst.Config.ID != "" {
		if err := g.removeBridgeBackend(inst.Config.ID); err != nil {
			g.logger.Warn("failed to remove workspace backend instance", "id", inst.Config.ID, "error", err)
		}
	}
}

// removeBackendTools removes all tools registered by a backend.
// Triggers notifications/tools/list_changed to all connected clients.
func (g *Gateway) removeBackendTools(b *Backend) {
	if len(b.ToolNames) == 0 {
		return
	}
	g.server.RemoveTools(b.ToolNames...)
	g.logger.Info("removed tools from backend", "id", b.Config.ID, "count", len(b.ToolNames))
}

// Close disconnects all backends. Each backend's in-flight tool calls are
// drained before its session is closed.
func (g *Gateway) Close() {
	g.mu.Lock()
	backends := g.backends
	g.backends = make(map[string]*Backend)
	instances := g.workspaceInst
	g.workspaceInst = make(map[string]*Backend)
	g.mu.Unlock()

	for id, b := range backends {
		b.inflight.Wait()
		if b.Session != nil {
			_ = b.Session.Close()
		}
		g.logger.Info("disconnected backend", "id", id)
	}
	for _, inst := range instances {
		g.closeWorkspaceInstance(inst)
	}
}

// BackendIDs returns the IDs of all connected backends.
func (g *Gateway) BackendIDs() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ids := make([]string, 0, len(g.backends))
	for id := range g.backends {
		ids = append(ids, id)
	}
	return ids
}

// BackendCredentialInfo is the obfuscated credential metadata returned in status.
type BackendCredentialInfo struct {
	Type       string `json:"type"`              // "static", "env", "command", "none"
	Header     string `json:"header,omitempty"`  // which header is set
	Env        string `json:"env,omitempty"`     // env var name (env type only)
	Command    string `json:"command,omitempty"` // shell command (command type only)
	Configured bool   `json:"configured"`        // true if a credential is registered
}

// BackendStatus returns connection info for a backend.
type BackendStatus struct {
	ID             string                  `json:"id"`
	Namespace      string                  `json:"namespace"`
	URL            string                  `json:"url"`
	Enabled        bool                    `json:"enabled"`
	Transport      string                  `json:"transport,omitempty"`
	CircuitBreaker string                  `json:"circuit_breaker,omitempty"`
	Credential     *BackendCredentialInfo  `json:"credential,omitempty"`
	Tools          []BackendToolInfo       `json:"tools,omitempty"`
	BridgeManaged  bool                    `json:"bridge_managed,omitempty"`
	Runtime        string                  `json:"runtime,omitempty"`
	Sandbox        config.SandboxConfig    `json:"sandbox,omitempty"`
	Workspace      *config.WorkspaceConfig `json:"workspace,omitempty"`
	// Disconnected is true for backends that exist in KV but failed to
	// reconnect on the current run. Lets the UI flag them as broken/
	// deletable without confusing them with healthy backends.
	Disconnected bool `json:"disconnected,omitempty"`
}

// RegisterCredential registers a credential for a backend in the credential store.
func (g *Gateway) RegisterCredential(backendID string, cred credentials.Credential) {
	g.credStore.Register(backendID, cred)
}

// UnregisterCredential removes a credential for a backend from the credential store.
func (g *Gateway) UnregisterCredential(backendID string) {
	g.credStore.Unregister(backendID)
}

// CredentialInfo returns non-secret metadata about a backend's credential.
// Returns nil if no credential is registered.
func (g *Gateway) CredentialInfo(backendID string) *BackendCredentialInfo {
	info := g.credStore.Info(backendID)
	if info == nil {
		return nil
	}
	return &BackendCredentialInfo{
		Type:       info.Type,
		Header:     info.Header,
		Env:        info.Env,
		Command:    info.Command,
		Configured: true,
	}
}

// annotateDisabledTools copies BackendToolInfo entries with the Disabled flag
// set from the live disabled set. The slice in Backend.Tools doesn't carry
// the flag itself — keeping the source-of-truth on Backend.DisabledTools
// avoids drift when toggles are flipped after registration.
func annotateDisabledTools(tools []BackendToolInfo, namespace string, disabled map[string]struct{}) []BackendToolInfo {
	if len(tools) == 0 {
		return tools
	}
	out := make([]BackendToolInfo, len(tools))
	prefix := namespace + namespaceSeparator
	for i, t := range tools {
		bare := strings.TrimPrefix(t.Name, prefix)
		_, off := disabled[bare]
		out[i] = BackendToolInfo{
			Name:        t.Name,
			Description: t.Description,
			Disabled:    off,
		}
	}
	return out
}

// Status returns status info for all backends, including any KV-persisted
// entries that failed to reconnect this run (so the UI can show them as
// broken and the operator can remove them).
func (g *Gateway) Status() []BackendStatus {
	g.mu.RLock()
	statuses := make([]BackendStatus, 0, len(g.backends))
	connected := make(map[string]struct{}, len(g.backends))
	for _, b := range g.backends {
		s := BackendStatus{
			ID:            b.Config.ID,
			Namespace:     b.Config.Namespace,
			URL:           b.Config.URL,
			Enabled:       true,
			Transport:     b.Transport,
			Tools:         annotateDisabledTools(b.Tools, b.Config.Namespace, b.DisabledTools),
			BridgeManaged: b.Config.BridgeManaged,
			Runtime:       b.Config.BridgeRuntime,
			Sandbox:       config.NormalizeSandboxConfig(&b.Config.Sandbox, config.SandboxProfileDefault),
			Workspace:     config.NormalizeWorkspaceConfig(b.Config.Workspace),
		}
		if b.CB != nil {
			s.CircuitBreaker = b.CB.State().String()
		}
		s.Credential = g.CredentialInfo(b.Config.ID)
		statuses = append(statuses, s)
		connected[b.Config.ID] = struct{}{}
	}
	g.mu.RUnlock()

	if g.kvStore != nil {
		keys, err := g.kvStore.List(backendKVPrefix)
		if err == nil {
			for _, key := range keys {
				id := strings.TrimPrefix(key, backendKVPrefix)
				if _, ok := connected[id]; ok {
					continue
				}
				orphan := BackendStatus{
					ID:        id,
					Namespace: id,
					Enabled:   true,
				}
				if data, getErr := g.kvStore.Get(key); getErr == nil {
					var pb persistedBackend
					if json.Unmarshal(data, &pb) == nil {
						orphan.URL = pb.URL
						orphan.Enabled = pb.isEnabled()
						orphan.BridgeManaged = pb.BridgeManaged
						orphan.Runtime = pb.Runtime
						orphan.Sandbox = pb.sandboxConfig()
						orphan.Workspace = pb.workspaceConfig()
						if pb.isOpenAPI() {
							orphan.Transport = "openapi"
							if pb.OpenAPIBaseURL != "" {
								orphan.URL = pb.OpenAPIBaseURL
							}
						}
					}
				}
				if orphan.Enabled {
					orphan.Disconnected = true
					orphan.CircuitBreaker = "open"
				}
				orphan.Credential = g.CredentialInfo(id)
				statuses = append(statuses, orphan)
			}
		}
	}
	// Stable order keeps the UI deterministic across refreshes — backends come
	// from a map, so iteration order would otherwise reshuffle every call.
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].ID < statuses[j].ID
	})
	return statuses
}

// buildCredential converts a CredentialConfig into the matching Credential implementation.
// Type is inferred from which field is set (validated at config load time).
func buildCredential(c *config.CredentialConfig) credentials.Credential {
	header := c.Header
	if header == "" {
		header = "Authorization"
	}

	switch c.InferredType() {
	case "static":
		return &credentials.Static{Header: header, Value: c.Value}
	case "env":
		return &credentials.Env{Header: header, EnvVar: c.Env}
	case "file":
		return &credentials.File{Header: header, Path: c.File}
	case "command":
		return &credentials.Command{Header: header, Cmd: c.Command, TTL: c.TTL.Duration()}
	default:
		return &credentials.Static{}
	}
}

// parseNamespacedTool splits "namespace__tool_name" into its parts.
func parseNamespacedTool(name string) (namespace, tool string, ok bool) {
	idx := strings.Index(name, namespaceSeparator)
	if idx < 0 {
		return "", "", false
	}
	ns := name[:idx]
	t := name[idx+len(namespaceSeparator):]
	if ns == "" || t == "" {
		return "", "", false
	}
	return ns, t, true
}
