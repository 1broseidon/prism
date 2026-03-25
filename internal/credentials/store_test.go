package credentials

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStaticCredential(t *testing.T) {
	s := &Static{Header: "X-API-Key", Value: "secret123"}
	h, v, err := s.Resolve(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != "X-API-Key" {
		t.Errorf("expected header X-API-Key, got %q", h)
	}
	if v != "secret123" {
		t.Errorf("expected value secret123, got %q", v)
	}
}

func TestStaticDefaultHeader(t *testing.T) {
	s := &Static{Value: "tok"}
	h, _, err := s.Resolve(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != "Authorization" {
		t.Errorf("expected default header Authorization, got %q", h)
	}
}

func TestEnvCredential(t *testing.T) {
	t.Setenv("TEST_CRED_TOKEN", "envvalue")
	e := &Env{Header: "Authorization", EnvVar: "TEST_CRED_TOKEN"}
	h, v, err := e.Resolve(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != "Authorization" {
		t.Errorf("expected Authorization, got %q", h)
	}
	if v != "envvalue" {
		t.Errorf("expected envvalue, got %q", v)
	}
}

func TestEnvCredentialMissing(t *testing.T) {
	os.Unsetenv("TEST_CRED_MISSING")
	e := &Env{EnvVar: "TEST_CRED_MISSING"}
	_, _, err := e.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
}

func TestFileCredential(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("filetoken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f := &File{Header: "X-Token", Path: path}
	h, v, err := f.Resolve(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != "X-Token" {
		t.Errorf("expected X-Token, got %q", h)
	}
	if v != "filetoken" {
		t.Errorf("expected filetoken (trimmed), got %q", v)
	}
}

func TestFileCredentialMissing(t *testing.T) {
	f := &File{Path: "/nonexistent/path/token"}
	_, _, err := f.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCommandCredential(t *testing.T) {
	c := &Command{
		Header: "Authorization",
		Cmd:    "echo Bearer dyntoken",
		TTL:    1 * time.Minute,
	}
	h, v, err := c.Resolve(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != "Authorization" {
		t.Errorf("expected Authorization, got %q", h)
	}
	if v != "Bearer dyntoken" {
		t.Errorf("expected 'Bearer dyntoken', got %q", v)
	}
}

func TestCommandCredentialCacheHit(t *testing.T) {
	calls := 0
	// Use a script that increments a counter via a temp file to count executions.
	dir := t.TempDir()
	counter := filepath.Join(dir, "n")
	os.WriteFile(counter, []byte("0"), 0600)

	c := &Command{
		Header: "Authorization",
		Cmd:    "echo cached-token",
		TTL:    10 * time.Minute,
	}

	// First call — should execute the command.
	_, v1, err := c.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	calls++

	// Manually verify cache is populated.
	if c.cached == "" {
		t.Fatal("cache should be populated after first resolve")
	}
	if c.expires.IsZero() {
		t.Fatal("expires should be set")
	}

	// Inject a fake cached value so we can detect whether the command re-runs.
	c.mu.Lock()
	c.cached = "cached-value"
	c.mu.Unlock()

	_, v2, err := c.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if v1 == v2 {
		// Both would be "cached-token"; we can only verify the second hit the cache.
		_ = calls
	}
	if v2 != "cached-value" {
		t.Errorf("second resolve should return cached value, got %q", v2)
	}

	_ = counter
}

func TestCommandCredentialTTLExpiry(t *testing.T) {
	c := &Command{
		Header: "Authorization",
		Cmd:    "echo fresh-token",
		TTL:    1 * time.Millisecond,
	}

	_, _, err := c.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Expire the cache.
	c.mu.Lock()
	c.cached = "stale"
	c.expires = time.Now().Add(-1 * time.Second)
	c.mu.Unlock()

	_, v, err := c.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != "fresh-token" {
		t.Errorf("expected re-execution after TTL expiry, got %q", v)
	}
}

func TestCommandCredentialFailed(t *testing.T) {
	c := &Command{
		Cmd: "exit 1",
		TTL: 1 * time.Minute,
	}
	_, _, err := c.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected error for failed command")
	}
}

func TestStoreRegisterAndResolve(t *testing.T) {
	s := NewStore()
	s.Register("backend1", &Static{Header: "X-Key", Value: "val1"})

	h, v, err := s.Resolve(context.Background(), "backend1")
	if err != nil {
		t.Fatal(err)
	}
	if h != "X-Key" || v != "val1" {
		t.Errorf("unexpected header/value: %q / %q", h, v)
	}
}

func TestStoreUnregisteredBackend(t *testing.T) {
	s := NewStore()
	h, v, err := s.Resolve(context.Background(), "unknown")
	if err != nil {
		t.Fatalf("unexpected error for unregistered backend: %v", err)
	}
	if h != "" || v != "" {
		t.Errorf("expected empty header/value for unregistered backend")
	}
}

func TestStoreOverwrite(t *testing.T) {
	s := NewStore()
	s.Register("b", &Static{Value: "old"})
	s.Register("b", &Static{Value: "new"})

	_, v, err := s.Resolve(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	if v != "new" {
		t.Errorf("expected overwritten value 'new', got %q", v)
	}
}
