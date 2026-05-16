package adminauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// HandleLogin starts an OIDC flow. Generates state + nonce, persists the
// attempt, and redirects the browser to the provider's authorize endpoint.
//
// Optional ?return=<path> query parameter sets where to land after a
// successful sign-in. Defaults to "/".
func (s *Service) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		http.Error(w, "auth not configured", http.StatusNotFound)
		return
	}

	state, err := randomToken(32)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken(16)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	returnURL := r.URL.Query().Get("return")
	if returnURL == "" || !isSafeReturn(returnURL) {
		returnURL = "/"
	}

	if err := s.store.PutLoginAttempt(&LoginAttempt{
		State:     state,
		Nonce:     nonce,
		ReturnURL: returnURL,
		CreatedAt: time.Now(),
	}); err != nil {
		s.logger.Error("persist login attempt", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	authURL := s.oauth.AuthCodeURL(state, oidc.Nonce(nonce))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// HandleCallback completes an OIDC flow. Validates state, exchanges the code,
// verifies the ID token, resolves the operator's role, creates a session, and
// redirects to the original return URL.
func (s *Service) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		http.Error(w, "auth not configured", http.StatusNotFound)
		return
	}

	state, code, attempt, ok := s.parseCallback(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	idToken, ok := s.exchangeAndVerify(ctx, w, code, attempt.Nonce)
	if !ok {
		return
	}

	email, name, groups, err := s.extractClaims(idToken)
	if err != nil || email == "" {
		http.Error(w, "could not read user claims", http.StatusBadRequest)
		return
	}

	role := resolveRole(email, groups, s.cfg.Rules)
	if role == "" {
		s.logger.Warn("sign-in denied: no matching rbac rule", "email", email)
		http.Error(w, "access denied: your account is not in the allowlist", http.StatusForbidden)
		return
	}

	sess, ok := s.createSession(w, email, name, idToken.Subject, role)
	if !ok {
		return
	}

	s.setSessionCookie(w, sess)
	s.logger.Info("admin signed in", "email", email, "role", role, "state", state)
	http.Redirect(w, r, attempt.ReturnURL, http.StatusFound)
}

// parseCallback validates the query, consumes the login attempt, and returns
// state/code/attempt. Writes an error response and returns ok=false on failure.
func (s *Service) parseCallback(w http.ResponseWriter, r *http.Request) (state, code string, attempt *LoginAttempt, ok bool) {
	q := r.URL.Query()
	if errParam := q.Get("error"); errParam != "" {
		s.logger.Warn("oidc callback returned error", "error", errParam, "desc", q.Get("error_description"))
		http.Error(w, "sign-in canceled or denied", http.StatusBadRequest)
		return "", "", nil, false
	}
	state = q.Get("state")
	code = q.Get("code")
	if state == "" || code == "" {
		http.Error(w, "missing state or code", http.StatusBadRequest)
		return "", "", nil, false
	}
	attempt, err := s.store.TakeLoginAttempt(state, loginAttemptMaxAge)
	if err != nil || attempt == nil {
		http.Error(w, "invalid or expired sign-in attempt", http.StatusBadRequest)
		return "", "", nil, false
	}
	return state, code, attempt, true
}

// exchangeAndVerify trades the code for tokens and verifies the id_token.
// Returns the verified token, or ok=false after writing an error response.
func (s *Service) exchangeAndVerify(ctx context.Context, w http.ResponseWriter, code, expectedNonce string) (*oidc.IDToken, bool) {
	tokens, err := s.oauth.Exchange(ctx, code)
	if err != nil {
		s.logger.Warn("oauth code exchange failed", "error", err)
		http.Error(w, "sign-in failed", http.StatusBadRequest)
		return nil, false
	}
	rawIDToken, ok := tokens.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		http.Error(w, "provider did not return an id_token", http.StatusBadRequest)
		return nil, false
	}
	idToken, err := s.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		s.logger.Warn("id_token verification failed", "error", err)
		http.Error(w, "sign-in failed", http.StatusBadRequest)
		return nil, false
	}
	if idToken.Nonce != expectedNonce {
		http.Error(w, "nonce mismatch", http.StatusBadRequest)
		return nil, false
	}
	return idToken, true
}

// createSession mints a session ID, persists it, and returns it. Writes an
// error response and returns ok=false on failure.
func (s *Service) createSession(w http.ResponseWriter, email, name, subject string, role Role) (*Session, bool) {
	id, err := randomToken(32)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	now := time.Now()
	sess := &Session{
		ID:        id,
		Email:     email,
		Name:      name,
		Subject:   subject,
		Role:      role,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.SessionTTL()),
	}
	if err := s.store.PutSession(sess); err != nil {
		s.logger.Error("persist session", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	return sess, true
}

// HandleLogout invalidates the current session and clears the cookie.
func (s *Service) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		_ = s.store.DeleteSession(c.Value)
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// HandleMe returns the current operator's identity + role. Used by the SPA
// to drive the login screen vs. dashboard split.
func (s *Service) HandleMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"auth":  "open",
			"role":  string(RoleAdmin),
			"email": "",
		})
		return
	}
	sess, ok := s.authenticate(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"auth":   "required",
			"issuer": s.cfg.Issuer,
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"auth":       "session",
		"email":      sess.Email,
		"name":       sess.Name,
		"role":       string(sess.Role),
		"expires_at": sess.ExpiresAt,
	})
}

func (s *Service) extractClaims(idToken *oidc.IDToken) (email, name string, groups []string, err error) {
	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", nil, fmt.Errorf("decode id_token claims: %w", err)
	}
	// Groups claim name is configurable per provider.
	var raw map[string]any
	if err := idToken.Claims(&raw); err == nil {
		if v, ok := raw[s.cfg.GroupsClaim]; ok {
			groups = toStringSlice(v)
		}
	}
	return claims.Email, claims.Name, groups, nil
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	case string:
		return []string{t}
	default:
		return nil
	}
}

// isSafeReturn keeps the post-login redirect target on the same host.
// Accepts only absolute paths starting with "/", rejects "//" (protocol-relative).
func isSafeReturn(target string) bool {
	if target == "" || target[0] != '/' {
		return false
	}
	if len(target) > 1 && target[1] == '/' {
		return false
	}
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	return u.Scheme == "" && u.Host == ""
}

func (s *Service) setSessionCookie(w http.ResponseWriter, sess *Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		Domain:   s.cfg.CookieDomain,
		Expires:  sess.ExpiresAt,
		MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Domain:   s.cfg.CookieDomain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}
