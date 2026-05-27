package auth

import "context"

// Auth scheme identifiers as they appear in the HTTP Authorization header.
const (
	AuthSchemeBearer = "Bearer"
	AuthSchemeDPoP   = "DPoP"
)

// RequestAuthInfo carries per-request authentication metadata the gateway
// needs to make grant decisions. It is populated by the auth middleware
// and consumed by the grant pipeline + compat gate.
type RequestAuthInfo struct {
	// Scheme is the HTTP authorization scheme that delivered the token.
	// One of AuthSchemeBearer or AuthSchemeDPoP.
	Scheme string

	// DPoPJKT is the RFC 7638 thumbprint of the DPoP proof key, set when
	// Scheme == AuthSchemeDPoP and the proof validated cleanly.
	DPoPJKT string

	// BearerCompatWarned indicates the auth layer already emitted a
	// bearer-compat warning for this request. The grant pipeline uses
	// this to suppress duplicate per-tool-call warnings.
	BearerCompatWarned bool
}

type authInfoCtxKey struct{}

// ContextWithRequestAuthInfo returns ctx with info attached.
func ContextWithRequestAuthInfo(ctx context.Context, info *RequestAuthInfo) context.Context {
	if info == nil {
		return ctx
	}
	return context.WithValue(ctx, authInfoCtxKey{}, info)
}

// RequestAuthInfoFromContext returns the auth info previously stored on
// ctx, or nil if none was attached.
func RequestAuthInfoFromContext(ctx context.Context) *RequestAuthInfo {
	info, _ := ctx.Value(authInfoCtxKey{}).(*RequestAuthInfo)
	return info
}
