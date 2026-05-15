# Prism

An MCP gateway that governs how AI agents access backend services.

Agents authenticate once to Prism with an OAuth token. Prism resolves per-backend credentials, enforces scope-based access control, and produces a structured audit trail. The agent never sees raw API keys.

```
                         ┌─────────────┐
Agent ──→ Prism (OAuth) ─┤ Bridge      ├→ stdio MCP server
                         ├─────────────┤
                         │ Bridge      ├→ stdio MCP server
                         ├─────────────┤
                         │ Native HTTP ├→ HTTP MCP server
                         └─────────────┘
```

## Why

MCP servers are multiplying. Each one needs credentials, access control, and observability. Without a gateway:

- Agents hold raw API keys (and can leak them)
- Every MCP server implements its own auth
- There's no central audit trail of what agents actually did
- Revoking access means updating every server

Prism solves this by sitting between agents and MCP servers, acting as both an MCP server (facing agents) and an MCP client (facing backends).

## Two Binaries

| Binary | Purpose |
|---|---|
| **`prism`** | The gateway. OAuth, credential injection, scope enforcement, audit logging, namespace aggregation. |
| **`prism-bridge`** | Transport adapter. Wraps stdio MCP servers or single functions as Streamable HTTP endpoints. |

Most MCP servers speak stdio (npx, uvx, local binaries). Prism speaks HTTP. The bridge normalizes any MCP server into an HTTP endpoint that Prism can connect to — each in its own isolated container or pod.

## Features

| Feature | Description |
|---|---|
| **Credential brokering** | 4 credential types: static, env var, file, shell command (with TTL cache). Agent never sees raw values. |
| **OAuth 2.1 + RFC 9728** | Token validation, audience checking, scope enforcement, Protected Resource Metadata discovery. |
| **Scope-filtered discovery** | `tools/list` only returns tools the agent is authorized to use. No information leakage. |
| **Structured audit log** | Every tool call — allowed or denied — produces a single-line JSON entry for SIEM ingestion. |
| **Namespace aggregation** | N backends appear as one MCP server. Tools are prefixed: `github__create_issue`, `fs__read_file`. |
| **stdio → HTTP bridge** | Wrap any stdio MCP server as an HTTP endpoint. Each in its own container for isolation. |
| **Tools as functions** | Write a bash/Python/Node script, deploy it as an MCP tool. No SDK, no boilerplate. |
| **Circuit breaking** | Per-backend failure isolation. A down backend doesn't take out the gateway. |
| **Rate limiting** | Global and per-backend token bucket rate limiting. |
| **Admin API** | Health checks, backend status, uptime — on a separate port. |
| **Single binary** | 12MB binaries, JSON config, no runtime dependencies. |

## Quick Start

```bash
git clone https://github.com/1broseidon/prism.git
cd prism
make build    # builds bin/prism and bin/prism-bridge
```

**→ [Full getting started guide](docs/getting-started.md)** — walks through setting up MCP servers (stdio + HTTP), configuring Prism, writing tools as functions, and connecting agent harnesses (Claude Desktop, Claude Code, Cursor, Windsurf, OpenAI Agents SDK, custom Go/Python agents).

## Configuration Reference

### Top-Level

| Field | Type | Default | Description |
|---|---|---|---|
| `listen` | string | `:8080` | MCP gateway listen address |
| `admin` | string | `:9090` | Admin API listen address |
| `mcpServers` | object | required | Backend MCP servers, keyed by namespace |
| `policy` | object | none | Agents, groups, and scopes. When present, Prism embeds an OAuth 2.1 auth server. |
| `audit` | object | none | Structured audit logging |
| `rate_limit` | object | none | Global rate limiting |
| `store` | object | bbolt | KV backend for DCR clients, refresh tokens, audit log |
| `tls` | object | none | HTTPS on the gateway listener (no reverse proxy needed) |
| `shutdown_timeout` | duration | `10s` | Graceful shutdown timeout |

### Server Config

`mcpServers` is a map. The map key is both the server ID and the tool namespace (e.g. tools from the `github` entry are exposed as `github__create_issue`). Each entry mirrors `claude_desktop_config.json` plus Prism extensions:

| Field | Type | Default | Description |
|---|---|---|---|
| `command` | string | — | Executable to spawn for stdio transport |
| `args` | array | — | Command arguments |
| `env` | object | — | Environment variables for the spawned process |
| `url` | string | — | HTTP endpoint (alternative to `command`) |
| `credentials` | object | none | Outbound credential injection (HTTP backends only) |
| `timeout` | duration | `30s` | Per-request timeout to this backend |
| `circuit_breaker` | object | none | Circuit breaker settings |
| `rate_limit` | object | none | Per-backend rate limiting |

A backend is either stdio (`command` + `args`) or HTTP (`url`) — not both.

### Credential Types

Credentials are resolved at call time and injected into outbound HTTP requests. The agent never sees the raw value. The credential type is determined by which field is set — exactly one of `value`, `env`, `file`, or `command`. `header` is optional (default: `Authorization`).

**Static** — fixed value, suitable for long-lived API keys:
```json
{
  "header": "X-API-Key",
  "value": "sk_live_your_key"
}
```

**Environment variable** — resolved at call time:
```json
{
  "env": "GITHUB_TOKEN"
}
```

**File** — read from disk (Kubernetes mounted secrets, service account tokens):
```json
{
  "file": "/var/run/secrets/kubernetes.io/serviceaccount/token"
}
```

**Command** — execute a shell command, cache the result with TTL:
```json
{
  "command": "vault kv get -field=token secret/mcp/github",
  "ttl": "5m"
}
```

Command credentials cache stdout for the configured TTL (default 5 minutes), then re-execute. This works with Vault, AWS STS, `gcloud auth print-access-token`, or any CLI that outputs a credential.

### Authentication

When `policy` is present, Prism embeds an OAuth 2.1 authorization server in-process — there is no external IdP to configure. When `policy` is omitted, the gateway runs open (no auth).

```json
{
  "policy": {
    "agents": {
      "ci-agent": { "secret": "change-me-ci",    "groups": ["deployers"] },
      "analyst":  { "secret": "change-me-read",  "groups": ["readers"] },
      "admin":    { "secret": "change-me-admin", "groups": ["deployers", "readers"], "grant": ["*"] }
    },
    "groups": {
      "deployers": { "scopes": ["github:*", "filesystem:*"] },
      "readers":   { "scopes": ["github:list_prs", "filesystem:read_file"] }
    },
    "default_scopes": []
  }
}
```

Each agent has a `secret` used as the OAuth `client_secret` (client-credentials grant). An agent's effective scopes are the union of its groups' `scopes`, plus any `grant` on the agent, minus any `deny` (deny wins). `default_scopes` applies to agents that have no group membership (e.g. dynamic client registration pending).

Per request, Prism:

1. Validates the Bearer token it issued
2. Resolves the agent's effective scopes
3. Filters `tools/list` to only scoped tools
4. Authorizes `tools/call` against the requested `namespace:tool`
5. Serves `/.well-known/oauth-protected-resource` per RFC 9728

Scope format: `namespace:tool` (e.g. `github:create_issue`) or `namespace:*` for all tools in a namespace. The literal `*` in `grant` is the admin wildcard.

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
              ┌────────────────────┼────────────────────┐
              │                    │                     │
              ▼                    ▼                     ▼
     ┌──────────────┐    ┌──────────────┐     ┌──────────────┐
     │ Bridge       │    │ Bridge       │     │ Native HTTP  │
     │ (container)  │    │ (container)  │     │              │
     │ stdio→HTTP   │    │ func→HTTP    │     │              │
     │  ↓           │    │  ↓           │     │              │
     │ npx github   │    │ python       │     │ custom-api   │
     └──────────────┘    └──────────────┘     └──────────────┘
```

Prism is both:
- An **MCP server** facing agents (Streamable HTTP transport)
- Multiple **MCP clients** connecting to backends

Backends are either:
- **Native HTTP** MCP servers (connect directly)
- **Bridged** stdio MCP servers or functions (via `prism-bridge` in isolated containers)

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
make build        # Build both binaries (bin/prism, bin/prism-bridge)
make test         # Run all tests
make lint         # Run golangci-lint
make fmt-check    # Check formatting
make vet          # Run go vet
make check        # All of the above
```

### Project Structure

```
cmd/
  prism/                Gateway entry point
  prism-bridge/         Bridge entry point (serve + tool modes)
internal/
  admin/                Admin API (health, backends, info)
  audit/                Structured JSON audit logger
  auth/                 OAuth 2.1 token validation, scope policy, RFC 9728
  config/               Configuration loading and validation
  credentials/          Credential store, 4 resolver types, injecting transport
  gateway/              MCP server, backend connections, tool routing
  middleware/           Auth, rate limiting, circuit breaking
examples/
  tools/                Example tool scripts + manifests
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
