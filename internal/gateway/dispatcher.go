package gateway

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolDispatcher abstracts the leaf "actually make the call" step of
// routeToolCall. Every backend transport (MCP session, OpenAPI HTTP, ...)
// implements this interface; the gateway's auth, policy, workspace, audit,
// circuit-breaker, and disabled-tool stack runs identically above it.
//
// Implementations must be safe for concurrent use. The dispatcher receives
// the tool's bare (un-namespaced) name and the raw MCP arguments JSON. It
// must not perform any policy enforcement — those decisions live above the
// dispatcher boundary in routeToolCall.
type ToolDispatcher interface {
	// Dispatch executes the named tool against the underlying transport and
	// returns an MCP CallToolResult. Returning a non-nil error signals a
	// transport-level failure (the gateway records circuit-breaker failure
	// and translates to an MCP protocol error). Soft errors that should
	// surface to the agent (HTTP 4xx, upstream rejections, timeouts on a
	// per-call basis) belong inside CallToolResult{IsError: true}.
	Dispatch(ctx context.Context, toolName string, arguments json.RawMessage) (*mcp.CallToolResult, error)

	// Tools returns the namespaced tool metadata this dispatcher exposes via
	// the gateway. The slice powers the BackendStatus tool list and is read
	// once per Status() call; implementations may return the same backing
	// slice across calls.
	Tools() []BackendToolInfo
}

// MCPSessionDispatcher wraps an *mcp.ClientSession so existing MCP backends
// route through the ToolDispatcher boundary with zero behavior change. The
// session is owned by the surrounding Backend — close lifecycle stays with
// disconnectBackend.
type MCPSessionDispatcher struct {
	session   *mcp.ClientSession
	tools     []BackendToolInfo
	namespace string
}

// NewMCPSessionDispatcher constructs an MCP-session-backed dispatcher.
// The tools slice is the *namespaced* tool list (e.g. "github__create_issue")
// captured at registration time, mirroring Backend.Tools.
func NewMCPSessionDispatcher(session *mcp.ClientSession, namespace string, tools []BackendToolInfo) *MCPSessionDispatcher {
	return &MCPSessionDispatcher{
		session:   session,
		namespace: namespace,
		tools:     tools,
	}
}

// Dispatch forwards the call to the wrapped MCP session. Mirrors the
// pre-refactor leaf in routeToolCall exactly so existing MCP backends keep
// their current semantics (including how the SDK surfaces upstream errors).
func (d *MCPSessionDispatcher) Dispatch(ctx context.Context, toolName string, arguments json.RawMessage) (*mcp.CallToolResult, error) {
	if d == nil || d.session == nil {
		return nil, fmt.Errorf("mcp session dispatcher is not initialized")
	}
	return d.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: arguments,
	})
}

// Tools returns the cached namespaced tool metadata.
func (d *MCPSessionDispatcher) Tools() []BackendToolInfo {
	if d == nil {
		return nil
	}
	return d.tools
}

// setTools replaces the cached tool slice. The gateway calls this from
// registerBackendTools after it knows the final per-backend tool list.
func (d *MCPSessionDispatcher) setTools(tools []BackendToolInfo) {
	if d == nil {
		return
	}
	d.tools = tools
}
