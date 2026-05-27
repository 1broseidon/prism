package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateToolInSet_Accepts(t *testing.T) {
	cases := [][]string{
		{"fs:write_file"},
		{"fs:write_file", "fs:append_file", "fs:delete_file", "fs:create_dir"},
		{"github:list_issues", "github:get_issue"},
		{"deploy:*"}, // wildcard tool is allowed (cross-backend "deploy" verb)
	}
	for _, c := range cases {
		if err := ValidateToolInSet(c); err != nil {
			t.Fatalf("ValidateToolInSet(%v) unexpected error: %v", c, err)
		}
	}
}

func TestValidateToolInSet_RejectsEmpty(t *testing.T) {
	if err := ValidateToolInSet(nil); err == nil {
		t.Fatal("expected error for nil")
	}
	if err := ValidateToolInSet([]string{}); err == nil {
		t.Fatal("expected error for empty")
	}
}

func TestValidateToolInSet_RejectsOversize(t *testing.T) {
	too := make([]string, ToolInSetMaxEntries+1)
	for i := range too {
		too[i] = "fs:tool_" + strings.Repeat("x", 1)
	}
	// duplicates would trip a different branch — keep entries unique.
	for i := range too {
		too[i] = "fs:tool_" + itoa(i)
	}
	if err := ValidateToolInSet(too); err == nil {
		t.Fatalf("expected error for %d entries", len(too))
	}
}

func TestValidateToolInSet_RejectsMalformed(t *testing.T) {
	bad := []string{
		"",         // empty
		"no_colon", // missing colon
		":missing_backend",
		"backend:",      // missing tool
		".badbackend:t", // backend starts with '.'
		"-badbackend:t", // backend starts with '-'
		"backend:.tool", // tool starts with '.'
		"backend:-tool", // tool starts with '-'
		"backend:tool!", // illegal char
	}
	for _, b := range bad {
		if err := ValidateToolInSet([]string{b}); err == nil {
			t.Errorf("expected error for entry %q", b)
		}
	}
}

func TestValidateToolInSet_RejectsDuplicates(t *testing.T) {
	if err := ValidateToolInSet([]string{"fs:write_file", "fs:write_file"}); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestPredicateUnmarshal_ToolInSetRoundtrip(t *testing.T) {
	raw := `{"tool_in_set":["fs:write_file","fs:append_file"]}`
	var p Predicate
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Validate canonicalizes entries by sorting lexicographically (so
	// ComputeTemplateHash is order-invariant). UnmarshalJSON calls Validate
	// after decoding, so the in-memory shape is already sorted.
	if len(p.ToolInSet) != 2 || p.ToolInSet[0] != "fs:append_file" || p.ToolInSet[1] != "fs:write_file" {
		t.Fatalf("decoded predicate = %+v", p)
	}
	// Re-validate explicitly to confirm Validate() also accepts the shape.
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate after unmarshal: %v", err)
	}
}

func TestPredicateUnmarshal_ToolInSetEmptyRejected(t *testing.T) {
	raw := `{"tool_in_set":[]}`
	var p Predicate
	if err := json.Unmarshal([]byte(raw), &p); err == nil {
		t.Fatal("expected error for empty tool_in_set")
	}
}

func TestPredicateUnmarshal_ToolInSetOversizeRejected(t *testing.T) {
	entries := make([]string, ToolInSetMaxEntries+1)
	for i := range entries {
		entries[i] = "fs:tool_" + itoa(i)
	}
	body, _ := json.Marshal(map[string]any{"tool_in_set": entries})
	var p Predicate
	if err := json.Unmarshal(body, &p); err == nil {
		t.Fatal("expected error for oversize tool_in_set")
	}
}

func TestPredicateUnmarshal_ToolInSetMalformedRejected(t *testing.T) {
	raw := `{"tool_in_set":["no_colon"]}`
	var p Predicate
	if err := json.Unmarshal([]byte(raw), &p); err == nil {
		t.Fatal("expected error for malformed tool_in_set entry")
	}
}

func TestPredicateUnmarshal_ToolInSetIsExclusive(t *testing.T) {
	// tool_in_set + equals must be rejected because predicates carry exactly
	// one operator.
	raw := `{"tool_in_set":["fs:write_file"],"equals":"x"}`
	var p Predicate
	if err := json.Unmarshal([]byte(raw), &p); err == nil {
		t.Fatal("expected error for combined predicate operators")
	}
}

func TestPredicateMatch_ToolInSet(t *testing.T) {
	p := Predicate{ToolInSet: []string{"fs:write_file", "fs:append_file"}}
	if !p.Match("fs:write_file") {
		t.Fatal("expected match for fs:write_file")
	}
	if !p.Match("fs:append_file") {
		t.Fatal("expected match for fs:append_file")
	}
	if p.Match("fs:read_file") {
		t.Fatal("did not expect match for fs:read_file")
	}
	// Non-string values never match.
	if p.Match(42) {
		t.Fatal("did not expect numeric to match")
	}
}

func TestMatchGrants_ToolInSetSyntheticTool(t *testing.T) {
	// Grant uses Tool:"*" and a tool_in_set predicate over _tool. This is the
	// verb-with-constraints compile path: one template covers several concrete
	// tool calls without forcing the caller to populate the args payload with
	// a backend:tool tuple themselves.
	g := IssuedGrant{
		Tool:    "*",
		Backend: "fs",
		Args: map[string]Predicate{
			"_tool": {ToolInSet: []string{"fs:write_file", "fs:append_file"}},
		},
	}
	call := GrantCall{Tool: "write_file", Backend: "fs"}
	res := MatchGrants(call, []IssuedGrant{g})
	if !res.Allowed {
		t.Fatalf("expected allow, got deny dim=%s detail=%s", res.DenyDim, res.Detail)
	}
	// Tool not in the set must deny via the args dimension.
	deny := MatchGrants(GrantCall{Tool: "read_file", Backend: "fs"}, []IssuedGrant{g})
	if deny.Allowed {
		t.Fatal("expected deny for fs:read_file (not in tool_in_set)")
	}
	if deny.DenyDim != GrantDenyArgs {
		t.Fatalf("expected args-dim deny, got %s", deny.DenyDim)
	}
}

// itoa avoids strconv for the few sites in this test file that need it.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

// TestToolInSet_ValidateSortsForCanonicalHash confirms that ValidateToolInSet
// canonicalizes its input by sorting in place. Two predicates with the same
// entries in different orders must produce identical tool_in_set slices after
// validation, which is the precondition for ComputeTemplateHash dedup.
func TestToolInSet_ValidateSortsForCanonicalHash(t *testing.T) {
	a := []string{"fs:write_file", "fs:append_file", "fs:create_dir"}
	b := []string{"fs:create_dir", "fs:write_file", "fs:append_file"}
	if err := ValidateToolInSet(a); err != nil {
		t.Fatalf("ValidateToolInSet(a) err=%v", err)
	}
	if err := ValidateToolInSet(b); err != nil {
		t.Fatalf("ValidateToolInSet(b) err=%v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("length mismatch a=%v b=%v", a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("entries differ after sort at [%d]: a=%v b=%v", i, a, b)
		}
	}
	for i := 1; i < len(a); i++ {
		if a[i-1] > a[i] {
			t.Fatalf("entries not sorted after Validate: %v", a)
		}
	}
}
