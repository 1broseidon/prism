package auth

import (
	"log/slog"
	"sync/atomic"
)

const (
	BearerCompatWarn = "warn"
	BearerCompatDeny = "deny"
)

type bearerCompatSnapshot struct {
	mode string
}

// BearerCompatGate controls the migration from scope-only Bearer tokens to
// DPoP-bound grant-bearing tokens.
type BearerCompatGate struct {
	snapshot atomic.Pointer[bearerCompatSnapshot]
	logger   *slog.Logger
}

type BearerCompatDecision struct {
	Allowed bool
	Warned  bool
	Error   string
}

var defaultBearerCompatGate = NewBearerCompatGate(BearerCompatWarn, nil)

func DefaultBearerCompatGate() *BearerCompatGate {
	return defaultBearerCompatGate
}

func NewBearerCompatGate(mode string, logger *slog.Logger) *BearerCompatGate {
	g := &BearerCompatGate{logger: logger}
	g.Update(mode)
	return g
}

func (g *BearerCompatGate) Update(mode string) {
	if g == nil {
		return
	}
	normalized := NormalizeBearerCompatMode(mode)
	g.snapshot.Store(&bearerCompatSnapshot{mode: normalized})
}

func (g *BearerCompatGate) Mode() string {
	if g == nil {
		return BearerCompatWarn
	}
	snap := g.snapshot.Load()
	if snap == nil || snap.mode == "" {
		return BearerCompatWarn
	}
	return snap.mode
}

func (g *BearerCompatGate) Evaluate(claims *Claims, authInfo *RequestAuthInfo, requestPath string, suppressWarn bool) BearerCompatDecision {
	if claims == nil || len(claims.AuthorizationDetails) == 0 {
		return BearerCompatDecision{Allowed: true}
	}
	if authInfo == nil || authInfo.Scheme != AuthSchemeBearer {
		return BearerCompatDecision{Allowed: true}
	}
	if claims.Cnf != nil && claims.Cnf.JKT != "" {
		return BearerCompatDecision{Allowed: false, Error: "dpop_required"}
	}
	if grantsRequireDPoP(claims.AuthorizationDetails) {
		return BearerCompatDecision{Allowed: false, Error: "dpop_required"}
	}
	if g.Mode() == BearerCompatDeny {
		return BearerCompatDecision{Allowed: false, Error: "dpop_required"}
	}
	if g != nil && g.logger != nil && !suppressWarn {
		g.logger.Warn("Bearer token used with grant-bearing token",
			"token_jti", claims.JTI,
			"template_hashes", grantTemplateHashes(claims.AuthorizationDetails),
			"path", requestPath,
		)
		return BearerCompatDecision{Allowed: true, Warned: true}
	}
	return BearerCompatDecision{Allowed: true}
}

func NormalizeBearerCompatMode(mode string) string {
	switch mode {
	case BearerCompatDeny:
		return BearerCompatDeny
	default:
		return BearerCompatWarn
	}
}

func grantTemplateHashes(grants []IssuedGrant) []string {
	out := make([]string, 0, len(grants))
	for _, grant := range grants {
		if grant.TemplateHash != "" {
			out = append(out, grant.TemplateHash)
		}
	}
	return out
}
