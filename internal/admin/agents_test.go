package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/auth"
)

// mockAgentManager is a test double for AgentManager.
type mockAgentManager struct {
	agents   []mockAgent
	policies map[string]*AgentPolicy
}

type mockAgent struct {
	ClientID string `json:"client_id"`
	PrismID  string `json:"prism_id,omitempty"`
	Label    string `json:"label,omitempty"`
	Dynamic  bool   `json:"dynamic"`
}

func newMockAgentManager() *mockAgentManager {
	return &mockAgentManager{
		agents: []mockAgent{
			{ClientID: "static-1", Dynamic: false},
			{ClientID: "dcr-1", PrismID: "prism-uuid-1", Label: "Agent One", Dynamic: true},
			{ClientID: "dcr-2", PrismID: "prism-uuid-2", Label: "Agent Two", Dynamic: true},
		},
		policies: make(map[string]*AgentPolicy),
	}
}

func (m *mockAgentManager) ListAgents() []any {
	result := make([]any, len(m.agents))
	for i, a := range m.agents {
		result[i] = a
	}
	return result
}

func (m *mockAgentManager) GetAgentByPrismID(prismID string) any {
	for _, a := range m.agents {
		if a.PrismID == prismID {
			return a
		}
	}
	return nil
}

func (m *mockAgentManager) SetAgentPolicy(prismID string, groups []string, grant []string, deny []string) error {
	// Verify agent exists.
	found := false
	for _, a := range m.agents {
		if a.PrismID == prismID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("agent not found: %s", prismID)
	}
	m.policies[prismID] = &AgentPolicy{Groups: groups, Grant: grant, Deny: deny}
	return nil
}

func (m *mockAgentManager) SetAgentBackendPolicies(prismID string, policies map[string]auth.BackendPolicy) error {
	found := false
	for _, a := range m.agents {
		if a.PrismID == prismID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("agent not found: %s", prismID)
	}
	p, ok := m.policies[prismID]
	if !ok {
		p = &AgentPolicy{}
		m.policies[prismID] = p
	}
	if len(policies) == 0 {
		p.BackendPolicies = nil
	} else {
		p.BackendPolicies = policies
	}
	return nil
}

func (m *mockAgentManager) DeleteAgentPolicy(prismID string) error {
	delete(m.policies, prismID)
	return nil
}

func (m *mockAgentManager) RemoveAgent(clientID string) bool {
	for i, a := range m.agents {
		if a.ClientID == clientID && a.Dynamic {
			m.agents = append(m.agents[:i], m.agents[i+1:]...)
			return true
		}
	}
	return false
}

func (m *mockAgentManager) RemoveStaleAgents() int {
	return 0
}

func newTestAPI() (*API, *mockAgentManager) {
	mgr := newMockAgentManager()
	api := NewAPI(
		func() any { return map[string]string{"status": "ok"} },
		nil, // no backend manager
		func() []any { return mgr.ListAgents() },
		func(id string) bool { return mgr.RemoveAgent(id) },
		func() int { return mgr.RemoveStaleAgents() },
		func() []any { return nil },
		mgr,
		nil, // no group manager
		nil, // no OAuth callback
		nil, // no admin auth — open mode for tests
	)
	return api, mgr
}

type stubTraceProvider struct{ resolutions []AgentStorageResolution }

func (s *stubTraceProvider) AgentStorageResolutions(_ string) []AgentStorageResolution {
	return s.resolutions
}

func TestSetAgentBackendPolicies(t *testing.T) {
	api, mgr := newTestAPI()
	body := `{"brainfile":{"workspace_selector":"agent"}}`
	r := httptest.NewRequest(
		http.MethodPut,
		"/agents/prism-uuid-1/backend-policies",
		strings.NewReader(body),
	)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	p, ok := mgr.policies["prism-uuid-1"]
	if !ok || p.BackendPolicies["brainfile"].WorkspaceSelector != "agent" {
		t.Fatalf("policy not stored: %+v", p)
	}
}

func TestAgentStorageResolutionEndpoint(t *testing.T) {
	api, _ := newTestAPI()
	api.SetBackendPolicyTraceProvider(&stubTraceProvider{
		resolutions: []AgentStorageResolution{
			{BackendID: "brainfile", Selector: "agent", Source: "defaults", WorkspaceID: "a-repo"},
		},
	})

	r := httptest.NewRequest(http.MethodGet, "/agents/prism-uuid-1/storage-resolution", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got []AgentStorageResolution
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].BackendID != "brainfile" || got[0].WorkspaceID != "a-repo" {
		t.Fatalf("trace = %+v", got)
	}
}

func TestGetAgents(t *testing.T) {
	api, _ := newTestAPI()

	r := httptest.NewRequest(http.MethodGet, "/agents", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var agents []mockAgent
	if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(agents))
	}
}

func TestGetAgentByPrismID(t *testing.T) {
	api, _ := newTestAPI()

	r := httptest.NewRequest(http.MethodGet, "/agents/prism-uuid-1", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var agent mockAgent
	if err := json.NewDecoder(w.Body).Decode(&agent); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if agent.PrismID != "prism-uuid-1" {
		t.Errorf("PrismID = %q, want prism-uuid-1", agent.PrismID)
	}
	if agent.Label != "Agent One" {
		t.Errorf("Label = %q, want Agent One", agent.Label)
	}
}

func TestGetAgentByPrismID_NotFound(t *testing.T) {
	api, _ := newTestAPI()

	r := httptest.NewRequest(http.MethodGet, "/agents/nonexistent", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSetAgentPolicy(t *testing.T) {
	api, mgr := newTestAPI()

	body := `{"groups": ["readers", "writers"], "grant": ["extra:tool"], "deny": ["bad:tool"]}`
	r := httptest.NewRequest(http.MethodPut, "/agents/prism-uuid-1/policy", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify policy was stored.
	p, ok := mgr.policies["prism-uuid-1"]
	if !ok {
		t.Fatal("policy not stored")
	}
	if len(p.Groups) != 2 {
		t.Errorf("groups = %v, want 2 elements", p.Groups)
	}
	if len(p.Grant) != 1 || p.Grant[0] != "extra:tool" {
		t.Errorf("grant = %v, want [extra:tool]", p.Grant)
	}
	if len(p.Deny) != 1 || p.Deny[0] != "bad:tool" {
		t.Errorf("deny = %v, want [bad:tool]", p.Deny)
	}
}

func TestSetAgentPolicy_InvalidJSON(t *testing.T) {
	api, _ := newTestAPI()

	r := httptest.NewRequest(http.MethodPut, "/agents/prism-uuid-1/policy", strings.NewReader("not json"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteAgentPolicy(t *testing.T) {
	api, mgr := newTestAPI()

	// First set a policy.
	mgr.policies["prism-uuid-1"] = &AgentPolicy{Groups: []string{"readers"}}

	r := httptest.NewRequest(http.MethodDelete, "/agents/prism-uuid-1/policy", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify policy was deleted.
	if _, ok := mgr.policies["prism-uuid-1"]; ok {
		t.Error("policy should have been deleted")
	}
}

func TestRemoveAgent(t *testing.T) {
	api, mgr := newTestAPI()

	initial := len(mgr.agents)
	r := httptest.NewRequest(http.MethodDelete, "/agents/dcr-1", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if len(mgr.agents) != initial-1 {
		t.Errorf("expected %d agents after removal, got %d", initial-1, len(mgr.agents))
	}
}

func TestRemoveStaticAgent_Fails(t *testing.T) {
	api, _ := newTestAPI()

	r := httptest.NewRequest(http.MethodDelete, "/agents/static-1", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for static agent removal, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAgentMgrNil_Returns503(t *testing.T) {
	api := NewAPI(
		func() any { return nil },
		nil,
		nil,
		nil,
		nil,
		nil,
		nil, // no agent manager
		nil, // no group manager
		nil, // no OAuth callback
		nil, // no admin auth
	)

	r := httptest.NewRequest(http.MethodGet, "/agents/some-id", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}
