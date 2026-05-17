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
