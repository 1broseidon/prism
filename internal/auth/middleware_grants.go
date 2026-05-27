package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

const GrantDenyDPoPRequired = "dpop_required"

// GrantCallInput is the gateway-normalized state needed to enforce grants.
type GrantCallInput struct {
	Claims      *Claims
	AuthInfo    *RequestAuthInfo
	Tool        string
	Backend     string
	Arguments   json.RawMessage
	Workspace   *WorkspaceInstance
	Now         time.Time
	Emitter     Emitter
	RequestID   string
	RequestPath string
	CompatGate  *BearerCompatGate
}

// GrantPipelineResult is the authorization decision for a tool call.
type GrantPipelineResult struct {
	Allowed bool
	Legacy  bool

	Status    int
	Error     string
	AcrValues string
	DenyDim   string

	Event GrantEvent
	Match GrantMatchResult
}

// EnforceGrantCall evaluates token grants for one resolved tool call.
func EnforceGrantCall(ctx context.Context, in GrantCallInput) GrantPipelineResult {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	claims := in.Claims
	if claims == nil {
		claims = ClaimsFromContext(ctx)
	}
	authInfo := in.AuthInfo
	if authInfo == nil {
		authInfo = RequestAuthInfoFromContext(ctx)
	}

	base := GrantEvent{
		Timestamp:     now,
		RequestID:     in.RequestID,
		Backend:       in.Backend,
		Tool:          in.Tool,
		CallArgsHash:  hashGrantArgs(in.Arguments),
		MatchedIndex:  -1,
		WorkspaceID:   workspaceField(in.Workspace, "id"),
		WorkspaceType: workspaceField(in.Workspace, "type"),
	}
	if claims != nil {
		base.AgentID = claims.PrismID
		base.ClientID = claims.ClientID
		base.AuthTime = unixTime(claims.AuthTime)
		base.Acr = claims.Acr
		base.TokenJTI = claims.JTI
	}
	if authInfo != nil {
		base.DPoPjkt = authInfo.DPoPJKT
	}

	if claims == nil || len(claims.AuthorizationDetails) == 0 {
		return GrantPipelineResult{Allowed: true, Legacy: true, Event: base}
	}
	gate := in.CompatGate
	if gate == nil {
		gate = DefaultBearerCompatGate()
	}
	decision := gate.Evaluate(claims, authInfo, in.RequestPath, authInfo != nil && authInfo.BearerCompatWarned)
	if !decision.Allowed {
		base.Outcome = "denied"
		base.Trace = traceForDeny(GrantDenyDPoPRequired, "", nil)
		emitGrantEvent(ctx, in.Emitter, base)
		return GrantPipelineResult{
			Allowed: false, Status: http.StatusUnauthorized, Error: "dpop_required",
			DenyDim: GrantDenyDPoPRequired, Event: base,
		}
	}

	match := MatchGrant(GrantCall{
		Tool:      in.Tool,
		Backend:   in.Backend,
		Arguments: in.Arguments,
		Workspace: in.Workspace,
		Now:       now,
		AuthTime:  claimAuthTime(claims),
		Acr:       claimACR(claims),
	}, claims.AuthorizationDetails)

	base.MatchedIndex = match.MatchedIndex
	base.Trace = traceForMatch(match)
	if match.DenyDim == GrantDenyWorkspaceDrift {
		_, base.Trace.Drift = CompareWorkspace(grantWorkspace(match.Grant), in.Workspace)
	}
	if match.Grant != nil {
		base.TemplateID = match.Grant.TemplateID
		base.TemplateHash = match.Grant.TemplateHash
	}
	if match.Allowed {
		base.Outcome = "allowed"
		emitGrantEvent(ctx, in.Emitter, base)
		return GrantPipelineResult{Allowed: true, Event: base, Match: match}
	}

	base.Outcome = "denied"
	emitGrantEvent(ctx, in.Emitter, base)
	status, code, acr := challengeForDeny(match)
	return GrantPipelineResult{
		Allowed: false, Status: status, Error: code, AcrValues: acr,
		DenyDim: match.DenyDim, Event: base, Match: match,
	}
}

func grantsRequireDPoP(grants []IssuedGrant) bool {
	for _, grant := range grants {
		if grant.CnfRequired {
			return true
		}
	}
	return false
}

func challengeForDeny(match GrantMatchResult) (int, string, string) {
	switch match.DenyDim {
	case GrantDenyNeedsStepUp, GrantDenyACRRequired:
		acr := ""
		if match.Grant != nil {
			acr = match.Grant.AcrRequired
		}
		return http.StatusUnauthorized, "insufficient_user_authentication", acr
	case GrantDenyArgs, GrantDenyOutOfWindow, GrantDenyNotYet, GrantDenyExpired, GrantDenyWorkspaceDrift:
		return http.StatusUnauthorized, "policy_mismatch", ""
	case GrantDenyDPoPRequired:
		return http.StatusUnauthorized, "dpop_required", ""
	default:
		return http.StatusUnauthorized, "policy_mismatch", ""
	}
}

func traceForMatch(match GrantMatchResult) GrantTrace {
	if match.Allowed {
		return GrantTrace{
			What:    AxisResult{Verdict: "pass"},
			Context: AxisResult{Verdict: "pass"},
			When:    AxisResult{Verdict: "pass"},
			How:     AxisResult{Verdict: "pass"},
		}
	}
	return traceForDeny(match.DenyDim, match.Detail, match.Grant)
}

func traceForDeny(deny, detail string, grant *IssuedGrant) GrantTrace {
	trace := GrantTrace{DenyDim: deny}
	fail := AxisResult{Verdict: "fail", Detail: detail}
	pass := AxisResult{Verdict: "pass"}
	trace.What, trace.Context, trace.When, trace.How = pass, pass, pass, pass
	switch deny {
	case GrantDenyArgs, GrantDenyWorkspaceDrift:
		trace.Context = fail
	case GrantDenyNotYet, GrantDenyExpired, GrantDenyOutOfWindow, GrantDenyNeedsStepUp:
		trace.When = fail
	case GrantDenyACRRequired, GrantDenyDPoPRequired:
		trace.How = fail
	default:
		trace.What = fail
	}
	return trace
}

func emitGrantEvent(ctx context.Context, emitter Emitter, event GrantEvent) {
	if emitter != nil {
		emitter.Emit(ctx, event)
	}
}

func hashGrantArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	sum := sha256.Sum256(args)
	return "sha256-" + hex.EncodeToString(sum[:])
}

func workspaceField(ws *WorkspaceInstance, field string) string {
	if ws == nil {
		return ""
	}
	switch field {
	case "id":
		return ws.ID
	case "type":
		return ws.Type
	default:
		return ""
	}
}

func grantWorkspace(grant *IssuedGrant) *WorkspaceInstance {
	if grant == nil {
		return nil
	}
	return grant.Workspace
}

func claimAuthTime(claims *Claims) int64 {
	if claims == nil {
		return 0
	}
	return claims.AuthTime
}

func claimACR(claims *Claims) string {
	if claims == nil {
		return ""
	}
	return claims.Acr
}

func unixTime(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}
