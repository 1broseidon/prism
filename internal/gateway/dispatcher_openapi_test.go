package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/credentials"
	"github.com/1broseidon/prism/internal/openapi"
	"github.com/1broseidon/prism/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// openapiSpec200 returns a minimal spec with one GET operation returning 200
// JSON, used by the happy-path / truncation tests.
func openapiSpecGET(t *testing.T, baseURL string) *openapi.Spec {
	t.Helper()
	raw := fmt.Sprintf(`{
"openapi":"3.0.0",
"info":{"title":"t","version":"1"},
"servers":[{"url":%q}],
"paths":{"/echo":{"get":{
  "operationId":"echo",
  "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}
}}}
}`, baseURL)
	spec, err := openapi.NewParser().Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	return spec
}

func openapiSpecWithBearer(t *testing.T, baseURL string) *openapi.Spec {
	t.Helper()
	raw := fmt.Sprintf(`{
"openapi":"3.0.0",
"info":{"title":"t","version":"1"},
"servers":[{"url":%q}],
"components":{"securitySchemes":{"bearerAuth":{"type":"http","scheme":"bearer"}}},
"security":[{"bearerAuth":[]}],
"paths":{"/secret":{"get":{
  "operationId":"secret",
  "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}
}}}
}`, baseURL)
	spec, err := openapi.NewParser().Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	return spec
}

func openapiSpecWithApiKey(t *testing.T, baseURL string) *openapi.Spec {
	t.Helper()
	raw := fmt.Sprintf(`{
"openapi":"3.0.0",
"info":{"title":"t","version":"1"},
"servers":[{"url":%q}],
"components":{"securitySchemes":{"apiKey":{"type":"apiKey","in":"header","name":"X-API-Key"}}},
"security":[{"apiKey":[]}],
"paths":{"/secret":{"get":{
  "operationId":"secret",
  "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}
}}}
}`, baseURL)
	spec, err := openapi.NewParser().Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	return spec
}

func TestOpenAPIDispatcher_Dispatch200(t *testing.T) {
	want := `{"hello":"world"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/echo" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, want)
	}))
	defer srv.Close()

	spec := openapiSpecGET(t, srv.URL)
	d, err := NewOpenAPIDispatcher(spec, "", "", OpenAPIDispatcherOptions{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}

	res, err := d.Dispatch(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError: %+v", res)
	}
	text := dispatchText(t, res)
	if !strings.Contains(text, "HTTP 200 OK") {
		t.Errorf("missing header in text: %q", text)
	}
	if !strings.Contains(text, want) {
		t.Errorf("missing body in text: %q", text)
	}
}

func TestOpenAPIDispatcher_TruncatesAt32KB(t *testing.T) {
	// Server returns exactly limit+extra bytes so the dispatcher must clip
	// to the 32KB cap and append the footer.
	const extra = 1024
	payload := strings.Repeat("a", openapiResponseLimit+extra)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	spec := openapiSpecGET(t, srv.URL)
	d, err := NewOpenAPIDispatcher(spec, "", "", OpenAPIDispatcherOptions{})
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}

	res, err := d.Dispatch(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	text := dispatchText(t, res)
	if !strings.Contains(text, "response truncated") {
		t.Errorf("expected truncation footer, got: %q", text[len(text)-200:])
	}
	footer := fmt.Sprintf("showed %d of %d bytes", openapiResponseLimit, openapiResponseLimit+extra)
	if !strings.Contains(text, footer) {
		t.Errorf("expected exact truncation footer %q, got: %q", footer, text[len(text)-200:])
	}
	// Header announces the true total size, not the clipped size.
	if !strings.Contains(text, fmt.Sprintf("· %d bytes", openapiResponseLimit+extra)) {
		t.Errorf("expected header to report total bytes, got: %q", text[:200])
	}
}

func TestOpenAPIDispatcher_4xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad request"}`)
	}))
	defer srv.Close()

	spec := openapiSpecGET(t, srv.URL)
	d, err := NewOpenAPIDispatcher(spec, "", "", OpenAPIDispatcherOptions{})
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}

	res, err := d.Dispatch(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true on 4xx, got: %+v", res)
	}
	text := dispatchText(t, res)
	if !strings.Contains(text, "HTTP 400") || !strings.Contains(text, "bad request") {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestOpenAPIDispatcher_5xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
	}))
	defer srv.Close()

	spec := openapiSpecGET(t, srv.URL)
	d, err := NewOpenAPIDispatcher(spec, "", "", OpenAPIDispatcherOptions{})
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}

	res, err := d.Dispatch(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true on 5xx, got: %+v", res)
	}
	text := dispatchText(t, res)
	if !strings.Contains(text, "HTTP 500") || !strings.Contains(text, "boom") {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestOpenAPIDispatcher_NetworkTimeout(t *testing.T) {
	// Hold the request open longer than the dispatcher's client timeout.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	spec := openapiSpecGET(t, srv.URL)
	d, err := NewOpenAPIDispatcher(spec, "", "", OpenAPIDispatcherOptions{
		HTTPClient: &http.Client{Timeout: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}

	res, err := d.Dispatch(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on timeout, got: %+v", res)
	}
	text := dispatchText(t, res)
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "timeout") {
		t.Errorf("expected timeout reason in text: %q", text)
	}
}

func TestOpenAPIDispatcher_BearerCredentialInjection(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	spec := openapiSpecWithBearer(t, srv.URL)
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.RegisterCredential("bearer-backend", &credentials.Static{Header: "Authorization", Value: "s3cret"})

	d, err := NewOpenAPIDispatcher(spec, "", "bearerAuth", OpenAPIDispatcherOptions{
		CredResolver: gw.openAPICredResolver("bearer-backend", "bearerAuth", spec),
	})
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}

	if _, err := d.Dispatch(context.Background(), "secret", nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got.Load() != "Bearer s3cret" {
		t.Errorf("Authorization header = %v, want %q", got.Load(), "Bearer s3cret")
	}
}

func TestOpenAPIDispatcher_ApiKeyCredentialInjection(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("X-API-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	spec := openapiSpecWithApiKey(t, srv.URL)
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()
	gw.RegisterCredential("apikey-backend", &credentials.Static{Header: "X-API-Key", Value: "abc123"})

	d, err := NewOpenAPIDispatcher(spec, "", "apiKey", OpenAPIDispatcherOptions{
		CredResolver: gw.openAPICredResolver("apikey-backend", "apiKey", spec),
	})
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}

	if _, err := d.Dispatch(context.Background(), "secret", nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got.Load() != "abc123" {
		t.Errorf("X-API-Key header = %v, want %q", got.Load(), "abc123")
	}
}

// TestRouteToolCall_RejectsDisabledOpenAPITool guards the regression: the
// disabled-tool check in routeToolCall sits above the dispatcher boundary, so
// even an enabled upstream operation must be rejected with IsError when the
// operator has toggled it off.
func TestRouteToolCall_RejectsDisabledOpenAPITool(t *testing.T) {
	called := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	spec := openapiSpecGET(t, srv.URL)
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw.Close()

	if err := gw.ConnectOpenAPIBackend(context.Background(), "ext", spec, "", ""); err != nil {
		t.Fatalf("connect openapi: %v", err)
	}
	// Operator toggles the operation off.
	gw.applyDisabledTools("ext", []string{"echo"})

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{}`)}}
	res, err := gw.routeToolCall(context.Background(), "ext", "echo", req)
	if err != nil {
		t.Fatalf("routeToolCall: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected disabled-tool denial, got %+v", res)
	}
	if called.Load() {
		t.Fatal("upstream must not be called when tool is disabled")
	}
}

// TestRestoresOpenAPIBackendFromPersistedBytes covers the round-trip:
// persist a spec, restart with a fresh gateway, restore reconstructs the
// backend and its tools route through routeToolCall.
func TestRestoresOpenAPIBackendFromPersistedBytes(t *testing.T) {
	called := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	rawSpec := fmt.Sprintf(`{
"openapi":"3.0.0","info":{"title":"t","version":"1"},
"servers":[{"url":%q}],
"paths":{"/echo":{"get":{"operationId":"echo","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"}}}}}}}}
}`, srv.URL)

	kv := store.NewMemoryStore()

	// Persist with one gateway, then bring up a fresh one to simulate restart.
	gw1 := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	gw1.SetStore(kv)
	gw1.PersistOpenAPIBackend("ext", []byte(rawSpec), "", "", "")
	gw1.Close()

	gw2 := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer gw2.Close()
	gw2.SetStore(kv)
	gw2.LoadPersistedBackends(context.Background())

	status := gw2.Status()
	if len(status) != 1 {
		t.Fatalf("status count = %d", len(status))
	}
	if status[0].ID != "ext" || status[0].Disconnected {
		t.Fatalf("unexpected status: %+v", status[0])
	}
	if status[0].Transport != "openapi" {
		t.Errorf("transport = %q, want openapi", status[0].Transport)
	}
	if len(status[0].Tools) != 1 || status[0].Tools[0].Name != "ext__echo" {
		t.Fatalf("tools = %+v", status[0].Tools)
	}

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{}`)}}
	res, err := gw2.routeToolCall(context.Background(), "ext", "echo", req)
	if err != nil {
		t.Fatalf("routeToolCall: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected success on restored backend, got %+v", res)
	}
	if !called.Load() {
		t.Fatal("upstream should have been called")
	}
}

// dispatchText pulls the single TextContent payload off a CallToolResult so
// tests can assert against the rendered body. Fails fast if the result shape
// differs from the dispatcher's contract.
func dispatchText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T, want *mcp.TextContent", res.Content[0])
	}
	return tc.Text
}
