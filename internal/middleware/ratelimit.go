package middleware

import (
	"net/http"

	"golang.org/x/time/rate"
)

// RateLimitConfig configures the RateLimit middleware.
type RateLimitConfig struct {
	RequestsPerSecond float64
	Burst             int
}

// RateLimit returns a Middleware that limits request throughput using a token bucket.
func RateLimit(cfg RateLimitConfig) Middleware {
	limiter := rate.NewLimiter(rate.Limit(cfg.RequestsPerSecond), cfg.Burst)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
