package admin

import (
	"reflect"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/auth"
)

func TestRenderDisplaySummary_VerbResolvesLabel(t *testing.T) {
	spec := CapabilitySpec{Action: ActionSpec{Mode: "verb", VerbSlug: "write-files"}}
	got := renderDisplaySummary(spec)
	if !strings.Contains(got, "write files") {
		t.Fatalf("summary missing verb label: %q", got)
	}
	if strings.Contains(got, "write-files") {
		t.Fatalf("summary leaked slug: %q", got)
	}
	// task-46: the "Can " prefix was dropped — the row presentation now
	// conveys effect via the ALLOWED/DENIED section grouping plus a
	// color-coded left border, so the prose no longer pre-judges effect.
	if strings.HasPrefix(got, "Can ") {
		t.Fatalf("summary leaked 'Can ' prefix: %q", got)
	}
}

func TestRenderDisplaySummary_AgentHome(t *testing.T) {
	spec := CapabilitySpec{
		Action: ActionSpec{Mode: "verb", VerbSlug: "write-files"},
		Where:  &WhereSpec{Mode: "agent_home"},
	}
	got := renderDisplaySummary(spec)
	want := "write files in /workspace/${agent}/"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderDisplaySummary_FullStack(t *testing.T) {
	spec := CapabilitySpec{
		Action:    ActionSpec{Mode: "tool", Backend: "github", Tool: "create_issue"},
		Where:     &WhereSpec{Mode: "path_prefix", PathPrefix: "acme/"},
		When:      &WhenSpec{Mode: "business", Timezone: "America/Toronto"},
		HowSecure: &HowSecureSpec{Mode: "mfa", MfaFreshnessSec: 600},
	}
	got := renderDisplaySummary(spec)
	wantParts := []string{"call github.create_issue", "in acme/", "business hours", "with MFA"}
	for _, p := range wantParts {
		if !strings.Contains(got, p) {
			t.Errorf("summary missing %q in %q", p, got)
		}
	}
}

func TestRenderChips_OrderMatchesSpec(t *testing.T) {
	// Spec §5.2: where → storage → time → freshness → auth manner.
	// Build a spec that touches every chip kind and assert ordering.
	spec := CapabilitySpec{
		Action:    ActionSpec{Mode: "tool", Backend: "fs", Tool: "write_file"},
		Where:     &WhereSpec{Mode: "path_prefix", PathPrefix: "/srv/"},
		When:      &WhenSpec{Mode: "business", Timezone: "UTC"},
		HowSecure: &HowSecureSpec{Mode: "mfa", MfaFreshnessSec: 300},
	}
	chips := renderChips(spec)
	if len(chips) != 3 {
		t.Fatalf("expected 3 chips (where, time, freshness), got %d: %+v", len(chips), chips)
	}
	wantKinds := []string{"where", "time", "freshness"}
	for i, want := range wantKinds {
		if chips[i].Kind != want {
			t.Errorf("chip[%d].Kind = %q want %q", i, chips[i].Kind, want)
		}
	}
}

func TestRenderChips_StorageChipWhenSharedOrEphemeral(t *testing.T) {
	spec := CapabilitySpec{
		Action: ActionSpec{Mode: "tool", Backend: "fs", Tool: "write_file"},
		Where:  &WhereSpec{Mode: "shared"},
	}
	chips := renderChips(spec)
	if len(chips) != 1 || chips[0].Kind != "storage" {
		t.Fatalf("expected storage chip, got %+v", chips)
	}
}

func TestRenderChips_MfaDpopUsesAuthManner(t *testing.T) {
	spec := CapabilitySpec{
		Action:    ActionSpec{Mode: "tool", Backend: "fs", Tool: "write_file"},
		HowSecure: &HowSecureSpec{Mode: "mfa_dpop", MfaFreshnessSec: 600},
	}
	chips := renderChips(spec)
	if len(chips) != 1 || chips[0].Kind != "auth_manner" {
		t.Fatalf("expected auth_manner chip, got %+v", chips)
	}
	if !strings.Contains(chips[0].Label, "DPoP") {
		t.Fatalf("label missing DPoP: %q", chips[0].Label)
	}
}

func TestSpecFromScope_BackendWildcard(t *testing.T) {
	spec := specFromScope("github:*")
	if spec.Action.Mode != "backend_wildcard" || spec.Action.Backend != "github" {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestSpecFromScope_SpecificTool(t *testing.T) {
	spec := specFromScope("fs:read_file")
	if spec.Action.Mode != "tool" || spec.Action.Backend != "fs" || spec.Action.Tool != "read_file" {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestSpecFromGrantTemplate_VerbRoundtrip(t *testing.T) {
	t.Helper()
	// Build a template that mimics what compileToGrantSpec produces for a
	// "write-files" verb + agent_home where clause.
	pfx := "/workspace/${agent.prism_id}/"
	entries := []string{"fs:append_file", "fs:create_dir", "fs:delete_file", "fs:write_file"}
	tmpl := auth.GrantTemplate{
		Spec: auth.GrantSpec{
			Type:    auth.GrantTypeMCPCall,
			Tool:    "*",
			Backend: "*",
			Args: map[string]auth.Predicate{
				"_tool": {ToolInSet: entries},
				"path":  {Prefix: &pfx},
			},
		},
	}
	binding := auth.GrantBinding{
		Subjects: auth.SubjectSelector{Groups: []string{"engineering"}},
	}
	spec := specFromGrantTemplate(tmpl, binding)
	if spec.Action.Mode != "verb" || spec.Action.VerbSlug != "write-files" {
		t.Fatalf("expected verb=write-files, got %+v", spec.Action)
	}
	if spec.Where == nil || spec.Where.Mode != "agent_home" {
		t.Fatalf("expected agent_home where, got %+v", spec.Where)
	}
}

func TestSpecFromGrantTemplate_AdvancedRoundtrip(t *testing.T) {
	// Template with a non-_tool/non-path predicate, role required, and a
	// workspace write_mode constraint — all must round-trip into Advanced.
	wmEq := "rw"
	wm := auth.Predicate{Equals: wmEq}
	tmpl := auth.GrantTemplate{
		Spec: auth.GrantSpec{
			Type:    auth.GrantTypeMCPCall,
			Tool:    "deploy",
			Backend: "k8s",
			Args: map[string]auth.Predicate{
				"env": {Equals: "prod"},
			},
			Workspace: auth.WorkspaceConstraint{
				WriteMode: &wm,
			},
		},
	}
	binding := auth.GrantBinding{
		Subjects: auth.SubjectSelector{Roles: []string{"senior"}, RoleRequired: "approver"},
	}
	spec := specFromGrantTemplate(tmpl, binding)
	if spec.Advanced == nil {
		t.Fatalf("expected Advanced to be populated, got nil")
	}
	if spec.Advanced.RoleRequired != "approver" {
		t.Fatalf("expected role_required=approver, got %q", spec.Advanced.RoleRequired)
	}
	if _, ok := spec.Advanced.Args["env"]; !ok {
		t.Fatalf("expected env arg in Advanced, got %+v", spec.Advanced.Args)
	}
	if spec.Advanced.Workspace == nil || spec.Advanced.Workspace.WriteMode == nil {
		t.Fatalf("expected workspace.write_mode preserved, got %+v", spec.Advanced.Workspace)
	}
}

func TestMatchVerbFromToolInSet_NoMatchReturnsEmpty(t *testing.T) {
	got := matchVerbFromToolInSet([]string{"fs:write_file", "fs:not_a_verb_tool"})
	if got != "" {
		t.Fatalf("expected empty match, got %q", got)
	}
}

func TestMatchVerbFromToolInSet_WriteFilesMatches(t *testing.T) {
	// Both fs and filesystem patterns are part of "write-files"; supply the
	// full union so the matcher recognizes it.
	v, _ := FindVerb("write-files")
	got := matchVerbFromToolInSet(expandVerbAllBackends(v))
	if got != "write-files" {
		t.Fatalf("expected write-files, got %q", got)
	}
}

// TestComposeCapabilityViews_IncludesDenyScopes (task-46) covers the
// requirement that AgentPolicy.Deny entries are surfaced as Effect="deny"
// capability rows alongside the existing Effect="allow" rows. Verifies:
//
//   - readSubjectScopes returns both lists
//   - composeCapabilityViews emits a deny row per deny scope
//   - deny rows carry Source="scope", Effect="deny", and ids prefixed with
//     "scope-deny-"
//   - role-prefix sentinels in Deny are filtered (defensive)
func TestComposeCapabilityViews_IncludesDenyScopes(t *testing.T) {
	env := newPolicyTestEnv(t)
	const prismID = "prism-agent-1"
	// Seed an AgentPolicy with both allow and deny scopes plus a stray
	// role: entry on each side that must NOT surface as a capability row.
	if err := env.agentMgr.SetAgentPolicy(prismID,
		[]string{"engineering"},
		[]string{"fs:write_file", "role:senior"},
		[]string{"github:delete_repo", "role:bogus"},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	views, err := env.api.composeCapabilityViews(subjectTypeAgents, prismID)
	if err != nil {
		t.Fatalf("composeCapabilityViews: %v", err)
	}

	var allows, denies []CapabilityView
	for _, v := range views {
		switch v.Effect {
		case capabilityEffectAllow:
			allows = append(allows, v)
		case capabilityEffectDeny:
			denies = append(denies, v)
		default:
			t.Fatalf("unexpected effect %q on view %+v", v.Effect, v)
		}
	}
	if len(allows) != 1 {
		t.Fatalf("expected 1 allow row, got %d: %+v", len(allows), allows)
	}
	if len(denies) != 1 {
		t.Fatalf("expected 1 deny row (role: filtered), got %d: %+v", len(denies), denies)
	}
	deny := denies[0]
	if deny.Source != capabilitySourceScope {
		t.Errorf("deny.Source = %q, want %q", deny.Source, capabilitySourceScope)
	}
	if !strings.HasPrefix(deny.ID, scopeDenyIDPrefix) {
		t.Errorf("deny.ID = %q, expected prefix %q", deny.ID, scopeDenyIDPrefix)
	}
	if deny.Spec.Action.Backend != "github" || deny.Spec.Action.Tool != "delete_repo" {
		t.Errorf("deny spec mismatch: %+v", deny.Spec.Action)
	}
	// readSubjectScopes returns (allow, deny) — sanity-check the helper
	// directly so future regressions can't sneak through composeCapabilityViews.
	allow, denyScopes, err := env.api.readSubjectScopes(subjectTypeAgents, prismID)
	if err != nil {
		t.Fatalf("readSubjectScopes: %v", err)
	}
	if len(allow) != 1 || allow[0] != "fs:write_file" {
		t.Errorf("allow scopes = %v, want [fs:write_file]", allow)
	}
	if len(denyScopes) != 1 || denyScopes[0] != "github:delete_repo" {
		t.Errorf("deny scopes = %v, want [github:delete_repo]", denyScopes)
	}
}

func TestExpandVerbAllBackends_WildcardSentinel(t *testing.T) {
	v, _ := FindVerb("deploy")
	got := expandVerbAllBackends(v)
	want := []string{"*:deploy", "*:release", "*:rollout"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
