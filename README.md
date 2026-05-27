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

## Binaries

| Binary | Purpose |
|---|---|
| **`prism`** | The gateway. OAuth, credential injection, scope enforcement, audit logging, namespace aggregation, and admin UI/API. |
| **`prism-bridge`** | Transport adapter. Wraps stdio MCP servers or single functions as Streamable HTTP endpoints. Also runs managed bridge/workspace modes. |
| **`prism-auth`** | Standalone OAuth 2.1 authorization server for advanced separated deployments. Most deployments use the auth server embedded in `prism`. |

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
| **OpenAPI backends** | Point Prism at any OpenAPI 3 spec (URL, inline JSON/YAML, or generated from a `curl` command). Operations become MCP tools automatically. |
| **Managed binary backends** | Upload (or fetch by URL) a single static binary stdio MCP server. Prism stores it in the binstore and runs it sandboxed. |
| **Per-tool toggles** | Disable individual tools on a backend from the admin console — no need to fork the upstream server. |
| **Workspace bridge** | Optional per-agent sidecar (`prism-bridge workspace`) that exposes a project directory as workspace-scoped tools, with snapshot copies and stage/auto-apply patch-back. |
| **Sandboxed stdio** | When the Docker socket is available, stdio backends run in isolated containers with configurable CPU/memory/PIDs limits and read-only rootfs. |
| **Circuit breaking** | Per-backend failure isolation. A down backend doesn't take out the gateway. |
| **Rate limiting** | Global, per-backend, and per-policy (per-agent / per-group) token bucket rate limiting. |
| **Admin API/UI** | Health checks, backend status, runtime config, policy/grants, analytics, audit history, and browser admin console on a separate port. |
| **Admin SSO** | Optional OIDC sign-in (Google, Okta, Auth0, Keycloak, …) protecting the admin console and API, with email/domain/group role mapping. |
| **OpenTelemetry traces** | Set `OTEL_EXPORTER_OTLP_ENDPOINT` and Prism emits OTLP/HTTP traces for the request path. |
| **Static binaries** | Each role ships as one static binary. JSON config + KV state. No runtime dependencies. |

## Quick Start

```bash
git clone https://github.com/1broseidon/prism.git
cd prism
make build    # builds bin/prism, bin/prism-bridge, and bin/prism-auth
```

**→ [Full getting started guide](docs/getting-started.md)** — walks through setting up MCP servers (stdio + HTTP), configuring Prism, writing tools as functions, and connecting agent harnesses (Claude Desktop, Claude Code, Cursor, Windsurf, OpenAI Agents SDK, custom Go/Python agents).

## Configuration Reference

### Top-Level

| Field | Type | Default | Description |
|---|---|---|---|
| `listen` | string | `:8080` | MCP gateway listen address |
| `admin` | string | `:9086` | Admin API listen address |
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
| `enabled` | bool | `true` | Set to `false` to keep an entry in config but skip connecting it |
| `credentials` | object | none | Outbound credential injection (HTTP backends only) |
| `timeout` | duration | `30s` | Per-request timeout to this backend |
| `circuit_breaker` | object | none | Circuit breaker settings |
| `rate_limit` | object | none | Per-backend rate limiting |
| `sandbox` | object | none | Docker isolation for stdio backends (profile, memory/CPU/PIDs limits, read-only rootfs, mounts) |
| `workspace` | object | none | Bind a sandboxed stdio backend to a workspace snapshot with `proxied`/`virtual`/`ephemeral` storage and `sandbox_only`/`stage`/`auto_apply` write modes |

A backend is either stdio (`command` + `args`) or HTTP (`url`) — not both. OpenAPI-backed and binary-backed backends are managed through the admin API/console; their state lives in the KV store, not the JSON config.

### Backend Sources

Most operators add backends from the admin console rather than the JSON config. Five backend sources are supported:

| Source | How to add | When to use |
|---|---|---|
| **Native HTTP** | Servers → Add → HTTP URL | MCP server already speaks Streamable HTTP. |
| **Bridged stdio** | Servers → Add → stdio command | Standard `npx`/`uvx`/local-binary MCP servers. Prism wraps them in HTTP via the bridge — sandboxed when the Docker socket is available. |
| **Tool function** | `prism-bridge tool --manifest …` (see getting-started) | One-off scripts (bash/Python/Node) exposed as a single tool. |
| **OpenAPI** | Servers → Add → OpenAPI; paste a spec, URL, or `curl` command | Any HTTP API with an OpenAPI 3 spec becomes an MCP server. Operations map to tools. |
| **Managed binary** | Servers → Add → Binary; upload a file or paste a fetch URL | Distributing a single static stdio MCP binary without shipping a container image. Stored in `$PRISM_BINSTORE_DIR`, run in a sandbox. |

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

### Operational Environment Variables

These environment variables are part of Prism's operational contract. Backend credential variables such as `GITHUB_TOKEN` are user-defined and referenced from `credentials.env`; they are not reserved by Prism.

| Variable | Used by | Default | Purpose |
|---|---|---|---|
| `PRISM_DATA_DIR` | `prism` | `~/.prism` for local runs; `/data` in the container image | Base directory for persistent state when a more specific path is not set. |
| `PRISM_SIGNING_KEY_FILE` | `prism` | `$PRISM_DATA_DIR/.prism/signing-key.pem` or `~/.prism/signing-key.pem` | Persistent RSA signing key for embedded OAuth tokens. |
| `PRISM_ANALYTICS_DB` | `prism` | `$PRISM_DATA_DIR/grant_events.sqlite` or `~/.prism/grant_events.sqlite` | SQLite database for grant analytics. |
| `PRISM_BINSTORE_DIR` | `prism` | `$PRISM_DATA_DIR/binaries` or `~/.prism/binaries` | Binary backend artifact store. |
| `PRISM_KV_KEY_FILE` | `prism` | `~/.prism/kv-encryption.key` | At-rest encryption key for sensitive KV entries (OAuth client secrets, refresh tokens). Auto-generated on first start; pin the path on read-only roots or shared volumes. |
| `PRISM_WORKSPACE_TOKEN` | `prism`, `prism-bridge workspace` | unset | Shared token for workspace bridge registration. |
| `PRISM_STDIO_SPAWN_MODE` | `prism` | auto-detected from config/container state | Selects local process, bridge HTTP, or container-backed stdio spawning. |
| `PRISM_BRIDGE_URL` / `PRISM_BRIDGE_URLS` | `prism` | unset | One or more sidecar bridge manager base URLs. |
| `PRISM_BRIDGE_NETWORK` | `prism` | unset | Docker network passed to the internal bridge manager. |
| `PRISM_SANDBOX_IMAGE` | `prism` | `ghcr.io/1broseidon/prism:latest` in the container image | Default image for sandboxed managed backends. |
| `PRISM_SANDBOX_IMAGE_NODE` | `prism` | `PRISM_SANDBOX_IMAGE` | Node-focused sandbox image override. |
| `PRISM_SANDBOX_IMAGE_PYTHON` | `prism` | `PRISM_SANDBOX_IMAGE` | Python-focused sandbox image override. |
| `PRISM_GATEWAY_URL` | `prism-bridge workspace` | unset | Gateway base URL for workspace bridge mode. |
| `PRISM_AGENT_TOKEN` | `prism-bridge workspace` | unset | Agent OAuth access token; takes precedence over `PRISM_WORKSPACE_TOKEN`. |
| `PRISM_WORKSPACE_ID` | `prism-bridge workspace` | hostname | Stable workspace ID. |
| `PRISM_WORKSPACE_BACKEND` | `prism-bridge workspace` | `Brainfile` | Workspace backend ID. |
| `PRISM_WORKSPACE_NAMESPACE` | `prism-bridge workspace` | `<backend>-<workspace>` | Tool namespace registered with Prism. |
| `PRISM_WORKSPACE_ROOT` | `prism-bridge workspace` | current working directory | Root directory exposed by workspace bridge mode. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `prism` | unset | When set (e.g. `http://otel-collector:4318`), Prism emits OTLP/HTTP traces. `OTEL_EXPORTER_OTLP_HEADERS` adds extra headers. |

`prism-bridge manage` also accepts `BRIDGE_IMAGE_BASE`, `BRIDGE_IMAGE_NODE`, `BRIDGE_IMAGE_PYTHON`, `BRIDGE_IMAGE_FULL`, and `BRIDGE_NETWORK` as defaults for its matching CLI flags.

### Admin API Contract

The admin listener defaults to `:9086`. Browser UI and JSON API routes are documented in [docs/admin-api.md](docs/admin-api.md). JSON endpoints are versioned under `/api/v1`; root-level `/health`, `/metrics`, `/auth/callback`, and `/oauth/callback` are intentionally outside that prefix.

### Authentication

Prism has two independent auth surfaces:

- **Agent auth** — how MCP clients (agents) authenticate to the gateway.
- **Admin auth** — how operators authenticate to the admin console/API on the admin port.

#### Agent auth

The embedded OAuth 2.1 authorization server is always on. There is no external IdP to configure. Agents either use static credentials from `policy.agents` (client-credentials grant) or self-register via Dynamic Client Registration. Either way, an agent exchanges its credentials at `POST /token` for a Bearer access token and sends `Authorization: Bearer <token>` to `/mcp`.

When `policy` is omitted, the embedded server still issues tokens, but no agents or scopes are pre-defined — operators must create agents and groups from the admin console.

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

Scopes are one half of the access decision. The other half is the **backend policy stack**, which selects per-backend behavior (which workspace to bind to, what rate-limit to apply) layered as agent → group → default. Backend policies live in the KV store and are edited from the admin console.

#### Admin auth

Set the top-level `admin_auth` block (or wire it from the admin console under Settings → Sign-In) to require an OIDC login to reach the admin port. Any OIDC provider works — Google, Okta, Auth0, Keycloak, Authentik. Roles (`admin` for full access, `viewer` for read-only) are granted by matching the authenticated user's email, email domain, or group claim:

```json
{
  "admin_auth": {
    "issuer": "https://accounts.google.com",
    "client_id": "…apps.googleusercontent.com",
    "client_secret": "…",
    "redirect_url": "https://prism.example.com/auth/callback",
    "scopes": ["openid", "profile", "email"],
    "rules": [
      { "role": "admin",  "emails":  ["ops@example.com"] },
      { "role": "viewer", "domains": ["example.com"] }
    ]
  }
}
```

When `admin_auth` is absent (or disabled from the console) the admin port runs open — appropriate only for trusted/local networks. Admin sessions are stored encrypted in the KV store; rotate `PRISM_KV_KEY_FILE` to invalidate them.

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

- Go 1.26+
- golangci-lint (for `make lint`)

### Commands

```bash
make build        # Build all binaries: bin/prism, bin/prism-bridge, bin/prism-auth
make test         # Run all tests
make lint         # Run golangci-lint
make fmt-check    # Check formatting
make vet          # Run go vet
make check        # All of the above
```

### Project Structure

```
cmd/
  prism/                Gateway entry point (serve + service subcommands)
  prism-bridge/         Bridge entry point (serve, tool, manage, workspace modes)
  prism-auth/           Standalone OAuth authorization server for separated deployments
internal/
  admin/                Admin API + SPA handler (backends, agents, groups, policies, analytics, etc.)
  adminauth/            Admin SSO (OIDC login, sessions, role mapping)
  analytics/            Grant-event SQLite store + in-memory ring buffer for SSE tailing
  audit/                Structured JSON audit logger
  auth/                 OAuth 2.1 token validation, scope policy, RFC 9728
  authserver/           Embedded OAuth 2.1 authorization server (DCR, /token, /authorize)
  binstore/             Content-addressed binary store for managed binary backends
  config/               Configuration loading and validation
  credentials/          Credential store, 4 resolver types, injecting transport
  gateway/              MCP server, backend connections, tool routing, workspace bridge, OpenAPI dispatcher
  identity/             Subject/identity registry
  metrics/              Prometheus metrics
  middleware/           Auth, rate limiting, circuit breaking
  store/                KV abstraction (bbolt or Redis) with encrypted value support
  telemetry/            OpenTelemetry tracer wiring
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
