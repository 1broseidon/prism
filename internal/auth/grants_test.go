package auth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPredicateUnmarshalRejectsReservedKeys(t *testing.T) {
	var p Predicate
	err := json.Unmarshal([]byte(`{"expression":"true"}`), &p)
	if err == nil || !strings.Contains(err.Error(), "reserved predicate key") {
		t.Fatalf("expected reserved key error, got %v", err)
	}
}

func TestPredicateMatch(t *testing.T) {
	prefix := "/workspace/a/"
	size := int64(5)
	min, max := 2.0, 4.0
	pattern := `^feat-[0-9]+$`
	tests := []struct {
		name string
		p    Predicate
		v    any
		want bool
	}{
		{"equals numeric json", Predicate{Equals: float64(3)}, 3, true},
		{"prefix pass", Predicate{Prefix: &prefix}, "/workspace/a/file.txt", true},
		{"prefix fail", Predicate{Prefix: &prefix}, "/workspace/b/file.txt", false},
		{"oneOf pass", Predicate{OneOf: []any{"a", "b"}}, "b", true},
		{"pattern pass", Predicate{Pattern: &pattern}, "feat-123", true},
		{"size pass", Predicate{SizeMax: &size}, "12345", true},
		{"size fail", Predicate{SizeMax: &size}, "123456", false},
		{"range pass", Predicate{Range: &RangePredicate{Min: &min, Max: &max}}, float64(3), true},
		{"range fail", Predicate{Range: &RangePredicate{Min: &min, Max: &max}}, float64(5), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Match(tt.v); got != tt.want {
				t.Fatalf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSubjectSelectorMatches(t *testing.T) {
	s := SubjectSelector{Groups: []string{"eng"}, RoleRequired: "senior"}
	if !s.Matches("agent-a", []string{"eng"}, []string{"senior"}) {
		t.Fatal("expected group + required role match")
	}
	if s.Matches("agent-a", []string{"eng"}, []string{"junior"}) {
		t.Fatal("expected missing required role to fail")
	}
	if (SubjectSelector{}).Matches("agent-a", []string{"eng"}, []string{"senior"}) {
		t.Fatal("empty selector must match no one")
	}
}

func TestSubstituteGrantSpec(t *testing.T) {
	var spec GrantSpec
	if err := json.Unmarshal([]byte(`{
		"type":"prism.mcp.call",
		"tool":"fs.write_file",
		"backend":"local",
		"args":{"path":{"prefix":"/workspace/${agent.prism_id}/"}}
	}`), &spec); err != nil {
		t.Fatal(err)
	}
	got, err := SubstituteGrantSpec(spec, map[string]string{"agent.prism_id": "a1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Args["path"].Prefix == nil || *got.Args["path"].Prefix != "/workspace/a1/" {
		t.Fatalf("prefix not substituted: %+v", got.Args["path"])
	}
}

func TestMatchGrants(t *testing.T) {
	prefix := "/workspace/a/"
	grant := IssuedGrant{
		Type:         GrantTypeMCPCall,
		TemplateID:   "tmpl",
		TemplateHash: "sha256-x",
		Tool:         "fs.write_file",
		Backend:      "local",
		Args:         map[string]Predicate{"path": {Prefix: &prefix}},
		Workspace:    &WorkspaceInstance{ID: "ws-a", Type: "ephemeral", WriteMode: "stage"},
	}
	call := GrantCall{
		Tool:      "fs.write_file",
		Backend:   "local",
		Arguments: json.RawMessage(`{"path":"/workspace/a/file.txt"}`),
		Workspace: &WorkspaceInstance{ID: "ws-a", Type: "ephemeral", WriteMode: "stage"},
		Now:       time.Unix(100, 0),
	}
	got := MatchGrants(call, []IssuedGrant{grant})
	if !got.Allowed || got.MatchedIndex != 0 {
		t.Fatalf("expected allow, got %+v", got)
	}

	call.Arguments = json.RawMessage(`{"path":"/tmp/file.txt"}`)
	got = MatchGrants(call, []IssuedGrant{grant})
	if got.Allowed || got.DenyDim != GrantDenyArgs {
		t.Fatalf("expected args denial, got %+v", got)
	}

	call.Arguments = json.RawMessage(`{"path":"/workspace/a/file.txt"}`)
	call.Workspace = &WorkspaceInstance{ID: "ws-b", Type: "ephemeral", WriteMode: "stage"}
	got = MatchGrants(call, []IssuedGrant{grant})
	if got.Allowed || got.DenyDim != GrantDenyWorkspaceDrift {
		t.Fatalf("expected workspace drift, got %+v", got)
	}
}

func TestGrantHours(t *testing.T) {
	ts := time.Date(2026, 5, 18, 14, 30, 0, 0, time.UTC) // Monday 09:30 America/Chicago
	if !TimeInGrantHours(ts, "weekdays 09:00-18:00 America/Chicago") {
		t.Fatal("expected timestamp to be inside weekday hours")
	}
	weekend := time.Date(2026, 5, 17, 14, 30, 0, 0, time.UTC)
	if TimeInGrantHours(weekend, "weekdays 09:00-18:00 America/Chicago") {
		t.Fatal("expected weekend to be outside weekday hours")
	}
}

func TestCanonicalGrantHashStable(t *testing.T) {
	spec := GrantSpec{Type: GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local"}
	a, err := CanonicalGrantHash(spec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalGrantHash(spec)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || !strings.HasPrefix(a, "sha256-") {
		t.Fatalf("unstable hash %q %q", a, b)
	}
	changed, err := CanonicalGrantHash(GrantSpec{Type: GrantTypeMCPCall, Tool: "fs.read_file", Backend: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if changed == a {
		t.Fatal("hash must change when spec content changes")
	}
}

// TestGrantStoreTemplateImmutabilityAndHashIndex and
// TestGrantStoreSaveBindingRequiresLatestTemplateHash previously exercised the
// auth.GrantStore parallel implementation. That type has been removed —
// authserver.Server is the single KV-backed grant store used in production,
// and its coverage lives in internal/authserver/grants_store_test.go +
// internal/admin/grant_templates_test.go.

// TestCanonicalGrantHashIsKeyOrderInvariant pins the canonical-JSON contract:
// the hash must depend only on field values (and their JSON tag names), not on
// the declaration order of fields inside GrantSpec. Refactoring GrantSpec to
// reorder fields, or adding/removing optional zero-valued fields, must not
// invalidate hashes for existing templates.
func TestCanonicalGrantHashIsKeyOrderInvariant(t *testing.T) {
	prefix := "/workspace/a/"
	a := GrantSpec{
		Type:        GrantTypeMCPCall,
		Tool:        "fs.write_file",
		Backend:     "local",
		Args:        map[string]Predicate{"path": {Prefix: &prefix}},
		Hours:       "09:00-18:00 UTC",
		CnfRequired: true,
		AcrRequired: "urn:prism:mfa",
	}
	// Construct an equivalent spec by reassigning fields in a different order.
	b := GrantSpec{
		AcrRequired: "urn:prism:mfa",
		CnfRequired: true,
		Hours:       "09:00-18:00 UTC",
		Args:        map[string]Predicate{"path": {Prefix: &prefix}},
		Backend:     "local",
		Tool:        "fs.write_file",
		Type:        GrantTypeMCPCall,
	}
	ha, err := CanonicalGrantHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := CanonicalGrantHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("hash differs under field-order reorder: %q vs %q", ha, hb)
	}
}

func TestSubstituteVarsRejectsUnknownVars(t *testing.T) {
	prefix := "/workspace/${env.HOME}/"
	_, err := SubstituteVars(GrantSpec{
		Type:    GrantTypeMCPCall,
		Tool:    "fs.write_file",
		Backend: "local",
		Args:    map[string]Predicate{"path": {Prefix: &prefix}},
	}, SubVars{AgentPrismID: "a1", AgentClientID: "c1"})
	if err == nil || !strings.Contains(err.Error(), "unknown substitution variable") {
		t.Fatalf("expected unknown var error, got %v", err)
	}
}

func TestGrantSpecRejectsInvalidPredicate(t *testing.T) {
	pattern := strings.Repeat("a", 257)
	spec := GrantSpec{
		Type:    GrantTypeMCPCall,
		Tool:    "fs.write_file",
		Backend: "local",
		Args:    map[string]Predicate{"path": {Pattern: &pattern}},
	}
	err := spec.Validate()
	if err == nil || !strings.Contains(err.Error(), "pattern") {
		t.Fatalf("expected pattern validation error, got %v", err)
	}
}

// TestComputeTemplateHash_ToolInSetOrderInvariant pins the dedup invariant:
// two GrantSpec values with a tool_in_set predicate whose entries differ only
// in order must produce identical template hashes after Validate (which sorts
// the entries in place). Without canonicalization, operators authoring the
// same capability in different entry orders would silently miss the dedup
// path and create duplicate templates.
func TestComputeTemplateHash_ToolInSetOrderInvariant(t *testing.T) {
	mkSpec := func(entries []string) GrantSpec {
		return GrantSpec{
			Type:    GrantTypeMCPCall,
			Tool:    "*",
			Backend: "*",
			Args:    map[string]Predicate{"_tool": {ToolInSet: entries}},
		}
	}
	a := mkSpec([]string{"fs:write_file", "fs:append_file", "fs:create_dir"})
	b := mkSpec([]string{"fs:create_dir", "fs:write_file", "fs:append_file"})

	if err := a.Validate(); err != nil {
		t.Fatalf("a.Validate: %v", err)
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("b.Validate: %v", err)
	}
	ha, err := ComputeTemplateHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := ComputeTemplateHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("hash differs under tool_in_set reorder: %q vs %q (a=%v b=%v)",
			ha, hb, a.Args["_tool"].ToolInSet, b.Args["_tool"].ToolInSet)
	}
}
