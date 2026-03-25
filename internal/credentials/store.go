// Package credentials provides a credential broker for Prism.
//
// Backends register credentials by ID. At call time, Prism resolves the
// credential and injects it into the outbound HTTP request. The agent never
// receives raw API keys — it only ever presents its own OAuth token to Prism.
package credentials

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Credential resolves an HTTP header name and value at call time.
// Implementations are free to cache, rotate, or shell out as needed.
type Credential interface {
	Resolve(ctx context.Context) (header string, value string, err error)
}

// ─── Credential types ────────────────────────────────────────────────────────

// Static returns a fixed header/value pair. Suitable for long-lived API keys
// that don't need rotation.
type Static struct {
	Header string
	Value  string
}

func (s *Static) Resolve(_ context.Context) (string, string, error) {
	h := s.Header
	if h == "" {
		h = "Authorization"
	}
	return h, s.Value, nil
}

// Env resolves the credential from an environment variable at call time.
// This avoids baking secrets into the config file — mount them as env vars
// instead (e.g. from a Kubernetes Secret or Docker secret).
type Env struct {
	Header string
	EnvVar string
}

func (e *Env) Resolve(_ context.Context) (string, string, error) {
	h := e.Header
	if h == "" {
		h = "Authorization"
	}
	val := os.Getenv(e.EnvVar)
	if val == "" {
		return "", "", fmt.Errorf("credential env var %q is not set or empty", e.EnvVar)
	}
	return h, val, nil
}

// File reads the credential value from a file at call time.
// Suitable for Kubernetes service account tokens, mounted secrets, etc.
type File struct {
	Header string
	Path   string
}

func (f *File) Resolve(_ context.Context) (string, string, error) {
	h := f.Header
	if h == "" {
		h = "Authorization"
	}
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return "", "", fmt.Errorf("read credential file %q: %w", f.Path, err)
	}
	return h, strings.TrimSpace(string(data)), nil
}

// Command executes a shell command and uses its stdout as the credential value.
// Suitable for dynamic tokens: AWS STS, Vault CLI, gcloud, etc.
// Results are cached for TTL to avoid shelling out on every tool call.
type Command struct {
	Header  string
	Cmd     string
	TTL     time.Duration
	mu      sync.Mutex
	cached  string
	expires time.Time
}

func (c *Command) Resolve(ctx context.Context) (string, string, error) {
	h := c.Header
	if h == "" {
		h = "Authorization"
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cached != "" && time.Now().Before(c.expires) {
		return h, c.cached, nil
	}

	// Execute the command via the shell so that pipes, env vars, etc. work.
	out, err := exec.CommandContext(ctx, "sh", "-c", c.Cmd).Output()
	if err != nil {
		return "", "", fmt.Errorf("credential command %q failed: %w", c.Cmd, err)
	}

	val := strings.TrimSpace(string(out))
	if val == "" {
		return "", "", fmt.Errorf("credential command %q produced empty output", c.Cmd)
	}

	c.cached = val
	ttl := c.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute // sensible default
	}
	c.expires = time.Now().Add(ttl)

	return h, val, nil
}

// ─── Store ───────────────────────────────────────────────────────────────────

// Store maps backend IDs to their Credential implementations.
// All methods are safe for concurrent use.
type Store struct {
	mu    sync.RWMutex
	creds map[string]Credential
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{creds: make(map[string]Credential)}
}

// Register associates a Credential with a backend ID.
// Registering over an existing ID replaces the previous credential.
func (s *Store) Register(backendID string, cred Credential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creds[backendID] = cred
}

// Resolve returns the header name and value for the given backend.
// If no credential is registered for backendID, it returns empty strings
// and no error — callers should skip injection in that case.
func (s *Store) Resolve(ctx context.Context, backendID string) (header string, value string, err error) {
	s.mu.RLock()
	cred, ok := s.creds[backendID]
	s.mu.RUnlock()

	if !ok {
		return "", "", nil
	}
	return cred.Resolve(ctx)
}
