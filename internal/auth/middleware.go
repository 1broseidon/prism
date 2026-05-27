package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/metrics"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type contextKey string

const (
	claimsKey contextKey = "auth.claims"
	policyKey contextKey = "auth.policy"
)

// ClaimsFromContext returns the validated claims from the request context.
func ClaimsFromContext(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsKey).(*Claims)
	return c
}

// ContextWithClaims returns a derived context that carries the validated
// claims. Use this when a handler validates a token itself rather than
// going through the standard Middleware (e.g., the workspace bridge
// endpoint, which accepts either OAuth or a shared token).
func ContextWithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

// PolicyFromContext returns the access policy from the request context.
func PolicyFromContext(ctx context.Context) *Policy {
	p, _ := ctx.Value(policyKey).(*Policy)
	return p
}

// MiddlewareOption tunes optional behavior on the auth middleware.
type MiddlewareOption func(*middlewareConfig)

type middlewareConfig struct {
	dpopReplay *ReplayCache
	now        func() time.Time
	compatGate *BearerCompatGate
}

// WithDPoPReplayCache wires the DPoP proof replay cache so the middleware
// can reject repeated proofs.
func WithDPoPReplayCache(cache *ReplayCache) MiddlewareOption {
	return func(c *middlewareConfig) { c.dpopReplay = cache }
}

// WithMiddlewareClock injects a clock for DPoP proof iat validation. Tests
// substitute a controllable clock; production leaves this nil so the
// middleware defaults to time.Now.
func WithMiddlewareClock(now func() time.Time) MiddlewareOption {
	return func(c *middlewareConfig) { c.now = now }
}

// WithBearerCompatGate wires the bearer-compat gate consulted before
// granting access on grant-bearing tokens.
func WithBearerCompatGate(gate *BearerCompatGate) MiddlewareOption {
	return func(c *middlewareConfig) { c.compatGate = gate }
}

// Middleware returns an HTTP middleware that validates OAuth 2.1 Bearer or
// DPoP-bound access tokens.
//
// On 401, it returns the WWW-Authenticate header per RFC 9728, including
// the resource_metadata URI so MCP clients can discover the authorization
// server. On 403, it returns insufficient_scope with the required scopes.
//
// Options control DPoP proof validation and the bearer-compat gate.
func Middleware(validator *TokenValidator, resourceURI string, opts ...MiddlewareOption) func(http.Handler) http.Handler {
	cfg := &middlewareConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	tracer := otel.Tracer("prism.auth")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.Start(r.Context(), "prism.auth.validate",
				trace.WithSpanKind(trace.SpanKindInternal),
			)
			defer span.End()

			scheme, token, err := extractAuthToken(r)
			if err != nil {
				span.SetAttributes(attribute.String("auth.result", "error"))
				span.SetStatus(codes.Error, err.Error())
				metrics.RecordAuthValidation("error")
				writeWWWAuthenticate(w, resourceURI, http.StatusUnauthorized,
					"", err.Error())
				return
			}

			claims, policy, err := validator.Validate(ctx, token)
			if err != nil {
				span.SetAttributes(attribute.String("auth.result", "error"))
				span.SetStatus(codes.Error, err.Error())
				metrics.RecordAuthValidation("error")
				writeWWWAuthenticate(w, resourceURI, http.StatusUnauthorized,
					"", err.Error())
				return
			}

			authInfo := &RequestAuthInfo{Scheme: scheme}
			if scheme == AuthSchemeDPoP {
				proofHeader := r.Header.Get("DPoP")
				proof, perr := ParseAndValidateProof(proofHeader, r.Method, absRequestURL(r), "", token, cfg.now(), cfg.dpopReplay)
				if perr != nil {
					span.SetAttributes(attribute.String("auth.result", "error"))
					span.SetStatus(codes.Error, perr.Error())
					metrics.RecordAuthValidation("error")
					if errors.Is(perr, ErrDPoPNonceMismatch) {
						writeWWWAuthenticateChallenge(w, resourceURI, http.StatusUnauthorized, AuthSchemeDPoP, map[string]string{
							"error":             "use_dpop_nonce",
							"error_description": perr.Error(),
						}, true)
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					writeWWWAuthenticateChallenge(w, resourceURI, http.StatusUnauthorized, AuthSchemeDPoP, map[string]string{
						"error":             "invalid_dpop_proof",
						"error_description": perr.Error(),
					}, false)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				authInfo.DPoPJKT = proof.Thumbprint
				// If the access token has a cnf.jkt confirmation, it MUST match
				// the proof's thumbprint per RFC 9449 §6.2.
				if claims.Cnf != nil && claims.Cnf.JKT != "" && claims.Cnf.JKT != proof.Thumbprint {
					writeWWWAuthenticateChallenge(w, resourceURI, http.StatusUnauthorized, AuthSchemeDPoP, map[string]string{
						"error":             "invalid_dpop_proof",
						"error_description": "DPoP key thumbprint mismatch",
					}, false)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
			}

			// Bearer-compat gate: when the token carries grants but the caller
			// presented a plain Bearer, the gate decides whether to allow,
			// warn, or deny. Run before scope policy so denials surface the
			// canonical DPoP-required challenge.
			gate := cfg.compatGate
			if gate == nil {
				gate = DefaultBearerCompatGate()
			}
			decision := gate.Evaluate(claims, authInfo, r.URL.Path, false)
			if !decision.Allowed {
				writeWWWAuthenticateChallenge(w, resourceURI, http.StatusUnauthorized, AuthSchemeDPoP, map[string]string{
					"error":             decision.Error,
					"error_description": "DPoP-bound token required",
				}, false)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if decision.Warned {
				authInfo.BearerCompatWarned = true
			}

			span.SetAttributes(
				attribute.String("auth.client_id", claims.ClientID),
				attribute.Int("auth.scope_count", len(policy.AllowedScopes)),
				attribute.String("auth.result", "ok"),
				attribute.String("auth.scheme", scheme),
			)
			metrics.RecordAuthValidation("ok")

			// Inject claims, policy, and request auth info into request context.
			ctx = context.WithValue(ctx, claimsKey, claims)
			ctx = context.WithValue(ctx, policyKey, policy)
			ctx = ContextWithRequestAuthInfo(ctx, authInfo)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireScope returns a middleware that checks for specific scopes.
// Use this to protect specific endpoints beyond the base token validation.
func RequireScope(scopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			policy := PolicyFromContext(r.Context())
			if policy == nil {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			for _, scope := range scopes {
				found := false
				for s := range policy.AllowedScopes {
					if s == scope || s == "*" {
						found = true
						break
					}
				}
				if !found {
					w.Header().Set("WWW-Authenticate",
						`Bearer error="insufficient_scope", scope=`+fmt.Sprintf("%q", scope),
					)
					http.Error(w, "Forbidden: insufficient scope", http.StatusForbidden)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractAuthToken parses the Authorization header and returns the scheme
// (AuthSchemeBearer or AuthSchemeDPoP) and the raw token. Errors mirror
// ExtractBearerToken so the legacy-Bearer-only path returns the same
// error messages for clients that don't know about DPoP.
func extractAuthToken(r *http.Request) (string, string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", "", errors.New("missing Authorization header")
	}
	switch {
	case strings.HasPrefix(header, "Bearer "):
		tok := strings.TrimPrefix(header, "Bearer ")
		if tok == "" {
			return "", "", errors.New("empty bearer token")
		}
		return AuthSchemeBearer, tok, nil
	case strings.HasPrefix(header, "DPoP "):
		tok := strings.TrimPrefix(header, "DPoP ")
		if tok == "" {
			return "", "", errors.New("empty DPoP token")
		}
		return AuthSchemeDPoP, tok, nil
	default:
		return "", "", errors.New("authorization header must use Bearer or DPoP scheme")
	}
}

// absRequestURL returns the canonical http(s)://host/path form used to
// validate the DPoP `htu` claim. Matches the auth-server's absolute URL
// builder for consistency.
func absRequestURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, r.URL.Path)
}

// writeWWWAuthenticate writes a proper WWW-Authenticate response per MCP spec.
func writeWWWAuthenticate(w http.ResponseWriter, resourceURI string, status int, scope, description string) {
	challenge := "Bearer"
	parts := []string{}

	if resourceURI != "" {
		parts = append(parts, fmt.Sprintf("resource_metadata=%q", protectedResourceMetadataURL(resourceURI)))
	}

	if status == http.StatusForbidden && scope != "" {
		parts = append(parts, `error="insufficient_scope"`, fmt.Sprintf("scope=%q", scope))
	} else if status == http.StatusUnauthorized {
		parts = append(parts, `error="invalid_token"`)
	}

	if description != "" {
		parts = append(parts, fmt.Sprintf("error_description=%q", description))
	}

	if len(parts) > 0 {
		challenge += " " + joinParts(parts)
	}

	w.Header().Set("WWW-Authenticate", challenge)
	if status == http.StatusUnauthorized {
		http.Error(w, "Unauthorized", status)
	} else {
		http.Error(w, "Forbidden", status)
	}
}

func protectedResourceMetadataURL(resourceURI string) string {
	u, err := url.Parse(resourceURI)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(resourceURI, "/") + "/.well-known/oauth-protected-resource"
	}

	resourcePath := strings.TrimRight(u.Path, "/")
	u.Path = "/.well-known/oauth-protected-resource"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	if resourcePath != "" {
		u.Path += resourcePath
	}
	return u.String()
}

func joinParts(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	return result
}
