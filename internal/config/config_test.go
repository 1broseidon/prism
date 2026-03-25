package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8080",
		"admin": ":9090",
		"mcpServers": {
			"github": {
				"url": "http://localhost:3001/mcp"
			},
			"fs": {
				"url": "http://localhost:3002/mcp",
				"credentials": {
					"value": "Bearer token123"
				}
			}
		}
	}`)

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(c.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(c.Servers))
	}
}

func TestLoadStdioBackend(t *testing.T) {
	path := writeConfig(t, `{
		"mcpServers": {
			"fs": {
				"command": "npx",
				"args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
				"env": {"DEBUG": "true"}
			}
		}
	}`)

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(c.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(c.Servers))
	}
	srv := c.Servers[0]
	if !srv.IsStdio() {
		t.Error("expected stdio backend")
	}
	if len(srv.Command) != 4 {
		t.Errorf("expected 4 command parts, got %d: %v", len(srv.Command), srv.Command)
	}
}

func TestLoadWithPolicy(t *testing.T) {
	path := writeConfig(t, `{
		"mcpServers": {
			"fs": { "url": "http://localhost:3001/mcp" }
		},
		"policy": {
			"agents": {
				"ci-agent": { "secret": "s3cret", "groups": ["deployers"] },
				"analyst": { "secret": "s3cret2" }
			},
			"groups": {
				"deployers": { "scopes": ["fs:*"] }
			},
			"default_scopes": ["fs:read_file"]
		}
	}`)

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if c.EmbeddedAuth == nil {
		t.Fatal("expected EmbeddedAuth to be set")
	}
	if len(c.EmbeddedAuth.Clients) != 2 {
		t.Fatalf("expected 2 clients, got %d", len(c.EmbeddedAuth.Clients))
	}

	// ci-agent should have fs:* + mcp:connect from group
	// analyst should have fs:read_file + mcp:connect from default_scopes
	for _, client := range c.EmbeddedAuth.Clients {
		hasMCPConnect := false
		for _, s := range client.AllowedScopes {
			if s == "mcp:connect" {
				hasMCPConnect = true
			}
		}
		if !hasMCPConnect {
			t.Errorf("client %s missing mcp:connect scope", client.ClientID)
		}
	}
}

func TestLoadNoServers(t *testing.T) {
	path := writeConfig(t, `{"mcpServers": {}}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty mcpServers")
	}
}

func TestLoadBothCommandAndURL(t *testing.T) {
	path := writeConfig(t, `{
		"mcpServers": {
			"bad": { "command": "foo", "url": "http://localhost:3001/mcp" }
		}
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for both command and url")
	}
}

func TestLoadNeitherCommandNorURL(t *testing.T) {
	path := writeConfig(t, `{
		"mcpServers": { "bad": {} }
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for neither command nor url")
	}
}

func TestLoadCredentialInferred(t *testing.T) {
	tests := []struct {
		name     string
		cred     string
		wantType string
	}{
		{"static", `"credentials": {"value": "tok"}`, "static"},
		{"env", `"credentials": {"env": "MY_TOKEN"}`, "env"},
		{"file", `"credentials": {"file": "/run/secrets/token"}`, "file"},
		{"command", `"credentials": {"command": "vault kv get -field=token secret/api"}`, "command"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, `{
				"mcpServers": {
					"s": { "url": "http://localhost:1/mcp", `+tc.cred+` }
				}
			}`)
			c, err := Load(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := c.Servers[0].Credentials.InferredType()
			if got != tc.wantType {
				t.Errorf("expected type %q, got %q", tc.wantType, got)
			}
		})
	}
}

func TestLoadCredentialEmpty(t *testing.T) {
	path := writeConfig(t, `{
		"mcpServers": {
			"s": { "url": "http://localhost:1/mcp", "credentials": {} }
		}
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error: empty credentials")
	}
}

func TestLoadCredentialsOnStdioRejected(t *testing.T) {
	path := writeConfig(t, `{
		"mcpServers": {
			"s": { "command": "echo", "credentials": {"value": "tok"} }
		}
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error: credentials on stdio backend")
	}
}

func TestLoadPolicyUnknownGroup(t *testing.T) {
	path := writeConfig(t, `{
		"mcpServers": { "s": { "url": "http://localhost:1/mcp" } },
		"policy": {
			"agents": { "a": { "secret": "s", "groups": ["nonexistent"] } }
		}
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown group reference")
	}
}

func TestLoadDefaults(t *testing.T) {
	path := writeConfig(t, `{
		"mcpServers": { "test": { "url": "http://localhost:3000/mcp" } }
	}`)

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if c.Listen != ":8080" {
		t.Errorf("expected default listen :8080, got %q", c.Listen)
	}
	if c.Admin != ":9090" {
		t.Errorf("expected default admin :9090, got %q", c.Admin)
	}
	if c.Servers[0].Namespace != "test" {
		t.Errorf("expected namespace to default to key 'test', got %q", c.Servers[0].Namespace)
	}
}

func TestLoadGrantDeny(t *testing.T) {
	path := writeConfig(t, `{
		"mcpServers": { "s": { "url": "http://localhost:1/mcp" } },
		"policy": {
			"agents": {
				"agent1": {
					"secret": "s",
					"groups": ["readers"],
					"grant": ["admin:restart"],
					"deny": ["fs:write_file"]
				}
			},
			"groups": {
				"readers": { "scopes": ["fs:read_file", "fs:write_file"] }
			}
		}
	}`)

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	var agent *EmbeddedClient
	for i, cl := range c.EmbeddedAuth.Clients {
		if cl.ClientID == "agent1" {
			agent = &c.EmbeddedAuth.Clients[i]
		}
	}
	if agent == nil {
		t.Fatal("agent1 not found")
	}

	scopeSet := make(map[string]struct{})
	for _, s := range agent.AllowedScopes {
		scopeSet[s] = struct{}{}
	}

	if _, ok := scopeSet["admin:restart"]; !ok {
		t.Error("expected admin:restart from grant")
	}
	if _, ok := scopeSet["fs:write_file"]; ok {
		t.Error("expected fs:write_file to be denied")
	}
	if _, ok := scopeSet["fs:read_file"]; !ok {
		t.Error("expected fs:read_file from group")
	}
	if _, ok := scopeSet["mcp:connect"]; !ok {
		t.Error("expected mcp:connect to be auto-added")
	}
}
