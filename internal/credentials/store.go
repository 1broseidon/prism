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
	Resolve(ctx context.Context) (header, value string, err error)
}

// ─── Credential types ────────────────────────────────────────────────────────

// Static returns a fixed header/value pair. Suitable for long-lived API keys
// that don't need rotation.
type Static struct {
	Header string
	Value  string
}

// Resolve returns the static header and value.
func (s *Static) Resolve(_ context.Context) (header, value string, err error) {
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

// Resolve reads the credential from the environment variable.
func (e *Env) Resolve(_ context.Context) (header, value string, err error) {
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

// Resolve reads the credential from the configured file path.
func (f *File) Resolve(_ context.Context) (header, value string, err error) {
	h := f.Header
	if h == "" {
		h = "Authorization"
	}
	data, err := os.ReadFile(f.Path) //nolint:gosec // Path is from trusted config, not user input
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

// Resolve executes the command (or returns a cached result) and returns the credential.
func (c *Command) Resolve(ctx context.Context) (header, value string, err error) {
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
	out, err := exec.CommandContext(ctx, "sh", "-c", c.Cmd).Output() //nolint:gosec // Command is from trusted config
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

// CredentialInfo holds non-secret metadata about a registered credential.
// Use this to display credential status without exposing resolved values.
type CredentialInfo struct {
	Type    string `json:"type"`              // "static", "env", "command", "file"
	Header  string `json:"header"`            // which HTTP header is set
	Env     string `json:"env,omitempty"`     // env var name (env type only)
	Command string `json:"command,omitempty"` // shell command (command type only)
}

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

// Unregister removes the credential for a backend ID.
func (s *Store) Unregister(backendID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.creds, backendID)
}

// Info returns non-secret metadata about a registered credential.
// Returns nil if no credential is registered for backendID.
func (s *Store) Info(backendID string) *CredentialInfo {
	s.mu.RLock()
	cred, ok := s.creds[backendID]
	s.mu.RUnlock()

	if !ok {
		return nil
	}

	info := &CredentialInfo{}
	switch c := cred.(type) {
	case *Static:
		info.Type = "static"
		info.Header = c.Header
	case *Env:
		info.Type = "env"
		info.Header = c.Header
		info.Env = c.EnvVar
	case *File:
		info.Type = "file"
		info.Header = c.Header
	case *Command:
		info.Type = "command"
		info.Header = c.Header
		info.Command = c.Cmd
	case *OAuth:
		info.Type = "oauth"
		info.Header = c.header
	default:
		info.Type = "unknown"
	}
	if info.Header == "" {
		info.Header = "Authorization"
	}
	return info
}

// Resolve returns the header name and value for the given backend.
// If no credential is registered for backendID, it returns empty strings
// and no error — callers should skip injection in that case.
func (s *Store) Resolve(ctx context.Context, backendID string) (header, value string, err error) {
	s.mu.RLock()
	cred, ok := s.creds[backendID]
	s.mu.RUnlock()

	if !ok {
		return "", "", nil
	}
	return cred.Resolve(ctx)
}
