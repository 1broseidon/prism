// Package gateway implements the MCP aggregation core.
//
// It acts as an MCP server to clients and an MCP client to each backend,
// aggregating tools, resources, and prompts under namespaced prefixes.
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prism-gateway/prism/internal/audit"
	"github.com/prism-gateway/prism/internal/auth"
	"github.com/prism-gateway/prism/internal/config"
	"github.com/prism-gateway/prism/internal/credentials"
	"github.com/prism-gateway/prism/internal/middleware"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const namespaceSeparator = "__"

// Backend represents a connected backend MCP server.
type Backend struct {
	Config  config.ServerConfig
	Client  *mcp.Client
	Session *mcp.ClientSession
	CB      *middleware.CircuitBreaker
}

// Gateway aggregates multiple MCP backends behind a single server.
type Gateway struct {
	mu       sync.RWMutex
	backends map[string]*Backend // keyed by server ID
	server   *mcp.Server
	logger   *slog.Logger
	auditor  *audit.Logger
	credStore *credentials.Store
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

// SetAuditLogger replaces the audit logger used by the gateway.
// Call this before serving any requests. It is not safe to call concurrently
// with active requests.
func (g *Gateway) SetAuditLogger(al *audit.Logger) {
	if al == nil {
		al = audit.Noop()
	}
	g.auditor = al
}

// scopeFilterMiddleware returns an MCP receiving middleware that intercepts
// tools/list responses and strips out tools the caller cannot access.
//
// If no policy is in context (open / unauthenticated mode) all tools are returned.
// If a policy is present, only tools whose namespace:name scope is granted pass through.
func (g *Gateway) scopeFilterMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if method != "tools/list" || err != nil {
				return result, err
			}

			policy := auth.PolicyFromContext(ctx)
			if policy == nil {
				// Open / unauthenticated mode: return all tools.
				return result, nil
			}

			toolsResult, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, nil
			}

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
		func(r *http.Request) *mcp.Server { return g.server },
		nil,
	)
}

// ConnectBackend establishes a connection to a backend MCP server
// and registers its tools on the gateway server.
func (g *Gateway) ConnectBackend(ctx context.Context, cfg config.ServerConfig) error {
	client := mcp.NewClient(
		&mcp.Implementation{
			Name:    "prism-client",
			Version: "0.1.0",
		},
		nil,
	)

	// Register credential for this backend if configured.
	if cfg.Credentials != nil {
		cred := buildCredential(cfg.Credentials)
		g.credStore.Register(cfg.ID, cred)
		g.logger.Info("registered credential for backend",
			"id", cfg.ID,
			"type", cfg.Credentials.Type,
		)
	}

	// Build an HTTP client that injects the backend credential on every request.
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

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect to %s (%s): %w", cfg.ID, cfg.URL, err)
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

	// Discover and register tools from this backend
	if err := g.registerBackendTools(ctx, backend); err != nil {
		g.logger.Warn("failed to register tools", "id", cfg.ID, "error", err)
		// Don't fail — the backend is connected, tools can be registered later
	}

	g.logger.Info("connected to backend", "id", cfg.ID, "url", cfg.URL, "namespace", cfg.Namespace)
	return nil
}

// registerBackendTools fetches tools from a backend and registers them on the gateway server.
func (g *Gateway) registerBackendTools(ctx context.Context, b *Backend) error {
	result, err := b.Session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return fmt.Errorf("list tools from %s: %w", b.Config.ID, err)
	}

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
		g.logger.Debug("registered tool", "name", namespacedName, "backend", b.Config.ID)
	}

	g.logger.Info("registered tools from backend", "id", b.Config.ID, "count", len(result.Tools))
	return nil
}

// routeToolCall forwards a tool call to the correct backend.
// It enforces scope-based access control and emits a structured audit log entry
// for every call (allowed or denied).
func (g *Gateway) routeToolCall(ctx context.Context, backendID, toolName string, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	g.mu.RLock()
	b, ok := g.backends[backendID]
	g.mu.RUnlock()

	if !ok {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("backend %q not found", backendID)},
			},
		}, nil
	}

	// credInjected tracks whether a backend credential is registered for this backend.
	// The actual injection happens in the HTTP transport; we record the fact (not the value).
	credHeader, _, _ := g.credStore.Resolve(ctx, backendID)
	credInjected := credHeader != ""

	// Scope enforcement: check if the caller has permission for this tool.
	if policy := auth.PolicyFromContext(ctx); policy != nil {
		if !policy.CanAccessTool(b.Config.Namespace, toolName) {
			g.logger.Warn("tool call denied by scope policy",
				"backend", backendID,
				"tool", toolName,
				"namespace", b.Config.Namespace,
			)
			// Audit: log the denial before returning.
			g.auditor.LogCall(ctx, b.Config.Namespace, toolName, backendID, false, credInjected, 0, nil)
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

	if b.CB != nil && !b.CB.Allow() {
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
	latencyMS := time.Since(start).Milliseconds()

	// Audit: log the outcome after the call (success or backend error).
	g.auditor.LogCall(ctx, b.Config.Namespace, toolName, backendID, true, credInjected, latencyMS, err)

	if err != nil {
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

	if b.Session != nil {
		b.Session.Close()
	}
	g.logger.Info("disconnected backend", "id", id)
	return nil
}

// removeBackendTools removes all tools with a given backend's namespace prefix.
func (g *Gateway) removeBackendTools(b *Backend) {
	prefix := b.Config.Namespace + namespaceSeparator
	// We don't have a way to list registered tools from the server,
	// so we'd need to track them. For now, we'll use RemoveTools with known names.
	// This will be populated during registerBackendTools in a future iteration.
	_ = prefix
}

// Close disconnects all backends.
func (g *Gateway) Close() {
	g.mu.Lock()
	backends := g.backends
	g.backends = make(map[string]*Backend)
	g.mu.Unlock()

	for id, b := range backends {
		if b.Session != nil {
			b.Session.Close()
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

// BackendStatus returns connection info for a backend.
type BackendStatus struct {
	ID             string `json:"id"`
	Namespace      string `json:"namespace"`
	URL            string `json:"url"`
	CircuitBreaker string `json:"circuit_breaker,omitempty"`
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
		statuses = append(statuses, s)
	}
	return statuses
}

// buildCredential converts a CredentialConfig into the matching Credential implementation.
func buildCredential(c *config.CredentialConfig) credentials.Credential {
	switch c.Type {
	case "static":
		return &credentials.Static{Header: c.Header, Value: c.Value}
	case "env":
		return &credentials.Env{Header: c.Header, EnvVar: c.EnvVar}
	case "file":
		return &credentials.File{Header: c.Header, Path: c.Path}
	case "command":
		return &credentials.Command{Header: c.Header, Cmd: c.Command, TTL: c.TTL.Duration()}
	default:
		// Should never happen — config validation rejects unknown types.
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
