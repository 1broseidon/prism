package authserver

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/store"
)

// newTestServerWithStore creates a test server backed by an in-memory KV store
// with optional group definitions. Groups and default scopes enable policy resolution.
func newTestServerWithStore(t *testing.T, groups map[string]GroupConfig, defaultScopes []string) (*Server, store.Store) {
	t.Helper()

	km, err := NewKeyManager("")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}

	kv := store.NewMemoryStore()

	cfg := &Config{
		ListenAddr:      ":9100",
		Issuer:          testIssuer,
		TokenTTLSeconds: 3600,
		Clients: []ClientConfig{
			{
				ClientID:      "static-agent",
				ClientSecret:  "static-secret",
				AllowedScopes: []string{"mcp:connect", "github:create_issue"},
			},
		},
		DefaultScopes: defaultScopes,
	}

	srv := NewServer(cfg, km, kv, nil, groups)

	return srv, kv
}

// registerDCRClient creates a dynamic client with a PrismID in the test server.
func registerDCRClient(t *testing.T, srv *Server, clientID, prismID, label string) {
	t.Helper()

	secretHash := sha256Hash("test-secret")
	srv.mu.Lock()
	srv.clients[clientID] = &ClientConfig{
		ClientID:      clientID,
		ClientSecret:  secretHash,
		AllowedScopes: []string{"mcp:connect"},
		Description:   label,
	}
	srv.mu.Unlock()

	srv.oauth.mu.Lock()
	srv.oauth.dynamics[clientID] = &dynamicClient{
		ClientID:   clientID,
		PrismID:    prismID,
		Label:      label,
		CreatedAt:  "2024-01-01T00:00:00Z",
		LastUsedAt: "2024-01-01T00:00:00Z",
	}
	srv.oauth.mu.Unlock()
}

func sortedScopes(scopes []string) []string {
	out := make([]string, len(scopes))
	copy(out, scopes)
	sort.Strings(out)
	return out
}

func TestResolveScopesByPrismID_NoPolicy_FallsBackToDefaults(t *testing.T) {
	srv, _ := newTestServerWithStore(t, nil, []string{"read:tools"})

	scopes := srv.ResolveScopesByPrismID("nonexistent-prism-id")
	sorted := sortedScopes(scopes)

	// Should get mcp:connect + default_scopes.
	expected := sortedScopes([]string{"mcp:connect", "read:tools"})

	if len(sorted) != len(expected) {
		t.Fatalf("got %d scopes %v, want %d %v", len(sorted), sorted, len(expected), expected)
	}
	for i, s := range expected {
		if sorted[i] != s {
			t.Errorf("scope[%d] = %q, want %q", i, sorted[i], s)
		}
	}
}

func TestResolveScopesByPrismID_NoPolicy_NoDefaults(t *testing.T) {
	srv, _ := newTestServerWithStore(t, nil, nil)

	scopes := srv.ResolveScopesByPrismID("nonexistent-prism-id")
	if len(scopes) != 1 || scopes[0] != "mcp:connect" {
		t.Fatalf("expected only mcp:connect, got %v", scopes)
	}
}

func TestResolveScopesByPrismID_WithGroups(t *testing.T) {
	groups := map[string]GroupConfig{
		"readers": {Scopes: []string{"github:list_prs", "github:get_issue"}},
		"writers": {Scopes: []string{"github:create_issue", "github:merge_pr"}},
	}
	srv, _ := newTestServerWithStore(t, groups, nil)

	prismID := "agent-uuid-1"
	policy := &AgentPolicy{
		Groups: []string{"readers", "writers"},
	}
	if err := srv.SetAgentPolicy(prismID, policy); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}

	scopes := sortedScopes(srv.ResolveScopesByPrismID(prismID))
	expected := sortedScopes([]string{
		"mcp:connect",
		"github:list_prs", "github:get_issue",
		"github:create_issue", "github:merge_pr",
	})

	if len(scopes) != len(expected) {
		t.Fatalf("got %d scopes %v, want %d %v", len(scopes), scopes, len(expected), expected)
	}
	for i := range expected {
		if scopes[i] != expected[i] {
			t.Errorf("scope[%d] = %q, want %q", i, scopes[i], expected[i])
		}
	}
}

func TestResolveScopesByPrismID_GrantAndDeny(t *testing.T) {
	groups := map[string]GroupConfig{
		"readers": {Scopes: []string{"github:list_prs", "github:get_issue", "fs:read_file"}},
	}
	srv, _ := newTestServerWithStore(t, groups, nil)

	prismID := "agent-uuid-2"
	policy := &AgentPolicy{
		Groups: []string{"readers"},
		Grant:  []string{"extra:tool"},
		Deny:   []string{"fs:read_file"},
	}
	if err := srv.SetAgentPolicy(prismID, policy); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}

	scopes := sortedScopes(srv.ResolveScopesByPrismID(prismID))
	expected := sortedScopes([]string{
		"mcp:connect",
		"github:list_prs", "github:get_issue",
		"extra:tool",
		// fs:read_file is denied
	})

	if len(scopes) != len(expected) {
		t.Fatalf("got %d scopes %v, want %d %v", len(scopes), scopes, len(expected), expected)
	}
	for i := range expected {
		if scopes[i] != expected[i] {
			t.Errorf("scope[%d] = %q, want %q", i, scopes[i], expected[i])
		}
	}
}

func TestResolveScopesByPrismID_DenyOverridesGrant(t *testing.T) {
	srv, _ := newTestServerWithStore(t, nil, nil)

	prismID := "agent-uuid-3"
	policy := &AgentPolicy{
		Grant: []string{"tool:a", "tool:b"},
		Deny:  []string{"tool:b"},
	}
	if err := srv.SetAgentPolicy(prismID, policy); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}

	scopes := sortedScopes(srv.ResolveScopesByPrismID(prismID))
	expected := sortedScopes([]string{"mcp:connect", "tool:a"})

	if len(scopes) != len(expected) {
		t.Fatalf("got %v, want %v", scopes, expected)
	}
	for i := range expected {
		if scopes[i] != expected[i] {
			t.Errorf("scope[%d] = %q, want %q", i, scopes[i], expected[i])
		}
	}
}

func TestGetSetDeleteAgentPolicy(t *testing.T) {
	srv, _ := newTestServerWithStore(t, nil, nil)
	prismID := "test-prism-id"

	// Initially no policy.
	p, err := srv.GetAgentPolicy(prismID)
	if err != nil {
		t.Fatalf("GetAgentPolicy: %v", err)
	}
	if p != nil {
		t.Fatalf("expected nil policy, got %+v", p)
	}

	// Set policy.
	policy := &AgentPolicy{
		Groups: []string{"readers"},
		Grant:  []string{"extra:tool"},
		Deny:   []string{"bad:tool"},
	}
	if setErr := srv.SetAgentPolicy(prismID, policy); setErr != nil {
		t.Fatalf("SetAgentPolicy: %v", setErr)
	}

	// Read it back.
	got, err := srv.GetAgentPolicy(prismID)
	if err != nil {
		t.Fatalf("GetAgentPolicy after set: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil policy")
	}
	if len(got.Groups) != 1 || got.Groups[0] != "readers" {
		t.Errorf("groups = %v, want [readers]", got.Groups)
	}
	if len(got.Grant) != 1 || got.Grant[0] != "extra:tool" {
		t.Errorf("grant = %v, want [extra:tool]", got.Grant)
	}
	if len(got.Deny) != 1 || got.Deny[0] != "bad:tool" {
		t.Errorf("deny = %v, want [bad:tool]", got.Deny)
	}

	// Delete policy.
	if delErr := srv.DeleteAgentPolicy(prismID); delErr != nil {
		t.Fatalf("DeleteAgentPolicy: %v", delErr)
	}

	// Should be nil again.
	p, err = srv.GetAgentPolicy(prismID)
	if err != nil {
		t.Fatalf("GetAgentPolicy after delete: %v", err)
	}
	if p != nil {
		t.Fatalf("expected nil policy after delete, got %+v", p)
	}
}

func TestConfigAgentsUnaffected(t *testing.T) {
	// Static/config agents should not have their scopes changed by policy resolution.
	groups := map[string]GroupConfig{
		"readers": {Scopes: []string{"github:list_prs"}},
	}
	srv, _ := newTestServerWithStore(t, groups, nil)

	// Verify the static agent exists with its original scopes.
	srv.mu.RLock()
	client, ok := srv.clients["static-agent"]
	srv.mu.RUnlock()

	if !ok {
		t.Fatal("static-agent not found")
	}

	expected := sortedScopes([]string{"mcp:connect", "github:create_issue"})
	got := sortedScopes(client.AllowedScopes)

	if len(got) != len(expected) {
		t.Fatalf("static agent scopes = %v, want %v", got, expected)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Errorf("scope[%d] = %q, want %q", i, got[i], expected[i])
		}
	}
}

func TestGetAgentByPrismID(t *testing.T) {
	srv, _ := newTestServerWithStore(t, nil, nil)

	registerDCRClient(t, srv, "dcr-client-1", "prism-uuid-abc", "My Agent")

	agent := srv.GetAgentByPrismID("prism-uuid-abc")
	if agent == nil {
		t.Fatal("expected agent, got nil")
	}
	if agent.PrismID != "prism-uuid-abc" {
		t.Errorf("PrismID = %q, want prism-uuid-abc", agent.PrismID)
	}
	if agent.Label != "My Agent" {
		t.Errorf("Label = %q, want 'My Agent'", agent.Label)
	}
	if !agent.Dynamic {
		t.Error("expected Dynamic = true")
	}

	// Non-existent PrismID.
	if got := srv.GetAgentByPrismID("nonexistent"); got != nil {
		t.Errorf("expected nil for nonexistent PrismID, got %+v", got)
	}
}

func TestGetAgentByPrismID_IncludesPolicy(t *testing.T) {
	groups := map[string]GroupConfig{
		"readers": {Scopes: []string{"github:list_prs"}},
	}
	srv, _ := newTestServerWithStore(t, groups, nil)

	registerDCRClient(t, srv, "dcr-1", "prism-uuid-1", "Agent One")

	policy := &AgentPolicy{Groups: []string{"readers"}, Grant: []string{"extra:tool"}}
	if err := srv.SetAgentPolicy("prism-uuid-1", policy); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}

	agent := srv.GetAgentByPrismID("prism-uuid-1")
	if agent == nil {
		t.Fatal("expected agent")
	}
	if agent.Policy == nil {
		t.Fatal("expected policy on agent")
	}
	if len(agent.Policy.Groups) != 1 || agent.Policy.Groups[0] != "readers" {
		t.Errorf("policy.groups = %v, want [readers]", agent.Policy.Groups)
	}
}

func TestListAgents_IncludesPolicy(t *testing.T) {
	groups := map[string]GroupConfig{
		"readers": {Scopes: []string{"github:list_prs"}},
	}
	srv, _ := newTestServerWithStore(t, groups, nil)

	registerDCRClient(t, srv, "dcr-1", "prism-uuid-1", "Agent One")

	policy := &AgentPolicy{Groups: []string{"readers"}, Grant: []string{"extra:tool"}}
	if err := srv.SetAgentPolicy("prism-uuid-1", policy); err != nil {
		t.Fatalf("SetAgentPolicy: %v", err)
	}

	agents := srv.ListAgents()
	var found *AgentInfo
	for i := range agents {
		if agents[i].PrismID == "prism-uuid-1" {
			found = &agents[i]
			break
		}
	}
	if found == nil {
		t.Fatal("DCR agent not found in ListAgents")
	}
	if found.Policy == nil {
		t.Fatal("expected policy on DCR agent")
	}
	if len(found.Policy.Groups) != 1 || found.Policy.Groups[0] != "readers" {
		t.Errorf("policy.groups = %v, want [readers]", found.Policy.Groups)
	}
}

func TestMintToken_IncludesPrismID(t *testing.T) {
	srv, _ := newTestServerWithStore(t, nil, nil)

	token, err := srv.mintToken("test-client", []string{"mcp:connect"}, "prism-uuid-mint")
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}

	claims := parseJWTClaims(t, token)
	if claims["prism_id"] != "prism-uuid-mint" {
		t.Errorf("prism_id = %v, want prism-uuid-mint", claims["prism_id"])
	}
	// sub should still be client_id, not prism_id.
	if claims["sub"] != "test-client" {
		t.Errorf("sub = %v, want test-client", claims["sub"])
	}
}

func TestMintToken_NoPrismID(t *testing.T) {
	srv, _ := newTestServerWithStore(t, nil, nil)

	token, err := srv.mintToken("test-client", []string{"mcp:connect"})
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}

	claims := parseJWTClaims(t, token)
	if _, ok := claims["prism_id"]; ok {
		t.Error("prism_id should not be present when not provided")
	}
}

// parseJWTClaims extracts the claims payload from a JWT without signature verification.
func parseJWTClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
}
