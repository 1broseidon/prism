// Package gateway implements the MCP aggregation core.
//
// It acts as an MCP server to clients and an MCP client to each backend,
// aggregating tools, resources, and prompts under namespaced prefixes.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

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
)

const namespaceSeparator = "__"

// Backend represents a connected backend MCP server.
type Backend struct {
	Config    *config.ServerConfig
	Client    *mcp.Client
	Session   *mcp.ClientSession
	CB        *middleware.CircuitBreaker
	ToolNames []string // namespaced tool names registered on the gateway
}

// Gateway aggregates multiple MCP backends behind a single server.
type Gateway struct {
	mu             sync.RWMutex
	backends       map[string]*Backend // keyed by server ID
	server         *mcp.Server
	logger         *slog.Logger
	auditor        *audit.Logger
	credStore      *credentials.Store
	kvStore        store.Store         // optional KV store for persisting runtime credential configs
	policyResolver auth.PolicyResolver // live policy resolution, bypasses stale session context
}

// New creates a new Gateway.
func New(logger *slog.Logger) *Gateway {
	if logger == nil {
		logger = slog.Default()
	}
	g := &Gateway{
		backends:  make(map[string]*Backend),
		logger:    logger,
		auditor:   audit.Noop(), // replaced via SetAuditLogger if audit is configured
		credStore: credentials.NewStore(),
	}

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
func (g *Gateway) SetPolicyResolver(pr auth.PolicyResolver) {
	g.policyResolver = pr
}

// NotifyToolsChanged triggers a tools/list_changed notification to all MCP sessions.
// Clients that receive this notification re-fetch tools/list, which goes through
// the scope filter with live policy — so newly granted or denied tools appear/disappear.
func (g *Gateway) NotifyToolsChanged() {
	// The MCP SDK doesn't expose a public method to send arbitrary notifications.
	// Adding and immediately removing a sentinel tool triggers changeAndNotify
	// for notificationToolListChanged, which debounces and sends to all sessions.
	sentinel := &mcp.Tool{Name: "__prism_policy_refresh"}
	g.server.AddTool(sentinel, nil)
	g.server.RemoveTools(sentinel.Name)
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

// KV key prefixes for backend persistence.
const credKVPrefix = "backend/cred/"
const backendKVPrefix = "backend/config/"

// persistedBackend is the JSON representation of a runtime-added backend stored in KV.
type persistedBackend struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
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

		sc := &config.ServerConfig{
			ID:        backendID,
			Namespace: backendID,
			URL:       pb.URL,
			Env:       pb.Env,
			Timeout:   config.Duration(30 * time.Second),
		}
		if pb.Command != "" {
			sc.Command = append([]string{pb.Command}, pb.Args...)
		}

		if err := g.ConnectBackend(ctx, sc); err != nil {
			g.logger.Warn("failed to reconnect persisted backend", "id", backendID, "error", err)
			continue
		}
		g.logger.Info("restored persisted backend", "id", backendID)
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

			policy := auth.LivePolicy(ctx, g.policyResolver)
			if policy == nil {
				// Open / unauthenticated mode: return all tools.
				return result, nil
			}

			toolsResult, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, nil
			}

			ctx, span := tracer.Start(ctx, "prism.gateway.scope_filter",
				trace.WithSpanKind(trace.SpanKindInternal),
			)
			defer span.End()

			toolsBefore := len(toolsResult.Tools)

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
				if policy.CanAccessTool(ns, name) {
					filtered = append(filtered, t)
				}
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

	backend := &Backend{
		Config:  cfg,
		Client:  client,
		Session: session,
		CB:      cb,
	}

	g.mu.Lock()
	g.backends[cfg.ID] = backend
	g.mu.Unlock()

	if err := g.registerBackendTools(ctx, backend); err != nil {
		g.logger.Warn("failed to register tools", "id", cfg.ID, "error", err)
	}

	ttype := "http"
	if cfg.IsStdio() {
		ttype = "stdio"
	}
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

		handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return g.routeToolCall(ctx, backendID, originalName, req)
		}

		g.server.AddTool(namespacedTool, handler)
		names = append(names, namespacedName)
	}

	// Track tool names so we can remove them when the backend disconnects.
	b.ToolNames = names

	g.logger.Info("registered tools from backend", "id", b.Config.ID, "count", len(result.Tools))
	return nil
}

// routeToolCall forwards a tool call to the correct backend.
// It enforces scope-based access control and emits a structured audit log entry
// for every call (allowed or denied).
func (g *Gateway) routeToolCall(ctx context.Context, backendID, toolName string, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tracer := otel.Tracer("prism.gateway")
	ctx, span := tracer.Start(ctx, "prism.gateway.tool_call",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	defer span.End()

	g.mu.RLock()
	b, ok := g.backends[backendID]
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

	span.SetAttributes(
		attribute.String("tool.namespace", b.Config.Namespace),
		attribute.String("tool.name", toolName),
		attribute.String("tool.backend", backendID),
	)

	// credInjected tracks whether a backend credential is registered for this backend.
	// The actual injection happens in the HTTP transport; we record the fact (not the value).
	credHeader, _, _ := g.credStore.Resolve(ctx, backendID)
	credInjected := credHeader != ""

	// Scope enforcement: check if the caller has permission for this tool.
	// Uses LivePolicy to resolve from KV store (not stale session context).
	if policy := auth.LivePolicy(ctx, g.policyResolver); policy != nil {
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
	}

	span.SetAttributes(attribute.Bool("tool.allowed", true))

	if b.CB != nil && !b.CB.Allow() {
		span.SetStatus(codes.Error, "circuit breaker open")
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("backend %q circuit breaker is open", backendID)},
			},
		}, nil
	}

	start := time.Now()
	result, err := b.Session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: req.Params.Arguments,
	})
	elapsed := time.Since(start)
	latencyMS := elapsed.Milliseconds()

	span.SetAttributes(attribute.Int64("tool.latency_ms", latencyMS))

	// Audit: log the outcome after the call (success or backend error).
	g.auditor.LogCall(ctx, b.Config.Namespace, toolName, backendID, true, credInjected, latencyMS, err)
	metrics.RecordToolCall(b.Config.Namespace, toolName, backendID, true, elapsed)

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

	return result, nil
}

// DisconnectBackend closes the connection to a backend and removes its tools.
func (g *Gateway) DisconnectBackend(id string) error {
	g.mu.Lock()
	b, ok := g.backends[id]
	if ok {
		delete(g.backends, id)
	}
	g.mu.Unlock()

	if !ok {
		return fmt.Errorf("backend %q not found", id)
	}

	// Remove tools registered under this backend's namespace
	g.removeBackendTools(b)

	// Remove credential registration and persisted configs
	g.credStore.Unregister(id)
	g.deletePersistedCredential(id)
	g.deletePersistedBackend(id)

	if b.Session != nil {
		_ = b.Session.Close()
	}
	g.logger.Info("disconnected backend", "id", id)
	metrics.DecActiveBackends()
	return nil
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

// Close disconnects all backends.
func (g *Gateway) Close() {
	g.mu.Lock()
	backends := g.backends
	g.backends = make(map[string]*Backend)
	g.mu.Unlock()

	for id, b := range backends {
		if b.Session != nil {
			_ = b.Session.Close()
		}
		g.logger.Info("disconnected backend", "id", id)
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
	ID             string                 `json:"id"`
	Namespace      string                 `json:"namespace"`
	URL            string                 `json:"url"`
	CircuitBreaker string                 `json:"circuit_breaker,omitempty"`
	Credential     *BackendCredentialInfo `json:"credential,omitempty"`
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

// Status returns status info for all backends.
func (g *Gateway) Status() []BackendStatus {
	g.mu.RLock()
	defer g.mu.RUnlock()

	statuses := make([]BackendStatus, 0, len(g.backends))
	for _, b := range g.backends {
		s := BackendStatus{
			ID:        b.Config.ID,
			Namespace: b.Config.Namespace,
			URL:       b.Config.URL,
		}
		if b.CB != nil {
			s.CircuitBreaker = b.CB.State().String()
		}
		s.Credential = g.CredentialInfo(b.Config.ID)
		statuses = append(statuses, s)
	}
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
