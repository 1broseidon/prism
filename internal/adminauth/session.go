// Package adminauth gates the admin console behind an OIDC operator login.
//
// When config.AdminAuth is nil the package's middleware is a no-op — the admin
// API runs open, suitable for trusted home-lab networks. When configured, all
// admin routes (besides the OIDC handlers themselves and the SPA shell)
// require a valid session cookie. Read-only routes accept any authenticated
// session; mutation routes require the "admin" role.
package adminauth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/1broseidon/prism/internal/store"
)

const (
	sessionKVPrefix = "adminauth/session/"
	loginKVPrefix   = "adminauth/login/"

	sessionCookieName = "prism_session"
)

// Role is the authorization level granted to a signed-in operator.
type Role string

// Role values granted to signed-in operators.
const (
	RoleAdmin  Role = "admin"
	RoleViewer Role = "viewer"
)

// Session is an authenticated admin operator's persisted state.
type Session struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name,omitempty"`
	Subject   string    `json:"sub,omitempty"`
	Role      Role      `json:"role"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Expired reports whether the session is past its expires_at.
func (s *Session) Expired(now time.Time) bool {
	return now.After(s.ExpiresAt)
}

// LoginAttempt records a pending OIDC redirect so we can match the callback.
type LoginAttempt struct {
	State     string    `json:"state"`
	Nonce     string    `json:"nonce"`
	ReturnURL string    `json:"return_url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store wraps a KV backend with the read/write helpers adminauth needs.
type Store struct {
	kv store.Store
}

// NewStore returns a Store backed by the given KV.
func NewStore(kv store.Store) *Store {
	return &Store{kv: kv}
}

// PutSession persists a session under its ID.
func (s *Store) PutSession(sess *Session) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return s.kv.Set(sessionKVPrefix+sess.ID, data)
}

// GetSession returns the session, or (nil, nil) if missing or expired.
// Expired sessions are deleted lazily.
func (s *Store) GetSession(id string) (*Session, error) {
	data, err := s.kv.Get(sessionKVPrefix + id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session: %w", err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		// Corrupted entry — best to drop it.
		_ = s.kv.Delete(sessionKVPrefix + id)
		return nil, nil
	}
	if sess.Expired(time.Now()) {
		_ = s.kv.Delete(sessionKVPrefix + id)
		return nil, nil
	}
	return &sess, nil
}

// DeleteSession removes a session by ID; missing keys are not an error.
func (s *Store) DeleteSession(id string) error {
	return s.kv.Delete(sessionKVPrefix + id)
}

// PutLoginAttempt persists a pending OIDC redirect keyed by state.
func (s *Store) PutLoginAttempt(attempt *LoginAttempt) error {
	data, err := json.Marshal(attempt)
	if err != nil {
		return fmt.Errorf("marshal login attempt: %w", err)
	}
	return s.kv.Set(loginKVPrefix+attempt.State, data)
}

// TakeLoginAttempt returns and removes the attempt in a single call.
// Returns (nil, nil) if the attempt doesn't exist or has aged out.
func (s *Store) TakeLoginAttempt(state string, maxAge time.Duration) (*LoginAttempt, error) {
	key := loginKVPrefix + state
	data, err := s.kv.Get(key)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("read login attempt: %w", err)
	}
	// One-time use: delete immediately, regardless of validity.
	_ = s.kv.Delete(key)

	var a LoginAttempt
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, nil
	}
	if time.Since(a.CreatedAt) > maxAge {
		return nil, nil
	}
	return &a, nil
}

// randomToken returns a cryptographically random URL-safe token.
// Used for session IDs, OAuth state values, and nonces.
func randomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
