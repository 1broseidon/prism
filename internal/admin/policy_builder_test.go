package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/authserver"
	"github.com/1broseidon/prism/internal/store"
)

// policyTestEnv wires the admin API against the production authserver.Server
// for grants + a small in-memory group manager. Tests use this to assert the
// end-to-end compile-down behavior without re-implementing the storage.
type policyTestEnv struct {
	api       *API
	agentMgr  *policyMockAgentManager
	groupMgr  *policyMockGroupManager
	grantSrv  *authserver.Server
	backendMg *policyMockBackendManager
}

func newPolicyTestEnv(t *testing.T) *policyTestEnv {
	t.Helper()
	agentMgr := &policyMockAgentManager{
		agents:   map[string]bool{"prism-agent-1": true, "prism-agent-2": true},
		policies: map[string]*AgentPolicy{},
	}
	groupMgr := &policyMockGroupManager{
		groups: map[string]*GroupInfo{
			"engineering": {Name: "engineering", Scopes: nil, Source: "dynamic"},
			"contractors": {Name: "contractors", Scopes: nil, Source: "dynamic"},
		},
	}
	km, err := authserver.NewKeyManager("")
	if err != nil {
		t.Fatal(err)
	}
	srv := authserver.NewServer(&authserver.Config{Issuer: "http://localhost", TokenTTLSeconds: 3600}, km, store.NewMemoryStore(), nil)

	backendMgr := &policyMockBackendManager{backends: []string{"fs", "github"}}
	api := NewAPI(
		func() any { return map[string]any{"backends": []any{}} },
		backendMgr,
		func() []any { return nil },
		func(string) bool { return false },
		func() int { return 0 },
		func() []any { return nil },
		agentMgr,
		groupMgr,
		nil,
		nil,
	)
	api.SetGrantManager(&policyGrantShim{srv: srv})
	return &policyTestEnv{api: api, agentMgr: agentMgr, groupMgr: groupMgr, grantSrv: srv, backendMg: backendMgr}
}

func (e *policyTestEnv) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body == nil {
		rd = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		rd = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, "/api/v1"+path, rd)
	w := httptest.NewRecorder()
	e.api.Handler().ServeHTTP(w, r)
	return w
}

func TestCreateCapability_NoConstraintsVerbExpandsToScopes(t *testing.T) {
	env := newPolicyTestEnv(t)
	spec := CapabilitySpec{
		Action: ActionSpec{Mode: "verb", VerbSlug: "write-files"},
	}
	w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", spec)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	got := env.groupMgr.groups["engineering"].Scopes
	want := []string{"fs:append_file", "fs:create_dir", "fs:delete_file", "fs:write_file"}
	if len(got) != len(want) {
		t.Fatalf("got %d scopes %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scope[%d] = %q want %q (full = %v)", i, got[i], want[i], got)
		}
	}
}

func TestCreateCapability_NoConstraintsSpecificTool(t *testing.T) {
	env := newPolicyTestEnv(t)
	spec := CapabilitySpec{Action: ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"}}
	w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", spec)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	got := env.groupMgr.groups["engineering"].Scopes
	if len(got) != 1 || got[0] != "fs:read_file" {
		t.Fatalf("scopes = %v", got)
	}
}

func TestCreateCapability_BackendWildcardScope(t *testing.T) {
	env := newPolicyTestEnv(t)
	spec := CapabilitySpec{Action: ActionSpec{Mode: "backend_wildcard", Backend: "github"}}
	w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", spec)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	got := env.groupMgr.groups["engineering"].Scopes
	if len(got) != 1 || got[0] != "github:*" {
		t.Fatalf("scopes = %v", got)
	}
}

func TestCreateCapability_VerbPlusConstraint_CreatesTemplateAndBinding(t *testing.T) {
	env := newPolicyTestEnv(t)
	spec := CapabilitySpec{
		Action: ActionSpec{Mode: "verb", VerbSlug: "write-files"},
		Where:  &WhereSpec{Mode: "agent_home"},
	}
	w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", spec)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	templates := env.grantSrv.ListGrantTemplates()
	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tmpl := templates[0]
	if tmpl.Spec.Tool != "*" || tmpl.Spec.Backend != "*" {
		t.Fatalf("expected Tool=* Backend=*, got Tool=%q Backend=%q", tmpl.Spec.Tool, tmpl.Spec.Backend)
	}
	pred, ok := tmpl.Spec.Args["_tool"]
	if !ok {
		t.Fatalf("expected _tool predicate, args = %+v", tmpl.Spec.Args)
	}
	if len(pred.ToolInSet) != 4 {
		t.Fatalf("expected 4 tool_in_set entries, got %d: %v", len(pred.ToolInSet), pred.ToolInSet)
	}
	bindings := env.grantSrv.ListGrantBindings()
	if len(bindings) != 1 || bindings[0].TemplateHash != tmpl.Hash {
		t.Fatalf("binding mismatch: %+v", bindings)
	}
	if len(bindings[0].Subjects.Groups) != 1 || bindings[0].Subjects.Groups[0] != "engineering" {
		t.Fatalf("subject selector = %+v", bindings[0].Subjects)
	}
	// Scopes should NOT have been touched on the group when constraints applied.
	if len(env.groupMgr.groups["engineering"].Scopes) != 0 {
		t.Fatalf("expected no scope changes, got %v", env.groupMgr.groups["engineering"].Scopes)
	}
}

func TestCreateCapability_DedupReusesTemplate(t *testing.T) {
	env := newPolicyTestEnv(t)
	spec := CapabilitySpec{
		Action: ActionSpec{Mode: "verb", VerbSlug: "write-files"},
		Where:  &WhereSpec{Mode: "agent_home"},
	}
	if w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", spec); w.Code != http.StatusCreated {
		t.Fatalf("first post status=%d body=%s", w.Code, w.Body.String())
	}
	if w := env.do(t, http.MethodPost, "/policy/subjects/groups/contractors/capabilities", spec); w.Code != http.StatusCreated {
		t.Fatalf("second post status=%d body=%s", w.Code, w.Body.String())
	}
	templates := env.grantSrv.ListGrantTemplates()
	if len(templates) != 1 {
		t.Fatalf("expected dedup to keep 1 template, got %d", len(templates))
	}
	bindings := env.grantSrv.ListGrantBindings()
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings (one per group), got %d", len(bindings))
	}
}

func TestEditCapability_ForkLeavesOtherSubjectUntouched(t *testing.T) {
	env := newPolicyTestEnv(t)
	spec := CapabilitySpec{
		Action: ActionSpec{Mode: "verb", VerbSlug: "write-files"},
		Where:  &WhereSpec{Mode: "agent_home"},
	}
	if w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", spec); w.Code != http.StatusCreated {
		t.Fatalf("first post status=%d body=%s", w.Code, w.Body.String())
	}
	if w := env.do(t, http.MethodPost, "/policy/subjects/groups/contractors/capabilities", spec); w.Code != http.StatusCreated {
		t.Fatalf("second post status=%d body=%s", w.Code, w.Body.String())
	}
	originalTemplates := env.grantSrv.ListGrantTemplates()
	if len(originalTemplates) != 1 {
		t.Fatalf("expected 1 dedup template, got %d", len(originalTemplates))
	}
	originalHash := originalTemplates[0].Hash

	// Edit engineering's capability with a different where clause.
	listW := env.do(t, http.MethodGet, "/policy/subjects/groups/engineering/capabilities", nil)
	var caps []CapabilityView
	if err := json.NewDecoder(listW.Body).Decode(&caps); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(caps) != 1 {
		t.Fatalf("expected 1 capability for engineering, got %d", len(caps))
	}
	cap := caps[0]
	edited := CapabilitySpec{
		Action: ActionSpec{Mode: "verb", VerbSlug: "write-files"},
		Where:  &WhereSpec{Mode: "path_prefix", PathPrefix: "/srv/data/"},
	}
	editW := env.do(t, http.MethodPut, "/policy/subjects/groups/engineering/capabilities/"+cap.ID, edited)
	if editW.Code != http.StatusOK {
		t.Fatalf("edit status=%d body=%s", editW.Code, editW.Body.String())
	}

	// Engineering binding now points at a new template; contractors' binding
	// still points at the original.
	bindings := env.grantSrv.ListGrantBindings()
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings post-fork, got %d", len(bindings))
	}
	var engHash, conHash string
	for _, b := range bindings {
		if containsString(b.Subjects.Groups, "engineering") {
			engHash = b.TemplateHash
		}
		if containsString(b.Subjects.Groups, "contractors") {
			conHash = b.TemplateHash
		}
	}
	if engHash == originalHash {
		t.Fatalf("expected engineering binding to point at a new template hash; still on %s", originalHash)
	}
	if conHash != originalHash {
		t.Fatalf("expected contractors to retain original template hash %s, got %s", originalHash, conHash)
	}
}

func TestDeleteCapability_ScopeShape(t *testing.T) {
	env := newPolicyTestEnv(t)
	spec := CapabilitySpec{Action: ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"}}
	w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", spec)
	if w.Code != http.StatusCreated {
		t.Fatalf("post status=%d body=%s", w.Code, w.Body.String())
	}
	if len(env.groupMgr.groups["engineering"].Scopes) != 1 {
		t.Fatalf("expected 1 scope, got %v", env.groupMgr.groups["engineering"].Scopes)
	}
	// Fetch list to get the capability ID.
	listW := env.do(t, http.MethodGet, "/policy/subjects/groups/engineering/capabilities", nil)
	var caps []CapabilityView
	if err := json.NewDecoder(listW.Body).Decode(&caps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(caps) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(caps))
	}
	dw := env.do(t, http.MethodDelete, "/policy/subjects/groups/engineering/capabilities/"+caps[0].ID, nil)
	if dw.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", dw.Code, dw.Body.String())
	}
	if len(env.groupMgr.groups["engineering"].Scopes) != 0 {
		t.Fatalf("expected scope removed, got %v", env.groupMgr.groups["engineering"].Scopes)
	}
	// Templates list untouched.
	if len(env.grantSrv.ListGrantTemplates()) != 0 {
		t.Fatalf("expected no templates touched")
	}
}

func TestDeleteCapability_GrantShape(t *testing.T) {
	env := newPolicyTestEnv(t)
	spec := CapabilitySpec{
		Action: ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
		Where:  &WhereSpec{Mode: "path_prefix", PathPrefix: "/srv/data/"},
	}
	w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", spec)
	if w.Code != http.StatusCreated {
		t.Fatalf("post status=%d body=%s", w.Code, w.Body.String())
	}
	bindings := env.grantSrv.ListGrantBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}

	listW := env.do(t, http.MethodGet, "/policy/subjects/groups/engineering/capabilities", nil)
	var caps []CapabilityView
	_ = json.NewDecoder(listW.Body).Decode(&caps)
	if len(caps) != 1 {
		t.Fatalf("expected 1 view, got %d", len(caps))
	}
	dw := env.do(t, http.MethodDelete, "/policy/subjects/groups/engineering/capabilities/"+caps[0].ID, nil)
	if dw.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", dw.Code, dw.Body.String())
	}
	if len(env.grantSrv.ListGrantBindings()) != 0 {
		t.Fatalf("expected binding deleted")
	}
}

func TestGetCapabilities_ReturnsBothShapes(t *testing.T) {
	env := newPolicyTestEnv(t)
	// One scope-shape and one grant-shape capability.
	if w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities",
		CapabilitySpec{Action: ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"}}); w.Code != http.StatusCreated {
		t.Fatalf("scope post: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities",
		CapabilitySpec{
			Action: ActionSpec{Mode: "tool", Backend: "github", Tool: "list_issues"},
			Where:  &WhereSpec{Mode: "path_prefix", PathPrefix: "acme/"},
		}); w.Code != http.StatusCreated {
		t.Fatalf("grant post: %d %s", w.Code, w.Body.String())
	}
	listW := env.do(t, http.MethodGet, "/policy/subjects/groups/engineering/capabilities", nil)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listW.Code, listW.Body.String())
	}
	var caps []CapabilityView
	if err := json.NewDecoder(listW.Body).Decode(&caps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(caps) != 2 {
		t.Fatalf("expected 2 capabilities, got %d (%+v)", len(caps), caps)
	}
	var sawScope, sawGrant bool
	for _, c := range caps {
		switch c.Source {
		case capabilitySourceScope:
			sawScope = true
		case capabilitySourceGrant:
			sawGrant = true
		}
	}
	if !sawScope || !sawGrant {
		t.Fatalf("expected one scope + one grant source, got %+v", caps)
	}
}

func TestVerbsEndpoint_ListsAndResolves(t *testing.T) {
	env := newPolicyTestEnv(t)
	w := env.do(t, http.MethodGet, "/policy/verbs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var list []Verb
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) < 7 {
		t.Fatalf("expected at least 7 verbs, got %d", len(list))
	}
	r := env.do(t, http.MethodGet, "/policy/verbs/write-files/resolve?enabled_backends=fs,github", nil)
	if r.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", r.Code, r.Body.String())
	}
	var pairs []ResolvedTool
	if err := json.NewDecoder(r.Body).Decode(&pairs); err != nil {
		t.Fatalf("decode pairs: %v", err)
	}
	if len(pairs) != 4 {
		t.Fatalf("expected 4 pairs (fs only), got %d %+v", len(pairs), pairs)
	}
	un := env.do(t, http.MethodGet, "/policy/verbs/no-such-verb/resolve", nil)
	if un.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", un.Code)
	}
}

func TestCreateCapability_RoleSubjectUsesBindingEvenWithoutConstraints(t *testing.T) {
	env := newPolicyTestEnv(t)
	spec := CapabilitySpec{Action: ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"}}
	w := env.do(t, http.MethodPost, "/policy/subjects/roles/senior/capabilities", spec)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	bindings := env.grantSrv.ListGrantBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding for role subject, got %d", len(bindings))
	}
	if !containsString(bindings[0].Subjects.Roles, "senior") {
		t.Fatalf("expected role binding, got %+v", bindings[0].Subjects)
	}
}

func TestCreateCapability_InvalidSpecRejected(t *testing.T) {
	env := newPolicyTestEnv(t)
	cases := []CapabilitySpec{
		{Action: ActionSpec{Mode: "verb"}},                                                                    // missing slug
		{Action: ActionSpec{Mode: "verb", VerbSlug: "no-such"}},                                               // unknown verb
		{Action: ActionSpec{Mode: "tool"}},                                                                    // missing backend/tool
		{Action: ActionSpec{Mode: "backend_wildcard"}},                                                        // missing backend
		{Action: ActionSpec{Mode: "unknown"}},                                                                 // unknown mode
		{Action: ActionSpec{Mode: "tool", Backend: "fs", Tool: "rf"}, Where: &WhereSpec{Mode: "path_prefix"}}, // missing prefix
	}
	for i, c := range cases {
		w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("case %d: expected 400, got %d body=%s", i, w.Code, w.Body.String())
		}
	}
}

func TestCreateCapability_InvalidPathRejected(t *testing.T) {
	env := newPolicyTestEnv(t)
	w := env.do(t, http.MethodGet, "/policy/subjects/typo/engineering/capabilities", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown subject type, got %d", w.Code)
	}
}

// ---- mocks ----

type policyMockAgentManager struct {
	agents   map[string]bool
	policies map[string]*AgentPolicy
}

func (m *policyMockAgentManager) ListAgents() []any               { return nil }
func (m *policyMockAgentManager) GetAgentByPrismID(id string) any { return nil }
func (m *policyMockAgentManager) RemoveAgent(string) bool         { return false }
func (m *policyMockAgentManager) RemoveStaleAgents() int          { return 0 }
func (m *policyMockAgentManager) DeleteAgentPolicy(prismID string) error {
	delete(m.policies, prismID)
	return nil
}
func (m *policyMockAgentManager) SetAgentBackendPolicies(string, map[string]auth.BackendPolicy) error {
	return nil
}
func (m *policyMockAgentManager) SetAgentPolicy(prismID string, groups, grant, deny []string) error {
	if !m.agents[prismID] {
		return errors.New("agent not found")
	}
	m.policies[prismID] = &AgentPolicy{Groups: groups, Grant: grant, Deny: deny}
	return nil
}

// GetAgentPolicy satisfies the optional PolicyAgentReader.
func (m *policyMockAgentManager) GetAgentPolicy(prismID string) (*AgentPolicy, error) {
	if !m.agents[prismID] {
		return nil, nil
	}
	p, ok := m.policies[prismID]
	if !ok {
		return &AgentPolicy{}, nil
	}
	return p, nil
}

type policyMockGroupManager struct {
	groups   map[string]*GroupInfo
	defaults []string
}

func (m *policyMockGroupManager) ListGroups() []GroupInfo {
	out := make([]GroupInfo, 0, len(m.groups))
	for _, g := range m.groups {
		out = append(out, *g)
	}
	return out
}
func (m *policyMockGroupManager) GetGroup(name string) *GroupInfo {
	if g, ok := m.groups[name]; ok {
		return g
	}
	return nil
}
func (m *policyMockGroupManager) SetGroup(name string, scopes []string) error {
	g, ok := m.groups[name]
	if !ok {
		return fmt.Errorf("group not found: %s", name)
	}
	g.Scopes = append([]string(nil), scopes...)
	return nil
}
func (m *policyMockGroupManager) SetGroupBackendPolicies(string, map[string]auth.BackendPolicy) error {
	return nil
}
func (m *policyMockGroupManager) DeleteGroup(name string) error     { delete(m.groups, name); return nil }
func (m *policyMockGroupManager) DefaultScopes() []string           { return m.defaults }
func (m *policyMockGroupManager) SetDefaultScopes(s []string) error { m.defaults = s; return nil }
func (m *policyMockGroupManager) DefaultBackendPolicies() map[string]auth.BackendPolicy {
	return nil
}
func (m *policyMockGroupManager) SetDefaultBackendPolicies(map[string]auth.BackendPolicy) error {
	return nil
}

type policyMockBackendManager struct {
	backends []string
	notified bool
}

func (m *policyMockBackendManager) AddBackend(_ context.Context, _ string, _ BackendConfig) error {
	return nil
}
func (m *policyMockBackendManager) RemoveBackend(string) error { return nil }
func (m *policyMockBackendManager) NotifyToolsChanged()        { m.notified = true }

// ListBackendIDs satisfies the duck-typed listing interface in enabledBackends.
func (m *policyMockBackendManager) ListBackendIDs() []string {
	out := make([]string, len(m.backends))
	copy(out, m.backends)
	return out
}

// AddBackend signature must match interface; need ctx context. Use shim with right import.
// The interface signature uses context.Context, which we mirror via a thin wrapper below.

// policyGrantShim mirrors the production GrantManager wired around an
// authserver.Server. Identical in shape to testGrantStore in grant_templates_test.go;
// duplicated to keep policy tests independent of that file's load order.
type policyGrantShim struct{ srv *authserver.Server }

func (s *policyGrantShim) ListGrantTemplates() []auth.GrantTemplate {
	return s.srv.ListGrantTemplates()
}
func (s *policyGrantShim) GetGrantTemplate(id string, version int) (auth.GrantTemplate, error) {
	return s.srv.GetGrantTemplate(id, version)
}
func (s *policyGrantShim) GetGrantTemplateByHash(hash string) (auth.GrantTemplate, error) {
	return s.srv.GetGrantTemplateByHash(hash)
}
func (s *policyGrantShim) SaveGrantTemplate(t auth.GrantTemplate) (auth.GrantTemplate, error) {
	return s.srv.SaveGrantTemplate(t)
}
func (s *policyGrantShim) DeleteGrantTemplate(id string, version int) error {
	return s.srv.DeleteGrantTemplate(id, version)
}
func (s *policyGrantShim) ListGrantBindings() []auth.GrantBinding {
	return s.srv.ListGrantBindings()
}
func (s *policyGrantShim) GetGrantBinding(id string) (auth.GrantBinding, error) {
	return s.srv.GetGrantBinding(id)
}
func (s *policyGrantShim) SetGrantBinding(b auth.GrantBinding) (auth.GrantBinding, error) {
	return s.srv.SetGrantBinding(b)
}
func (s *policyGrantShim) DeleteGrantBinding(id string) error {
	return s.srv.DeleteGrantBinding(id)
}

func TestParsePolicySubjectsPath(t *testing.T) {
	cases := []struct {
		path     string
		wantType string
		wantID   string
		wantCap  string
		wantOK   bool
	}{
		{"/policy/subjects/groups/eng/capabilities", "groups", "eng", "", true},
		{"/policy/subjects/agents/prism-1/capabilities/scope-abc", "agents", "prism-1", "scope-abc", true},
		{"/policy/subjects/roles/senior/capabilities/bind-xyz", "roles", "senior", "bind-xyz", true},
		{"/policy/subjects/typo/foo/capabilities", "", "", "", false},
		{"/policy/subjects/groups/.bad/capabilities", "", "", "", false},
		{"/policy/subjects/groups/eng/other", "", "", "", false},
		{"/policy/subjects/groups/eng", "", "", "", false},
	}
	for _, c := range cases {
		gotType, gotID, gotCap, gotOK := parsePolicySubjectsPath(c.path)
		if gotType != c.wantType || gotID != c.wantID || gotCap != c.wantCap || gotOK != c.wantOK {
			t.Errorf("parsePolicySubjectsPath(%q) = (%q,%q,%q,%v) want (%q,%q,%q,%v)",
				c.path, gotType, gotID, gotCap, gotOK, c.wantType, c.wantID, c.wantCap, c.wantOK)
		}
	}
}

func TestEncodeDecodeScopeID_RoundTrip(t *testing.T) {
	spec := CapabilitySpec{Action: ActionSpec{Mode: "verb", VerbSlug: "write-files"}}
	scopes := []string{"fs:write_file", "fs:append_file"}
	id := encodeScopeCapabilityID(spec, scopes)
	if !strings.HasPrefix(id, "scope-") {
		t.Fatalf("id = %q (no scope- prefix)", id)
	}
	got, ok := decodeScopeID(id)
	if !ok {
		t.Fatalf("decodeScopeID failed for %q", id)
	}
	if got.Mode != "verb" || got.VerbSlug != "write-files" {
		t.Fatalf("payload = %+v", got)
	}
	if len(got.Scopes) != len(scopes) {
		t.Fatalf("scopes mismatch: got %v want %v", got.Scopes, scopes)
	}
}

func TestHasConstraints_FalseForEmptyOrDefaults(t *testing.T) {
	cases := []CapabilitySpec{
		{Action: ActionSpec{Mode: "tool"}},
		{Action: ActionSpec{Mode: "tool"}, Where: &WhereSpec{Mode: "anywhere"}},
		{Action: ActionSpec{Mode: "tool"}, When: &WhenSpec{Mode: "anytime"}},
		{Action: ActionSpec{Mode: "tool"}, HowSecure: &HowSecureSpec{Mode: "token"}},
	}
	for i, c := range cases {
		if c.hasConstraints() {
			t.Errorf("case %d: expected hasConstraints=false, got true (%+v)", i, c)
		}
	}
}

func TestHasConstraints_TrueWhenAnyChipSet(t *testing.T) {
	cases := []CapabilitySpec{
		{Action: ActionSpec{Mode: "tool"}, Where: &WhereSpec{Mode: "agent_home"}},
		{Action: ActionSpec{Mode: "tool"}, When: &WhenSpec{Mode: "business"}},
		{Action: ActionSpec{Mode: "tool"}, HowSecure: &HowSecureSpec{Mode: "mfa"}},
		{Action: ActionSpec{Mode: "tool"}, Advanced: &AdvancedSpec{RoleRequired: "senior"}},
	}
	for i, c := range cases {
		if !c.hasConstraints() {
			t.Errorf("case %d: expected hasConstraints=true, got false (%+v)", i, c)
		}
	}
}

// TestEditCapability_NoOpDoesNotDelete verifies that PUT with the SAME spec
// on a grant-shape capability does not destroy the row. The bug: both the
// create and the delete in the create-then-delete fork sequence target the
// same deterministic bindingID, so a no-op edit would otherwise delete the
// just-saved binding.
func TestEditCapability_NoOpDoesNotDelete(t *testing.T) {
	env := newPolicyTestEnv(t)
	pfx := "/srv/data/"
	spec := CapabilitySpec{
		Action: ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
		Where:  &WhereSpec{Mode: "path_prefix", PathPrefix: pfx},
	}
	w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", spec)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", w.Code, w.Body.String())
	}
	var created CapabilityView
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.Source != capabilitySourceGrant {
		t.Fatalf("expected grant source, got %q", created.Source)
	}

	// Snapshot counts before the no-op edit.
	bindingsBefore := len(env.grantSrv.ListGrantBindings())
	listBefore := env.do(t, http.MethodGet, "/policy/subjects/groups/engineering/capabilities", nil)
	if listBefore.Code != http.StatusOK {
		t.Fatalf("GET before status=%d", listBefore.Code)
	}
	var capsBefore []CapabilityView
	_ = json.NewDecoder(listBefore.Body).Decode(&capsBefore)

	// Re-PUT with the exact same spec — must short-circuit.
	w2 := env.do(t, http.MethodPut, "/policy/subjects/groups/engineering/capabilities/"+created.ID, spec)
	if w2.Code != http.StatusOK {
		t.Fatalf("PUT no-op status=%d body=%s", w2.Code, w2.Body.String())
	}
	var edited CapabilityView
	if err := json.NewDecoder(w2.Body).Decode(&edited); err != nil {
		t.Fatalf("decode edited: %v", err)
	}
	if edited.ID != created.ID {
		t.Fatalf("no-op edit should preserve id; got %q want %q", edited.ID, created.ID)
	}

	bindingsAfter := len(env.grantSrv.ListGrantBindings())
	if bindingsAfter != bindingsBefore {
		t.Fatalf("binding count changed: before=%d after=%d (no-op edit destroyed or duplicated the row)", bindingsBefore, bindingsAfter)
	}

	listAfter := env.do(t, http.MethodGet, "/policy/subjects/groups/engineering/capabilities", nil)
	if listAfter.Code != http.StatusOK {
		t.Fatalf("GET after status=%d", listAfter.Code)
	}
	var capsAfter []CapabilityView
	_ = json.NewDecoder(listAfter.Body).Decode(&capsAfter)
	if len(capsAfter) != len(capsBefore) {
		t.Fatalf("capability count changed: before=%d after=%d", len(capsBefore), len(capsAfter))
	}

	// Capability must still be fetchable by its original ID.
	view, err := env.api.lookupCapability("groups", "engineering", created.ID)
	if err != nil {
		t.Fatalf("lookupCapability after no-op edit failed: %v", err)
	}
	if view.ID != created.ID {
		t.Fatalf("looked-up view id mismatch: got %q want %q", view.ID, created.ID)
	}
}

// TestEditCapability_NoOpScopeShape covers the same no-op short-circuit for
// scope-shape capabilities. The encodeScopeCapabilityID is deterministic over
// the spec content, so the bug would manifest identically: create returns the
// scoped ID, delete on the same ID strips the same scope back out.
func TestEditCapability_NoOpScopeShape(t *testing.T) {
	env := newPolicyTestEnv(t)
	spec := CapabilitySpec{
		Action: ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
	}
	w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", spec)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", w.Code, w.Body.String())
	}
	var created CapabilityView
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.Source != capabilitySourceScope {
		t.Fatalf("expected scope source, got %q", created.Source)
	}

	scopesBefore := append([]string(nil), env.groupMgr.groups["engineering"].Scopes...)

	w2 := env.do(t, http.MethodPut, "/policy/subjects/groups/engineering/capabilities/"+created.ID, spec)
	if w2.Code != http.StatusOK {
		t.Fatalf("PUT no-op status=%d body=%s", w2.Code, w2.Body.String())
	}
	scopesAfter := env.groupMgr.groups["engineering"].Scopes
	if len(scopesAfter) != len(scopesBefore) {
		t.Fatalf("scope count changed across no-op edit: before=%v after=%v", scopesBefore, scopesAfter)
	}
	for i := range scopesBefore {
		if scopesBefore[i] != scopesAfter[i] {
			t.Fatalf("scope[%d] changed: before=%q after=%q", i, scopesBefore[i], scopesAfter[i])
		}
	}
}

// TestInlineRawEditorEquivalent_NoOpDoesNotDelete simulates the InlineRawEditor
// UI path: read a capability via GET, then PUT the exact serialized spec back.
// This is the round-trip the raw JSON editor produces; it must not destroy the
// capability.
func TestInlineRawEditorEquivalent_NoOpDoesNotDelete(t *testing.T) {
	env := newPolicyTestEnv(t)
	pfx := "/srv/data/"
	create := CapabilitySpec{
		Action: ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
		Where:  &WhereSpec{Mode: "path_prefix", PathPrefix: pfx},
	}
	w := env.do(t, http.MethodPost, "/policy/subjects/groups/engineering/capabilities", create)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", w.Code, w.Body.String())
	}
	var created CapabilityView
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	// Round-trip the Spec field via JSON, then PUT it back unchanged.
	raw, err := json.Marshal(created.Spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	var roundTrip CapabilitySpec
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("unmarshal spec: %v", err)
	}

	bindingsBefore := len(env.grantSrv.ListGrantBindings())
	w2 := env.do(t, http.MethodPut, "/policy/subjects/groups/engineering/capabilities/"+created.ID, roundTrip)
	if w2.Code != http.StatusOK {
		t.Fatalf("PUT round-trip status=%d body=%s", w2.Code, w2.Body.String())
	}
	bindingsAfter := len(env.grantSrv.ListGrantBindings())
	if bindingsAfter != bindingsBefore {
		t.Fatalf("round-trip no-op edit changed binding count: before=%d after=%d", bindingsBefore, bindingsAfter)
	}
	if _, err := env.api.lookupCapability("groups", "engineering", created.ID); err != nil {
		t.Fatalf("capability lost after round-trip no-op edit: %v", err)
	}
}

// TestPolicyErrorStatus_WrappedSentinelsRoute verifies that policyErrorStatus
// honors errors.Is on the package sentinels, even when wrapped with extra
// context via fmt.Errorf. This is the regression test for moving away from
// strings.Contains-based status routing.
func TestPolicyErrorStatus_WrappedSentinelsRoute(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"capability not found wrapped", fmt.Errorf("binding bxyz: %w", ErrCapabilityNotFound), http.StatusNotFound},
		{"subject not found wrapped", fmt.Errorf("group eng: %w", ErrSubjectNotFound), http.StatusNotFound},
		{"invalid spec wrapped", fmt.Errorf("decode: %w", ErrInvalidSpec), http.StatusBadRequest},
		{"template immutable wrapped", fmt.Errorf("tmpl-abc: %w", ErrTemplateImmutable), http.StatusConflict},
		{"policy conflict wrapped", fmt.Errorf("save: %w", ErrPolicyConflict), http.StatusConflict},
		{"policy storage unavailable wrapped", fmt.Errorf("kv: %w", ErrPolicyNotConfigured), http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		got := policyErrorStatus(c.err)
		if got != c.want {
			t.Errorf("%s: status=%d want=%d (err=%v)", c.name, got, c.want, c.err)
		}
	}
}
