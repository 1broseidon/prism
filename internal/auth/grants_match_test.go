package auth

import (
	"encoding/json"
	"testing"
	"time"
)

func TestGrantsMatchFullPipeline(t *testing.T) {
	prefix := "/workspace/a/"
	now := time.Unix(1_000, 0)
	grant := IssuedGrant{
		Type:             GrantTypeMCPCall,
		TemplateID:       "tmpl",
		TemplateHash:     "sha256-x",
		Tool:             "fs.write_file",
		Backend:          "local",
		Args:             map[string]Predicate{"path": {Prefix: &prefix}},
		NotBefore:        now.Add(-time.Minute).Unix(),
		ExpiresAt:        now.Add(time.Minute).Unix(),
		AuthFreshnessMax: 60,
		AcrRequired:      "urn:prism:mfa",
		Workspace:        &WorkspaceInstance{ID: "ws-a", Type: "ephemeral", WriteMode: "stage"},
	}
	got := MatchGrant(CallContext{
		Tool:      grant.Tool,
		Backend:   grant.Backend,
		Arguments: json.RawMessage(`{"path":"/workspace/a/file.txt"}`),
		Workspace: grant.Workspace,
		Now:       now,
		AuthTime:  now.Add(-60 * time.Second).Unix(),
		Acr:       "urn:prism:mfa",
	}, []IssuedGrant{grant})
	if !got.Allowed {
		t.Fatalf("expected allow, got %+v", got)
	}
}

func TestGrantsMatchDenyPrecedence(t *testing.T) {
	prefix := "/workspace/a/"
	now := time.Unix(1_000, 0)
	grant := IssuedGrant{
		Type:      GrantTypeMCPCall,
		Tool:      "fs.write_file",
		Backend:   "local",
		Args:      map[string]Predicate{"path": {Prefix: &prefix}},
		ExpiresAt: now.Add(-time.Second).Unix(),
	}
	got := MatchGrant(CallContext{
		Tool:      "fs.write_file",
		Backend:   "local",
		Arguments: json.RawMessage(`{"path":"/tmp/file.txt"}`),
		Now:       now,
	}, []IssuedGrant{grant})
	if got.DenyDim != GrantDenyArgs {
		t.Fatalf("deny = %q, want args", got.DenyDim)
	}
}

func TestGrantsMatchEmptyArgsMeansNoConstraint(t *testing.T) {
	got := MatchGrant(CallContext{
		Tool:      "fs.read_file",
		Backend:   "local",
		Arguments: json.RawMessage(`{}`),
		Now:       time.Unix(1, 0),
	}, []IssuedGrant{{Type: GrantTypeMCPCall, Tool: "fs.read_file", Backend: "local"}})
	if !got.Allowed {
		t.Fatalf("expected allow, got %+v", got)
	}
}

func TestGrantsMatchHoursAndDST(t *testing.T) {
	grant := IssuedGrant{
		Type:    GrantTypeMCPCall,
		Tool:    "deploy.run",
		Backend: "deploy",
		Hours:   "weekdays 09:00-18:00 America/Chicago",
	}
	inside := time.Date(2026, 3, 9, 14, 30, 0, 0, time.UTC) // Monday after DST shift, 09:30 CDT
	got := MatchGrant(CallContext{Tool: grant.Tool, Backend: grant.Backend, Arguments: json.RawMessage(`{}`), Now: inside}, []IssuedGrant{grant})
	if !got.Allowed {
		t.Fatalf("expected inside hours allow, got %+v", got)
	}
	outside := time.Date(2026, 3, 9, 23, 30, 0, 0, time.UTC)
	got = MatchGrant(CallContext{Tool: grant.Tool, Backend: grant.Backend, Arguments: json.RawMessage(`{}`), Now: outside}, []IssuedGrant{grant})
	if got.Allowed || got.DenyDim != GrantDenyOutOfWindow {
		t.Fatalf("expected out_of_window, got %+v", got)
	}
}
