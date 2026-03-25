// Package middleware provides HTTP middleware for auth, rate limiting, and circuit breaking.
package middleware

import (
	"net/http"
)

// AuthConfig configures the Auth middleware.
type AuthConfig struct {
	Header    string
	ValidKeys []string
}

// Auth returns a Middleware that validates an API key from a request header.
func Auth(cfg AuthConfig) Middleware {
	header := cfg.Header
	if header == "" {
		header = "X-API-Key"
	}

	keySet := make(map[string]struct{}, len(cfg.ValidKeys))
	for _, k := range cfg.ValidKeys {
		keySet[k] = struct{}{}
	}
	allowAll := len(cfg.ValidKeys) == 0

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !allowAll {
				key := r.Header.Get(header)
				if _, ok := keySet[key]; !ok {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
