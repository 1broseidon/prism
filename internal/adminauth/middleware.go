package adminauth

import (
	"context"
	"net/http"
)

type ctxKey int

const sessionCtxKey ctxKey = 0

// FromContext returns the authenticated session attached to the request, or nil.
func FromContext(ctx context.Context) *Session {
	if v, ok := ctx.Value(sessionCtxKey).(*Session); ok {
		return v
	}
	return nil
}

// RequireSession admits any signed-in operator (admin or viewer). Anonymous
// requests get 401. The session is attached to the request context for handlers
// to read via FromContext.
//
// On a nil *Service (auth disabled), the middleware is a pass-through.
func (s *Service) RequireSession(next http.Handler) http.Handler {
	if s == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.authenticate(r)
		if !ok {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionCtxKey, sess)))
	})
}

// RequireAdmin admits only operators with the admin role. Viewers get 403.
// Anonymous requests get 401.
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	if s == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.authenticate(r)
		if !ok {
			writeUnauthorized(w)
			return
		}
		if sess.Role != RoleAdmin {
			writeForbidden(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionCtxKey, sess)))
	})
}

// authenticate resolves the session cookie. Returns (nil, false) for any
// problem — caller writes the appropriate response.
func (s *Service) authenticate(r *http.Request) (*Session, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	sess, err := s.store.GetSession(c.Value)
	if err != nil || sess == nil {
		return nil, false
	}
	return sess, true
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="prism-admin"`)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}

func writeForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"forbidden: admin role required"}`))
}
