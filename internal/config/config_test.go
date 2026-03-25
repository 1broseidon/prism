package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValid(t *testing.T) {
	cfg := `{
		"listen_addr": ":8080",
		"admin_addr": ":9090",
		"servers": [
			{
				"id": "github",
				"url": "http://localhost:3001/mcp",
				"namespace": "gh"
			},
			{
				"id": "filesystem",
				"url": "http://localhost:3002/mcp",
				"namespace": "fs",
				"credentials": {
					"type": "static",
					"header": "Authorization",
					"value": "Bearer token123"
				}
			}
		],
		"auth": {
			"header": "X-API-Key",
			"valid_keys": ["test-key"]
		},
		"rate_limit": {
			"requests_per_second": 100,
			"burst": 200
		}
	}`

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(c.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(c.Servers))
	}
	if c.Servers[0].Namespace != "gh" {
		t.Errorf("expected namespace 'gh', got %q", c.Servers[0].Namespace)
	}
	if c.Auth == nil || len(c.Auth.ValidKeys) != 1 {
		t.Errorf("expected 1 valid key")
	}
}

func TestLoadDuplicateID(t *testing.T) {
	cfg := `{
		"servers": [
			{"id": "a", "url": "http://localhost:1/mcp"},
			{"id": "a", "url": "http://localhost:2/mcp"}
		]
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(cfg), 0o600)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate id")
	}
}

func TestLoadDuplicateNamespace(t *testing.T) {
	cfg := `{
		"servers": [
			{"id": "a", "url": "http://localhost:1/mcp", "namespace": "same"},
			{"id": "b", "url": "http://localhost:2/mcp", "namespace": "same"}
		]
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(cfg), 0o600)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate namespace")
	}
}

func TestLoadNoServers(t *testing.T) {
	cfg := `{"servers": []}`
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(cfg), 0o600)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty servers")
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCredentialStatic(t *testing.T) {
	path := writeConfig(t, `{
		"servers": [{"id": "s", "url": "http://localhost:1/mcp",
			"credentials": {"type":"static","value":"tok"}}]
	}`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Servers[0].Credentials.Type != "static" {
		t.Errorf("expected type static")
	}
}

func TestCredentialStaticMissingValue(t *testing.T) {
	path := writeConfig(t, `{
		"servers": [{"id": "s", "url": "http://localhost:1/mcp",
			"credentials": {"type":"static"}}]
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error: static requires value")
	}
}

func TestCredentialEnv(t *testing.T) {
	path := writeConfig(t, `{
		"servers": [{"id": "s", "url": "http://localhost:1/mcp",
			"credentials": {"type":"env","env_var":"MY_TOKEN"}}]
	}`)
	_, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCredentialEnvMissingVar(t *testing.T) {
	path := writeConfig(t, `{
		"servers": [{"id": "s", "url": "http://localhost:1/mcp",
			"credentials": {"type":"env"}}]
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error: env requires env_var")
	}
}

func TestCredentialFile(t *testing.T) {
	path := writeConfig(t, `{
		"servers": [{"id": "s", "url": "http://localhost:1/mcp",
			"credentials": {"type":"file","path":"/run/secrets/token"}}]
	}`)
	_, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCredentialFileMissingPath(t *testing.T) {
	path := writeConfig(t, `{
		"servers": [{"id": "s", "url": "http://localhost:1/mcp",
			"credentials": {"type":"file"}}]
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error: file requires path")
	}
}

func TestCredentialCommand(t *testing.T) {
	path := writeConfig(t, `{
		"servers": [{"id": "s", "url": "http://localhost:1/mcp",
			"credentials": {"type":"command","command":"vault kv get -field=token secret/api","ttl":"5m"}}]
	}`)
	_, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCredentialCommandMissingCmd(t *testing.T) {
	path := writeConfig(t, `{
		"servers": [{"id": "s", "url": "http://localhost:1/mcp",
			"credentials": {"type":"command"}}]
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error: command requires command field")
	}
}

func TestCredentialUnknownType(t *testing.T) {
	path := writeConfig(t, `{
		"servers": [{"id": "s", "url": "http://localhost:1/mcp",
			"credentials": {"type":"magic"}}]
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error: unknown credential type")
	}
}

func TestCredentialMissingType(t *testing.T) {
	path := writeConfig(t, `{
		"servers": [{"id": "s", "url": "http://localhost:1/mcp",
			"credentials": {}}]
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error: type is required")
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg := `{
		"servers": [
			{"id": "test", "url": "http://localhost:3000/mcp"}
		]
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(cfg), 0o600)

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if c.ListenAddr != ":8080" {
		t.Errorf("expected default listen_addr :8080, got %q", c.ListenAddr)
	}
	if c.AdminAddr != ":9090" {
		t.Errorf("expected default admin_addr :9090, got %q", c.AdminAddr)
	}
	if c.Servers[0].Namespace != "test" {
		t.Errorf("expected namespace to default to id 'test', got %q", c.Servers[0].Namespace)
	}
}
