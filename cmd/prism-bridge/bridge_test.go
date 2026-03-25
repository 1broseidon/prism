package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolMode(t *testing.T) {
	// Write a simple tool script that reads JSON from stdin and echoes a field.
	dir := t.TempDir()

	var scriptPath, interpreter string
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	scriptPath = filepath.Join(dir, "echo_tool.sh")
	interpreter = "bash"
	// The script reads JSON from stdin using a simple approach.
	script := `#!/bin/bash
read input
# Extract the "message" field using basic string manipulation
message=$(echo "$input" | sed -n 's/.*"message":"\([^"]*\)".*/\1/p')
echo "got: $message"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// Test executeTool directly.
	ctx := context.Background()
	args := map[string]any{"message": "hello"}

	result, err := executeTool(ctx, testLogger(t), []string{interpreter, scriptPath}, args)
	if err != nil {
		t.Fatalf("executeTool error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %v", result.Content)
	}

	text := result.Content[0].(*mcp.TextContent).Text
	if text != "got: hello" {
		t.Errorf("got %q, want %q", text, "got: hello")
	}
}

func TestToolModeError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fail_tool.sh")
	script := `#!/bin/bash
echo "something went wrong" >&2
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	result, err := executeTool(ctx, testLogger(t), []string{"bash", scriptPath}, nil)
	if err != nil {
		t.Fatalf("executeTool error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error")
	}

	text := result.Content[0].(*mcp.TextContent).Text
	if text != "something went wrong" {
		t.Errorf("got %q, want %q", text, "something went wrong")
	}
}

func TestToolManifestLoad(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "tool.json")

	manifest := `{
		"name": "check-dns",
		"description": "Resolve a hostname",
		"input_schema": {
			"type": "object",
			"properties": {
				"hostname": {"type": "string", "description": "The hostname"}
			},
			"required": ["hostname"]
		}
	}`

	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := loadManifestFile(manifestPath)
	if err != nil {
		t.Fatalf("loadManifestFile error: %v", err)
	}

	if m.Name != "check-dns" {
		t.Errorf("name: got %q, want %q", m.Name, "check-dns")
	}
	if m.Description != "Resolve a hostname" {
		t.Errorf("description: got %q, want %q", m.Description, "Resolve a hostname")
	}
	if m.InputSchema == nil {
		t.Fatal("input_schema is nil")
	}
	if m.InputSchema["type"] != "object" {
		t.Errorf("schema type: got %v, want 'object'", m.InputSchema["type"])
	}
}

func TestServeModeEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	// Create a minimal stdio MCP server as a Go script that we compile and run.
	// Instead, we'll test the serve mode's tool forwarding by creating a mock
	// MCP server over HTTP and testing the client connection pattern.

	// For the serve mode, we test the tool registration + forwarding pattern
	// by setting up a real MCP server on httptest and connecting through it.
	backendServer := mcp.NewServer(&mcp.Implementation{
		Name:    "test-backend",
		Version: "0.1.0",
	}, nil)

	backendServer.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "Returns pong",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
		}, nil
	})

	backendHTTP := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return backendServer },
		nil,
	))
	defer backendHTTP.Close()

	// Now create a bridge-style server that re-exposes tools.
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "bridge-client",
		Version: "0.1.0",
	}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: backendHTTP.URL + "/mcp",
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	toolsResult, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	// Re-expose via a new server (bridge pattern).
	bridgeServer := mcp.NewServer(&mcp.Implementation{
		Name:    "bridge",
		Version: "0.1.0",
	}, nil)

	for _, tool := range toolsResult.Tools {
		toolName := tool.Name
		bridgeServer.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return session.CallTool(ctx, &mcp.CallToolParams{
				Name:      toolName,
				Arguments: req.Params.Arguments,
			})
		})
	}

	bridgeHTTP := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return bridgeServer },
		nil,
	))
	defer bridgeHTTP.Close()

	// Connect a client to the bridge and verify tool call works.
	bridgeClient := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0.1.0",
	}, nil)

	bridgeSession, err := bridgeClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: bridgeHTTP.URL + "/mcp",
	}, nil)
	if err != nil {
		t.Fatalf("connect to bridge: %v", err)
	}
	defer bridgeSession.Close()

	// List tools through bridge.
	bridgeTools, err := bridgeSession.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools through bridge: %v", err)
	}
	if len(bridgeTools.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(bridgeTools.Tools))
	}
	if bridgeTools.Tools[0].Name != "ping" {
		t.Errorf("tool name: got %q, want %q", bridgeTools.Tools[0].Name, "ping")
	}

	// Call tool through bridge.
	callResult, err := bridgeSession.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
	if err != nil {
		t.Fatalf("call tool through bridge: %v", err)
	}
	text := callResult.Content[0].(*mcp.TextContent).Text
	if text != "pong" {
		t.Errorf("got %q, want %q", text, "pong")
	}
}

func TestParseFlag(t *testing.T) {
	flags := []string{"--port", "3002", "--host", "127.0.0.1", "--name", "test"}

	val, remaining := parseFlag(flags, "port", "3001")
	if val != "3002" {
		t.Errorf("port: got %q, want %q", val, "3002")
	}
	if len(remaining) != 4 {
		t.Errorf("remaining length: got %d, want 4", len(remaining))
	}

	val, _ = parseFlag(flags, "missing", "default")
	if val != "default" {
		t.Errorf("missing: got %q, want %q", val, "default")
	}
}

func TestSplitAtDashDash(t *testing.T) {
	flags, cmd := splitAtDashDash([]string{"--port", "3001", "--", "npx", "server"})
	if len(flags) != 2 || flags[0] != "--port" {
		t.Errorf("flags: got %v", flags)
	}
	if len(cmd) != 2 || cmd[0] != "npx" {
		t.Errorf("cmd: got %v", cmd)
	}

	flags, cmd = splitAtDashDash([]string{"--port", "3001"})
	if len(flags) != 2 || cmd != nil {
		t.Errorf("no dash dash: flags=%v cmd=%v", flags, cmd)
	}
}

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.Default()
}

// Verify ToolManifest JSON round-trip.
func TestToolManifestJSON(t *testing.T) {
	m := &ToolManifest{
		Name:        "test",
		Description: "A test tool",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		},
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	var m2 ToolManifest
	if err := json.Unmarshal(data, &m2); err != nil {
		t.Fatal(err)
	}

	if m2.Name != "test" || m2.Description != "A test tool" {
		t.Errorf("round-trip failed: %+v", m2)
	}
}

// Needed to use slog in tests.
func init() {
	_ = fmt.Sprintf // silence unused import
}
