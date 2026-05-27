package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/config"
)

type reconnectTestBackendManager struct {
	reconnectedID string
	updatedID     string
	update        BackendUpdate
	err           error
}

func (m *reconnectTestBackendManager) AddBackend(context.Context, string, BackendConfig) error {
	return nil
}

func (m *reconnectTestBackendManager) RemoveBackend(string) error { return nil }

func (m *reconnectTestBackendManager) NotifyToolsChanged() {}

func (m *reconnectTestBackendManager) ReconnectBackend(_ context.Context, id string) error {
	m.reconnectedID = id
	return m.err
}

func (m *reconnectTestBackendManager) UpdateBackend(_ context.Context, id string, update BackendUpdate) error {
	m.updatedID = id
	m.update = update
	return m.err
}

func TestCallbackBaseFromRequestAllowsOnlyPinnedForwardedHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/backends/test", nil)
	r.Host = "internal.example:9086"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "attacker.example")

	got := callbackBaseFromRequest(r, true, []string{"prism.example.com"})
	if got != "https://internal.example:9086" {
		t.Fatalf("callback base = %q", got)
	}

	r.Header.Set("X-Forwarded-Host", "prism.example.com:443")
	got = callbackBaseFromRequest(r, true, []string{"prism.example.com"})
	if got != "https://prism.example.com:443" {
		t.Fatalf("callback base = %q", got)
	}
}

func TestIsValidIDRejectsTraversalAndNestedPaths(t *testing.T) {
	valid := []string{"backend_1", "agent.name", "group-1"}
	for _, id := range valid {
		if !isValidID(id) {
			t.Fatalf("expected %q to be valid", id)
		}
	}

	invalid := []string{"", "../secret", ".hidden", "-flag", "foo/bar", "foo/auth-status", "space id"}
	for _, id := range invalid {
		if isValidID(id) {
			t.Fatalf("expected %q to be invalid", id)
		}
	}
}

func TestIsPolicyPathRequiresExactAgentPolicyRoute(t *testing.T) {
	if !isPolicyPath("/agents/agent-1/policy") {
		t.Fatal("expected exact policy route to match")
	}
	for _, path := range []string{
		"/agents/agent-1/my-policy",
		"/agents/agent-1/extra/policy",
		"/agents/../policy",
	} {
		if isPolicyPath(path) {
			t.Fatalf("expected %q not to match policy route", path)
		}
	}
}

func TestHandleReconnectBackend(t *testing.T) {
	mgr := &reconnectTestBackendManager{}
	api := &API{backendMgr: mgr}
	req := httptest.NewRequest(http.MethodPost, "/backends/Linear/reconnect", nil)
	rec := httptest.NewRecorder()

	api.handleReconnectBackend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if mgr.reconnectedID != "Linear" {
		t.Fatalf("reconnected id = %q", mgr.reconnectedID)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("response = %+v", body)
	}
}

func TestHandleReconnectBackendSurfacesErrors(t *testing.T) {
	mgr := &reconnectTestBackendManager{err: errors.New("no token")}
	api := &API{backendMgr: mgr}
	req := httptest.NewRequest(http.MethodPost, "/backends/Linear/reconnect", nil)
	rec := httptest.NewRecorder()

	api.handleReconnectBackend(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePatchBackendUpdatesSettings(t *testing.T) {
	mgr := &reconnectTestBackendManager{}
	api := &API{backendMgr: mgr}
	body := strings.NewReader(`{"enabled":false,"sandbox":{"profile":"default","memory":"256m"}}`)
	req := httptest.NewRequest(http.MethodPatch, "/backends/Brainfile", body)
	rec := httptest.NewRecorder()

	api.handlePatchBackend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if mgr.updatedID != "Brainfile" {
		t.Fatalf("updated id = %q", mgr.updatedID)
	}
	if mgr.update.Enabled == nil || *mgr.update.Enabled {
		t.Fatalf("enabled update = %#v", mgr.update.Enabled)
	}
	if mgr.update.Sandbox == nil || mgr.update.Sandbox.Memory != "256m" {
		t.Fatalf("sandbox update = %+v", mgr.update.Sandbox)
	}
}

func TestHandlePatchBackendRejectsDangerousSandbox(t *testing.T) {
	mgr := &reconnectTestBackendManager{}
	api := &API{backendMgr: mgr}
	sandbox := config.SandboxConfig{
		Mounts: []config.SandboxMount{{Source: "/var/run/docker.sock", Target: "/workspace/docker.sock"}},
	}
	payload, err := json.Marshal(BackendUpdate{Sandbox: &sandbox})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/backends/Brainfile", strings.NewReader(string(payload)))
	rec := httptest.NewRecorder()

	api.handlePatchBackend(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if mgr.updatedID != "" {
		t.Fatalf("unexpected update id = %q", mgr.updatedID)
	}
}

type asyncAddBackendManager struct {
	added chan string
}

func (m *asyncAddBackendManager) AddBackend(_ context.Context, id string, _ BackendConfig) error {
	m.added <- id
	return nil
}

func (m *asyncAddBackendManager) RemoveBackend(string) error { return nil }

func (m *asyncAddBackendManager) NotifyToolsChanged() {}

func TestHandleAddBackendRunsStdioAddsAsync(t *testing.T) {
	mgr := &asyncAddBackendManager{added: make(chan string, 1)}
	api := &API{backendMgr: mgr}
	req := httptest.NewRequest(http.MethodPost, "/backends/Brainfile", strings.NewReader(`{"command":"npx @brainfile/cli mcp"}`))
	rec := httptest.NewRecorder()

	api.handleAddBackend(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "connecting" || body["id"] != "Brainfile" {
		t.Fatalf("response = %+v", body)
	}

	select {
	case id := <-mgr.added:
		if id != "Brainfile" {
			t.Fatalf("added id = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("async AddBackend was not called")
	}
}
