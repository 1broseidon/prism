package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseNamespacedTool(t *testing.T) {
	tests := []struct {
		input    string
		wantNS   string
		wantTool string
		wantOK   bool
	}{
		{"github__create_issue", "github", "create_issue", true},
		{"fs__read_file", "fs", "read_file", true},
		{"ns__deeply__nested", "ns", "deeply__nested", true},
		{"notool", "", "", false},
		{"__leading", "", "", false},
		{"trailing__", "", "", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		ns, tool, ok := parseNamespacedTool(tt.input)
		if ok != tt.wantOK || ns != tt.wantNS || tool != tt.wantTool {
			t.Errorf("parseNamespacedTool(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.input, ns, tool, ok, tt.wantNS, tt.wantTool, tt.wantOK)
		}
	}
}

func TestConnectBackendViaBridgeDelegatesConfiguredStdio(t *testing.T) {
	backendServer := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.1.0"}, nil)
	backendServer.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "test tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
	})

	var spawnPayload map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /manage/spawn", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&spawnPayload); err != nil {
			t.Fatalf("decode spawn payload: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":       "stdio-test",
			"endpoint": "/mcp/stdio-test",
			"status":   "running",
			"tools":    []string{"ping"},
		})
	})
	mux.Handle("/mcp/stdio-test", mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backendServer },
		nil,
	))
	bridge := httptest.NewServer(mux)
	defer bridge.Close()

	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetBridgeURL(bridge.URL)
	err := gw.ConnectBackendViaBridge(context.Background(), &config.ServerConfig{
		ID:        "stdio-test",
		Namespace: "stdio-test",
		Command:   []string{"npx", "-y", "@example/server"},
	})
	if err != nil {
		t.Fatalf("connect via bridge: %v", err)
	}
	if spawnPayload["command"] != "npx" {
		t.Fatalf("spawn command = %v", spawnPayload["command"])
	}
	status := gw.Status()
	if len(status) != 1 {
		t.Fatalf("status count = %d", len(status))
	}
	if !status[0].BridgeManaged || status[0].URL != bridge.URL+"/mcp/stdio-test" {
		t.Fatalf("backend was not bridge-managed: %+v", status[0])
	}
}

func TestConnectBackendViaBridgeRecreatesStaleBridgeConflict(t *testing.T) {
	backendServer := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.1.0"}, nil)
	backendServer.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "test tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backendServer },
		nil,
	)

	var mu sync.Mutex
	spawnCalls := 0
	deleteCalls := 0
	active := false

	mux := http.NewServeMux()
	mux.HandleFunc("POST /manage/spawn", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		spawnCalls++
		if spawnCalls == 1 {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "backend already exists"})
			return
		}
		active = true
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":       "stdio-test",
			"endpoint": "/mcp/stdio-test",
			"status":   "running",
			"tools":    []string{"ping"},
		})
	})
	mux.HandleFunc("DELETE /manage/stdio-test", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		deleteCalls++
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
	})
	mux.HandleFunc("/mcp/stdio-test", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		isActive := active
		mu.Unlock()
		if !isActive {
			http.NotFound(w, r)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
	bridge := httptest.NewServer(mux)
	defer bridge.Close()

	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetBridgeURL(bridge.URL)
	err := gw.ConnectBackendViaBridge(context.Background(), &config.ServerConfig{
		ID:        "stdio-test",
		Namespace: "stdio-test",
		Command:   []string{"npx", "-y", "@example/server"},
	})
	if err != nil {
		t.Fatalf("connect via bridge: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if spawnCalls != 2 {
		t.Fatalf("spawn calls = %d, want 2", spawnCalls)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", deleteCalls)
	}
}

func TestConnectBackendViaBridgeTriesNextBridgeURL(t *testing.T) {
	backendServer := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.1.0"}, nil)
	backendServer.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "test tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
	})

	failingBridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer failingBridge.Close()

	goodMux := http.NewServeMux()
	goodMux.HandleFunc("POST /manage/spawn", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":       "stdio-test",
			"endpoint": "/mcp/stdio-test",
			"status":   "running",
			"tools":    []string{"ping"},
		})
	})
	goodMux.Handle("/mcp/stdio-test", mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backendServer },
		nil,
	))
	goodBridge := httptest.NewServer(goodMux)
	defer goodBridge.Close()

	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetBridgeURLs([]string{failingBridge.URL, goodBridge.URL})
	if err := gw.ConnectBackendViaBridge(context.Background(), &config.ServerConfig{
		ID:        "stdio-test",
		Namespace: "stdio-test",
		Command:   []string{"npx", "-y", "@example/server"},
	}); err != nil {
		t.Fatalf("connect via bridge URLs: %v", err)
	}
	status := gw.Status()
	if len(status) != 1 || status[0].URL != goodBridge.URL+"/mcp/stdio-test" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestConnectBackendViaBridgeReturnsClearUnavailableError(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.DisableProcessStdio("mount the Docker socket")

	err := gw.ConnectBackendViaBridge(context.Background(), &config.ServerConfig{
		ID:        "stdio-test",
		Namespace: "stdio-test",
		Command:   []string{"npx", "@example/server"},
	})
	if err == nil || !strings.Contains(err.Error(), "mount the Docker socket") {
		t.Fatalf("error = %v", err)
	}
}

func TestReconnectBackendUsesPersistedConfig(t *testing.T) {
	backendServer := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.1.0"}, nil)
	backendServer.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "test tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
	})
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backendServer },
		nil,
	))
	defer upstream.Close()

	kv := store.NewMemoryStore()
	data, err := json.Marshal(&persistedBackend{URL: upstream.URL})
	if err != nil {
		t.Fatalf("marshal persisted backend: %v", err)
	}
	if err := kv.Set(backendKVPrefix+"Linear", data); err != nil {
		t.Fatalf("persist backend: %v", err)
	}

	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(kv)
	if err := gw.ReconnectBackend(context.Background(), "Linear"); err != nil {
		t.Fatalf("reconnect backend: %v", err)
	}

	status := gw.Status()
	if len(status) != 1 {
		t.Fatalf("status count = %d", len(status))
	}
	if status[0].ID != "Linear" || status[0].Disconnected {
		t.Fatalf("unexpected status: %+v", status[0])
	}
	if len(status[0].Tools) != 1 || status[0].Tools[0].Name != "Linear__ping" {
		t.Fatalf("tools = %+v", status[0].Tools)
	}
}

func TestRouteToolCallRequiresWorkspaceScopeWhenPolicyUsesWorkspaces(t *testing.T) {
	called := false
	backendServer := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.1.0"}, nil)
	backendServer.AddTool(&mcp.Tool{
		Name:        "list_tasks",
		Description: "test tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backendServer },
		nil,
	))
	defer upstream.Close()

	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	if err := gw.ConnectBackend(context.Background(), &config.ServerConfig{
		ID:        "brainfile",
		Namespace: "brainfile",
		URL:       upstream.URL,
		Workspace: &config.WorkspaceConfig{
			ID: "repo",
		},
	}); err != nil {
		t.Fatalf("connect backend: %v", err)
	}

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{}`)}}
	denied, err := gw.routeToolCall(contextWithPolicy("brainfile:list_tasks workspace:other"), "brainfile", "list_tasks", req)
	if err != nil {
		t.Fatalf("denied route returned error: %v", err)
	}
	if denied == nil || !denied.IsError {
		t.Fatalf("expected workspace denial, got %+v", denied)
	}
	if called {
		t.Fatal("backend should not be called when workspace scope is denied")
	}

	legacyAllowed, err := gw.routeToolCall(contextWithPolicy("brainfile:list_tasks"), "brainfile", "list_tasks", req)
	if err != nil {
		t.Fatalf("legacy route returned error: %v", err)
	}
	if legacyAllowed == nil || legacyAllowed.IsError {
		t.Fatalf("expected legacy tool-only policy to remain allowed, got %+v", legacyAllowed)
	}
	if !called {
		t.Fatal("backend should be called for legacy tool-only policy")
	}

	called = false
	allowed, err := gw.routeToolCall(contextWithPolicy("brainfile:list_tasks workspace:repo"), "brainfile", "list_tasks", req)
	if err != nil {
		t.Fatalf("allowed route returned error: %v", err)
	}
	if allowed == nil || allowed.IsError {
		t.Fatalf("expected allowed result, got %+v", allowed)
	}
	if !called {
		t.Fatal("backend should be called when workspace scope is allowed")
	}
}

func TestWorkspaceSelectorHelpers(t *testing.T) {
	schema := addWorkspaceSelectorToSchema(map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
	})
	props := schema.(map[string]any)["properties"].(map[string]any)
	if props[prismWorkspaceArg] == nil || props["query"] == nil {
		t.Fatalf("schema properties = %+v", props)
	}

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"query":"x","_prism_workspace":"repo"}`)}}
	workspaceID, forwarded, err := splitWorkspaceSelector(req)
	if err != nil {
		t.Fatalf("split workspace selector: %v", err)
	}
	if workspaceID != "repo" {
		t.Fatalf("workspace id = %q", workspaceID)
	}
	if strings.Contains(string(forwarded.Params.Arguments), prismWorkspaceArg) {
		t.Fatalf("workspace selector was not stripped: %s", forwarded.Params.Arguments)
	}
}

func TestWorkspaceRegistryCreatesListsDeletesRemoteWorkspaces(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(store.NewMemoryStore())

	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:               "team-a",
		Type:             config.WorkspaceTypeVirtual,
		Owner:            "owner@example.com",
		AllowedAgents:    []string{"agent-b", "agent-a", "agent-a"},
		AllowedTemplates: []string{"brainfile"},
		QuotaBytes:       42,
		RetentionSeconds: 3600,
	}); err != nil {
		t.Fatalf("create virtual workspace: %v", err)
	}
	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:   "local",
		Type: config.WorkspaceTypeProxied,
	}); err == nil {
		t.Fatal("expected proxied registry create to be rejected")
	}

	list := gw.ListWorkspaces()
	if len(list) != 1 || list[0].ID != "team-a" || list[0].Type != config.WorkspaceTypeVirtual || !list[0].Connected {
		t.Fatalf("workspace list = %+v", list)
	}
	if list[0].Owner != "owner@example.com" ||
		strings.Join(list[0].AllowedAgents, ",") != "agent-a,agent-b" ||
		strings.Join(list[0].AllowedTemplates, ",") != "brainfile" ||
		list[0].QuotaBytes != 42 ||
		list[0].RetentionSeconds != 3600 {
		t.Fatalf("workspace policy metadata = %+v", list[0])
	}
	// Registered virtual without live usage reporting is healthy (UsedBytes 0,
	// quota > 0 but well under).
	if list[0].HealthStatus != admin.WorkspaceHealthOK {
		t.Fatalf("expected health_status=ok for connected virtual under quota, got %q", list[0].HealthStatus)
	}
	if !gw.DisconnectWorkspace("team-a") {
		t.Fatal("expected registry workspace delete to succeed")
	}
	if gw.DisconnectWorkspace("team-a") {
		t.Fatal("second delete should report not found")
	}
}

func TestWorkspaceBridgeRecordsUsedBytesFromPoll(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	kv := store.NewMemoryStore()
	gw.SetStore(kv)
	if err := gw.InitWorkspaceBridge(kv, ""); err != nil {
		t.Fatalf("init workspace bridge: %v", err)
	}

	handler := gw.WorkspaceBridgeHandler()

	if _, err := gw.SetWorkspaceBridgeConfig(admin.WorkspaceBridgeUpdate{
		Enabled: true,
		Token:   "test-token-min-length-1234567",
	}); err != nil {
		t.Fatalf("enable workspace bridge: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	auth := func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer test-token-min-length-1234567")
	}

	// Register a proxied workspace with a single dummy backend.
	regBody := `{"workspace_id":"repo-a","hostname":"laptop","root":"/tmp/r","version":"0.1","backends":[{"id":"Brainfile","namespace":"Brainfile","tools":[{"name":"hello","description":""}]}]}`
	regReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/workspace/register", strings.NewReader(regBody))
	regReq.Header.Set("Content-Type", "application/json")
	auth(regReq)
	regResp, err := http.DefaultClient.Do(regReq)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	regResp.Body.Close()
	if regResp.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d", regResp.StatusCode)
	}

	// Poll with used_bytes=2048. Poll long-polls — send via a goroutine that
	// closes the request once we have what we need (the gateway updates state
	// before blocking on the request queue).
	pollReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/workspace/poll?workspace_id=repo-a&used_bytes=2048", nil)
	auth(pollReq)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pollReq = pollReq.WithContext(ctx)
	go func() {
		// Cancel after a brief moment — the gateway has already cached
		// used_bytes by the time it starts blocking on the queue.
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	resp, _ := http.DefaultClient.Do(pollReq)
	if resp != nil {
		resp.Body.Close()
	}

	list := gw.ListWorkspaces()
	if len(list) != 1 || list[0].ID != "repo-a" {
		t.Fatalf("expected one workspace named repo-a, got %+v", list)
	}
	if list[0].UsedBytes != 2048 {
		t.Errorf("UsedBytes = %d, want 2048", list[0].UsedBytes)
	}
	if list[0].HealthStatus != admin.WorkspaceHealthOK {
		t.Errorf("HealthStatus = %q, want %q", list[0].HealthStatus, admin.WorkspaceHealthOK)
	}
}

// stubBackendPolicyResolver lets tests inject a static set of layers.
type stubBackendPolicyResolver struct {
	layers []auth.BackendPolicyLayer
}

func (s *stubBackendPolicyResolver) ResolveBackendPolicy(_ *auth.Claims) []auth.BackendPolicyLayer {
	return s.layers
}

func TestResolveBackendWorkspaceStackOrder(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(store.NewMemoryStore())

	// Register a virtual workspace so id:<X> selector has a target.
	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:   "team-virtual",
		Type: config.WorkspaceTypeVirtual,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	backend := &Backend{Config: &config.ServerConfig{
		ID:        "brainfile",
		Namespace: "Brainfile",
		Workspace: &config.WorkspaceConfig{ID: "static-floor", Type: config.WorkspaceTypeProxied},
	}}
	claims := &auth.Claims{PrismID: "prism-a", ClientID: "client-a"}

	tests := []struct {
		name       string
		layers     []auth.BackendPolicyLayer
		wantWS     string
		wantSel    string
		wantSource string
		wantDeny   string
	}{
		{
			name:       "no policy uses backend static floor",
			layers:     nil,
			wantWS:     "static-floor",
			wantSel:    "static",
			wantSource: "backend.static",
		},
		{
			name: "agent layer overrides group and defaults",
			layers: []auth.BackendPolicyLayer{
				{Source: "agent:prism-a", Policies: map[string]auth.BackendPolicy{
					"brainfile": {WorkspaceSelector: "id:team-virtual"},
				}},
				{Source: "group:engineering", Policies: map[string]auth.BackendPolicy{
					"brainfile": {WorkspaceSelector: "static"},
				}},
				{Source: "defaults", Policies: map[string]auth.BackendPolicy{
					"brainfile": {WorkspaceSelector: "static"},
				}},
			},
			wantWS:     "team-virtual",
			wantSel:    "id:team-virtual",
			wantSource: "agent:prism-a",
		},
		{
			name: "group layer wins over defaults when agent has no rule",
			layers: []auth.BackendPolicyLayer{
				{Source: "group:engineering", Policies: map[string]auth.BackendPolicy{
					"brainfile": {WorkspaceSelector: "id:team-virtual"},
				}},
				{Source: "defaults", Policies: map[string]auth.BackendPolicy{
					"brainfile": {WorkspaceSelector: "static"},
				}},
			},
			wantWS:     "team-virtual",
			wantSel:    "id:team-virtual",
			wantSource: "group:engineering",
		},
		{
			name: "id selector missing workspace is denied",
			layers: []auth.BackendPolicyLayer{
				{Source: "defaults", Policies: map[string]auth.BackendPolicy{
					"brainfile": {WorkspaceSelector: "id:does-not-exist"},
				}},
			},
			wantDeny: `policy pins workspace "does-not-exist" but it is not registered`,
		},
		{
			name: "static selector at a layer falls back to backend floor",
			layers: []auth.BackendPolicyLayer{
				{Source: "defaults", Policies: map[string]auth.BackendPolicy{
					"brainfile": {WorkspaceSelector: "static"},
				}},
			},
			wantWS:     "static-floor",
			wantSel:    "static",
			wantSource: "defaults",
		},
		{
			name: "rules for other backends are ignored",
			layers: []auth.BackendPolicyLayer{
				{Source: "agent:prism-a", Policies: map[string]auth.BackendPolicy{
					"linear": {WorkspaceSelector: "agent"},
				}},
			},
			wantWS:     "static-floor",
			wantSel:    "static",
			wantSource: "backend.static",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw.SetBackendPolicyResolver(&stubBackendPolicyResolver{layers: tc.layers})
			cfg, res, deny := gw.ResolveBackendWorkspace(claims, backend)
			if tc.wantDeny != "" {
				if deny != tc.wantDeny {
					t.Fatalf("deny = %q, want %q", deny, tc.wantDeny)
				}
				return
			}
			if deny != "" {
				t.Fatalf("unexpected deny: %s", deny)
			}
			if cfg == nil || cfg.ID != tc.wantWS {
				t.Fatalf("workspace = %+v, want id=%q", cfg, tc.wantWS)
			}
			if res.WorkspaceID != tc.wantWS {
				t.Errorf("trace.WorkspaceID = %q, want %q", res.WorkspaceID, tc.wantWS)
			}
			if res.Selector != tc.wantSel {
				t.Errorf("trace.Selector = %q, want %q", res.Selector, tc.wantSel)
			}
			if res.Source != tc.wantSource {
				t.Errorf("trace.Source = %q, want %q", res.Source, tc.wantSource)
			}
		})
	}
}

func TestResolveBackendWorkspaceAgentSelector(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(store.NewMemoryStore())

	backend := &Backend{Config: &config.ServerConfig{
		ID:        "brainfile",
		Namespace: "Brainfile",
	}}
	gw.SetBackendPolicyResolver(&stubBackendPolicyResolver{layers: []auth.BackendPolicyLayer{
		{Source: "defaults", Policies: map[string]auth.BackendPolicy{
			"brainfile": {WorkspaceSelector: "agent"},
		}},
	}})

	// Zero registered workspaces for the agent → clear error.
	_, _, deny := gw.ResolveBackendWorkspace(
		&auth.Claims{PrismID: "agent-zero"},
		backend,
	)
	if deny == "" || !strings.Contains(deny, "no workspace is registered") {
		t.Fatalf("expected zero-match deny, got %q", deny)
	}

	// Register one workspace owned by the agent.
	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:    "agent-a-repo",
		Type:  config.WorkspaceTypeVirtual,
		Owner: "prism-a",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	cfg, res, deny := gw.ResolveBackendWorkspace(
		&auth.Claims{PrismID: "prism-a"},
		backend,
	)
	if deny != "" {
		t.Fatalf("unexpected deny: %s", deny)
	}
	if cfg == nil || cfg.ID != "agent-a-repo" {
		t.Fatalf("workspace = %+v, want agent-a-repo", cfg)
	}
	if res.Source != "defaults" || res.Selector != "agent" {
		t.Fatalf("trace = %+v, want defaults/agent", res)
	}

	// Register a second workspace owned by the same agent → disambiguation error.
	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:    "agent-a-repo-two",
		Type:  config.WorkspaceTypeVirtual,
		Owner: "prism-a",
	}); err != nil {
		t.Fatalf("create second workspace: %v", err)
	}
	_, _, deny = gw.ResolveBackendWorkspace(
		&auth.Claims{PrismID: "prism-a"},
		backend,
	)
	if deny == "" || !strings.Contains(deny, "2 workspaces") {
		t.Fatalf("expected multi-match deny, got %q", deny)
	}
}

func TestValidateBackendWorkspaceBinding(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(store.NewMemoryStore())

	// Register a virtual workspace.
	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:   "shared",
		Type: config.WorkspaceTypeVirtual,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	cases := []struct {
		name    string
		cfg     *config.WorkspaceConfig
		wantErr bool
	}{
		{
			name:    "nil config is allowed",
			cfg:     nil,
			wantErr: false,
		},
		{
			name:    "no id is allowed (lazy)",
			cfg:     &config.WorkspaceConfig{Type: config.WorkspaceTypeVirtual},
			wantErr: false,
		},
		{
			name:    "unregistered id is allowed (lazy)",
			cfg:     &config.WorkspaceConfig{ID: "new-one", Type: config.WorkspaceTypeVirtual},
			wantErr: false,
		},
		{
			name:    "type matches registry",
			cfg:     &config.WorkspaceConfig{ID: "shared", Type: config.WorkspaceTypeVirtual},
			wantErr: false,
		},
		{
			name:    "type mismatch is rejected",
			cfg:     &config.WorkspaceConfig{ID: "shared", Type: config.WorkspaceTypeEphemeral},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := gw.validateBackendWorkspaceBinding(c.cfg)
			if (err != nil) != c.wantErr {
				t.Errorf("validateBackendWorkspaceBinding(%+v) error = %v, wantErr = %v",
					c.cfg, err, c.wantErr)
			}
		})
	}
}

func TestListWorkspacesBackfillsUsedBytesFromBridge(t *testing.T) {
	// Stand up a fake bridge that reports usage for two workspaces.
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manage/workspaces" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspaces":[{"id":"shared","used_bytes":12345},{"id":"scratch","used_bytes":99}]}`))
	}))
	defer bridge.Close()

	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(store.NewMemoryStore())
	gw.SetBridgeURL(bridge.URL)

	// Register one virtual + one ephemeral workspace in the registry.
	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:   "shared",
		Type: config.WorkspaceTypeVirtual,
	}); err != nil {
		t.Fatalf("create virtual: %v", err)
	}
	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:   "scratch",
		Type: config.WorkspaceTypeEphemeral,
	}); err != nil {
		t.Fatalf("create ephemeral: %v", err)
	}

	statuses := gw.ListWorkspaces()
	got := map[string]int64{}
	for _, s := range statuses {
		got[s.ID] = s.UsedBytes
	}
	if got["shared"] != 12345 {
		t.Errorf("shared used_bytes = %d, want 12345", got["shared"])
	}
	if got["scratch"] != 99 {
		t.Errorf("scratch used_bytes = %d, want 99", got["scratch"])
	}
}

func TestPersistProxiedRegistryEntryStampsOwner(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	kv := store.NewMemoryStore()
	gw.SetStore(kv)

	if err := gw.persistProxiedRegistryEntry("repo-a", "prism-uuid-x"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	entry, ok := gw.registeredWorkspace("repo-a")
	if !ok || entry.Owner != "prism-uuid-x" {
		t.Fatalf("registry entry = %+v", entry)
	}
	if entry.Type != config.WorkspaceTypeProxied {
		t.Errorf("type = %q, want proxied", entry.Type)
	}

	// agent selector should now resolve to this workspace for that prism_id.
	backend := &Backend{Config: &config.ServerConfig{ID: "brainfile", Namespace: "Brainfile"}}
	gw.SetBackendPolicyResolver(&stubBackendPolicyResolver{layers: []auth.BackendPolicyLayer{
		{Source: "defaults", Policies: map[string]auth.BackendPolicy{
			"brainfile": {WorkspaceSelector: "agent"},
		}},
	}})
	cfg, _, deny := gw.ResolveBackendWorkspace(&auth.Claims{PrismID: "prism-uuid-x"}, backend)
	if deny != "" || cfg == nil || cfg.ID != "repo-a" {
		t.Fatalf("resolve cfg=%+v deny=%q", cfg, deny)
	}
}

func TestPersistProxiedRegistryEntryPreservesExistingMetadata(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(store.NewMemoryStore())

	// Pre-seed an entry with non-default fields.
	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:               "shared",
		Type:             config.WorkspaceTypeVirtual,
		QuotaBytes:       4096,
		RetentionSeconds: 60,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	// Re-stamp ownership. Quota/retention should survive.
	if err := gw.persistProxiedRegistryEntry("shared", "prism-uuid-y"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	entry, ok := gw.registeredWorkspace("shared")
	if !ok {
		t.Fatal("entry missing after re-stamp")
	}
	if entry.Owner != "prism-uuid-y" {
		t.Errorf("owner = %q, want prism-uuid-y", entry.Owner)
	}
	if entry.QuotaBytes != 4096 || entry.RetentionSeconds != 60 {
		t.Errorf("metadata not preserved: %+v", entry)
	}
}

func TestResolveBackendRateLimitStackOrder(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()

	backend := &Backend{Config: &config.ServerConfig{ID: "brainfile"}}
	claims := &auth.Claims{PrismID: "prism-a"}

	tests := []struct {
		name       string
		layers     []auth.BackendPolicyLayer
		wantRPS    float64
		wantSource string
	}{
		{
			name:       "no policy = no limit",
			layers:     nil,
			wantRPS:    0,
			wantSource: "",
		},
		{
			name: "agent layer wins over defaults",
			layers: []auth.BackendPolicyLayer{
				{Source: "agent:prism-a", Policies: map[string]auth.BackendPolicy{
					"brainfile": {RateLimit: &auth.BackendRateLimit{RPS: 5, Burst: 5}},
				}},
				{Source: "defaults", Policies: map[string]auth.BackendPolicy{
					"brainfile": {RateLimit: &auth.BackendRateLimit{RPS: 100, Burst: 100}},
				}},
			},
			wantRPS:    5,
			wantSource: "agent:prism-a",
		},
		{
			name: "group fills in when agent has no rule",
			layers: []auth.BackendPolicyLayer{
				{Source: "group:engineering", Policies: map[string]auth.BackendPolicy{
					"brainfile": {RateLimit: &auth.BackendRateLimit{RPS: 20, Burst: 20}},
				}},
				{Source: "defaults", Policies: map[string]auth.BackendPolicy{
					"brainfile": {RateLimit: &auth.BackendRateLimit{RPS: 100, Burst: 100}},
				}},
			},
			wantRPS:    20,
			wantSource: "group:engineering",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw.SetBackendPolicyResolver(&stubBackendPolicyResolver{layers: tc.layers})
			limit, res := gw.ResolveBackendRateLimit(claims, backend)
			if tc.wantRPS == 0 {
				if limit != nil {
					t.Fatalf("expected nil limit, got %+v", limit)
				}
				return
			}
			if limit == nil || limit.RPS != tc.wantRPS {
				t.Fatalf("limit = %+v, want RPS=%v", limit, tc.wantRPS)
			}
			if res.Source != tc.wantSource {
				t.Errorf("source = %q, want %q", res.Source, tc.wantSource)
			}
		})
	}
}

func TestAllowBackendCallExhaustsBucket(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()

	limit := &auth.BackendRateLimit{RPS: 0.001, Burst: 2}
	claims := &auth.Claims{PrismID: "prism-rl"}

	// Burst lets through exactly 2 calls; the third gets denied.
	if !gw.allowBackendCall(claims, "brainfile", limit) {
		t.Fatal("first call should be allowed")
	}
	if !gw.allowBackendCall(claims, "brainfile", limit) {
		t.Fatal("second call should be allowed (within burst)")
	}
	if gw.allowBackendCall(claims, "brainfile", limit) {
		t.Fatal("third call should be denied")
	}

	// A different backend has its own bucket — call against linear succeeds.
	if !gw.allowBackendCall(claims, "linear", limit) {
		t.Fatal("call against a different backend should be allowed independently")
	}

	// Nil limit always allows.
	if !gw.allowBackendCall(claims, "brainfile", nil) {
		t.Fatal("nil limit should always allow")
	}
}

func TestWorkspaceHealth(t *testing.T) {
	cases := []struct {
		name      string
		connected bool
		used      int64
		quota     int64
		want      string
	}{
		{"disconnected is stale", false, 0, 0, admin.WorkspaceHealthStale},
		{"disconnected wins over quota", false, 9999, 100, admin.WorkspaceHealthStale},
		{"connected no quota is ok", true, 1024, 0, admin.WorkspaceHealthOK},
		{"connected under 90%", true, 50, 100, admin.WorkspaceHealthOK},
		{"connected at 90% is warn", true, 90, 100, admin.WorkspaceHealthQuotaWarn},
		{"connected over quota is exceeded", true, 200, 100, admin.WorkspaceHealthQuotaExceeded},
		{"connected at exactly quota is exceeded", true, 100, 100, admin.WorkspaceHealthQuotaExceeded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := workspaceHealth(c.connected, c.used, c.quota)
			if got != c.want {
				t.Errorf("workspaceHealth(connected=%v, used=%d, quota=%d) = %q, want %q",
					c.connected, c.used, c.quota, got, c.want)
			}
		})
	}
}

func TestRouteToolCallEnforcesWorkspaceRegistryTemplatePolicy(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(store.NewMemoryStore())
	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:               "team-a",
		Type:             config.WorkspaceTypeVirtual,
		AllowedAgents:    []string{"*"},
		AllowedTemplates: []string{"linear"},
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	gw.backends["brainfile"] = &Backend{
		Config: &config.ServerConfig{
			ID:              "brainfile",
			Namespace:       "brainfile",
			BridgeManaged:   true,
			OriginalCommand: []string{"npx", "@brainfile/cli", "mcp"},
			Workspace:       &config.WorkspaceConfig{ID: "default", Type: config.WorkspaceTypeVirtual},
		},
	}

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"_prism_workspace":"team-a"}`)}}
	result, err := gw.routeToolCall(
		contextWithPolicyAndClaims("brainfile:list_tasks workspace:team-a", &auth.Claims{ClientID: "agent-a"}),
		"brainfile",
		"list_tasks",
		req,
	)
	if err != nil {
		t.Fatalf("route tool: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected registry denial, got %+v", result)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "backend") || !strings.Contains(text, "team-a") {
		t.Fatalf("denial text = %q", text)
	}
}

func TestRouteToolCallEnforcesWorkspaceRegistryAgentPolicy(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(store.NewMemoryStore())
	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:               "team-a",
		Type:             config.WorkspaceTypeVirtual,
		AllowedAgents:    []string{"agent-a"},
		AllowedTemplates: []string{"brainfile"},
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	gw.backends["brainfile"] = &Backend{
		Config: &config.ServerConfig{
			ID:              "brainfile",
			Namespace:       "brainfile",
			BridgeManaged:   true,
			OriginalCommand: []string{"npx", "@brainfile/cli", "mcp"},
			Workspace:       &config.WorkspaceConfig{ID: "default", Type: config.WorkspaceTypeVirtual},
		},
	}

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"_prism_workspace":"team-a"}`)}}
	result, err := gw.routeToolCall(
		contextWithPolicyAndClaims("brainfile:list_tasks workspace:team-a", &auth.Claims{ClientID: "agent-b", PrismID: "prism-b"}),
		"brainfile",
		"list_tasks",
		req,
	)
	if err != nil {
		t.Fatalf("route tool: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected registry denial, got %+v", result)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "agent") || !strings.Contains(text, "team-a") {
		t.Fatalf("denial text = %q", text)
	}
}

func TestRouteToolCallCanAttachAlternateWorkspaceInstance(t *testing.T) { //nolint:gocyclo // exercises spawn, routing, schema stripping, and registry overlay together
	var mu sync.Mutex
	var spawnPayloads []map[string]any
	var callArgs []string

	backendServer := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.1.0"}, nil)
	backendServer.AddTool(&mcp.Tool{
		Name:        "list_tasks",
		Description: "test tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		mu.Lock()
		callArgs = append(callArgs, string(req.Params.Arguments))
		mu.Unlock()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /manage/spawn", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode spawn payload: %v", err)
		}
		mu.Lock()
		spawnPayloads = append(spawnPayloads, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":       payload["id"],
			"endpoint": "/mcp/" + payload["id"].(string),
			"status":   "running",
			"tools":    []string{"list_tasks"},
		})
	})
	mux.Handle("/mcp/", mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backendServer },
		nil,
	))
	mux.HandleFunc("DELETE /manage/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
	})
	bridge := httptest.NewServer(mux)
	defer bridge.Close()

	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(store.NewMemoryStore())
	gw.SetBridgeURL(bridge.URL)
	if _, err := gw.CreateWorkspace(context.Background(), admin.WorkspaceCreateRequest{
		ID:               "other",
		Type:             config.WorkspaceTypeEphemeral,
		QuotaBytes:       99,
		RetentionSeconds: 60,
	}); err != nil {
		t.Fatalf("create remote workspace: %v", err)
	}
	if err := gw.ConnectBackendViaBridge(context.Background(), &config.ServerConfig{
		ID:              "brainfile",
		Namespace:       "brainfile",
		Command:         []string{"npx", "@brainfile/cli", "mcp"},
		BridgeManaged:   true,
		BridgeRuntime:   "node",
		Sandbox:         config.DefaultSandboxConfig(),
		Workspace:       &config.WorkspaceConfig{ID: "repo", Type: config.WorkspaceTypeVirtual},
		OriginalCommand: []string{"npx", "@brainfile/cli", "mcp"},
	}); err != nil {
		t.Fatalf("connect backend: %v", err)
	}

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"_prism_workspace":"other","query":"x"}`)}}
	result, err := gw.routeToolCall(contextWithPolicy("brainfile:list_tasks workspace:other"), "brainfile", "list_tasks", req)
	if err != nil {
		t.Fatalf("route tool: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	result, err = gw.routeToolCall(contextWithPolicy("brainfile:list_tasks workspace:other"), "brainfile", "list_tasks", req)
	if err != nil {
		t.Fatalf("second route tool: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("second result = %+v", result)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(spawnPayloads) != 2 {
		t.Fatalf("spawn payload count = %d, want 2: %+v", len(spawnPayloads), spawnPayloads)
	}
	workspace, _ := spawnPayloads[1]["workspace"].(map[string]any)
	if workspace["id"] != "other" {
		t.Fatalf("dynamic workspace payload = %+v", workspace)
	}
	if workspace["type"] != config.WorkspaceTypeEphemeral || workspace["quota_bytes"] != float64(99) || workspace["retention_seconds"] != float64(60) {
		t.Fatalf("registered workspace metadata was not applied: %+v", workspace)
	}
	if len(callArgs) != 2 || strings.Contains(callArgs[0], prismWorkspaceArg) || strings.Contains(callArgs[1], prismWorkspaceArg) {
		t.Fatalf("forwarded call args = %+v", callArgs)
	}
}

func TestReconnectPersistedBackendsForWorkspace(t *testing.T) {
	backendServer := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.1.0"}, nil)
	backendServer.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "test tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
	})
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backendServer },
		nil,
	))
	defer upstream.Close()

	kv := store.NewMemoryStore()
	brainfile, err := json.Marshal(&persistedBackend{
		URL: upstream.URL,
		Workspace: &config.WorkspaceConfig{
			ID: "prism-repo",
		},
	})
	if err != nil {
		t.Fatalf("marshal brainfile backend: %v", err)
	}
	if setErr := kv.Set(backendKVPrefix+"Brainfile", brainfile); setErr != nil {
		t.Fatalf("persist brainfile backend: %v", setErr)
	}
	other, err := json.Marshal(&persistedBackend{
		URL: upstream.URL,
		Workspace: &config.WorkspaceConfig{
			ID: "other-repo",
		},
	})
	if err != nil {
		t.Fatalf("marshal other backend: %v", err)
	}
	if err := kv.Set(backendKVPrefix+"Other", other); err != nil {
		t.Fatalf("persist other backend: %v", err)
	}

	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(kv)
	gw.reconnectPersistedBackendsForWorkspace(context.Background(), "prism-repo")

	status := gw.Status()
	if len(status) != 2 {
		t.Fatalf("status count = %d, want connected Brainfile plus disconnected Other: %+v", len(status), status)
	}
	byID := map[string]BackendStatus{}
	for _, s := range status {
		byID[s.ID] = s
	}
	if byID["Brainfile"].Disconnected || len(byID["Brainfile"].Tools) != 1 {
		t.Fatalf("Brainfile was not restored: %+v", byID["Brainfile"])
	}
	if !byID["Other"].Disconnected {
		t.Fatalf("Other workspace should stay disconnected: %+v", byID["Other"])
	}
}

func TestUpdateBackendDisableAndEnablePreservesPersistedConfig(t *testing.T) {
	backendServer := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.1.0"}, nil)
	backendServer.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "test tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
	})
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backendServer },
		nil,
	))
	defer upstream.Close()

	kv := store.NewMemoryStore()
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(kv)

	if err := gw.AddBackend(context.Background(), "Brainfile", admin.BackendConfig{URL: upstream.URL}); err != nil {
		t.Fatalf("add backend: %v", err)
	}
	disabled := false
	if err := gw.UpdateBackend(context.Background(), "Brainfile", admin.BackendUpdate{Enabled: &disabled}); err != nil {
		t.Fatalf("disable backend: %v", err)
	}
	status := gw.Status()
	if len(status) != 1 {
		t.Fatalf("status count after disable = %d", len(status))
	}
	if status[0].Enabled || status[0].Disconnected || len(status[0].Tools) != 0 {
		t.Fatalf("disabled status = %+v", status[0])
	}
	if _, err := kv.Get(backendKVPrefix + "Brainfile"); err != nil {
		t.Fatalf("persisted backend was deleted: %v", err)
	}

	enabled := true
	if err := gw.UpdateBackend(context.Background(), "Brainfile", admin.BackendUpdate{Enabled: &enabled}); err != nil {
		t.Fatalf("enable backend: %v", err)
	}
	status = gw.Status()
	if len(status) != 1 || !status[0].Enabled || status[0].Disconnected {
		t.Fatalf("enabled status = %+v", status)
	}
	if len(status[0].Tools) != 1 || status[0].Tools[0].Name != "Brainfile__ping" {
		t.Fatalf("tools after enable = %+v", status[0].Tools)
	}
}

func TestUpdateBackendDoesNotPersistFailedReconnectSettings(t *testing.T) {
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/manage/spawn" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "spawn failed"})
			return
		}
		http.NotFound(w, r)
	}))
	defer bridge.Close()

	kv := store.NewMemoryStore()
	root := true
	readonly := false
	previous := &persistedBackend{
		Command: "npx",
		Args:    []string{"@brainfile/cli", "mcp"},
		Enabled: boolPtr(true),
		Sandbox: &config.SandboxConfig{
			Profile:        config.SandboxProfileCompat,
			NetworkProfile: config.SandboxNetworkStandard,
			RunAsRoot:      &root,
			ReadOnlyRootFS: &readonly,
		},
	}
	data, err := json.Marshal(previous)
	if err != nil {
		t.Fatalf("marshal previous backend: %v", err)
	}
	if setErr := kv.Set(backendKVPrefix+"Brainfile", data); setErr != nil {
		t.Fatalf("persist previous backend: %v", setErr)
	}

	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.SetStore(kv)
	gw.SetBridgeURL(bridge.URL)

	nonRoot := false
	readonlyRoot := true
	err = gw.UpdateBackend(context.Background(), "Brainfile", admin.BackendUpdate{
		Sandbox: &config.SandboxConfig{
			Profile:        config.SandboxProfileDefault,
			NetworkProfile: config.SandboxNetworkStandard,
			RunAsRoot:      &nonRoot,
			UID:            config.DefaultSandboxUID,
			GID:            config.DefaultSandboxGID,
			ReadOnlyRootFS: &readonlyRoot,
			Memory:         config.DefaultSandboxMemory,
			CPUs:           config.DefaultSandboxCPUs,
			PidsLimit:      config.DefaultSandboxPidsLimit,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "spawn failed") {
		t.Fatalf("UpdateBackend error = %v", err)
	}

	gotData, err := kv.Get(backendKVPrefix + "Brainfile")
	if err != nil {
		t.Fatalf("read persisted backend: %v", err)
	}
	var got persistedBackend
	if err := json.Unmarshal(gotData, &got); err != nil {
		t.Fatalf("decode persisted backend: %v", err)
	}
	if got.Sandbox == nil || got.Sandbox.Profile != config.SandboxProfileCompat || !got.Sandbox.RunsAsRoot() || got.Sandbox.ReadOnlyRoot() {
		t.Fatalf("persisted backend was overwritten: %+v", got.Sandbox)
	}
}
