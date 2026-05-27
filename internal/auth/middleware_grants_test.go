package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
)

type recordingEmitter struct {
	events []GrantEvent
}

func (e *recordingEmitter) Emit(_ context.Context, event GrantEvent) {
	e.events = append(e.events, event)
}

func TestGrantPipelineAllowsMatchingDPoPGrant(t *testing.T) {
	emitter := &recordingEmitter{}
	claims := grantPipelineClaims(matchingGrant())
	result := EnforceGrantCall(context.Background(), GrantCallInput{
		Claims:    claims,
		AuthInfo:  &RequestAuthInfo{Scheme: AuthSchemeDPoP, DPoPJKT: "jkt"},
		Tool:      "fs.write_file",
		Backend:   "local",
		Arguments: json.RawMessage(`{"path":"/tmp/ok.txt"}`),
		Workspace: &WorkspaceInstance{ID: "ws-1", Type: "ephemeral", WriteMode: "stage"},
		Now:       time.Unix(1_800_000_000, 0),
		Emitter:   emitter,
	})
	if !result.Allowed {
		t.Fatalf("allowed = false, deny=%s", result.DenyDim)
	}
	if len(emitter.events) != 1 || emitter.events[0].Outcome != "allowed" {
		t.Fatalf("events = %+v", emitter.events)
	}
}

func TestGrantPipelineDeniesArgsMismatch(t *testing.T) {
	emitter := &recordingEmitter{}
	result := EnforceGrantCall(context.Background(), GrantCallInput{
		Claims:    grantPipelineClaims(matchingGrant()),
		AuthInfo:  &RequestAuthInfo{Scheme: AuthSchemeDPoP, DPoPJKT: "jkt"},
		Tool:      "fs.write_file",
		Backend:   "local",
		Arguments: json.RawMessage(`{"path":"/etc/passwd"}`),
		Workspace: &WorkspaceInstance{ID: "ws-1", Type: "ephemeral", WriteMode: "stage"},
		Now:       time.Unix(1_800_000_000, 0),
		Emitter:   emitter,
	})
	if result.Allowed || result.Error != "policy_mismatch" || result.DenyDim != GrantDenyArgs {
		t.Fatalf("result = %+v", result)
	}
	if len(emitter.events) != 1 || emitter.events[0].Trace.DenyDim != GrantDenyArgs {
		t.Fatalf("events = %+v", emitter.events)
	}
}

func TestGrantPipelineDeniesOutOfWindow(t *testing.T) {
	grant := matchingGrant()
	grant.Args = nil
	grant.Hours = "09:00-10:00 UTC"
	result := EnforceGrantCall(context.Background(), GrantCallInput{
		Claims:    grantPipelineClaims(grant),
		AuthInfo:  &RequestAuthInfo{Scheme: AuthSchemeDPoP, DPoPJKT: "jkt"},
		Tool:      "fs.write_file",
		Backend:   "local",
		Arguments: json.RawMessage(`{}`),
		Workspace: grant.Workspace,
		Now:       time.Date(2026, 5, 18, 20, 0, 0, 0, time.UTC),
	})
	if result.Allowed || result.DenyDim != GrantDenyOutOfWindow || result.Error != "policy_mismatch" {
		t.Fatalf("result = %+v", result)
	}
}

func TestGrantPipelineDeniesStaleAuthTimeWithStepUpChallenge(t *testing.T) {
	grant := matchingGrant()
	grant.AuthFreshnessMax = 60
	grant.AcrRequired = "urn:prism:mfa"
	claims := grantPipelineClaims(grant)
	claims.AuthTime = time.Unix(1_800_000_000, 0).Add(-10 * time.Minute).Unix()
	result := EnforceGrantCall(context.Background(), GrantCallInput{
		Claims:    claims,
		AuthInfo:  &RequestAuthInfo{Scheme: AuthSchemeDPoP, DPoPJKT: "jkt"},
		Tool:      "fs.write_file",
		Backend:   "local",
		Arguments: json.RawMessage(`{"path":"/tmp/ok.txt"}`),
		Workspace: grant.Workspace,
		Now:       time.Unix(1_800_000_000, 0),
	})
	if result.Allowed || result.Error != "insufficient_user_authentication" || result.AcrValues != "urn:prism:mfa" {
		t.Fatalf("result = %+v", result)
	}
}

func TestGrantPipelineDeniesBearerForCnfGrant(t *testing.T) {
	result := EnforceGrantCall(context.Background(), GrantCallInput{
		Claims:    grantPipelineClaims(matchingGrant()),
		AuthInfo:  &RequestAuthInfo{Scheme: AuthSchemeBearer},
		Tool:      "fs.write_file",
		Backend:   "local",
		Arguments: json.RawMessage(`{"path":"/tmp/ok.txt"}`),
		Workspace: &WorkspaceInstance{ID: "ws-1", Type: "ephemeral", WriteMode: "stage"},
		Now:       time.Unix(1_800_000_000, 0),
	})
	if result.Allowed || result.Error != "dpop_required" || result.DenyDim != GrantDenyDPoPRequired {
		t.Fatalf("result = %+v", result)
	}
}

func TestGrantPipelineDPoPProofMismatchAndReplay(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	token := "access-token"
	privA, pubA := testDPoPKey(t, jwa.ES256)
	_, pubB := testDPoPKey(t, jwa.ES256)
	proof := signDPoPProof(t, jwa.ES256, privA, pubA, dpopClaims{
		HTM: "POST", HTU: "http://example.com/mcp", IAT: now, JTI: "resource-jti-1", ATH: AccessTokenHash(token),
	}, "dpop+jwt")
	req := httptest.NewRequest(http.MethodPost, "http://example.com/mcp", nil)
	req.Header.Set("DPoP", proof)
	replay := NewReplayCache(time.Minute, 10)

	parsed, err := validateResourceDPoPProof(req, token, replay, now)
	if err != nil {
		t.Fatal(err)
	}
	otherJKT, err := ComputeJKT(pubB)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Thumbprint == otherJKT {
		t.Fatal("test keys unexpectedly share a thumbprint")
	}
	if _, err := validateResourceDPoPProof(req, token, replay, now); err == nil {
		t.Fatal("expected replay rejection")
	}
}

func TestGrantPipelineWorkspaceDriftTrace(t *testing.T) {
	grant := matchingGrant()
	result := EnforceGrantCall(context.Background(), GrantCallInput{
		Claims:    grantPipelineClaims(grant),
		AuthInfo:  &RequestAuthInfo{Scheme: AuthSchemeDPoP, DPoPJKT: "jkt"},
		Tool:      "fs.write_file",
		Backend:   "local",
		Arguments: json.RawMessage(`{"path":"/tmp/ok.txt"}`),
		Workspace: &WorkspaceInstance{ID: "ws-2", Type: "ephemeral", WriteMode: "stage"},
		Now:       time.Unix(1_800_000_000, 0),
	})
	if result.Allowed || result.DenyDim != GrantDenyWorkspaceDrift || result.Event.Trace.Drift == nil {
		t.Fatalf("result = %+v", result)
	}
	if result.Event.Trace.Drift.GrantHash == "" || result.Event.Trace.Drift.LiveHash == "" {
		t.Fatalf("drift = %+v", result.Event.Trace.Drift)
	}
}

func TestWorkspaceCompare(t *testing.T) {
	grant := &WorkspaceInstance{ID: "repo", Type: "ephemeral", WriteMode: "stage"}
	live := &WorkspaceInstance{ID: "repo", Type: "ephemeral", WriteMode: "stage"}
	if ok, drift := CompareWorkspace(grant, live); !ok || drift != nil {
		t.Fatalf("CompareWorkspace equal = %v, %+v", ok, drift)
	}
	live.Type = "virtual"
	ok, drift := CompareWorkspace(grant, live)
	if ok || drift == nil || drift.GrantHash == "" || drift.LiveHash == "" || drift.GrantHash == drift.LiveHash {
		t.Fatalf("CompareWorkspace drift = %v, %+v", ok, drift)
	}
	ok, drift = CompareWorkspace(grant, nil)
	if ok || drift == nil || drift.LiveHash != "" {
		t.Fatalf("CompareWorkspace nil live = %v, %+v", ok, drift)
	}
}

func TestGrantPipelineLegacyScopeOnlyAllows(t *testing.T) {
	result := EnforceGrantCall(context.Background(), GrantCallInput{
		Claims:    &Claims{ClientID: "ci-agent", Scope: "fs:write_file"},
		AuthInfo:  &RequestAuthInfo{Scheme: AuthSchemeBearer},
		Tool:      "fs.write_file",
		Backend:   "local",
		Arguments: json.RawMessage(`{"path":"/etc/passwd"}`),
	})
	if !result.Allowed || !result.Legacy {
		t.Fatalf("result = %+v", result)
	}
}

func TestGrantPipelineMultiGrantMatchedIndex(t *testing.T) {
	grant := matchingGrant()
	other := grant
	other.Tool = "fs.read_file"
	third := grant
	third.Tool = "fs.delete_file"
	claims := grantPipelineClaims(other, grant, third)
	result := EnforceGrantCall(context.Background(), GrantCallInput{
		Claims:    claims,
		AuthInfo:  &RequestAuthInfo{Scheme: AuthSchemeDPoP, DPoPJKT: "jkt"},
		Tool:      "fs.write_file",
		Backend:   "local",
		Arguments: json.RawMessage(`{"path":"/tmp/ok.txt"}`),
		Workspace: grant.Workspace,
		Now:       time.Unix(1_800_000_000, 0),
	})
	if !result.Allowed || result.Event.MatchedIndex != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func matchingGrant() IssuedGrant {
	prefix := "/tmp/"
	return IssuedGrant{
		Type:         GrantTypeMCPCall,
		TemplateID:   "tpl-write",
		TemplateHash: "sha256-template",
		Tool:         "fs.write_file",
		Backend:      "local",
		Args: map[string]Predicate{
			"path": {Prefix: &prefix},
		},
		Workspace:   &WorkspaceInstance{ID: "ws-1", Type: "ephemeral", WriteMode: "stage"},
		CnfRequired: true,
	}
}

func grantPipelineClaims(grants ...IssuedGrant) *Claims {
	return &Claims{
		ClientID:             "ci-agent",
		PrismID:              "agent-1",
		Scope:                "fs:write_file",
		AuthTime:             time.Unix(1_800_000_000, 0).Unix(),
		Acr:                  "urn:prism:mfa",
		Cnf:                  &Confirmation{JKT: "jkt"},
		AuthorizationDetails: grants,
	}
}
