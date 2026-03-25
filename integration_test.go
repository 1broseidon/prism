// Package prism_test contains end-to-end integration tests for the Prism gateway.
//
// Each test spins up a real MCP backend server (via httptest.Server), connects
// the Prism gateway to it, and drives requests through a real MCP client.
// No Docker or external services are required.
package prism_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/audit"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/gateway"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newBackendServer creates a real MCP backend httptest.Server.
func newBackendServer(t *testing.T, s *mcp.Server) *httptest.Server {
	t.Helper()
	handler := mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return s },
		nil,
	)
	hs := httptest.NewServer(handler)
	t.Cleanup(hs.Close)
	return hs
}

// connectClient connects a fresh MCP client to prismURL/mcp and returns the session.
func connectClient(t *testing.T, ctx context.Context, prismURL string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             prismURL + "/mcp",
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// newGateway wires a gateway with an optional audit buffer and connects backends.
func newGateway(t *testing.T, ctx context.Context, auditBuf *bytes.Buffer, backends []backendDef) (*gateway.Gateway, *httptest.Server) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
	gw := gateway.New(logger)
	if auditBuf != nil {
		gw.SetAuditLogger(audit.New(auditBuf))
	}

	for _, b := range backends {
		cfg := &config.ServerConfig{
			ID:          b.id,
			URL:         b.url + "/mcp",
			Namespace:   b.namespace,
			Credentials: b.credentials,
		}
		if err := gw.ConnectBackend(ctx, cfg); err != nil {
			t.Fatalf("ConnectBackend(%s): %v", b.id, err)
		}
	}

	prismHTTP := httptest.NewServer(gw.Handler())
	t.Cleanup(func() {
		prismHTTP.Close()
		gw.Close()
	})
	return gw, prismHTTP
}

type backendDef struct {
	id          string
	url         string
	namespace   string
	credentials *config.CredentialConfig
}

// ─── Tool param / result types ────────────────────────────────────────────────

type echoParams struct {
	Text string `json:"text"`
}

type addParams struct {
	A int `json:"a"`
	B int `json:"b"`
}

type authCheckParams struct {
	Dummy string `json:"dummy,omitempty"`
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestIntegration_ToolDiscoveryAndCall(t *testing.T) {
	ctx := context.Background()

	backendSrv := mcp.NewServer(&mcp.Implementation{Name: "math-backend", Version: "0.1.0"}, nil)
	mcp.AddTool(backendSrv, &mcp.Tool{Name: "echo", Description: "Echo input"},
		func(_ context.Context, _ *mcp.CallToolRequest, p echoParams) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: p.Text}},
			}, nil, nil
		},
	)
	mcp.AddTool(backendSrv, &mcp.Tool{Name: "add", Description: "Add two numbers"},
		func(_ context.Context, _ *mcp.CallToolRequest, p addParams) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%d", p.A+p.B)}},
			}, nil, nil
		},
	)
	backendHS := newBackendServer(t, backendSrv)

	_, prismHS := newGateway(t, ctx, nil, []backendDef{
		{id: "math", url: backendHS.URL, namespace: "math"},
	})

	session := connectClient(t, ctx, prismHS.URL)

	listRes, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, len(listRes.Tools))
	for i, tool := range listRes.Tools {
		names[i] = tool.Name
	}
	for _, want := range []string{"math__echo", "math__add"} {
		if !slices.Contains(names, want) {
			t.Errorf("expected tool %q in list; got %v", want, names)
		}
	}

	echoRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "math__echo",
		Arguments: map[string]any{"text": "hello"},
	})
	if err != nil {
		t.Fatalf("CallTool math__echo: %v", err)
	}
	if echoRes.IsError {
		t.Fatalf("math__echo returned error content: %v", echoRes.Content)
	}
	if text := firstText(echoRes); !strings.Contains(text, "hello") {
		t.Errorf("math__echo: expected \"hello\" in response, got %q", text)
	}

	addRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "math__add",
		Arguments: map[string]any{"a": 2, "b": 3},
	})
	if err != nil {
		t.Fatalf("CallTool math__add: %v", err)
	}
	if addRes.IsError {
		t.Fatalf("math__add returned error content: %v", addRes.Content)
	}
	if text := firstText(addRes); !strings.Contains(text, "5") {
		t.Errorf("math__add: expected \"5\" in response, got %q", text)
	}
}

func TestIntegration_CredentialInjection(t *testing.T) {
	ctx := context.Background()

	var receivedAuth string

	innerMCPHandler := mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server {
			srv := mcp.NewServer(&mcp.Implementation{Name: "auth-backend", Version: "0.1.0"}, nil)
			mcp.AddTool(srv, &mcp.Tool{Name: "whoami", Description: "Check auth"},
				func(_ context.Context, _ *mcp.CallToolRequest, _ authCheckParams) (*mcp.CallToolResult, any, error) {
					text := "no-auth"
					if receivedAuth != "" {
						text = "authed:" + receivedAuth
					}
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: text}},
					}, nil, nil
				},
			)
			return srv
		},
		nil,
	)

	backendHS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		innerMCPHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(backendHS.Close)

	var auditBuf bytes.Buffer
	_, prismHS := newGateway(t, ctx, &auditBuf, []backendDef{
		{
			id:        "auth-backend",
			url:       backendHS.URL,
			namespace: "sec",
			credentials: &config.CredentialConfig{
				Value: "Bearer test-token-123",
			},
		},
	})

	session := connectClient(t, ctx, prismHS.URL)

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "sec__whoami",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool sec__whoami: %v", err)
	}
	if res.IsError {
		t.Fatalf("sec__whoami returned error: %v", firstText(res))
	}
	text := firstText(res)
	if !strings.HasPrefix(text, "authed:") {
		t.Errorf("expected authed response, got %q", text)
	}
	if !strings.Contains(text, "Bearer test-token-123") {
		t.Errorf("expected token in response, got %q", text)
	}

	entries := parseAuditLog(t, &auditBuf)
	if len(entries) == 0 {
		t.Fatal("no audit entries written")
	}
	found := false
	for _, e := range entries {
		if e.Tool == "whoami" {
			found = true
			if !e.CredInjected {
				t.Errorf("audit entry for whoami: expected cred_injected=true, got false")
			}
		}
	}
	if !found {
		t.Errorf("no audit entry for tool 'whoami'; entries: %v", entries)
	}
}

func TestIntegration_AuditLog(t *testing.T) {
	ctx := context.Background()

	backendSrv := mcp.NewServer(&mcp.Implementation{Name: "audit-backend", Version: "0.1.0"}, nil)
	mcp.AddTool(backendSrv, &mcp.Tool{Name: "ping", Description: "Ping"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
			}, nil, nil
		},
	)
	backendHS := newBackendServer(t, backendSrv)

	var auditBuf bytes.Buffer
	_, prismHS := newGateway(t, ctx, &auditBuf, []backendDef{
		{id: "audit-svc", url: backendHS.URL, namespace: "svc"},
	})

	session := connectClient(t, ctx, prismHS.URL)

	const calls = 3
	for i := range calls {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "svc__ping",
			Arguments: map[string]any{},
		})
		if err != nil {
			t.Fatalf("call %d: CallTool: %v", i, err)
		}
		if res.IsError {
			t.Fatalf("call %d: svc__ping returned error", i)
		}
	}

	entries := parseAuditLog(t, &auditBuf)
	if len(entries) != calls {
		t.Fatalf("expected %d audit entries, got %d", calls, len(entries))
	}
	for i, e := range entries {
		if e.Namespace != "svc" {
			t.Errorf("entry %d: namespace=%q, want %q", i, e.Namespace, "svc")
		}
		if e.Tool != "ping" {
			t.Errorf("entry %d: tool=%q, want %q", i, e.Tool, "ping")
		}
		if e.Backend != "audit-svc" {
			t.Errorf("entry %d: backend=%q, want %q", i, e.Backend, "audit-svc")
		}
		if !e.Allowed {
			t.Errorf("entry %d: allowed=false, want true", i)
		}
	}
}

func TestIntegration_MultipleBackends(t *testing.T) {
	ctx := context.Background()

	githubSrv := mcp.NewServer(&mcp.Implementation{Name: "github-backend", Version: "0.1.0"}, nil)
	mcp.AddTool(githubSrv, &mcp.Tool{Name: "create_issue", Description: "Create a GitHub issue"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "issue-created"}},
			}, nil, nil
		},
	)
	githubHS := newBackendServer(t, githubSrv)

	fsSrv := mcp.NewServer(&mcp.Implementation{Name: "fs-backend", Version: "0.1.0"}, nil)
	mcp.AddTool(fsSrv, &mcp.Tool{Name: "read_file", Description: "Read a file"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "file-contents"}},
			}, nil, nil
		},
	)
	fsHS := newBackendServer(t, fsSrv)

	_, prismHS := newGateway(t, ctx, nil, []backendDef{
		{id: "github", url: githubHS.URL, namespace: "github"},
		{id: "fs", url: fsHS.URL, namespace: "fs"},
	})

	session := connectClient(t, ctx, prismHS.URL)

	listRes, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, len(listRes.Tools))
	for i, tool := range listRes.Tools {
		names[i] = tool.Name
	}
	for _, want := range []string{"github__create_issue", "fs__read_file"} {
		if !slices.Contains(names, want) {
			t.Errorf("expected tool %q in list; got %v", want, names)
		}
	}

	ghRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "github__create_issue",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool github__create_issue: %v", err)
	}
	if !strings.Contains(firstText(ghRes), "issue-created") {
		t.Errorf("github__create_issue: unexpected response %q", firstText(ghRes))
	}

	fsRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "fs__read_file",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool fs__read_file: %v", err)
	}
	if !strings.Contains(firstText(fsRes), "file-contents") {
		t.Errorf("fs__read_file: unexpected response %q", firstText(fsRes))
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func firstText(r *mcp.CallToolResult) string {
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

type auditEntry struct {
	Timestamp    string `json:"ts"`
	Subject      string `json:"subject"`
	ClientID     string `json:"client_id"`
	Namespace    string `json:"namespace"`
	Tool         string `json:"tool"`
	Allowed      bool   `json:"allowed"`
	LatencyMS    int64  `json:"latency_ms"`
	Backend      string `json:"backend"`
	Error        string `json:"error"`
	CredInjected bool   `json:"cred_injected"`
}

func parseAuditLog(t *testing.T, buf *bytes.Buffer) []auditEntry {
	t.Helper()
	var entries []auditEntry
	scanner := bufio.NewScanner(buf)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e auditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("failed to parse audit line %q: %v", line, err)
			continue
		}
		entries = append(entries, e)
	}
	return entries
}
