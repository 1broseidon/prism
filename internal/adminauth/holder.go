package adminauth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/store"
)

// Holder wraps the active *Service so it can be swapped at runtime when an
// operator toggles admin auth on/off or saves new OIDC config from the
// console. The zero-value Holder is a valid "auth disabled" state — every
// middleware call passes through, matching the open-mode semantics.
type Holder struct {
	v       atomic.Pointer[Service]
	kv      store.Store
	logger  *slog.Logger
	limiter *loginRateLimiter
}

// NewHolder returns a Holder with no active service (open mode).
func NewHolder(kv store.Store, logger *slog.Logger) *Holder {
	return &Holder{
		kv:      kv,
		logger:  logger,
		limiter: newLoginRateLimiter(),
	}
}

// LoginAllowed wraps the per-IP token bucket. Exposed so the admin handler
// can apply rate limiting even before reaching the (optional) Service.
func (h *Holder) LoginAllowed(r *http.Request) bool {
	if h == nil || h.limiter == nil {
		return true
	}
	return h.limiter.allow(clientIP(r))
}

// Get returns the currently active *Service. A nil result means auth is
// disabled — middleware should pass requests through.
func (h *Holder) Get() *Service {
	if h == nil {
		return nil
	}
	return h.v.Load()
}

// KV returns the underlying KV store. Exposed so the admin API can persist
// admin auth state through the same handle the Holder uses for sessions.
func (h *Holder) KV() store.Store {
	if h == nil {
		return nil
	}
	return h.kv
}

// Enabled reports whether admin auth is currently active.
func (h *Holder) Enabled() bool { return h.Get() != nil }

// Reload performs OIDC discovery against cfg and, on success, swaps the
// active service. Any in-flight requests using the previous service keep
// working — they hold their own *Service pointer for the request lifetime.
//
// Returns an error if discovery fails; the active service is unchanged.
func (h *Holder) Reload(ctx context.Context, cfg *config.AdminAuthConfig) error {
	if h == nil {
		return errors.New("nil holder")
	}
	if cfg == nil {
		return errors.New("nil admin auth config")
	}
	if h.kv == nil {
		return errors.New("kv store is required to enable admin auth")
	}
	svc, err := NewService(ctx, cfg, h.kv, h.logger)
	if err != nil {
		return err
	}
	if svc == nil {
		return errors.New("admin auth config produced no service")
	}
	h.v.Store(svc)
	return nil
}

// Disable clears the active service. Subsequent middleware calls pass through.
func (h *Holder) Disable() {
	if h == nil {
		return
	}
	h.v.Store(nil)
}

// RequireSession admits any signed-in operator. When auth is disabled, the
// middleware is a pass-through. Each request reads the current service
// pointer once, so an in-flight request that began before a swap completes
// against the previous service.
func (h *Holder) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Get().RequireSession(next).ServeHTTP(w, r)
	})
}

// RequireAdmin admits only operators with the admin role. Pass-through when
// auth is disabled.
func (h *Holder) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Get().RequireAdmin(next).ServeHTTP(w, r)
	})
}
