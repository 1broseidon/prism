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

	"github.com/1broseidon/prism/internal/admin"
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

func TestRouteToolCallCanAttachAlternateWorkspaceInstance(t *testing.T) {
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
	gw.SetBridgeURL(bridge.URL)
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
