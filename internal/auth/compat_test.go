package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWWWAuthenticateDPoPWireShape(t *testing.T) {
	required := `[{"type":"prism.mcp.call","tool":"fs.write_file"}]`
	rec := httptest.NewRecorder()
	writeWWWAuthenticateChallenge(rec, "", http.StatusUnauthorized, "DPoP", map[string]string{
		"error":                          "insufficient_authorization_details",
		"resource":                       "https://prism.example/mcp",
		"authorization_details_required": required,
	}, true)
	want := `DPoP realm="prism", error="insufficient_authorization_details", resource="https://prism.example/mcp", authorization_details_required="[{\"type\":\"prism.mcp.call\",\"tool\":\"fs.write_file\"}]"`
	if got := rec.Header().Get("WWW-Authenticate"); got != want {
		t.Fatalf("challenge = %q, want %q", got, want)
	}
	if rec.Header().Get("DPoP-Nonce") == "" {
		t.Fatal("missing DPoP-Nonce")
	}

	rec = httptest.NewRecorder()
	writeWWWAuthenticateChallenge(rec, "", http.StatusUnauthorized, "DPoP", map[string]string{"error": "dpop_required"}, true)
	if got, want := rec.Header().Get("WWW-Authenticate"), `DPoP realm="prism", error="dpop_required"`; got != want {
		t.Fatalf("challenge = %q, want %q", got, want)
	}

	rec = httptest.NewRecorder()
	writeWWWAuthenticateChallenge(rec, "", http.StatusUnauthorized, "DPoP", map[string]string{
		"error":      "insufficient_user_authentication",
		"acr_values": "urn:prism:mfa",
	}, false)
	if got, want := rec.Header().Get("WWW-Authenticate"), `DPoP realm="prism", error="insufficient_user_authentication", acr_values="urn:prism:mfa"`; got != want {
		t.Fatalf("challenge = %q, want %q", got, want)
	}
}

func TestCompatGateWarnAllowsBearerGrantAndLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	gate := NewBearerCompatGate(BearerCompatWarn, logger)
	grant := matchingGrant()
	grant.CnfRequired = false
	claims := grantPipelineClaims(grant)
	claims.Cnf = nil
	claims.JTI = "token-jti"

	result := EnforceGrantCall(context.Background(), GrantCallInput{
		Claims:      claims,
		AuthInfo:    &RequestAuthInfo{Scheme: AuthSchemeBearer},
		Tool:        "fs.write_file",
		Backend:     "local",
		Arguments:   json.RawMessage(`{"path":"/tmp/ok.txt"}`),
		Workspace:   grant.Workspace,
		Now:         time.Unix(1_800_000_000, 0),
		CompatGate:  gate,
		RequestPath: "/mcp",
	})
	if !result.Allowed {
		t.Fatalf("result = %+v", result)
	}
	logged := buf.String()
	if !strings.Contains(logged, "token-jti") || !strings.Contains(logged, "sha256-template") || !strings.Contains(logged, "/mcp") {
		t.Fatalf("warn log missing triage fields: %s", logged)
	}
}

func TestCompatGateWarnDoesNotOverrideCnfRequired(t *testing.T) {
	result := EnforceGrantCall(context.Background(), GrantCallInput{
		Claims:     grantPipelineClaims(matchingGrant()),
		AuthInfo:   &RequestAuthInfo{Scheme: AuthSchemeBearer},
		Tool:       "fs.write_file",
		Backend:    "local",
		Arguments:  json.RawMessage(`{"path":"/tmp/ok.txt"}`),
		Workspace:  &WorkspaceInstance{ID: "ws-1", Type: "ephemeral", WriteMode: "stage"},
		Now:        time.Unix(1_800_000_000, 0),
		CompatGate: NewBearerCompatGate(BearerCompatWarn, nil),
	})
	if result.Allowed || result.Error != "dpop_required" {
		t.Fatalf("result = %+v", result)
	}
}

func TestCompatGateDenyRejectsAnyBearerGrant(t *testing.T) {
	grant := matchingGrant()
	grant.CnfRequired = false
	claims := grantPipelineClaims(grant)
	claims.Cnf = nil
	result := EnforceGrantCall(context.Background(), GrantCallInput{
		Claims:     claims,
		AuthInfo:   &RequestAuthInfo{Scheme: AuthSchemeBearer},
		Tool:       "fs.write_file",
		Backend:    "local",
		Arguments:  json.RawMessage(`{"path":"/tmp/ok.txt"}`),
		Workspace:  grant.Workspace,
		Now:        time.Unix(1_800_000_000, 0),
		CompatGate: NewBearerCompatGate(BearerCompatDeny, nil),
	})
	if result.Allowed || result.Error != "dpop_required" {
		t.Fatalf("result = %+v", result)
	}
}

func TestCompatGateLegacyBearerUnaffected(t *testing.T) {
	for _, mode := range []string{BearerCompatWarn, BearerCompatDeny} {
		result := EnforceGrantCall(context.Background(), GrantCallInput{
			Claims:     &Claims{ClientID: "ci-agent", Scope: "fs:write_file"},
			AuthInfo:   &RequestAuthInfo{Scheme: AuthSchemeBearer},
			Tool:       "fs.write_file",
			Backend:    "local",
			Arguments:  json.RawMessage(`{"path":"/tmp/ok.txt"}`),
			CompatGate: NewBearerCompatGate(mode, nil),
		})
		if !result.Allowed || !result.Legacy {
			t.Fatalf("mode %s result = %+v", mode, result)
		}
	}
}

func TestGateHotReload(t *testing.T) {
	grant := matchingGrant()
	grant.CnfRequired = false
	claims := grantPipelineClaims(grant)
	claims.Cnf = nil
	gate := NewBearerCompatGate(BearerCompatWarn, nil)
	input := GrantCallInput{
		Claims:     claims,
		AuthInfo:   &RequestAuthInfo{Scheme: AuthSchemeBearer},
		Tool:       "fs.write_file",
		Backend:    "local",
		Arguments:  json.RawMessage(`{"path":"/tmp/ok.txt"}`),
		Workspace:  grant.Workspace,
		Now:        time.Unix(1_800_000_000, 0),
		CompatGate: gate,
	}
	if result := EnforceGrantCall(context.Background(), input); !result.Allowed {
		t.Fatalf("warn result = %+v", result)
	}
	gate.Update(BearerCompatDeny)
	if result := EnforceGrantCall(context.Background(), input); result.Allowed || result.Error != "dpop_required" {
		t.Fatalf("deny result = %+v", result)
	}
}
