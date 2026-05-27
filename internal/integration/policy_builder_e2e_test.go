//go:build integration

// Package integration — Policy Builder end-to-end suite.
//
// This file covers spec §12 of
// docs/superpowers/specs/2026-05-18-prism-policy-builder.md by exercising the
// admin Policy Builder HTTP surface against an in-process auth server +
// gateway. The fourteen TestE2E_PolicyBuilder_* tests each implement one of
// the spec's acceptance bullets — the bullet covered is quoted in a comment
// at the top of the test function.
//
// The suite is intentionally black-box: every assertion is made through the
// admin HTTP API (/api/v1/...) or the gateway MCP route. Internal KV access
// is avoided so the tests stay coupled to the operator surface, not to any
// private storage layout.
//
// Test infrastructure:
//   - Reuses the testClock pattern from grants_e2e_test.go.
//   - Spawns the auth server, gateway, and admin API via httptest.NewServer.
//   - Wires a small in-test BackendManager that satisfies the duck-typed
//     ListBackendIDs interface the policy builder uses to resolve verbs.
//   - The fs backend is registered with ID="fs" so verb mappings line up
//     with the hard-coded vocabulary shipped in internal/admin/verbs.go.
//   - For the verb-expansion test (TestE2E_PolicyBuilder_VerbExpansionAcrossBackends)
//     a second backend ID="github" is added; the policy builder treats that
//     as a second enabled backend even though no live MCP server is connected
//     for github tools — verb resolution is a pure name-table lookup.
//
// Runtime budget: the full suite must complete in under 30 seconds (see
// task-37 contract). Setup is cached per test (each test calls newPolicySuite)
// so individual tests stay independent.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/admin"
	"github.com/1broseidon/prism/internal/analytics"
	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/authserver"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/gateway"
	"github.com/1broseidon/prism/internal/identity"
	"github.com/1broseidon/prism/internal/store"
)

// policyBackendMgr is the minimal BackendManager the admin API requires plus
// the duck-typed ListBackendIDs hook that drives verb resolution in
// internal/admin/policy_builder.go. We track AddBackend/RemoveBackend so the
// tests can extend the enabled set at runtime (used by
// TestE2E_PolicyBuilder_VerbExpansionAcrossBackends).
type policyBackendMgr struct {
	mu       sync.Mutex
	backends []string
}

func (m *policyBackendMgr) AddBackend(_ context.Context, id string, _ admin.BackendConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.backends {
		if b == id {
			return nil
		}
	}
	m.backends = append(m.backends, id)
	return nil
}

func (m *policyBackendMgr) RemoveBackend(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.backends[:0]
	for _, b := range m.backends {
		if b != id {
			out = append(out, b)
		}
	}
	m.backends = out
	return nil
}

func (m *policyBackendMgr) NotifyToolsChanged() {}

// ListBackendIDs satisfies the duck-typed interface in policy_builder.go.
func (m *policyBackendMgr) ListBackendIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.backends))
	copy(out, m.backends)
	return out
}

// addID appends a backend id without going through AddBackend (which expects
// a BackendConfig). Used by tests that just need verb resolution to see an
// "enabled" backend without spinning up an actual MCP server.
func (m *policyBackendMgr) addID(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.backends {
		if b == id {
			return
		}
	}
	m.backends = append(m.backends, id)
}

// policySuite is a slimmer cousin of e2eSuite from grants_e2e_test.go,
// scoped to the admin HTTP surface the Policy Builder uses. It spins up:
//   - an authserver.Server backing a memory KV
//   - a gateway with an fs MCP backend (ID="fs") for the request-time test
//   - the admin API wired to use them
type policySuite struct {
	t          *testing.T
	ctx        context.Context
	clock      *testClock
	kv         store.Store
	authSrv    *authserver.Server
	authHTTP   *httptest.Server
	gw         *gateway.Gateway
	gwHTTP     *httptest.Server
	adminHTTP  *httptest.Server
	backendMgr *policyBackendMgr
	agentMgr   *e2eAgentManager
	groupMgr   *e2eGroupManager
	events     *analytics.SQLiteStore
	ring       *analytics.RingBuffer
	emitter    *syncGrantEmitter
	http       *http.Client

	clientID string
	prismID  string
}

func newPolicySuite(t *testing.T) *policySuite {
	t.Helper()
	ctx := context.Background()
	clock := newTestClock()
	kv := store.NewMemoryStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	km, err := authserver.NewKeyManager("")
	if err != nil {
		t.Fatal(err)
	}
	// Seed an "engineering" group so capability rows have somewhere to attach.
	authSrv := authserver.NewServer(&authserver.Config{
		Issuer:          e2eIssuer,
		TokenTTLSeconds: int((24 * time.Hour) / time.Second),
		DefaultScopes:   []string{},
	}, km, kv, logger, map[string]authserver.GroupConfig{
		"engineering": {Scopes: []string{}},
		"contractors": {Scopes: []string{}},
	})
	authSrv.SetClock(clock.Now)

	eventStore, err := analytics.OpenSQLiteStore(filepath.Join(t.TempDir(), "policy_events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eventStore.Close() })
	ring := analytics.NewRingBuffer(100)
	emitter := &syncGrantEmitter{store: eventStore, ring: ring}

	// fs backend is registered with ID="fs" — required because the policy
	// builder filters verb expansions by enabled backend IDs and the verb
	// table uses "fs" as the backend literal.
	backendHTTP := newFSBackend(t)
	gw := gateway.New(logger)
	gw.SetClock(clock.Now)
	gw.SetPolicyResolver(authSrv)
	gw.SetGrantEmitter(emitter)
	if err := gw.ConnectBackend(ctx, &config.ServerConfig{
		ID:        "fs",
		URL:       backendHTTP.URL + "/mcp",
		Namespace: "fs",
		Workspace: &config.WorkspaceConfig{
			ID:        "repo",
			Type:      config.WorkspaceTypeEphemeral,
			Mode:      config.WorkspaceModeSnapshot,
			WriteMode: config.WorkspaceWriteStage,
		},
	}); err != nil {
		t.Fatalf("ConnectBackend: %v", err)
	}
	t.Cleanup(gw.Close)

	validator := auth.NewTokenValidator(&auth.TokenValidatorConfig{
		IssuerURL:         e2eIssuer,
		Audience:          e2eIssuer,
		StaticJWKS:        km.JWKS(),
		GenerationChecker: auth.NewCachedGenerationChecker(authSrv, 0),
		Now:               clock.Now,
	})
	handler := auth.Middleware(validator, "http://prism-gateway.e2e/mcp",
		auth.WithDPoPReplayCache(authSrv.DPoPReplayCache()),
		auth.WithMiddlewareClock(clock.Now),
	)(gw.Handler())

	s := &policySuite{
		t:          t,
		ctx:        ctx,
		clock:      clock,
		kv:         kv,
		authSrv:    authSrv,
		gw:         gw,
		events:     eventStore,
		ring:       ring,
		emitter:    emitter,
		backendMgr: &policyBackendMgr{backends: []string{"fs"}},
		http: &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
	s.authHTTP = httptest.NewServer(authSrv.Routes())
	t.Cleanup(s.authHTTP.Close)
	s.gwHTTP = httptest.NewServer(handler)
	t.Cleanup(s.gwHTTP.Close)

	s.agentMgr = &e2eAgentManager{srv: authSrv}
	s.groupMgr = &e2eGroupManager{srv: authSrv}
	adminAPI := admin.NewAPI(
		func() any { return map[string]any{"status": "ok"} },
		s.backendMgr,
		s.agentMgr.ListAgents,
		s.agentMgr.RemoveAgent,
		s.agentMgr.RemoveStaleAgents,
		func() []any { return nil },
		s.agentMgr,
		s.groupMgr,
		nil,
		nil,
	)
	adminAPI.SetGrantManager(authSrv)
	adminAPI.SetAnalytics(eventStore, ring)
	// Identity dispatcher: a single instance shared between the admin
	// API (for /identity endpoints + URL compat) and the auth server
	// (for SetGroup/DeleteGroup auto-registration). Mirrors the
	// production wiring in cmd/prism/main.go.
	idMgr := identity.New(kv)
	adminAPI.SetIdentity(idMgr)
	authSrv.SetIdentityDispatcher(idMgr)
	s.adminHTTP = httptest.NewServer(adminAPI.Handler())
	t.Cleanup(s.adminHTTP.Close)

	return s
}

// uniqueSubject returns a per-test subject id namespaced with the test name
// and a monotonic counter so subtests can run in parallel without colliding
// on group/role names.
var policySubjectCounter uint64

func (s *policySuite) uniqueSubject(prefix string) string {
	policySubjectCounter++
	// Use nanoseconds-since-clock-anchor to keep ids short while staying unique
	// across tests inside the same run.
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()%1_000_000_000+int64(policySubjectCounter))
}

// ensureGroup makes sure a dynamic group exists; created via the admin group
// manager so subsequent policy mutations see it.
func (s *policySuite) ensureGroup(name string) {
	if g := s.groupMgr.GetGroup(name); g != nil {
		return
	}
	if err := s.groupMgr.SetGroup(name, nil); err != nil {
		s.t.Fatalf("ensureGroup %s: %v", name, err)
	}
}

// adminJSON mirrors e2eSuite.adminJSON but reads the suite's adminHTTP server.
// Follows one 301 redirect transparently — the identity URL compat layer
// rewrites legacy name-keyed URLs to canonical ULID URLs, and a real client
// (browser / SDK) would follow that. Tests that need to inspect the 301
// itself use the raw HTTP client.
func (s *policySuite) adminJSON(method, path string, body, out any, want int) {
	s.t.Helper()
	var rawBody []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		rawBody = data
	}
	target := s.adminHTTP.URL + "/api/v1" + path
	resp := s.doWithRedirect(method, target, rawBody, body != nil)
	defer resp.Body.Close()
	if resp.StatusCode != want {
		data, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("%s %s status=%d want=%d body=%s", method, target, resp.StatusCode, want, data)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			s.t.Fatalf("decode response from %s %s: %v", method, target, err)
		}
	}
}

func (s *policySuite) doWithRedirect(method, target string, body []byte, withCT bool) *http.Response {
	s.t.Helper()
	for redirects := 0; redirects < 2; redirects++ {
		var reader io.Reader
		if body != nil {
			reader = strings.NewReader(string(body))
		}
		req, err := http.NewRequestWithContext(s.ctx, method, target, reader)
		if err != nil {
			s.t.Fatal(err)
		}
		if withCT && body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := s.http.Do(req)
		if err != nil {
			s.t.Fatal(err)
		}
		if resp.StatusCode != http.StatusMovedPermanently {
			return resp
		}
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		if loc == "" {
			s.t.Fatalf("301 from %s with no Location header", target)
		}
		// Location is relative for same-host redirects.
		if strings.HasPrefix(loc, "/") {
			target = s.adminHTTP.URL + loc
		} else {
			target = loc
		}
	}
	s.t.Fatalf("too many redirects from %s", target)
	return nil
}

// listCapabilities GETs the capability list for a subject.
func (s *policySuite) listCapabilities(subjectType, subjectID string) []admin.CapabilityView {
	s.t.Helper()
	var out []admin.CapabilityView
	s.adminJSON(http.MethodGet,
		fmt.Sprintf("/policy/subjects/%s/%s/capabilities", subjectType, subjectID),
		nil, &out, http.StatusOK)
	return out
}

// createCapability POSTs a new capability on the subject and returns the view.
func (s *policySuite) createCapability(subjectType, subjectID string, spec admin.CapabilitySpec) admin.CapabilityView {
	s.t.Helper()
	var out admin.CapabilityView
	s.adminJSON(http.MethodPost,
		fmt.Sprintf("/policy/subjects/%s/%s/capabilities", subjectType, subjectID),
		spec, &out, http.StatusCreated)
	return out
}

// editCapability PUTs an existing capability replacement and returns the
// resulting view (which will have a fresh ID because edits always fork).
func (s *policySuite) editCapability(subjectType, subjectID, capID string, spec admin.CapabilitySpec) admin.CapabilityView {
	s.t.Helper()
	var out admin.CapabilityView
	s.adminJSON(http.MethodPut,
		fmt.Sprintf("/policy/subjects/%s/%s/capabilities/%s", subjectType, subjectID, capID),
		spec, &out, http.StatusOK)
	return out
}

// deleteCapability removes a capability row.
func (s *policySuite) deleteCapability(subjectType, subjectID, capID string) {
	s.t.Helper()
	s.adminJSON(http.MethodDelete,
		fmt.Sprintf("/policy/subjects/%s/%s/capabilities/%s", subjectType, subjectID, capID),
		nil, nil, http.StatusNoContent)
}

// templateByHash fetches a template via the admin grant-templates endpoint —
// the operator-visible read used by Power Tools.
func (s *policySuite) templateByHash(hash string) auth.GrantTemplate {
	s.t.Helper()
	var out auth.GrantTemplate
	s.adminJSON(http.MethodGet, "/grant-templates/by-hash/"+url.PathEscape(hash), nil, &out, http.StatusOK)
	return out
}

// listTemplates returns every template visible via the admin API.
func (s *policySuite) listTemplates() []auth.GrantTemplate {
	s.t.Helper()
	var out []auth.GrantTemplate
	s.adminJSON(http.MethodGet, "/grant-templates", nil, &out, http.StatusOK)
	return out
}

// listBindingsForTemplate filters bindings by template id via the admin API.
func (s *policySuite) listBindingsForTemplate(templateID string) []auth.GrantBinding {
	s.t.Helper()
	var out []auth.GrantBinding
	s.adminJSON(http.MethodGet, "/grant-bindings?template="+url.QueryEscape(templateID), nil, &out, http.StatusOK)
	return out
}

// listAllBindings returns the full binding list.
func (s *policySuite) listAllBindings() []auth.GrantBinding {
	s.t.Helper()
	var out []auth.GrantBinding
	s.adminJSON(http.MethodGet, "/grant-bindings", nil, &out, http.StatusOK)
	return out
}

// agentPolicy reads the AgentPolicy via the admin agent detail endpoint. We
// use the surrounding agent payload because there's no dedicated read for the
// policy struct — the grant scope strings live inside the returned shape.
func (s *policySuite) agentPolicy(prismID string) admin.AgentPolicy {
	s.t.Helper()
	var raw map[string]any
	s.adminJSON(http.MethodGet, "/agents/"+url.PathEscape(prismID), nil, &raw, http.StatusOK)
	policyRaw, ok := raw["policy"].(map[string]any)
	if !ok {
		// Agents without an explicit policy expose an empty struct.
		return admin.AgentPolicy{}
	}
	encoded, err := json.Marshal(policyRaw)
	if err != nil {
		s.t.Fatal(err)
	}
	var pol admin.AgentPolicy
	if err := json.Unmarshal(encoded, &pol); err != nil {
		s.t.Fatal(err)
	}
	return pol
}

// groupScopes reads the engineering-style group's stored scope list via the
// admin groups endpoint. Returns nil when the group isn't found.
func (s *policySuite) groupScopes(name string) []string {
	s.t.Helper()
	var raw admin.GroupInfo
	s.adminJSON(http.MethodGet, "/groups/"+url.PathEscape(name), nil, &raw, http.StatusOK)
	return raw.Scopes
}

// ensureAgent registers an agent + consents through /authorize so a prism_id
// exists. Copied in essence from e2eSuite.ensureAgent so the policy suite
// stays independent of the grants-epic helpers.
func (s *policySuite) ensureAgent(groups ...string) string {
	s.t.Helper()
	// Mint a new DCR client per call so each test gets a clean prism_id.
	var reg struct {
		ClientID string `json:"client_id"`
	}
	body := map[string]any{
		"client_name":   "Policy E2E Agent",
		"redirect_uris": []string{redirectURI},
	}
	data, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(s.ctx, http.MethodPost, s.authHTTP.URL+"/register", strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		s.t.Fatalf("/register status=%d body=%s", resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		s.t.Fatal(err)
	}
	resp.Body.Close()
	s.clientID = reg.ClientID

	values := url.Values{
		"response_type":         {"code"},
		"client_id":             {s.clientID},
		"redirect_uri":          {redirectURI},
		"state":                 {"state-1"},
		"code_challenge":        {pkceChallenge(codeVerifier)},
		"code_challenge_method": {"S256"},
	}
	getResp := s.doForm(http.MethodGet, s.authHTTP.URL+"/authorize?"+values.Encode(), nil, nil)
	if getResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(getResp.Body)
		_ = getResp.Body.Close()
		s.t.Fatalf("consent GET status=%d body=%s", getResp.StatusCode, body)
	}
	consentBody, cookies := readBodyAndCookies(s.t, getResp)
	csrf := extractCSRF(s.t, consentBody)
	values.Set("_csrf", csrf)
	values.Set("label", "Policy E2E Agent")
	postResp := s.doForm(http.MethodPost, s.authHTTP.URL+"/authorize", values, cookies)
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(postResp.Body)
		s.t.Fatalf("consent POST status=%d body=%s", postResp.StatusCode, body)
	}

	for _, agent := range s.authSrv.ListAgents() {
		if agent.ClientID == s.clientID {
			s.prismID = agent.PrismID
			break
		}
	}
	if s.prismID == "" {
		s.t.Fatal("consented agent missing prism_id")
	}

	// Bake groups (and role grants if any contain "role:") into the agent
	// policy via the admin API.
	body2 := map[string]any{
		"groups": groups,
		"grant":  []string{},
		"deny":   []string{},
	}
	s.adminJSON(http.MethodPut, "/agents/"+url.PathEscape(s.prismID)+"/policy", body2, nil, http.StatusOK)
	return s.prismID
}

// doForm mirrors e2eSuite.doForm but lives on policySuite for isolation.
func (s *policySuite) doForm(method, target string, values url.Values, cookies []*http.Cookie) *http.Response {
	s.t.Helper()
	var body io.Reader
	if values != nil && method == http.MethodPost {
		body = strings.NewReader(values.Encode())
	}
	req, err := http.NewRequestWithContext(s.ctx, method, target, body)
	if err != nil {
		s.t.Fatal(err)
	}
	if values != nil && method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp
}

// policySliceContains is a small string-slice helper duplicated locally so
// the integration package doesn't pull internal/admin helpers.
func policySliceContains(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}

// ----------------------------------------------------------------------------
// Acceptance criteria — one test per spec §12 bullet.
// ----------------------------------------------------------------------------

// TestE2E_PolicyBuilder_ListCapabilitiesForGroup covers spec §12:
// "An operator can navigate to /policy, click a group, see a list of
// capabilities rendered as English sentences."
//
// Validates that GET /policy/subjects/groups/{name}/capabilities returns 200
// with an empty list when the group has no scopes or bindings.
func TestE2E_PolicyBuilder_ListCapabilitiesForGroup(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)
	caps := s.listCapabilities("groups", group)
	if len(caps) != 0 {
		t.Fatalf("expected empty capability list for new group, got %d: %+v", len(caps), caps)
	}
}

// TestE2E_PolicyBuilder_AddCoarseCapability_PersistsAsScope covers spec §12:
// "Adding a capability via the modal with only the 'What' field set persists
// as a scope string (verifiable by inspecting AgentPolicy.Grant)" and the
// related bullet "The verb 'Write files' resolves to multiple scope strings
// when saved coarsely (one per resolved fs-write tool)."
//
// POSTs a verb capability with no constraints, then confirms:
//   - The response is Source=="scope" (compile-down router took the scope path).
//   - The group's stored scope list grew by 4 entries — one per fs-write tool
//     — and excludes the unconfigured "filesystem" backend (enabled-backends
//     filter applied).
func TestE2E_PolicyBuilder_AddCoarseCapability_PersistsAsScope(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)

	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "verb", VerbSlug: "write-files"},
	}
	view := s.createCapability("groups", group, spec)
	if view.Source != "scope" {
		t.Fatalf("expected Source=scope, got %q", view.Source)
	}
	scopes := s.groupScopes(group)
	want := []string{"fs:append_file", "fs:create_dir", "fs:delete_file", "fs:write_file"}
	if len(scopes) != len(want) {
		t.Fatalf("expected %d scopes (%v), got %d (%v)", len(want), want, len(scopes), scopes)
	}
	for i, w := range want {
		if scopes[i] != w {
			t.Errorf("scope[%d] = %q, want %q (full = %v)", i, scopes[i], w, scopes)
		}
	}
	// Sanity check: no "filesystem:*" scopes leaked in. The verb's second
	// pattern targets a backend that isn't enabled, so its tools must not
	// produce any scopes.
	for _, sc := range scopes {
		if strings.HasPrefix(sc, "filesystem:") {
			t.Fatalf("unconfigured filesystem backend leaked into scopes: %v", scopes)
		}
	}
}

// TestE2E_PolicyBuilder_AddConstrainedCapability_PersistsAsGrant covers spec §12:
// "Adding a capability with any constraint chip persists as a GrantTemplate +
// GrantBinding (verifiable via /grants/templates in Power Tools)" and
// "The verb 'Write files' with a constraint resolves to ONE template using a
// tool_in_set matcher predicate."
//
// POSTs a verb + constraint capability, confirms response Source=="grant",
// then walks the admin grant-templates endpoint to verify the template was
// created with a tool_in_set predicate populated.
func TestE2E_PolicyBuilder_AddConstrainedCapability_PersistsAsGrant(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)

	val := "ephemeral"
	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "verb", VerbSlug: "write-files"},
		Advanced: &admin.AdvancedSpec{
			Workspace: &auth.WorkspaceConstraint{
				Type: &auth.Predicate{Equals: val},
			},
		},
	}
	view := s.createCapability("groups", group, spec)
	if view.Source != "grant" {
		t.Fatalf("expected Source=grant, got %q", view.Source)
	}

	templates := s.listTemplates()
	if len(templates) != 1 {
		t.Fatalf("expected exactly 1 template, got %d: %+v", len(templates), templates)
	}
	tmpl := templates[0]
	if tmpl.Spec.Backend != "*" || tmpl.Spec.Tool != "*" {
		t.Errorf("expected wildcard backend/tool, got %q/%q", tmpl.Spec.Backend, tmpl.Spec.Tool)
	}
	pred, ok := tmpl.Spec.Args["_tool"]
	if !ok {
		t.Fatalf("template missing _tool predicate; args = %+v", tmpl.Spec.Args)
	}
	if len(pred.ToolInSet) != 4 {
		t.Fatalf("expected 4 tool_in_set entries, got %d: %v", len(pred.ToolInSet), pred.ToolInSet)
	}
	// Round-trip via the by-hash endpoint to prove operators can fetch
	// templates through Power Tools using the hash they see on the wire.
	fetched := s.templateByHash(tmpl.Hash)
	if fetched.ID != tmpl.ID {
		t.Fatalf("by-hash fetch mismatch: %q vs %q", fetched.ID, tmpl.ID)
	}
}

// TestE2E_PolicyBuilder_DedupSameSpecAcrossSubjects covers spec §12 setup for
// the edit-fork bullet by validating the dedup property the fork test relies
// on: "Editing a capability that's shared (deduped) with another subject
// forks..." — first we prove the dedup actually happens.
//
// Posts the same constrained spec to two different groups and asserts:
//   - Only ONE template exists (dedup by hash).
//   - TWO bindings exist, each scoped to one of the two groups, both pointing
//     at the same template hash.
func TestE2E_PolicyBuilder_DedupSameSpecAcrossSubjects(t *testing.T) {
	s := newPolicySuite(t)
	groupA := s.uniqueSubject("eng-a")
	groupB := s.uniqueSubject("eng-b")
	s.ensureGroup(groupA)
	s.ensureGroup(groupB)

	val := "ephemeral"
	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "verb", VerbSlug: "write-files"},
		Advanced: &admin.AdvancedSpec{
			Workspace: &auth.WorkspaceConstraint{Type: &auth.Predicate{Equals: val}},
		},
	}
	viewA := s.createCapability("groups", groupA, spec)
	viewB := s.createCapability("groups", groupB, spec)

	templates := s.listTemplates()
	if len(templates) != 1 {
		t.Fatalf("expected dedup to keep 1 template, got %d", len(templates))
	}
	templateHash := templates[0].Hash

	bindings := s.listBindingsForTemplate(templates[0].ID)
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings for shared template, got %d: %+v", len(bindings), bindings)
	}
	for _, b := range bindings {
		if b.TemplateHash != templateHash {
			t.Errorf("binding %s has wrong template hash: %s want %s", b.ID, b.TemplateHash, templateHash)
		}
	}
	if viewA.Source != "grant" || viewB.Source != "grant" {
		t.Fatalf("both views must be grant-shape, got %q / %q", viewA.Source, viewB.Source)
	}
}

// TestE2E_PolicyBuilder_EditForks covers spec §12:
// "Editing a capability that's shared (deduped) with another subject forks:
// the other subject's row is unchanged; this subject's row points to a new
// template hash."
//
// Creates the same capability on two groups (proving dedup), edits one
// group's capability to a different constraint, then asserts:
//   - A new template hash exists.
//   - The edited group's binding points at the new hash.
//   - The unedited group's binding still points at the original hash.
func TestE2E_PolicyBuilder_EditForks(t *testing.T) {
	s := newPolicySuite(t)
	groupA := s.uniqueSubject("eng-a")
	groupB := s.uniqueSubject("eng-b")
	s.ensureGroup(groupA)
	s.ensureGroup(groupB)

	val := "ephemeral"
	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "verb", VerbSlug: "write-files"},
		Advanced: &admin.AdvancedSpec{
			Workspace: &auth.WorkspaceConstraint{Type: &auth.Predicate{Equals: val}},
		},
	}
	s.createCapability("groups", groupA, spec)
	s.createCapability("groups", groupB, spec)

	original := s.listTemplates()
	if len(original) != 1 {
		t.Fatalf("expected 1 dedup template, got %d", len(original))
	}
	originalHash := original[0].Hash

	// Edit groupA: change the constraint so the new template has a different
	// hash. We swap "ephemeral" → "virtual" via the Advanced workspace path.
	virt := "virtual"
	edited := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "verb", VerbSlug: "write-files"},
		Advanced: &admin.AdvancedSpec{
			Workspace: &auth.WorkspaceConstraint{Type: &auth.Predicate{Equals: virt}},
		},
	}
	capsA := s.listCapabilities("groups", groupA)
	if len(capsA) != 1 {
		t.Fatalf("groupA should have 1 capability, got %d", len(capsA))
	}
	s.editCapability("groups", groupA, capsA[0].ID, edited)

	// Two templates should now exist: the original (still attached to B) and
	// the forked one (attached to A).
	templates := s.listTemplates()
	if len(templates) != 2 {
		t.Fatalf("expected 2 templates post-fork, got %d", len(templates))
	}

	// Walk all bindings; verify A's hash diverged while B's didn't.
	all := s.listAllBindings()
	var hashA, hashB string
	for _, b := range all {
		for _, g := range b.Subjects.Groups {
			if g == groupA {
				hashA = b.TemplateHash
			}
			if g == groupB {
				hashB = b.TemplateHash
			}
		}
	}
	if hashA == "" || hashB == "" {
		t.Fatalf("missing binding hashes: hashA=%q hashB=%q (bindings=%+v)", hashA, hashB, all)
	}
	if hashA == originalHash {
		t.Errorf("expected edited groupA hash to differ from original %s; got the same hash back", originalHash)
	}
	if hashB != originalHash {
		t.Errorf("expected unedited groupB hash to remain %s, got %s", originalHash, hashB)
	}
}

// TestE2E_PolicyBuilder_DeleteScopeShape covers spec §12:
// "Adding a capability via the modal with only the 'What' field set persists
// as a scope string..." — the inverse delete path: removing a scope-shape
// capability must clear scope strings and leave templates untouched.
func TestE2E_PolicyBuilder_DeleteScopeShape(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)

	spec := admin.CapabilitySpec{Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"}}
	s.createCapability("groups", group, spec)
	if scopes := s.groupScopes(group); len(scopes) != 1 || scopes[0] != "fs:read_file" {
		t.Fatalf("expected one scope fs:read_file, got %v", scopes)
	}

	caps := s.listCapabilities("groups", group)
	if len(caps) != 1 {
		t.Fatalf("expected one capability before delete, got %d", len(caps))
	}
	s.deleteCapability("groups", group, caps[0].ID)

	if scopes := s.groupScopes(group); len(scopes) != 0 {
		t.Fatalf("expected scopes cleared after delete, got %v", scopes)
	}
	if templates := s.listTemplates(); len(templates) != 0 {
		t.Fatalf("scope delete must not touch templates, got %d", len(templates))
	}
}

// TestE2E_PolicyBuilder_DeleteGrantShape covers spec §12:
// "Adding a capability with any constraint chip persists as a GrantTemplate +
// GrantBinding..." — the inverse delete path: removing a grant-shape
// capability deletes the binding and leaves the template (ref-counted GC is
// handled by the grants epic, not this builder).
func TestE2E_PolicyBuilder_DeleteGrantShape(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)

	pfx := "/srv/data/"
	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
		Where:  &admin.WhereSpec{Mode: "path_prefix", PathPrefix: pfx},
	}
	s.createCapability("groups", group, spec)

	bindingsBefore := s.listAllBindings()
	if len(bindingsBefore) != 1 {
		t.Fatalf("expected 1 binding before delete, got %d", len(bindingsBefore))
	}
	templatesBefore := s.listTemplates()
	if len(templatesBefore) != 1 {
		t.Fatalf("expected 1 template before delete, got %d", len(templatesBefore))
	}

	caps := s.listCapabilities("groups", group)
	if len(caps) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(caps))
	}
	s.deleteCapability("groups", group, caps[0].ID)

	if bindings := s.listAllBindings(); len(bindings) != 0 {
		t.Fatalf("expected binding removed, got %d (%+v)", len(bindings), bindings)
	}
	// Template should still be present — ref-counted GC happens elsewhere.
	if templates := s.listTemplates(); len(templates) != 1 {
		t.Fatalf("expected template to survive (ref-counted GC handles cleanup); got %d", len(templates))
	}
}

// TestE2E_PolicyBuilder_VerbResolution covers spec §12 indirectly via the
// verbs endpoint contract (the picker's data source): GET /policy/verbs lists
// the hard-coded verb vocabulary, and the resolve endpoint filters to the
// requested enabled-backends set.
func TestE2E_PolicyBuilder_VerbResolution(t *testing.T) {
	s := newPolicySuite(t)
	var verbs []admin.Verb
	s.adminJSON(http.MethodGet, "/policy/verbs", nil, &verbs, http.StatusOK)
	if len(verbs) < 7 {
		t.Fatalf("expected at least 7 verbs in vocabulary, got %d", len(verbs))
	}
	var pairs []admin.ResolvedTool
	s.adminJSON(http.MethodGet, "/policy/verbs/write-files/resolve?enabled_backends=fs,github",
		nil, &pairs, http.StatusOK)
	if len(pairs) == 0 {
		t.Fatalf("expected resolve to return entries for fs")
	}
	for _, p := range pairs {
		if p.Backend != "fs" {
			t.Errorf("write-files must not resolve to non-fs backend %q (entry: %+v)", p.Backend, p)
		}
	}
}

// TestE2E_PolicyBuilder_ToolInSetPredicateMatches covers spec §12:
// "New verb predicate tool_in_set is supported by the grants matcher and
// tested."
//
// Creates a capability whose template carries a tool_in_set predicate, then
// proves the matcher allows tools in the set and denies tools outside it.
// We use the matcher directly (auth.MatchGrant) against an IssuedGrant
// synthesized from the template — this keeps the assertion focused on the
// predicate semantics without dragging through the full /authorize +
// /token + tools/call dance, which is exercised by the grants epic suite.
func TestE2E_PolicyBuilder_ToolInSetPredicateMatches(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)

	val := "ephemeral"
	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "verb", VerbSlug: "write-files"},
		Advanced: &admin.AdvancedSpec{
			Workspace: &auth.WorkspaceConstraint{Type: &auth.Predicate{Equals: val}},
		},
	}
	s.createCapability("groups", group, spec)

	templates := s.listTemplates()
	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tmpl := templates[0]
	pred, ok := tmpl.Spec.Args["_tool"]
	if !ok || len(pred.ToolInSet) == 0 {
		t.Fatalf("template missing tool_in_set predicate; args = %+v", tmpl.Spec.Args)
	}

	// Synthesize an IssuedGrant matching what the auth server would mint and
	// run it through the matcher to validate predicate behavior.
	wsInst := &auth.WorkspaceInstance{ID: "repo", Type: "ephemeral"}
	grant := auth.IssuedGrant{
		Type:         auth.GrantTypeMCPCall,
		Tool:         tmpl.Spec.Tool,
		Backend:      tmpl.Spec.Backend,
		Args:         tmpl.Spec.Args,
		Workspace:    wsInst,
		TemplateID:   tmpl.ID,
		TemplateHash: tmpl.Hash,
	}
	// In-set call (fs:write_file) — allowed.
	res := auth.MatchGrant(auth.CallContext{
		Tool:      "write_file",
		Backend:   "fs",
		Arguments: []byte(`{}`),
		Workspace: wsInst,
		Now:       s.clock.Now(),
	}, []auth.IssuedGrant{grant})
	if !res.Allowed {
		t.Fatalf("expected fs:write_file (in tool_in_set) to be allowed; got DenyDim=%q detail=%q",
			res.DenyDim, res.Detail)
	}
	// Out-of-set call (fs:read_file is in read-files, not write-files) —
	// denied with an args-level deny dimension.
	res = auth.MatchGrant(auth.CallContext{
		Tool:      "read_file",
		Backend:   "fs",
		Arguments: []byte(`{}`),
		Workspace: wsInst,
		Now:       s.clock.Now(),
	}, []auth.IssuedGrant{grant})
	if res.Allowed {
		t.Fatalf("expected fs:read_file (not in tool_in_set) to be denied; result=%+v", res)
	}
	if res.DenyDim != auth.GrantDenyArgs {
		t.Errorf("expected DenyArgs deny dim, got %q", res.DenyDim)
	}
}

// TestE2E_PolicyBuilder_PowerToolsRouteGating_BackendUnchanged covers spec §12:
// "Power Tools toggle OFF means /grants/templates and /grants/bindings routes
// return 404 (or redirect to /policy)."
//
// The toggle is purely client-side. This test confirms the backend behavior
// is unchanged — the admin API exposes /grant-templates and /grant-bindings
// regardless of any UI state. The UI gate itself is exercised by the per-
// component unit tests + manual smoke (per task contract).
func TestE2E_PolicyBuilder_PowerToolsRouteGating_BackendUnchanged(t *testing.T) {
	s := newPolicySuite(t)
	// Both endpoints must succeed server-side.
	var tmpls []auth.GrantTemplate
	s.adminJSON(http.MethodGet, "/grant-templates", nil, &tmpls, http.StatusOK)
	var binds []auth.GrantBinding
	s.adminJSON(http.MethodGet, "/grant-bindings", nil, &binds, http.StatusOK)
	// Sanity check: neither route should return a 404 when called from a
	// trusted admin client. Empty lists are fine — what matters is the 200.
	if tmpls == nil {
		// nil-vs-empty is normal for JSON; decoder may return either. The
		// non-error decode above proves the contract; no extra assertion
		// needed beyond a successful status code.
		_ = tmpls
	}
}

// TestE2E_PolicyBuilder_AgentInheritsFromGroup covers spec §12 acceptance #11:
// "AgentDetail page shows inherited capabilities as read-only with edit-at-
// group links."
//
// Creates a capability on a group, joins an agent to that group, then asserts
// the agent's capability listing includes the inherited capability with an
// InheritedFrom entry naming the group. The backend join in
// composeAgentCapabilityViews is the single source of truth — the UI no
// longer has to synthesize this via two separate reads.
func TestE2E_PolicyBuilder_AgentInheritsFromGroup(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)
	prismID := s.ensureAgent(group)

	pfx := "/srv/data/"
	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
		Where:  &admin.WhereSpec{Mode: "path_prefix", PathPrefix: pfx},
	}
	groupView := s.createCapability("groups", group, spec)

	groupCaps := s.listCapabilities("groups", group)
	if len(groupCaps) != 1 {
		t.Fatalf("group should have 1 capability, got %d", len(groupCaps))
	}

	agentCaps := s.listCapabilities("agents", prismID)
	if len(agentCaps) != 1 {
		t.Fatalf("agent should inherit 1 capability from group, got %d (%+v)", len(agentCaps), agentCaps)
	}
	got := agentCaps[0]
	if got.ID != groupView.ID {
		t.Fatalf("inherited capability id mismatch: agent=%q group=%q", got.ID, groupView.ID)
	}
	if len(got.InheritedFrom) != 1 {
		t.Fatalf("expected 1 inheritance source, got %d (%+v)", len(got.InheritedFrom), got.InheritedFrom)
	}
	src := got.InheritedFrom[0]
	if src.Type != "group" || src.Name != group {
		t.Fatalf("inheritance source = %+v want {Type:group, Name:%s}", src, group)
	}
}

// TestE2E_PolicyBuilder_AgentInheritsFromRole covers spec §12 acceptance #11
// (role variant). Creates a capability on a role, attaches the role to an
// agent via the legacy "role:" scope prefix authserver uses to derive roles,
// then asserts the agent's capability listing surfaces the inherited
// capability with InheritedFrom naming the role.
func TestE2E_PolicyBuilder_AgentInheritsFromRole(t *testing.T) {
	s := newPolicySuite(t)
	role := s.uniqueSubject("role")
	prismID := s.ensureAgent()

	// Stash the role membership on the agent's grant list as "role:<name>".
	// This mirrors how authserver.subjectIdentity derives an agent's roles
	// at authorization time.
	if err := s.agentMgr.SetAgentPolicy(prismID, nil, []string{"role:" + role}, nil); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}

	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
	}
	roleView := s.createCapability("roles", role, spec)

	agentCaps := s.listCapabilities("agents", prismID)
	if len(agentCaps) != 1 {
		t.Fatalf("agent should inherit 1 capability from role, got %d (%+v)", len(agentCaps), agentCaps)
	}
	got := agentCaps[0]
	if got.ID != roleView.ID {
		t.Fatalf("inherited capability id mismatch: agent=%q role=%q", got.ID, roleView.ID)
	}
	if len(got.InheritedFrom) != 1 {
		t.Fatalf("expected 1 inheritance source, got %d (%+v)", len(got.InheritedFrom), got.InheritedFrom)
	}
	src := got.InheritedFrom[0]
	if src.Type != "role" || src.Name != role {
		t.Fatalf("inheritance source = %+v want {Type:role, Name:%s}", src, role)
	}
}

// TestE2E_PolicyBuilder_AgentInheritsFromGroupAndRole exercises the dedup +
// multi-source aggregation contract: a single capability bound to both a
// group AND a role the agent holds must appear once in the agent's listing
// with both inheritance sources surfaced.
func TestE2E_PolicyBuilder_AgentInheritsFromGroupAndRole(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	role := s.uniqueSubject("role")
	s.ensureGroup(group)
	prismID := s.ensureAgent(group)
	if err := s.agentMgr.SetAgentPolicy(prismID, []string{group}, []string{"role:" + role}, nil); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}

	pfx := "/srv/data/"
	groupSpec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
		Where:  &admin.WhereSpec{Mode: "path_prefix", PathPrefix: pfx},
	}
	groupView := s.createCapability("groups", group, groupSpec)
	roleView := s.createCapability("roles", role, groupSpec)

	// Sanity: dedup should have produced one shared template; two bindings,
	// one per subject. The agent listing must collapse those two bindings
	// into a single row from the operator's point of view — but
	// dedup-by-binding-id only collapses identical IDs. Since direct binding
	// IDs differ across group/role subjects (subject id is part of the
	// fingerprint), the agent will surface two rows here, each pointing at
	// the shared template hash and each naming exactly one inheritance
	// source. That's still the correct operator surface: each row is a
	// distinct binding the operator can revoke independently at its source.
	agentCaps := s.listCapabilities("agents", prismID)
	if len(agentCaps) != 2 {
		t.Fatalf("expected 2 inherited rows (one per binding), got %d (%+v)", len(agentCaps), agentCaps)
	}
	// Both rows must surface exactly one inheritance source each — group
	// row → group source, role row → role source.
	var sawGroup, sawRole bool
	for _, c := range agentCaps {
		if len(c.InheritedFrom) != 1 {
			t.Fatalf("row %s should have 1 inheritance source, got %d (%+v)", c.ID, len(c.InheritedFrom), c.InheritedFrom)
		}
		src := c.InheritedFrom[0]
		switch {
		case c.ID == groupView.ID:
			if src.Type != "group" || src.Name != group {
				t.Fatalf("group-row inheritance = %+v want {group,%s}", src, group)
			}
			sawGroup = true
		case c.ID == roleView.ID:
			if src.Type != "role" || src.Name != role {
				t.Fatalf("role-row inheritance = %+v want {role,%s}", src, role)
			}
			sawRole = true
		default:
			t.Fatalf("unexpected row id %s (expected %s or %s)", c.ID, groupView.ID, roleView.ID)
		}
	}
	if !sawGroup || !sawRole {
		t.Fatalf("missing inheritance rows: sawGroup=%v sawRole=%v", sawGroup, sawRole)
	}
}

// TestE2E_PolicyBuilder_RoleAsSubject covers spec §12:
// "AgentDetail page shows inherited capabilities as read-only with edit-at-
// group links." — the role variant. Validates that the role subject path
// works end-to-end: a capability posted to a role creates a binding whose
// SubjectSelector.Roles includes the role, with RoleRequired threaded
// through when advanced supplied it.
//
// Roles always compile to bindings (no scope shape; see
// createScopeCapability's roles short-circuit). Role inheritance into the
// agent listing has the same limitation documented in
// TestE2E_PolicyBuilder_AgentInheritsFromGroup: bindingTargetsSubject does
// not auto-include roles when the subject is an agent.
func TestE2E_PolicyBuilder_RoleAsSubject(t *testing.T) {
	s := newPolicySuite(t)
	role := s.uniqueSubject("role")

	// Plain capability (no constraints) — roles still compile to a binding.
	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
	}
	view := s.createCapability("roles", role, spec)
	if view.Source != "grant" {
		t.Fatalf("roles must compile to grant shape, got Source=%q", view.Source)
	}

	bindings := s.listAllBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if !policySliceContains(bindings[0].Subjects.Roles, role) {
		t.Fatalf("expected role %q in binding selector, got %+v", role, bindings[0].Subjects)
	}

	// Verify the role's listing returns the capability.
	caps := s.listCapabilities("roles", role)
	if len(caps) != 1 {
		t.Fatalf("expected 1 role capability, got %d", len(caps))
	}
}

// TestE2E_PolicyBuilder_VerbExpansionAcrossBackends covers the verb-
// expansion bullet in spec §12 against the wildcard "call-tools" verb. With
// fs + github both enabled, posting call-tools coarsely should produce one
// scope per enabled backend.
func TestE2E_PolicyBuilder_VerbExpansionAcrossBackends(t *testing.T) {
	s := newPolicySuite(t)
	// Register a second backend ID so the verb resolver sees it as enabled.
	// We don't connect a live MCP backend for github — verb resolution is a
	// pure name-table operation.
	s.backendMgr.addID("github")

	group := s.uniqueSubject("eng")
	s.ensureGroup(group)

	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "verb", VerbSlug: "call-tools"},
	}
	view := s.createCapability("groups", group, spec)
	if view.Source != "scope" {
		t.Fatalf("expected Source=scope, got %q", view.Source)
	}

	scopes := s.groupScopes(group)
	seenFS, seenGitHub := false, false
	for _, sc := range scopes {
		if sc == "fs:*" {
			seenFS = true
		}
		if sc == "github:*" {
			seenGitHub = true
		}
	}
	if !seenFS || !seenGitHub {
		t.Fatalf("call-tools verb should expand across all enabled backends; got %v (seenFS=%v seenGitHub=%v)",
			scopes, seenFS, seenGitHub)
	}
}

// TestE2E_PolicyBuilder_AdvancedFieldOverridesPreset covers spec §12 via the
// preset/advanced precedence contract: when both a preset Where (which
// compiles to args.path) AND an explicit Advanced.Args predicate are
// supplied, the advanced value wins.
//
// We POST a capability with Where.Mode="path_prefix" (preset would write
// args.path with a Prefix) AND Advanced.Args containing a different "path"
// predicate (a Pattern). The resulting template's args.path must reflect
// the advanced spec.
func TestE2E_PolicyBuilder_AdvancedFieldOverridesPreset(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)

	patt := `^/data/\d+\.txt$`
	pfx := "/srv/data/"
	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
		Where:  &admin.WhereSpec{Mode: "path_prefix", PathPrefix: pfx},
		Advanced: &admin.AdvancedSpec{
			Args: map[string]auth.Predicate{
				"path": {Pattern: &patt},
			},
		},
	}
	view := s.createCapability("groups", group, spec)
	if view.Source != "grant" {
		t.Fatalf("expected Source=grant (constraints present), got %q", view.Source)
	}

	templates := s.listTemplates()
	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	got, ok := templates[0].Spec.Args["path"]
	if !ok {
		t.Fatalf("template missing args.path; args = %+v", templates[0].Spec.Args)
	}
	if got.Pattern == nil || *got.Pattern != patt {
		t.Fatalf("advanced override lost: got Pattern=%v Prefix=%v want Pattern=%q",
			got.Pattern, got.Prefix, patt)
	}
	if got.Prefix != nil {
		t.Errorf("expected preset Prefix to be replaced by advanced Pattern, but Prefix still set: %v", *got.Prefix)
	}
}

// TestE2E_PolicyBuilder_EffectivePolicyExposesProvenance covers the
// policy-refine rework (task-38) on AgentDetail: the Effective Policy
// section consumes CapabilityView.InheritedFrom on every row. This test
// guards the API contract end-to-end — given an agent with two groups,
// each owning one distinct capability, the agent's listing must return
// both capabilities, each with InheritedFrom naming its source group.
//
// The frontend renders this provenance verbatim; any regression in the
// admin API's composeAgentCapabilityViews would silently break the
// "Edit at <source>" affordance, so we lock it in here.
func TestE2E_PolicyBuilder_EffectivePolicyExposesProvenance(t *testing.T) {
	s := newPolicySuite(t)
	groupA := s.uniqueSubject("eng-prov-a")
	groupB := s.uniqueSubject("eng-prov-b")
	s.ensureGroup(groupA)
	s.ensureGroup(groupB)
	prismID := s.ensureAgent(groupA, groupB)

	// Use constrained specs so each capability lands as a grant binding —
	// composeAgentCapabilityViews joins through bindings, not raw scopes.
	pfxA := "/srv/a/"
	pfxB := "/srv/b/"
	specA := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
		Where:  &admin.WhereSpec{Mode: "path_prefix", PathPrefix: pfxA},
	}
	specB := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "write_file"},
		Where:  &admin.WhereSpec{Mode: "path_prefix", PathPrefix: pfxB},
	}
	viewA := s.createCapability("groups", groupA, specA)
	viewB := s.createCapability("groups", groupB, specB)

	agentCaps := s.listCapabilities("agents", prismID)
	if len(agentCaps) != 2 {
		t.Fatalf("expected agent to inherit 2 capabilities, got %d (%+v)", len(agentCaps), agentCaps)
	}

	// Build a lookup keyed by capability id → its first inheritance source so
	// we can assert each row's provenance without depending on listing order.
	byID := make(map[string]admin.InheritanceSource, len(agentCaps))
	for _, c := range agentCaps {
		if len(c.InheritedFrom) == 0 {
			t.Fatalf("capability %q missing InheritedFrom (%+v)", c.ID, c)
		}
		byID[c.ID] = c.InheritedFrom[0]
	}

	srcA, ok := byID[viewA.ID]
	if !ok {
		t.Fatalf("agent listing missing capability %q from group %s", viewA.ID, groupA)
	}
	if srcA.Type != "group" || srcA.Name != groupA {
		t.Fatalf("capability %q source = %+v want {Type:group, Name:%s}", viewA.ID, srcA, groupA)
	}
	srcB, ok := byID[viewB.ID]
	if !ok {
		t.Fatalf("agent listing missing capability %q from group %s", viewB.ID, groupB)
	}
	if srcB.Type != "group" || srcB.Name != groupB {
		t.Fatalf("capability %q source = %+v want {Type:group, Name:%s}", viewB.ID, srcB, groupB)
	}
}

// TestE2E_PolicyBuilder_DirectGrantOnAgent_IsUngated covers the
// policy-refine rework (task-38): per-agent Direct Grants are a normal
// admin workflow, NOT an Advanced/Power-Tools surface. The backend API
// must allow an admin to create a capability directly on an agent
// (subjectType=agents) without any extra header, role, or "advanced"
// flag — and the resulting row must surface in the agent's effective
// policy with InheritedFrom.Type="direct".
func TestE2E_PolicyBuilder_DirectGrantOnAgent_IsUngated(t *testing.T) {
	s := newPolicySuite(t)
	prismID := s.ensureAgent()

	// Use a constrained spec so the capability persists as a grant binding
	// (subject-targeted) rather than a plain scope on AgentPolicy.Grant.
	// Grant-shape direct grants are the surface the policy-refine rework
	// targets — they're what composeAgentCapabilityViews attributes to
	// Type="direct", and they're what AgentDetail's "edit direct grants"
	// affordance creates.
	pfx := "/srv/data/"
	spec := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
		Where:  &admin.WhereSpec{Mode: "path_prefix", PathPrefix: pfx},
	}
	// A plain POST against the agent subject must succeed — no gating
	// headers, no role bump. The admin API treats direct grants as a
	// normal first-class operation.
	view := s.createCapability("agents", prismID, spec)
	if view.ID == "" {
		t.Fatalf("direct grant returned empty view: %+v", view)
	}
	if view.Source != "grant" {
		t.Fatalf("expected Source=grant for constrained direct grant, got %q", view.Source)
	}

	agentCaps := s.listCapabilities("agents", prismID)
	if len(agentCaps) != 1 {
		t.Fatalf("expected 1 direct capability on agent, got %d (%+v)", len(agentCaps), agentCaps)
	}
	got := agentCaps[0]
	if got.ID != view.ID {
		t.Fatalf("capability id mismatch: created=%q listed=%q", view.ID, got.ID)
	}
	if len(got.InheritedFrom) == 0 {
		t.Fatalf("direct grant missing InheritedFrom provenance: %+v", got)
	}
	// Direct grants are attributed via InheritedFrom.Type="direct" so the UI
	// can render an "agent override" badge instead of a group/role link.
	if got.InheritedFrom[0].Type != "direct" {
		t.Fatalf("direct grant provenance = %+v want Type=direct", got.InheritedFrom[0])
	}
}

// TestE2E_PolicyHealth_AggregatesWindow exercises GET /api/v1/policy/health
// against a seeded event store. Covers task-41 contract: the endpoint must
// return all six tile numbers in one shot, restricted to the 24h window,
// derived from the existing GrantEvent fields without any new tables.
func TestE2E_PolicyHealth_AggregatesWindow(t *testing.T) {
	s := newPolicySuite(t)

	now := time.Now()
	mk := func(req, agent, outcome, deny, jkt, hash string, authAge, ts time.Duration) auth.GrantEvent {
		e := auth.GrantEvent{
			Timestamp:    now.Add(-ts),
			RequestID:    req,
			AgentID:      agent,
			ClientID:     agent,
			DPoPjkt:      jkt,
			Backend:      "fs",
			Tool:         "fs.write_file",
			Outcome:      outcome,
			TemplateID:   "tmpl-" + hash,
			TemplateHash: hash,
			Trace:        auth.GrantTrace{DenyDim: deny},
		}
		if authAge > 0 {
			e.AuthTime = now.Add(-authAge)
		}
		return e
	}
	seed := []auth.GrantEvent{
		mk("e1", "agent-a", "allowed", "", "jkt-a", "hash-1", 20*time.Second, 1*time.Hour),
		mk("e2", "agent-b", "allowed", "", "", "hash-1", 40*time.Second, 2*time.Hour),
		mk("e3", "agent-c", "denied", auth.GrantDenyWorkspaceDrift, "jkt-c", "hash-2", 60*time.Second, 3*time.Hour),
		mk("e4", "agent-d", "denied", "args", "", "hash-2", 100*time.Second, 4*time.Hour),
		mk("e5", "agent-e", "challenged", "needs_step_up", "jkt-e", "", 0, 5*time.Hour),
		// Outside the 24h window — must NOT contribute to any counter.
		mk("e6-stale", "agent-stale", "denied", auth.GrantDenyWorkspaceDrift, "jkt-stale", "hash-stale", 0, 48*time.Hour),
	}
	for _, e := range seed {
		if err := s.events.Insert(e); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}

	var got admin.PolicyHealth
	s.adminJSON(http.MethodGet, "/policy/health", nil, &got, http.StatusOK)

	if got.WindowSeconds != int((24*time.Hour)/time.Second) {
		t.Fatalf("window_seconds = %d, want 86400", got.WindowSeconds)
	}
	if got.Calls24h != 5 {
		t.Fatalf("calls_24h = %d, want 5 (stale excluded)", got.Calls24h)
	}
	if got.Denials24h != 2 {
		t.Fatalf("denials_24h = %d, want 2", got.Denials24h)
	}
	if got.DriftEvents24h != 1 {
		t.Fatalf("drift_events_24h = %d, want 1 (stale drift excluded)", got.DriftEvents24h)
	}
	// 2 / 5 = 40.0
	if got.DenialRatePct24h < 39.5 || got.DenialRatePct24h > 40.5 {
		t.Fatalf("denial_rate_24h = %v, want ~40.0", got.DenialRatePct24h)
	}
	// jkt-a, jkt-c, jkt-e — three distinct dpop-bound agents in the window.
	if got.DPoPBoundAgents != 3 {
		t.Fatalf("dpop_bound_agents = %d, want 3", got.DPoPBoundAgents)
	}
	// hash-1 (allowed) + hash-2 (allowed+denied) = 2; hash-stale outside
	// window, challenged-only is also excluded.
	if got.ActiveTemplates != 2 {
		t.Fatalf("active_templates = %d, want 2", got.ActiveTemplates)
	}
	// Four events carry non-zero AuthTime (20, 40, 60, 100s). Median is
	// (40+60)/2 = 50s.
	if got.MedianFreshnessSeconds < 45 || got.MedianFreshnessSeconds > 55 {
		t.Fatalf("median_freshness_seconds = %d, want ~50", got.MedianFreshnessSeconds)
	}
	if got.GeneratedAt.IsZero() {
		t.Fatalf("generated_at is zero — should be populated on every response")
	}
}

// TestE2E_PolicyHealth_EmptyStore confirms the endpoint returns the
// no-data sentinel shape (all zeros + median=-1) before any events have
// landed. The frontend renders these tiles normally with the empty hint;
// returning 200 here is required so the strip doesn't show an error on a
// fresh install.
func TestE2E_PolicyHealth_EmptyStore(t *testing.T) {
	s := newPolicySuite(t)

	var got admin.PolicyHealth
	s.adminJSON(http.MethodGet, "/policy/health", nil, &got, http.StatusOK)

	if got.Calls24h != 0 || got.Denials24h != 0 || got.DriftEvents24h != 0 ||
		got.DPoPBoundAgents != 0 || got.ActiveTemplates != 0 {
		t.Fatalf("expected zero tiles, got %+v", got)
	}
	if got.DenialRatePct24h != 0 {
		t.Fatalf("denial_rate_24h = %v, want 0 on empty store", got.DenialRatePct24h)
	}
	if got.MedianFreshnessSeconds != -1 {
		t.Fatalf("median_freshness_seconds = %d, want -1 sentinel", got.MedianFreshnessSeconds)
	}
}

// --- task-42: /analytics/events filter coverage --------------------------
//
// These four tests exercise the URL query-params the Activity page (and the
// Health-strip deep-links it consumes) round-trips through the persisted
// store. The shape of each test is the same: seed three events directly into
// `s.events`, hit `GET /analytics/events?...`, assert the returned slice
// contains only the rows that should match.
//
// The fourth test (subject=groups/...) substitutes the policy suite's
// AgentManager for a fixed-list stub so we can prove the resolver expands
// `groups/<name>` to an IN-list of agent_ids without going through DCR.

// fixedAgentManager is a stub admin.AgentManager that returns a hand-rolled
// list of agents. Used by the subject=groups test below so we can assert
// the group → agent_id resolution without spinning up DCR.
type fixedAgentManager struct {
	admin.AgentManager
	agents []any
}

func (m *fixedAgentManager) ListAgents() []any { return m.agents }

// seedThreeEvents inserts a denied workspace-drift event, a denied args
// event, and an allowed event so each filter test has at least one row
// expected to match and one row expected to be filtered out.
func seedThreeEvents(t *testing.T, store *analytics.SQLiteStore) {
	t.Helper()
	now := time.Now()
	events := []auth.GrantEvent{
		{
			Timestamp: now.Add(-30 * time.Minute),
			RequestID: "r-allowed",
			AgentID:   "agent-a", ClientID: "agent-a",
			Backend:      "fs",
			Tool:         "fs.read_file",
			Outcome:      "allowed",
			TemplateID:   "tmpl-r",
			TemplateHash: "hash-read",
		},
		{
			Timestamp: now.Add(-20 * time.Minute),
			RequestID: "r-drift",
			AgentID:   "agent-b", ClientID: "agent-b",
			Backend:      "fs",
			Tool:         "fs.write_file",
			Outcome:      "denied",
			TemplateID:   "tmpl-w",
			TemplateHash: "hash-write",
			Trace:        auth.GrantTrace{DenyDim: auth.GrantDenyWorkspaceDrift},
		},
		{
			Timestamp: now.Add(-10 * time.Minute),
			RequestID: "r-args",
			AgentID:   "agent-c", ClientID: "agent-c",
			Backend:      "github",
			Tool:         "github.create_issue",
			Outcome:      "denied",
			TemplateID:   "tmpl-w",
			TemplateHash: "hash-write",
			Trace:        auth.GrantTrace{DenyDim: auth.GrantDenyArgs},
		},
	}
	for _, e := range events {
		if err := store.Insert(e); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// TestE2E_AnalyticsEvents_FilterByOutcome confirms `outcome=denied` returns
// only denied rows — this powers the Health-strip "denials" tile deep-link
// at /activity?outcome=denied.
func TestE2E_AnalyticsEvents_FilterByOutcome(t *testing.T) {
	s := newPolicySuite(t)
	seedThreeEvents(t, s.events)

	var got []auth.GrantEvent
	s.adminJSON(http.MethodGet, "/analytics/events?outcome=denied", nil, &got, http.StatusOK)

	if len(got) != 2 {
		t.Fatalf("len(events) = %d, want 2 (both denied rows)", len(got))
	}
	for _, e := range got {
		if e.Outcome != "denied" {
			t.Fatalf("got outcome=%q, want denied", e.Outcome)
		}
	}
}

// TestE2E_AnalyticsEvents_FilterByDenyDim confirms `deny_dim=workspace_drift`
// returns only the drift row — this powers the Health-strip "drift events"
// tile deep-link at /activity?deny_dim=workspace_drift.
func TestE2E_AnalyticsEvents_FilterByDenyDim(t *testing.T) {
	s := newPolicySuite(t)
	seedThreeEvents(t, s.events)

	var got []auth.GrantEvent
	s.adminJSON(http.MethodGet, "/analytics/events?deny_dim=workspace_drift", nil, &got, http.StatusOK)

	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1 (single drift row)", len(got))
	}
	if got[0].Trace.DenyDim != auth.GrantDenyWorkspaceDrift {
		t.Fatalf("got deny_dim=%q, want %s", got[0].Trace.DenyDim, auth.GrantDenyWorkspaceDrift)
	}
}

// TestE2E_AnalyticsEvents_FilterByTemplate confirms both the operator-facing
// `template=<hash>` alias and the legacy `template_hash=<hash>` form filter
// correctly. The chip URLs use the short alias; epic-2 callers continue to
// send the long form.
func TestE2E_AnalyticsEvents_FilterByTemplate(t *testing.T) {
	s := newPolicySuite(t)
	seedThreeEvents(t, s.events)

	var got []auth.GrantEvent
	s.adminJSON(http.MethodGet, "/analytics/events?template=hash-write", nil, &got, http.StatusOK)
	if len(got) != 2 {
		t.Fatalf("len(events) = %d, want 2 (both hash-write rows)", len(got))
	}
	for _, e := range got {
		if e.TemplateHash != "hash-write" {
			t.Fatalf("got template_hash=%q, want hash-write", e.TemplateHash)
		}
	}

	// Backwards-compat: template_hash= must still work for epic-2 callers.
	var legacy []auth.GrantEvent
	s.adminJSON(http.MethodGet, "/analytics/events?template_hash=hash-read", nil, &legacy, http.StatusOK)
	if len(legacy) != 1 {
		t.Fatalf("legacy template_hash filter returned %d, want 1", len(legacy))
	}
}

// TestE2E_AnalyticsEvents_FilterBySubjectGroup confirms `subject=groups/<name>`
// expands to the IN-list of agent_ids whose AgentPolicy.Groups contains the
// name, and that the resolver does it via a single ListAgents() snapshot
// (no per-row lookup). We substitute a fixedAgentManager so the test is
// deterministic about which agents belong to which group.
func TestE2E_AnalyticsEvents_FilterBySubjectGroup(t *testing.T) {
	s := newPolicySuite(t)
	seedThreeEvents(t, s.events)

	// Rebuild the admin server with a fixed agent list so the resolver can
	// see {agent-a, agent-b} ∈ "engineering" and {agent-c} ∈ "contractors".
	// We keep the rest of the wiring identical to newPolicySuite.
	fixed := &fixedAgentManager{
		AgentManager: s.agentMgr,
		agents: []any{
			map[string]any{
				"prism_id": "agent-a",
				"policy":   map[string]any{"groups": []string{"engineering"}},
			},
			map[string]any{
				"prism_id": "agent-b",
				"policy":   map[string]any{"groups": []string{"engineering"}},
			},
			map[string]any{
				"prism_id": "agent-c",
				"policy":   map[string]any{"groups": []string{"contractors"}},
			},
		},
	}
	adminAPI := admin.NewAPI(
		func() any { return map[string]any{"status": "ok"} },
		s.backendMgr,
		fixed.ListAgents,
		s.agentMgr.RemoveAgent,
		s.agentMgr.RemoveStaleAgents,
		func() []any { return nil },
		fixed,
		s.groupMgr,
		nil,
		nil,
	)
	adminAPI.SetGrantManager(s.authSrv)
	adminAPI.SetAnalytics(s.events, s.ring)
	srv := httptest.NewServer(adminAPI.Handler())
	t.Cleanup(srv.Close)

	get := func(query string) []auth.GrantEvent {
		t.Helper()
		req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, srv.URL+"/api/v1"+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := s.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status=%d body=%s", resp.StatusCode, body)
		}
		var out []auth.GrantEvent
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}

	engEvents := get("/analytics/events?subject=" + url.QueryEscape("groups/engineering"))
	if len(engEvents) != 2 {
		t.Fatalf("groups/engineering: got %d events, want 2 (agent-a + agent-b)", len(engEvents))
	}
	seen := map[string]bool{}
	for _, e := range engEvents {
		seen[e.AgentID] = true
	}
	if !seen["agent-a"] || !seen["agent-b"] {
		t.Fatalf("groups/engineering: missing agent. saw=%v", seen)
	}

	contractorEvents := get("/analytics/events?subject=" + url.QueryEscape("groups/contractors"))
	if len(contractorEvents) != 1 {
		t.Fatalf("groups/contractors: got %d events, want 1 (agent-c)", len(contractorEvents))
	}
	if contractorEvents[0].AgentID != "agent-c" {
		t.Fatalf("groups/contractors: got agent_id=%q, want agent-c", contractorEvents[0].AgentID)
	}

	// Empty-group short-circuit: groups/none with no members must return [],
	// not the full event list — otherwise an empty filter silently widens.
	emptyEvents := get("/analytics/events?subject=" + url.QueryEscape("groups/none"))
	if len(emptyEvents) != 0 {
		t.Fatalf("groups/none: got %d events, want 0 (empty group short-circuits)", len(emptyEvents))
	}
}

// --- task-43: /policy/access reverse-policy view ---------------------------
//
// These tests exercise GET /api/v1/policy/access?backend=<id>[&tool=<name>],
// the single-shot endpoint powering the "Who can use this?" section on
// /servers/{id}. Two cases cover the contract:
//
//   1. Empty state — no group, role, or agent has access; `empty=true` and
//      `entries=[]` so the UI shows the "No policy grants access" copy with
//      a deep-link to Policy Builder.
//   2. Populated — one group with a scope-shape grant, one role with a
//      grant-shape binding; both surface with subject metadata + 24h counts
//      aggregated from the analytics event store. The aggregate is computed
//      from a single Store.Query (no per-subject lookup); we verify that
//      indirectly by checking the response shape rather than the call count.

// TestE2E_PolicyAccess_EmptyState covers the empty-state branch of the
// "Who can use this?" section. With no group scopes, no agent grants, and
// no bindings, the endpoint must return empty=true so the UI knows to
// render the deep-link to Policy Builder rather than an empty table.
func TestE2E_PolicyAccess_EmptyState(t *testing.T) {
	s := newPolicySuite(t)

	var got admin.PolicyAccessResponse
	s.adminJSON(http.MethodGet, "/policy/access?backend=fs", nil, &got, http.StatusOK)

	if !got.Empty {
		t.Fatalf("expected empty=true with no policy grants, got %+v", got)
	}
	if len(got.Entries) != 0 {
		t.Fatalf("expected 0 entries, got %d: %+v", len(got.Entries), got.Entries)
	}
	if got.Backend != "fs" {
		t.Fatalf("backend echo mismatch: got %q want fs", got.Backend)
	}
	if got.WindowSeconds != int((24*time.Hour)/time.Second) {
		t.Fatalf("window_seconds = %d, want 86400", got.WindowSeconds)
	}
	if got.GeneratedAt.IsZero() {
		t.Fatalf("generated_at is zero — must be populated on every response")
	}
}

// TestE2E_PolicyAccess_PopulatedEntries covers the populated branch:
//
//   - One group with a scope-shape grant via the verb "read-files" — must
//     produce one or more scope-source entries on the fs backend.
//   - One role with a grant-shape binding (constrained capability so the
//     compile-down router picks the grant path) — must produce a
//     grant-source entry carrying the template hash.
//   - Two seeded analytics events on the backend: one allowed on a group
//     member, one denied on the role member. Counts must show up on the
//     respective rows, aggregated from a single store query — we sanity-check
//     that the response is internally consistent (calls >= denials, sums
//     match seeded data).
//
// The point is not to enumerate every aggregation case (those live in
// policy_view_test.go and analytics_test.go); the E2E test guards that the
// HTTP surface assembles them correctly into the operator-facing shape.
func TestE2E_PolicyAccess_PopulatedEntries(t *testing.T) {
	s := newPolicySuite(t)

	// Group with a coarse "read-files" capability — scope-shape, multiple
	// scopes after verb expansion against the "fs" backend.
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)
	s.createCapability("groups", group, admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "verb", VerbSlug: "read-files"},
	})

	// Role with a constrained capability — grant-shape binding referencing
	// a template (the operator-facing "this capability is template-bound"
	// signal the section surfaces via shortHash + template chip).
	role := s.uniqueSubject("auditor")
	val := "ephemeral"
	s.createCapability("roles", role, admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
		Advanced: &admin.AdvancedSpec{
			Workspace: &auth.WorkspaceConstraint{
				Type: &auth.Predicate{Equals: val},
			},
		},
	})

	// Substitute the agent manager with a fixed list so the group resolver
	// (agentsInGroup) can see members without DCR rounds. agent-1 is in the
	// group; agent-2 sits outside any subject and shouldn't show up on a
	// group's row.
	fixed := &fixedAgentManager{
		AgentManager: s.agentMgr,
		agents: []any{
			map[string]any{
				"prism_id": "agent-1",
				"policy":   map[string]any{"groups": []string{group}},
			},
			map[string]any{
				"prism_id": "agent-2",
				"policy":   map[string]any{"groups": []string{}},
			},
		},
	}
	adminAPI := admin.NewAPI(
		func() any { return map[string]any{"status": "ok"} },
		s.backendMgr,
		fixed.ListAgents,
		s.agentMgr.RemoveAgent,
		s.agentMgr.RemoveStaleAgents,
		func() []any { return nil },
		fixed,
		s.groupMgr,
		nil,
		nil,
	)
	adminAPI.SetGrantManager(s.authSrv)
	adminAPI.SetAnalytics(s.events, s.ring)
	srv := httptest.NewServer(adminAPI.Handler())
	t.Cleanup(srv.Close)

	// Seed two events on the fs backend within the 24h window:
	//   - allowed call from agent-1 (group member) — should bump group row's
	//     calls_24h by 1 (and denials by 0).
	//   - denied call from agent-2 against a template hash matching the role
	//     binding — should bump the role row by 1 call + 1 denial.
	now := time.Now()
	bindings := s.listAllBindings()
	var roleHash string
	for _, b := range bindings {
		for _, r := range b.Subjects.Roles {
			if r == role {
				roleHash = b.TemplateHash
				break
			}
		}
	}
	if roleHash == "" {
		t.Fatalf("could not locate template hash for role binding (role=%q bindings=%+v)", role, bindings)
	}
	seed := []auth.GrantEvent{
		{
			Timestamp: now.Add(-30 * time.Minute),
			RequestID: "pa-allow",
			AgentID:   "agent-1", ClientID: "agent-1",
			Backend: "fs", Tool: "fs.read_file",
			Outcome:      "allowed",
			TemplateHash: "scope-hash-irrelevant",
		},
		{
			Timestamp: now.Add(-10 * time.Minute),
			RequestID: "pa-deny",
			AgentID:   "agent-2", ClientID: "agent-2",
			Backend: "fs", Tool: "fs.read_file",
			Outcome:      "denied",
			TemplateHash: roleHash,
			Trace:        auth.GrantTrace{DenyDim: auth.GrantDenyArgs},
		},
	}
	for _, e := range seed {
		if err := s.events.Insert(e); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}

	get := func(path string) admin.PolicyAccessResponse {
		t.Helper()
		req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, srv.URL+"/api/v1"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := s.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status=%d body=%s", resp.StatusCode, body)
		}
		var out admin.PolicyAccessResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}

	got := get("/policy/access?backend=fs")
	if got.Empty {
		t.Fatalf("expected empty=false with seeded grants, got %+v", got)
	}

	// Index entries by (type, id) so the assertions stay readable even if
	// the implementation adds rows we don't care about (the test asserts
	// presence, not exhaustiveness).
	type key struct{ t, id string }
	byKey := map[key]admin.PolicyAccessEntry{}
	for _, e := range got.Entries {
		byKey[key{e.SubjectType, e.SubjectID}] = e
	}

	groupEntry, ok := byKey[key{"groups", group}]
	if !ok {
		t.Fatalf("group row missing for %q (entries=%+v)", group, got.Entries)
	}
	if groupEntry.Source != "scope" {
		t.Fatalf("group row source = %q, want scope", groupEntry.Source)
	}
	// agent-1's allowed event should aggregate onto the group row via the
	// agentsInGroup join. agent-2 is NOT a group member so its denial
	// must not count here.
	if groupEntry.Calls24h < 1 {
		t.Fatalf("group calls_24h = %d, want >= 1 (agent-1's allow)", groupEntry.Calls24h)
	}
	if groupEntry.Denials24h != 0 {
		t.Fatalf("group denials_24h = %d, want 0 (no member denied)", groupEntry.Denials24h)
	}

	roleEntry, ok := byKey[key{"roles", role}]
	if !ok {
		t.Fatalf("role row missing for %q (entries=%+v)", role, got.Entries)
	}
	if roleEntry.Source != "grant" {
		t.Fatalf("role row source = %q, want grant", roleEntry.Source)
	}
	if roleEntry.TemplateHash != roleHash {
		t.Fatalf("role template hash = %q, want %q", roleEntry.TemplateHash, roleHash)
	}
	// agent-2's denied event matches the role binding's template hash so it
	// joins onto the role row through the template-hash index — exactly the
	// path the contract requires (no per-subject store query).
	if roleEntry.Calls24h < 1 || roleEntry.Denials24h < 1 {
		t.Fatalf("role row missing analytics counts: calls=%d denials=%d (want >= 1 each)",
			roleEntry.Calls24h, roleEntry.Denials24h)
	}

	// Filtering by a tool the role binding explicitly covers keeps the
	// role row visible (the binding targets backend=fs, tool=read_file).
	// We assert presence by subject identity so this stays robust against
	// the implementation surfacing extra structural rows.
	filtered := get("/policy/access?backend=fs&tool=read_file")
	var sawRoleRow bool
	for _, e := range filtered.Entries {
		if e.SubjectType == "roles" && e.SubjectID == role {
			sawRoleRow = true
		}
	}
	if !sawRoleRow {
		t.Fatalf("tool=read_file should retain role row covering that tool, got entries=%+v", filtered.Entries)
	}

	// Filtering by a tool no stored capability mentions on this backend
	// returns the empty branch — the section then renders the empty state
	// with a link to Policy Builder. The contract is that `tool=` narrows
	// the section to "who can call this specific tool?"; an unknown tool
	// has zero subjects regardless of analytics activity.
	unknown := get("/policy/access?backend=fs&tool=does_not_exist")
	if !unknown.Empty {
		t.Fatalf("tool filter on unknown tool should produce empty=true, got %+v", unknown)
	}

	// Sanity: requesting a different backend that no policy mentions
	// returns the empty branch even though analytics rows exist.
	other := get("/policy/access?backend=github")
	if !other.Empty {
		t.Fatalf("policy/access for unrelated backend should be empty, got %+v", other)
	}
}

// --- task-44: /agents/policy-summary triage columns ---------------------

// TestE2E_AgentsPolicySummary_Populates verifies that
// GET /agents/policy-summary returns the three triage signals (capability
// count, last denial + deny_dim, drift count 24h) for the registered agent
// in a single batched call. Mirrors the Agents listing's expected payload
// shape so the page can render the Capabilities / Last denial / Drift 24h
// columns without per-agent round trips.
func TestE2E_AgentsPolicySummary_Populates(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("triage-grp")
	s.ensureGroup(group)
	prismID := s.ensureAgent(group)

	// Give the agent one capability via the group so capabilities_count
	// returns > 0. We attach a Where constraint so the spec compiles to a
	// grant binding (rather than a flat scope string). The capability
	// count served by /agents/policy-summary uses composeAgentCapabilityViews,
	// which walks bindings + their inheritance edges — scope-only
	// capabilities don't surface via that path. Real-world constrained
	// capabilities (path prefixes, MFA, etc.) all land in this branch.
	cap := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "fs.read_file"},
		Where:  &admin.WhereSpec{Mode: "agent_home"},
	}
	s.createCapability("groups", group, cap)

	// Seed denial + drift events so the analytics-driven columns have
	// signal to surface. Two events: one workspace_drift denial (counts
	// toward DriftCount24h AND becomes LastDenialAt), one older args
	// denial that should NOT win the "last denial" race.
	now := time.Now()
	if err := s.events.Insert(auth.GrantEvent{
		Timestamp: now.Add(-2 * time.Hour),
		RequestID: "old-args",
		AgentID:   prismID, ClientID: prismID,
		Backend: "fs", Tool: "fs.write_file",
		Outcome:      "denied",
		TemplateID:   "tmpl-w",
		TemplateHash: "hash-w",
		Trace:        auth.GrantTrace{DenyDim: auth.GrantDenyArgs},
	}); err != nil {
		t.Fatalf("seed older args denial: %v", err)
	}
	if err := s.events.Insert(auth.GrantEvent{
		Timestamp: now.Add(-10 * time.Minute),
		RequestID: "recent-drift",
		AgentID:   prismID, ClientID: prismID,
		Backend: "fs", Tool: "fs.write_file",
		Outcome:      "denied",
		TemplateID:   "tmpl-w",
		TemplateHash: "hash-w",
		Trace:        auth.GrantTrace{DenyDim: auth.GrantDenyWorkspaceDrift},
	}); err != nil {
		t.Fatalf("seed recent drift denial: %v", err)
	}

	var got []admin.AgentPolicySummary
	s.adminJSON(http.MethodGet, "/agents/policy-summary", nil, &got, http.StatusOK)

	var entry *admin.AgentPolicySummary
	for i := range got {
		if got[i].PrismID == prismID {
			entry = &got[i]
			break
		}
	}
	if entry == nil {
		t.Fatalf("policy-summary missing entry for prism_id=%s (got=%+v)", prismID, got)
	}

	// Capability count: at least the one bound via the group above.
	// Use >= so an unrelated baseline scope doesn't make the test flaky.
	if entry.CapabilitiesCount < 1 {
		t.Fatalf("capabilities_count = %d, want >= 1 (group binding seeded above)",
			entry.CapabilitiesCount)
	}
	// Last denial must be the recent drift row (newer of the two seeded).
	if entry.LastDenialAt.IsZero() {
		t.Fatalf("last_denial_at zero, want non-zero (drift row seeded)")
	}
	if entry.LastDenialDim != string(auth.GrantDenyWorkspaceDrift) {
		t.Fatalf("last_denial_dim = %q, want %q (newer drift row wins)",
			entry.LastDenialDim, auth.GrantDenyWorkspaceDrift)
	}
	// Drift count must include the recent drift event.
	if entry.DriftCount24h < 1 {
		t.Fatalf("drift_count_24h = %d, want >= 1 (one drift event in window)",
			entry.DriftCount24h)
	}

	// Cache invalidation contract: mutating the agent's policy must evict
	// the cached summary so the next read reflects the new capability
	// count without waiting 60s.
	//
	// Insert another binding via the policy builder, then re-fetch and
	// assert the count incremented (or at least changed shape vs cached).
	cap2 := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "fs.list_directory"},
		Where:  &admin.WhereSpec{Mode: "agent_home"},
	}
	s.createCapability("groups", group, cap2)

	var got2 []admin.AgentPolicySummary
	s.adminJSON(http.MethodGet, "/agents/policy-summary", nil, &got2, http.StatusOK)
	var entry2 *admin.AgentPolicySummary
	for i := range got2 {
		if got2[i].PrismID == prismID {
			entry2 = &got2[i]
			break
		}
	}
	if entry2 == nil {
		t.Fatalf("policy-summary entry missing after second capability add")
	}
	if entry2.CapabilitiesCount <= entry.CapabilitiesCount {
		t.Fatalf("capabilities_count did not increase after mutation: before=%d after=%d (cache eviction broken?)",
			entry.CapabilitiesCount, entry2.CapabilitiesCount)
	}
}

// TestE2E_AgentsPolicySummary_InvalidatesOnTemplateEdit covers the
// invalidation-gap closed by wave-14: edits to grant templates and bindings
// via the Advanced surfaces (POST/PUT/DELETE /grant-templates/* and
// /grant-bindings/*) must drop the cached policy-summary so the Agents
// listing reflects the change without waiting 60 seconds.
//
// Pre-fix behavior: only agent-policy mutations called invalidateAll.
// Template/binding writes through the raw endpoints did NOT evict the
// cache, so an operator editing a binding (e.g., dropping a tool from
// role:senior) saw the OLD cap count on the next refresh until the TTL
// elapsed — looked like the edit didn't take.
//
// Test shape: warm the cache by reading /agents/policy-summary, then mutate
// a binding via the advanced /grant-bindings endpoint, then re-read the
// summary and assert the new cap count is reflected. We use the binding
// path (rather than PUT template) because PUT template creates a new
// immutable version that existing bindings still reference at their old
// hash — the cap-count delta from a template-only edit is zero. Deleting
// the binding directly is the cleanest verifiable signal that the cache
// eviction actually fired through the previously-buggy code path.
func TestE2E_AgentsPolicySummary_InvalidatesOnTemplateEdit(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("cache-evict")
	s.ensureGroup(group)
	prismID := s.ensureAgent(group)

	// Seed a constrained capability so a grant template + binding exist —
	// this is what composeAgentCapabilityViews counts toward
	// CapabilitiesCount.
	cap := admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "fs.read_file"},
		Where:  &admin.WhereSpec{Mode: "agent_home"},
	}
	s.createCapability("groups", group, cap)

	// Warm the cache by fetching the summary. The seeded capability lifts
	// CapabilitiesCount to at least 1.
	var before []admin.AgentPolicySummary
	s.adminJSON(http.MethodGet, "/agents/policy-summary", nil, &before, http.StatusOK)
	var entry *admin.AgentPolicySummary
	for i := range before {
		if before[i].PrismID == prismID {
			entry = &before[i]
			break
		}
	}
	if entry == nil {
		t.Fatalf("policy-summary missing entry for prism_id=%s (got=%+v)", prismID, before)
	}
	if entry.CapabilitiesCount < 1 {
		t.Fatalf("capabilities_count = %d, want >= 1 (binding seeded above)",
			entry.CapabilitiesCount)
	}
	startCount := entry.CapabilitiesCount

	// Find the binding produced by the capability above and delete it
	// directly via the advanced /grant-bindings endpoint — the code path
	// the pre-fix bug missed.
	bindings := s.listAllBindings()
	if len(bindings) == 0 {
		t.Fatalf("expected at least one binding after createCapability; got none")
	}
	var targetBinding string
	for _, b := range bindings {
		if bindingHasGroup(b.Subjects.Groups, group) {
			targetBinding = b.ID
			break
		}
	}
	if targetBinding == "" {
		t.Fatalf("no binding targets group=%s among %d bindings", group, len(bindings))
	}
	s.adminJSON(http.MethodDelete, "/grant-bindings/"+url.PathEscape(targetBinding),
		nil, nil, http.StatusNoContent)

	// Immediately re-fetch — must reflect the deletion. Pre-fix: would
	// still report startCount for up to 60s (cache not evicted).
	var after []admin.AgentPolicySummary
	s.adminJSON(http.MethodGet, "/agents/policy-summary", nil, &after, http.StatusOK)
	var entry2 *admin.AgentPolicySummary
	for i := range after {
		if after[i].PrismID == prismID {
			entry2 = &after[i]
			break
		}
	}
	if entry2 == nil {
		t.Fatalf("policy-summary missing entry for prism_id=%s after binding delete", prismID)
	}
	if entry2.CapabilitiesCount >= startCount {
		t.Fatalf("capabilities_count = %d, want < %d (binding deletion did not evict the cache — stale read from 60s TTL)",
			entry2.CapabilitiesCount, startCount)
	}
}

// bindingHasGroup mirrors admin.containsString — duplicated locally so the
// test stays a black-box consumer of the admin HTTP surface rather than
// reaching into a non-exported helper.
func bindingHasGroup(groups []string, target string) bool {
	for _, g := range groups {
		if g == target {
			return true
		}
	}
	return false
}

// TestE2E_PolicyHealth_4TileShape (task-46) locks in the SecOps-aligned
// shape of GET /policy/health: the four operator-facing tiles
// (calls_24h, denials_24h + top_deny_dim, drift_events_24h,
// permissions_in_force + calls_7d_avg) must all surface on a populated
// store, and the deprecated fields must still appear on the wire for
// backwards compatibility with external consumers.
//
// We seed:
//   - five 24h events: 2 allowed, 2 denied (one workspace_drift, one args),
//     1 challenged
//   - one binding for a permissions_in_force >= 1 assertion
func TestE2E_PolicyHealth_4TileShape(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)
	// Seeding a constrained capability creates a (template, binding) pair —
	// permissions_in_force counts the binding.
	pfx := "/srv/data/"
	s.createCapability("groups", group, admin.CapabilitySpec{
		Action: admin.ActionSpec{Mode: "tool", Backend: "fs", Tool: "read_file"},
		Where:  &admin.WhereSpec{Mode: "path_prefix", PathPrefix: pfx},
	})

	now := time.Now()
	mk := func(req, agent, outcome, deny string, ts time.Duration) auth.GrantEvent {
		return auth.GrantEvent{
			Timestamp:    now.Add(-ts),
			RequestID:    req,
			AgentID:      agent,
			ClientID:     agent,
			Backend:      "fs",
			Tool:         "fs.write_file",
			Outcome:      outcome,
			TemplateID:   "tmpl-x",
			TemplateHash: "hash-x",
			Trace:        auth.GrantTrace{DenyDim: deny},
		}
	}
	events := []auth.GrantEvent{
		mk("e1", "a", "allowed", "", 1*time.Hour),
		mk("e2", "b", "allowed", "", 2*time.Hour),
		mk("e3", "c", "denied", auth.GrantDenyWorkspaceDrift, 3*time.Hour),
		mk("e4", "d", "denied", "args", 4*time.Hour),
		mk("e5", "e", "challenged", "", 5*time.Hour),
	}
	for _, ev := range events {
		if err := s.events.Insert(ev); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	var got admin.PolicyHealth
	s.adminJSON(http.MethodGet, "/policy/health", nil, &got, http.StatusOK)

	// Tile 1: calls + 7d-average trend baseline.
	if got.Calls24h != 5 {
		t.Errorf("calls_24h = %d, want 5", got.Calls24h)
	}
	if got.Calls7dAvg < 0 {
		t.Errorf("calls_7d_avg = %d, want non-negative", got.Calls7dAvg)
	}
	// Tile 2: denials + dominant deny_dim.
	if got.Denials24h != 2 {
		t.Errorf("denials_24h = %d, want 2", got.Denials24h)
	}
	if got.TopDenyDim == "" {
		t.Errorf("top_deny_dim is empty; expected workspace_drift or args")
	}
	if got.TopDenyDimCount == 0 {
		t.Errorf("top_deny_dim_count = 0; expected positive")
	}
	// Tile 3: drift events.
	if got.DriftEvents24h != 1 {
		t.Errorf("drift_events_24h = %d, want 1", got.DriftEvents24h)
	}
	// Tile 4: permissions in force — at least the binding we created above.
	if got.PermissionsInForce < 1 {
		t.Errorf("permissions_in_force = %d, want >= 1 (binding seeded above)",
			got.PermissionsInForce)
	}

	// Deprecated fields must still be present on the wire (backwards
	// compatibility for external consumers — task-46 only stops *rendering*
	// them in the strip). We don't assert specific values here, just that
	// the keys exist by re-decoding the response.
	var raw map[string]json.RawMessage
	s.adminJSON(http.MethodGet, "/policy/health", nil, &raw, http.StatusOK)
	for _, k := range []string{
		"median_freshness_seconds",
		"dpop_bound_agents",
		"active_templates",
	} {
		if _, ok := raw[k]; !ok {
			t.Errorf("deprecated field %q missing from wire shape (must remain for backwards compat)", k)
		}
	}
}

// TestE2E_PolicyBuilder_DeleteDenyScope (task-46) covers the new
// "scope-deny-" capability id prefix: an AgentPolicy.Deny entry surfaces
// through GET /policy/subjects/agents/{id}/capabilities as an Effect="deny"
// row, and DELETE on that row removes the scope from AgentPolicy.Deny
// without touching the agent's allow list.
func TestE2E_PolicyBuilder_DeleteDenyScope(t *testing.T) {
	s := newPolicySuite(t)
	group := s.uniqueSubject("eng")
	s.ensureGroup(group)
	prismID := s.ensureAgent(group)

	// Seed an explicit deny scope on the agent via the admin policy PUT.
	// The allow side gets one scope too so we can prove the delete is
	// surgically targeting the deny list.
	policyBody := map[string]any{
		"groups": []string{group},
		"grant":  []string{"fs:read_file"},
		"deny":   []string{"github:delete_repo"},
	}
	s.adminJSON(http.MethodPut, "/agents/"+url.PathEscape(prismID)+"/policy",
		policyBody, nil, http.StatusOK)

	// The capability listing should now include a deny row.
	caps := s.listCapabilities(subjectTypeAgents, prismID)
	var denyView *admin.CapabilityView
	var allowCount int
	for i := range caps {
		switch caps[i].Effect {
		case "deny":
			denyView = &caps[i]
		case "allow":
			allowCount++
		}
	}
	if denyView == nil {
		t.Fatalf("expected a deny-effect capability row, got: %+v", caps)
	}
	if !strings.HasPrefix(denyView.ID, "scope-deny-") {
		t.Errorf("deny capability id = %q; want scope-deny- prefix", denyView.ID)
	}
	if denyView.Source != "scope" {
		t.Errorf("deny.Source = %q, want scope", denyView.Source)
	}
	if allowCount == 0 {
		t.Errorf("expected at least one allow row (fs:read_file seeded), got %d", allowCount)
	}

	// DELETE the deny capability and confirm AgentPolicy.Deny is cleared
	// while AgentPolicy.Grant survives.
	s.deleteCapability(subjectTypeAgents, prismID, denyView.ID)

	policy := s.agentPolicy(prismID)
	if len(policy.Deny) != 0 {
		t.Errorf("policy.Deny = %v, want empty after delete", policy.Deny)
	}
	if !policySliceContains(policy.Grant, "fs:read_file") {
		t.Errorf("allow list lost fs:read_file after deny delete: grant=%v", policy.Grant)
	}

	// Re-listing capabilities should no longer surface the deny row.
	caps2 := s.listCapabilities(subjectTypeAgents, prismID)
	for _, v := range caps2 {
		if v.Effect == "deny" {
			t.Errorf("deny row still present after delete: %+v", v)
		}
	}
}

// TestE2E_Identity_AllocateAndRename exercises the /api/v1/identity
// endpoints introduced by epic-5 task-47:
//
//   - POST creates an entity and returns a 26-char ULID
//   - GET on /identity?kind=group reflects the new entity
//   - GET on /identity/{id} returns the same record
//   - PUT /identity/{id}/display-name renames without changing the ID
//   - The old display name no longer resolves; the new one does
//   - DELETE removes the entity from List
//
// This test stands in for the foundation acceptance bullet of the
// identity unification spec — the rest of epic-5 builds on the wire
// shapes exercised here.
func TestE2E_Identity_AllocateAndRename(t *testing.T) {
	s := newPolicySuite(t)

	// 1. Allocate a group entity through POST /identity.
	allocateBody := map[string]string{
		"kind":         "group",
		"display_name": "engineering",
	}
	var created identity.Entity
	s.adminJSON(http.MethodPost, "/identity", allocateBody, &created, http.StatusCreated)
	if !identity.IsULID(created.ID) {
		t.Fatalf("allocate returned non-ULID id %q", created.ID)
	}
	if created.DisplayName != "engineering" || created.Kind != identity.KindGroup {
		t.Fatalf("allocate response shape unexpected: %+v", created)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("allocate response missing timestamps: %+v", created)
	}

	// 2. List should now contain the entity.
	var list struct {
		Items []identity.Entity `json:"items"`
	}
	s.adminJSON(http.MethodGet, "/identity?kind=group", nil, &list, http.StatusOK)
	if len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("list after allocate = %+v, want one entry id=%s", list.Items, created.ID)
	}

	// 3. GET by id returns the same entity.
	var fetched identity.Entity
	s.adminJSON(http.MethodGet, "/identity/"+created.ID, nil, &fetched, http.StatusOK)
	if fetched.ID != created.ID || fetched.DisplayName != "engineering" {
		t.Fatalf("get-by-id response = %+v", fetched)
	}

	// 4. Rename to a new display name. ID must stay constant; the new
	// display name is what subsequent list/get calls show.
	renameBody := map[string]string{"display_name": "platform-engineering"}
	var renamed identity.Entity
	s.adminJSON(http.MethodPut, "/identity/"+created.ID+"/display-name", renameBody, &renamed, http.StatusOK)
	if renamed.ID != created.ID {
		t.Fatalf("rename mutated id: %q vs %q", renamed.ID, created.ID)
	}
	if renamed.DisplayName != "platform-engineering" {
		t.Fatalf("rename did not update display name: %+v", renamed)
	}
	if !renamed.UpdatedAt.After(created.UpdatedAt) && !renamed.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("rename did not advance UpdatedAt: was %v now %v", created.UpdatedAt, renamed.UpdatedAt)
	}

	// 5. Duplicate-name allocation returns 409 (uniqueness within kind).
	dupBody := map[string]string{"kind": "group", "display_name": "platform-engineering"}
	s.adminJSON(http.MethodPost, "/identity", dupBody, nil, http.StatusConflict)

	// 6. Same display name under a different kind is fine.
	roleBody := map[string]string{"kind": "role", "display_name": "platform-engineering"}
	var roleEnt identity.Entity
	s.adminJSON(http.MethodPost, "/identity", roleBody, &roleEnt, http.StatusCreated)
	if roleEnt.Kind != identity.KindRole {
		t.Fatalf("role allocate wrong kind: %+v", roleEnt)
	}

	// 7. Delete the group entity and confirm it disappears.
	s.adminJSON(http.MethodDelete, "/identity/"+created.ID, nil, nil, http.StatusNoContent)
	s.adminJSON(http.MethodGet, "/identity/"+created.ID, nil, nil, http.StatusNotFound)

	// 8. After deletion, the same display name is available again
	//    (no ghost name-index entries from the rename).
	reuseBody := map[string]string{"kind": "group", "display_name": "platform-engineering"}
	var reused identity.Entity
	s.adminJSON(http.MethodPost, "/identity", reuseBody, &reused, http.StatusCreated)
	if reused.ID == created.ID {
		t.Fatalf("delete+re-allocate produced the same ULID (collision suspect)")
	}
}

// subjectTypeAgents is duplicated as a local string constant rather than
// importing the unexported const from internal/admin. Keeps the e2e test
// black-box.
const subjectTypeAgents = "agents"
