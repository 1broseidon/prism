package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

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

// PolicyFromContext returns the access policy from the request context.
func PolicyFromContext(ctx context.Context) *Policy {
	p, _ := ctx.Value(policyKey).(*Policy)
	return p
}

// Middleware returns an HTTP middleware that validates OAuth 2.1 Bearer tokens.
//
// On 401, it returns the WWW-Authenticate header per RFC 9728, including
// the resource_metadata URI so MCP clients can discover the authorization server.
//
// On 403, it returns insufficient_scope with the required scopes.
func Middleware(validator *TokenValidator, resourceURI string) func(http.Handler) http.Handler {
	tracer := otel.Tracer("prism.auth")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.Start(r.Context(), "prism.auth.validate",
				trace.WithSpanKind(trace.SpanKindInternal),
			)
			defer span.End()

			token, err := ExtractBearerToken(r)
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

			span.SetAttributes(
				attribute.String("auth.client_id", claims.ClientID),
				attribute.Int("auth.scope_count", len(policy.AllowedScopes)),
				attribute.String("auth.result", "ok"),
			)
			metrics.RecordAuthValidation("ok")

			// Inject claims and policy into request context
			ctx = context.WithValue(ctx, claimsKey, claims)
			ctx = context.WithValue(ctx, policyKey, policy)

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
