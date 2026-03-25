# Prism

An MCP gateway that governs how AI agents access backend services.

Agents authenticate once to Prism with an OAuth token. Prism resolves per-backend credentials, enforces scope-based access control, and produces a structured audit trail. The agent never sees raw API keys.

```
Agent ──→ Prism (OAuth) ──→ GitHub MCP    (injected $GITHUB_TOKEN)
                         ──→ Postgres MCP  (injected vault secret)
                         ──→ K8s MCP       (injected service account)
```

## Why

MCP servers are multiplying. Each one needs credentials, access control, and observability. Without a gateway:

- Agents hold raw API keys (and can leak them)
- Every MCP server implements its own auth
- There's no central audit trail of what agents actually did
- Revoking access means updating every server

Prism solves this by sitting between agents and MCP servers, acting as both an MCP server (facing agents) and an MCP client (facing backends).

## Features

| Feature | Description |
|---|---|
| **Credential brokering** | 4 credential types: static, env var, file, shell command (with TTL cache). Agent never sees raw values. |
| **OAuth 2.1 + RFC 9728** | Token validation, audience checking, scope enforcement, Protected Resource Metadata discovery. |
| **Scope-filtered discovery** | `tools/list` only returns tools the agent is authorized to use. No information leakage. |
| **Structured audit log** | Every tool call — allowed or denied — produces a single-line JSON entry for SIEM ingestion. |
| **Namespace aggregation** | N backends appear as one MCP server. Tools are prefixed: `github__create_issue`, `fs__read_file`. |
| **Circuit breaking** | Per-backend failure isolation. A down backend doesn't take out the gateway. |
| **Rate limiting** | Global and per-backend token bucket rate limiting. |
| **Admin API** | Health checks, backend status, uptime — on a separate port. |
| **Single binary** | One 12MB binary, one JSON config file, no runtime dependencies. |

## Quick Start

### Build

```bash
git clone https://github.com/prism-gateway/prism.git
cd prism
go build -o prism ./cmd/prism
```

### Minimal Config (API Key Auth)

Create `config.json`:

```json
{
  "listen_addr": ":8080",
  "admin_addr": ":9090",
  "servers": [
    {
      "id": "github",
      "url": "http://localhost:3001/mcp",
      "namespace": "github",
      "credentials": {
        "type": "env",
        "header": "Authorization",
        "env_var": "GITHUB_TOKEN"
      }
    }
  ],
  "auth": {
    "header": "X-API-Key",
    "valid_keys": ["your-secret-key"]
  },
  "audit": {
    "enabled": true,
    "output": "stderr"
  }
}
```

### Run

```bash
export GITHUB_TOKEN="Bearer ghp_your_token"
./prism -config config.json
```

Prism is now listening on `:8080`. Connect any MCP client to `http://localhost:8080/mcp`.

### Verify

```bash
# Health check
curl http://localhost:9090/health

# Backend status
curl http://localhost:9090/backends

# Gateway info
curl http://localhost:9090/info
```

## Configuration Reference

### Top-Level

| Field | Type | Default | Description |
|---|---|---|---|
| `listen_addr` | string | `:8080` | MCP gateway listen address |
| `admin_addr` | string | `:9090` | Admin API listen address |
| `servers` | array | required | Backend MCP servers to connect to |
| `auth` | object | none | Client-facing authentication |
| `audit` | object | none | Structured audit logging |
| `rate_limit` | object | none | Global rate limiting |
| `resource_uri` | string | none | Canonical URI for RFC 9728 discovery |
| `shutdown_timeout` | duration | `10s` | Graceful shutdown timeout |

### Server Config

Each entry in `servers`:

| Field | Type | Default | Description |
|---|---|---|---|
| `id` | string | required | Unique backend identifier |
| `url` | string | required | Backend MCP server URL (must include `/mcp` path) |
| `namespace` | string | `id` | Namespace prefix for tools (e.g. `github__create_issue`) |
| `credentials` | object | none | Backend credential configuration |
| `timeout` | duration | `30s` | Per-request timeout to this backend |
| `circuit_breaker` | object | none | Circuit breaker settings |
| `rate_limit` | object | none | Per-backend rate limiting |

### Credential Types

Credentials are resolved at call time and injected into outbound HTTP requests. The agent never sees the raw value.

**Static** — fixed value, suitable for long-lived API keys:
```json
{
  "type": "static",
  "header": "X-API-Key",
  "value": "sk_live_your_key"
}
```

**Environment variable** — resolved at call time:
```json
{
  "type": "env",
  "header": "Authorization",
  "env_var": "GITHUB_TOKEN"
}
```

**File** — read from disk (Kubernetes mounted secrets, service account tokens):
```json
{
  "type": "file",
  "header": "Authorization",
  "path": "/var/run/secrets/kubernetes.io/serviceaccount/token"
}
```

**Command** — execute a shell command, cache the result with TTL:
```json
{
  "type": "command",
  "header": "Authorization",
  "command": "vault kv get -field=token secret/mcp/github",
  "ttl": "5m"
}
```

The `command` type caches stdout for the configured TTL (default 5 minutes), then re-executes. This works with Vault, AWS STS, `gcloud auth print-access-token`, or any CLI that outputs a credential.

### Authentication

#### API Key (development / internal)

```json
{
  "auth": {
    "header": "X-API-Key",
    "valid_keys": ["key-1", "key-2"]
  }
}
```

#### OAuth 2.1 (production / agentic)

```json
{
  "auth": {
    "oauth": {
      "issuer_url": "https://auth.example.com/realms/mcp",
      "audience": "https://prism.example.com",
      "required_scopes": ["mcp:connect"],
      "scopes_supported": [
        "mcp:connect",
        "github:*",
        "github:create_issue",
        "fs:read_file"
      ],
      "max_token_age": "1h"
    }
  },
  "resource_uri": "https://prism.example.com"
}
```

When OAuth is configured, Prism:

1. Validates the Bearer token signature via JWKS (auto-discovered from issuer)
2. Checks `aud` matches the configured audience
3. Checks `exp` and optional `max_token_age`
4. Extracts `scope` claim and builds an access policy
5. Serves `/.well-known/oauth-protected-resource` per RFC 9728

Scope format: `namespace:tool` (e.g. `github:create_issue`) or `namespace:*` for all tools in a namespace.

### Audit Logging

```json
{
  "audit": {
    "enabled": true,
    "output": "/var/log/prism/audit.json"
  }
}
```

Output options: `"stderr"` (default), `"stdout"`, or an absolute file path.

Each tool call produces one JSON line:

```json
{
  "ts": "2025-01-15T10:30:00Z",
  "subject": "ci-bot",
  "client_id": "ci-agent-prod",
  "namespace": "github",
  "tool": "create_issue",
  "allowed": true,
  "latency_ms": 142,
  "backend": "github",
  "error": "",
  "cred_injected": true
}
```

The `cred_injected` field confirms credentials were injected without ever logging the credential value.

### Rate Limiting

Global (all clients):

```json
{
  "rate_limit": {
    "requests_per_second": 100,
    "burst": 200
  }
}
```

Per-backend (on a server entry):

```json
{
  "rate_limit": {
    "requests_per_second": 10,
    "burst": 20
  }
}
```

### Circuit Breaker

Per-backend failure isolation:

```json
{
  "circuit_breaker": {
    "threshold": 5,
    "timeout": "30s",
    "max_half_open": 2
  }
}
```

After `threshold` consecutive failures, the circuit opens for `timeout`. Then `max_half_open` requests are allowed through to test recovery.

## Architecture

```
                    ┌─────────────────────────────────────┐
                    │              Prism                   │
                    │                                      │
Agent ──Bearer──→  │  Auth ─→ Scope Filter ─→ Router      │
                    │                           │          │
                    │         ┌─────────────────┼────────┐ │
                    │         │  Credential     │ Audit  │ │
                    │         │  Store          │ Logger │ │
                    │         └────┬────────────┴────────┘ │
                    │              │                        │
                    └──────────────┼────────────────────────┘
                                   │
                    ┌──────────────┼──────────────┐
                    │              │               │
                    ▼              ▼               ▼
              GitHub MCP    Postgres MCP     K8s MCP
              (+token)      (+vault cred)   (+sa token)
```

Prism is both:
- An **MCP server** facing agents (Streamable HTTP transport)
- Multiple **MCP clients** connecting to backends

Tools from all backends are aggregated under namespace prefixes. `tools/list` returns the union (filtered by scope). `tools/call` routes to the correct backend by prefix.

Built on the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk).

## Deployment

See [docs/deployment.md](docs/deployment.md) for detailed deployment instructions including:

- Systemd service setup
- Docker / Docker Compose
- Kubernetes with mounted secrets
- Reverse proxy (nginx/Caddy) configuration
- Production hardening checklist

## Development

### Prerequisites

- Go 1.25+
- golangci-lint (for `make lint`)

### Commands

```bash
make build        # Build binary
make test         # Run all tests
make lint         # Run golangci-lint
make fmt-check    # Check formatting
make vet          # Run go vet
make check        # All of the above
```

### Project Structure

```
cmd/prism/              Entry point
internal/
  admin/                Admin API (health, backends, info)
  audit/                Structured JSON audit logger
  auth/                 OAuth 2.1 token validation, scope policy, RFC 9728
  config/               Configuration loading and validation
  credentials/          Credential store, 4 resolver types, injecting transport
  gateway/              MCP server, backend connections, tool routing
  middleware/           Auth, rate limiting, circuit breaking
integration_test.go     End-to-end tests (real MCP sessions, no Docker)
```

### Tests

```bash
# All tests
go test ./...

# Integration tests only
go test -run TestIntegration -v

# With race detector
go test -race ./...
```

## License

Apache 2.0
