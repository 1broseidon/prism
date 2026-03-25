package gateway

import (
	"context"
	"fmt"
	"testing"

	"github.com/prism-gateway/prism/internal/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// policyCtx is a context.Context that satisfies auth.PolicyFromContext by
// intercepting Value() calls for the key "auth.policy".
//
// auth uses: type contextKey string; policyKey contextKey = "auth.policy"
// Because contextKey is unexported we cannot reference it directly. However,
// fmt.Sprintf("%v", key) on any contextKey value returns the underlying string
// (e.g. "auth.policy"), so we match on that.
type policyCtx struct {
	context.Context
	policy *auth.Policy
}

func (c *policyCtx) Value(key any) any {
	// Intercept the policy key lookup.
	if fmt.Sprintf("%v", key) == "auth.policy" {
		return c.policy
	}
	return c.Context.Value(key)
}

// contextWithPolicy returns a context that auth.PolicyFromContext will resolve
// to a policy built from the given scope string.
func contextWithPolicy(scopeString string) context.Context {
	return &policyCtx{Context: context.Background(), policy: auth.NewPolicy(scopeString)}
}

// makeListToolsResult builds a synthetic ListToolsResult with the given tool names.
func makeListToolsResult(names ...string) *mcp.ListToolsResult {
	tools := make([]*mcp.Tool, len(names))
	for i, n := range names {
		tools[i] = &mcp.Tool{Name: n, Description: "test tool " + n}
	}
	return &mcp.ListToolsResult{Tools: tools}
}

// toolNames extracts tool names from a result for easy assertions.
func toolNames(result *mcp.ListToolsResult) []string {
	names := make([]string, len(result.Tools))
	for i, t := range result.Tools {
		names[i] = t.Name
	}
	return names
}

// applyFilter runs the scope-filter middleware synchronously against a pre-built
// result, exercising the middleware logic without a running MCP server.
func applyFilter(ctx context.Context, g *Gateway, input *mcp.ListToolsResult) (*mcp.ListToolsResult, error) {
	baseHandler := mcp.MethodHandler(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return input, nil
	})
	filtered := g.scopeFilterMiddleware()(baseHandler)
	result, err := filtered(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	return result.(*mcp.ListToolsResult), nil
}

// --- Tests ---

func TestScopeFilter_OpenMode_ReturnsAllTools(t *testing.T) {
	g := New(nil)
	input := makeListToolsResult("github__create_issue", "github__delete_repo", "fs__read_file")

	// No policy in context — open/unauthenticated mode.
	result, err := applyFilter(context.Background(), g, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tools) != 3 {
		t.Errorf("open mode: got %d tools, want 3; names: %v", len(result.Tools), toolNames(result))
	}
}

func TestScopeFilter_ScopedMode_FiltersCorrectly(t *testing.T) {
	g := New(nil)
	input := makeListToolsResult("github__create_issue", "github__delete_repo", "fs__read_file")
	ctx := contextWithPolicy("github:create_issue fs:read_file")

	result, err := applyFilter(ctx, g, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := toolNames(result)
	if len(names) != 2 {
		t.Fatalf("got %d tools, want 2; names: %v", len(names), names)
	}

	has := func(name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}
	if !has("github__create_issue") {
		t.Error("expected github__create_issue to be visible")
	}
	if !has("fs__read_file") {
		t.Error("expected fs__read_file to be visible")
	}
	if has("github__delete_repo") {
		t.Error("expected github__delete_repo to be hidden (no scope)")
	}
}

func TestScopeFilter_NamespaceWildcard(t *testing.T) {
	g := New(nil)
	input := makeListToolsResult("github__create_issue", "github__delete_repo", "fs__read_file")
	ctx := contextWithPolicy("github:*")

	result, err := applyFilter(ctx, g, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := toolNames(result)
	if len(names) != 2 {
		t.Fatalf("got %d tools, want 2; names: %v", len(names), names)
	}
	for _, n := range names {
		ns, _, ok := parseNamespacedTool(n)
		if !ok || ns != "github" {
			t.Errorf("unexpected tool %q with github:* scope", n)
		}
	}
}

func TestScopeFilter_SuperuserScope_ReturnsAll(t *testing.T) {
	g := New(nil)
	input := makeListToolsResult("github__create_issue", "github__delete_repo", "fs__read_file")
	ctx := contextWithPolicy("*")

	result, err := applyFilter(ctx, g, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tools) != 3 {
		t.Errorf("superuser: got %d tools, want 3; names: %v", len(result.Tools), toolNames(result))
	}
}

func TestScopeFilter_NoMatchingScopes_ReturnsEmpty(t *testing.T) {
	g := New(nil)
	input := makeListToolsResult("github__create_issue", "github__delete_repo")
	ctx := contextWithPolicy("mcp:connect") // no tool scopes

	result, err := applyFilter(ctx, g, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Tools) != 0 {
		t.Errorf("got %d tools, want 0; names: %v", len(result.Tools), toolNames(result))
	}
}

func TestScopeFilter_NonToolsListMethod_Passthrough(t *testing.T) {
	g := New(nil)
	input := makeListToolsResult("github__create_issue")
	ctx := contextWithPolicy("mcp:connect")

	baseHandler := mcp.MethodHandler(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return input, nil
	})
	filtered := g.scopeFilterMiddleware()(baseHandler)

	// A method other than tools/list should be passed through unmodified.
	result, err := filtered(ctx, "tools/call", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	toolsResult := result.(*mcp.ListToolsResult)
	if len(toolsResult.Tools) != 1 {
		t.Errorf("non-tools/list method: got %d tools, want 1", len(toolsResult.Tools))
	}
}
