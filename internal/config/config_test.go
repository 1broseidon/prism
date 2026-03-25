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
				"auth": {
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
	if err := os.WriteFile(path, []byte(cfg), 0644); err != nil {
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
	os.WriteFile(path, []byte(cfg), 0644)

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
	os.WriteFile(path, []byte(cfg), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate namespace")
	}
}

func TestLoadNoServers(t *testing.T) {
	cfg := `{"servers": []}`
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(cfg), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty servers")
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg := `{
		"servers": [
			{"id": "test", "url": "http://localhost:3000/mcp"}
		]
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(cfg), 0644)

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
