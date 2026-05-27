package gateway

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// grantWorkspaceCtxKey carries the pinned workspace tuple from the
// middleware-side grant match into spawn/dispatch helpers that need to
// check for live-config drift before forwarding a call.
type grantWorkspaceCtxKey struct{}

// contextWithGrantWorkspace attaches a pinned workspace instance to ctx.
// Helpers downstream of the grant pipeline read this back via
// grantWorkspaceFromContext.
func contextWithGrantWorkspace(ctx context.Context, ws *auth.WorkspaceInstance) context.Context {
	if ws == nil {
		return ctx
	}
	return context.WithValue(ctx, grantWorkspaceCtxKey{}, ws)
}

// grantWorkspaceFromContext returns the pinned workspace stamped onto ctx,
// or nil when none was attached.
func grantWorkspaceFromContext(ctx context.Context) *auth.WorkspaceInstance {
	ws, _ := ctx.Value(grantWorkspaceCtxKey{}).(*auth.WorkspaceInstance)
	return ws
}

// enforceGrantsForCall runs auth.EnforceGrantCall for one tool invocation.
// Returns a non-nil error result (so the caller surfaces it to the MCP
// client) when the call is denied; returns nil + the enriched context
// when the call is allowed.
//
// For grants-bearing tokens, the matched grant's pinned workspace is
// stamped onto the returned context so downstream spawn paths can check
// for drift.
func (g *Gateway) enforceGrantsForCall(
	ctx context.Context,
	b *Backend,
	toolName string,
	arguments json.RawMessage,
	workspaceCfg *config.WorkspaceConfig,
	claims *auth.Claims,
) (*mcp.CallToolResult, context.Context) {
	if claims == nil || len(claims.AuthorizationDetails) == 0 {
		// Legacy scope-only path. Nothing to enforce here.
		return nil, ctx
	}
	authInfo := auth.RequestAuthInfoFromContext(ctx)
	result := auth.EnforceGrantCall(ctx, auth.GrantCallInput{
		Claims:      claims,
		AuthInfo:    authInfo,
		Tool:        backendQualifiedTool(b, toolName),
		Backend:     backendID(b),
		Arguments:   arguments,
		Workspace:   workspaceFromConfig(workspaceCfg),
		Now:         g.now(),
		Emitter:     g.getGrantEmitter(),
		RequestPath: "",
		CompatGate:  g.getCompatGate(),
	})
	if !result.Allowed {
		return grantDeniedResult(result), ctx
	}
	if result.Match.Grant != nil && result.Match.Grant.Workspace != nil {
		ctx = contextWithGrantWorkspace(ctx, result.Match.Grant.Workspace)
	}
	return nil, ctx
}

// workspaceFromConfig is the gateway-flavored equivalent of
// workspaceConfigInstance, kept private to grant_emitter.go so the call
// site reads as a single helper.
func workspaceFromConfig(cfg *config.WorkspaceConfig) *auth.WorkspaceInstance {
	if cfg == nil {
		return nil
	}
	return &auth.WorkspaceInstance{
		ID:        cfg.ID,
		Type:      cfg.Type,
		WriteMode: cfg.WriteMode,
	}
}

func backendID(b *Backend) string {
	if b == nil || b.Config == nil {
		return ""
	}
	return b.Config.ID
}

// backendQualifiedTool returns the tool name in the form grants expect
// (`namespace.tool`). Backends register tools under the namespaced form
// `namespace__tool`; grants reference the dotted form.
func backendQualifiedTool(b *Backend, tool string) string {
	if b == nil || b.Config == nil || b.Config.Namespace == "" {
		return tool
	}
	return b.Config.Namespace + "." + tool
}

// grantDeniedResult renders a CallToolResult that propagates the deny
// reason to the MCP client. The error text matches the canonical
// challenge codes the resource layer would surface on a non-MCP request
// path, so tests that match on substrings keep working.
func grantDeniedResult(result auth.GrantPipelineResult) *mcp.CallToolResult {
	msg := result.Error
	if msg == "" {
		msg = "policy_mismatch"
	}
	if result.AcrValues != "" {
		msg = fmt.Sprintf("%s acr_values=%s", msg, result.AcrValues)
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
