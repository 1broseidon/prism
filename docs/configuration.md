# Configuration

The complete configuration reference for Prism. This is the single home for every config field, credential type, auth setting, and environment variable. Other docs link here instead of repeating it.

New to Prism? Start with [Getting Started](./getting-started.md). Deploying for real? See [Deployment](./deployment.md). For the admin HTTP routes, see [Admin API](./admin-api.md).

---

## Config file

Prism reads a single JSON file. Pass it with `-config`:

```bash
prism -config /etc/prism/config.json
```

The published Docker image bakes a default config at `/etc/prism/config.json` (copied from [`deploy/config.container.json`](../deploy/config.container.json)) and runs it implicitly — `ENTRYPOINT ["prism"]`, `CMD ["-config", "/etc/prism/config.json"]`. The baked default is:

```json
{
  "listen": ":8080",
  "admin": ":9086",
  "store": {
    "type": "bbolt",
    "path": "/data/prism.db"
  },
  "audit": {
    "enabled": true,
    "output": "stderr"
  },
  "stdio_spawn_mode": "auto",
  "mcpServers": {}
}
```

To override it, mount your own file over `/etc/prism/config.json`.

Format notes:

- Duration fields are JSON strings: `"30s"`, `"5m"`, `"24h"`. A bare number is interpreted as nanoseconds.
- Defaults documented here are what `Load()` actually applies. "none" means the field is optional and unset by default.
- `mcpServers` is optional. Backends are normally managed from the admin console (state lives in the KV store); entries in `mcpServers` are a one-time seed applied on first boot.

Prism reloads its config on `SIGHUP` — send the signal to apply file changes without a restart.

---

## Top-level fields

The root `Config` object:

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `listen` | string | `:8080` | Listen address for the MCP gateway. Agents connect here. |
| `admin` | string | `:9086` | Listen address for the admin API/console. |
| `mcpServers` | object<string, server> | none (optional) | Backend MCP servers. Map key = server ID = tool namespace. One-time first-boot seed; backends are normally managed from the admin console. See [Backend servers](#backend-servers). |
| `policy` | object | none | Agents, groups, and scopes. The embedded OAuth 2.1 server runs regardless; `policy` pre-defines agents/scopes. See [Authentication](#authentication). |
| `audit` | object | none | Structured JSON audit logging of tool calls. See [Audit logging](#audit-logging). |
| `rate_limit` | object | none | Global (all-clients) rate limiting. See [Rate limiting](#rate-limiting). |
| `store` | object | bbolt at `~/.prism/prism.db` | KV backend for DCR clients, refresh tokens, audit retention, admin sessions. See [Storage](#storage). |
| `tls` | object | none | Direct HTTPS termination on the gateway listener (no reverse proxy needed). See [TLS](#tls). |
| `public_url` | string | derived from `listen`, else `http://localhost:{port}` (`https` if `tls` set) | Externally-reachable base URL for the gateway. Used as the OAuth issuer and the 401 `resource_metadata` hint. |
| `admin_public_url` | string | derived from `admin`, else `http://localhost:{port}` (`https` if `tls` set) | Externally-reachable base URL for the admin API. Used for OAuth callback URLs. |
| `bridge_url` | string | none | URL of a `prism-bridge` in manage mode; command-type backends are delegated to it. |
| `bridge_urls` | []string | none | Multi-bridge form; command backends are sharded across bridges by backend ID. Takes precedence over `bridge_url`. |
| `stdio_spawn_mode` | string | `auto` | How command/stdio backends are spawned. Allowed: `auto`, `bridge_http`, `internal_docker`, `process`, `disabled`. An empty value normalizes to `auto`. |
| `admin_auth` | object | none | OIDC sign-in protecting the admin console/API. Absent = admin runs open. See [Authentication](#authentication). |
| `shutdown_timeout` | duration | `10s` | Graceful shutdown duration. |

Notes:

- `public_url` / `admin_public_url` are derived when unset, but you must set them explicitly behind a reverse proxy so OAuth issuer and callback URLs are externally correct (see [Deployment](./deployment.md)).
- `stdio_spawn_mode` accepts `disabled` even though its doc comment historically listed only four values. The corresponding env var `PRISM_STDIO_SPAWN_MODE` uses the shorter names `auto`, `docker`, `process`, `bridge_http`.

---

## Backend servers

`mcpServers` is a map. The key is both the server ID and the tool namespace — tools from a `github` entry are exposed as `github__create_issue`. Each entry is either an **stdio** backend (`command` + `args`) or an **HTTP** backend (`url`) — exactly one, never both.

### Per-server fields (`McpServerConfig`)

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `command` | string | none | Executable to spawn (stdio transport). |
| `args` | []string | none | Arguments for `command`. |
| `env` | object<string,string> | none | Environment variables for the spawned process. |
| `enabled` | bool | `true` (when omitted) | Whether Prism connects this backend. Set `false` to keep the entry but skip it. |
| `url` | string | none | HTTP endpoint for an already-running MCP server (alternative to `command`). |
| `credentials` | object | none | Outbound credential injection. HTTP backends only — rejected for stdio. See [Credentials](#credentials). |
| `rate_limit` | object | none | Per-server rate limiting. See [Rate limiting](#rate-limiting). |
| `circuit_breaker` | object | none | Per-server circuit breaker. See [Circuit breaker](#circuit-breaker). |
| `timeout` | duration | `30s` | Per-request timeout to this backend. |
| `sandbox` | object | none (Docker isolation defaults applied when stdio runs sandboxed) | Docker isolation for stdio backends; ignored for HTTP. See [Sandbox](#sandbox). |
| `workspace` | object | none | Bind a sandboxed stdio backend to a local workspace snapshot. See [Workspace](#workspace). |

Validation constraints:

- Exactly one of `command` or `url` is required (not both).
- `url` must start with `http://` or `https://`.
- `credentials` are not allowed with `command` (stdio backends cannot inject outbound HTTP credentials).

### Backend sources

Most operators add backends from the admin console rather than the JSON config. Five sources are supported:

| Source | How to add | When to use |
|---|---|---|
| **Native HTTP** | Servers → Add → HTTP URL (or `url` in config) | MCP server already speaks Streamable HTTP. |
| **Bridged stdio** | Servers → Add → stdio command (or `command` in config) | Standard `npx`/`uvx`/local-binary MCP servers. Prism wraps them in HTTP via the bridge — sandboxed when the Docker socket is available. |
| **Tool function** | `prism-bridge tool --manifest …` (see [Getting Started](./getting-started.md)) | One-off scripts (bash/Python/Node) exposed as a single tool. |
| **OpenAPI** | Servers → Add → OpenAPI; paste a spec, URL, or `curl` command | Any HTTP API with an OpenAPI 3 spec. Operations map to MCP tools. |
| **Managed binary** | Servers → Add → Binary; upload a file or paste a fetch URL | Distribute a single static stdio MCP binary without a container image. Stored in `$PRISM_BINSTORE_DIR`, run in a sandbox. |

OpenAPI-backed and binary-backed backends are managed only through the admin API/console; their state lives in the KV store, not the JSON config.

### Sandbox

Docker isolation for stdio backends (`sandbox` on a server entry). Ignored for HTTP backends. Defaults depend on `profile`: `default` applies hardened isolation; `compat` preserves historical (looser) behavior. The table shows `default`-profile defaults.

| JSON key | Type | Default (profile=default) | Purpose |
|---|---|---|---|
| `profile` | string | `default` | Isolation profile: `default` (hardened) or `compat` (legacy). |
| `network_profile` | string | `standard` | Container network; only `standard` is valid. |
| `run_as_root` | bool | `false` | Run container as uid 0. (`compat` default: `true`.) |
| `uid` | int | `65532` | Container uid (non-root). Must be non-zero in non-root mode. |
| `gid` | int | `65532` | Container gid (non-root). Must be non-zero in non-root mode. |
| `readonly_rootfs` | bool | `true` | Read-only container root filesystem. (`compat` default: `false`.) |
| `memory` | string | `512m` | Docker-style memory limit (e.g. `512m`, `1g`). |
| `cpus` | float64 | `1` | CPU quota; must be ≥ 0. |
| `pids_limit` | int64 | `128` | Max processes; must be ≥ 0. |
| `mounts` | []object | none | Explicit host-path mounts. See below. |

`compat`-profile defaults: `run_as_root=true`, `readonly_rootfs=false`, uid/gid unset (0), and `memory`/`cpus`/`pids_limit` unset.

Each entry in `sandbox.mounts` (`SandboxMount`):

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `source` | string | required | Absolute host path. |
| `target` | string | required | Absolute container path. |
| `readonly` | bool | `true` (when omitted) | Mount read-only. |

Mount constraints: `source` must be absolute; you cannot mount `/var/run/docker.sock`; `target` cannot be (or be under) `/proc`, `/sys`, `/dev`, or `/var/run`.

### Workspace

Bind a sandboxed stdio backend to a local workspace snapshot (`workspace` on a server entry). A workspace is only activated when `id` is non-empty; otherwise the whole block is treated as nil.

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `id` | string | none (required to activate) | Workspace identifier; must match `^[A-Za-z0-9_.-]{1,64}$`. |
| `type` | string | `proxied` | Storage type: `proxied`, `virtual`, or `ephemeral`. |
| `mode` | string | `snapshot` | Sync mode; only `snapshot` is valid. |
| `write_mode` | string | `stage` | Patch-back policy: `sandbox_only`, `stage`, or `auto_apply`. |
| `include` | []string | none | Glob include patterns for the snapshot. |
| `exclude` | []string | none | Glob exclude patterns for the snapshot. |
| `max_bytes` | int64 | `33554432` (32 MiB) | Max snapshot size; must be > 0 and ≤ 512 MiB. |
| `quota_bytes` | int64 | `0` | Durable-storage quota; must be ≥ 0. |
| `retention_seconds` | int64 | `0` | Retention for durable workspace state; must be ≥ 0. |

---

## Credentials

Credentials are resolved at call time and injected into outbound HTTP requests. The agent never sees the raw value. Credentials apply to **HTTP/`url` backends only** — they are rejected for `command`/stdio backends.

The type is inferred from which field is set. Set **exactly one** of `value`, `env`, `file`, or `command`.

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `header` | string | `Authorization` | HTTP header to set. |
| `value` | string | none | Literal credential value → type `static`. |
| `env` | string | none | Env var name to read → type `env`. |
| `file` | string | none | File path to read → type `file`. |
| `command` | string | none | Shell command whose stdout is the credential → type `command`. |
| `ttl` | duration | `5m` (cache duration for `command` creds) | How long a command-type credential is cached before re-execution. |

**Static** — fixed value, suitable for long-lived API keys:

```json
{
  "header": "X-API-Key",
  "value": "<your-api-key>"
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

Command credentials cache stdout for `ttl` (default 5 minutes), then re-execute. This works with Vault, AWS STS, `gcloud auth print-access-token`, or any CLI that outputs a credential.

> The `env`-type credential reads an arbitrary, user-named variable (e.g. `GITHUB_TOKEN`). Those variable names are yours to choose; they are not reserved by Prism. They are distinct from the operational env vars in [Environment variables](#environment-variables).

---

## Authentication

Prism has two independent auth surfaces:

- **Agent auth** — how MCP clients (agents) authenticate to the gateway on the `listen` port.
- **Admin auth** — how operators authenticate to the admin console/API on the `admin` port.

### Agent auth

The embedded OAuth 2.1 authorization server is **always on**. There is no flag to disable it and no external IdP to configure. It runs on the same listener as `/mcp` (default `:8080`) and exposes:

| Method + path | Purpose |
|---|---|
| `POST /token` | Exchange credentials for a Bearer access token. |
| `GET`/`POST /authorize` | Authorization-code + PKCE flow (PKCE required, `S256`). |
| `POST /register` | Dynamic Client Registration (RFC 7591). |
| `GET /.well-known/jwks.json` | JWKS for token verification. |
| `GET /.well-known/oauth-authorization-server` | AS metadata (RFC 8414). |
| `GET /.well-known/oauth-protected-resource[/mcp]` | Protected Resource Metadata (RFC 9728). |

Token TTL is fixed at **3600s**. The required scope to connect is **`mcp:connect`** (auto-added to every agent).

**Static agents** use the client-credentials grant. Define them under `policy.agents` (or add them from the admin console), then exchange the secret at `POST /token`:

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/token \
  -d "grant_type=client_credentials&client_id=my-agent&client_secret=change-me" | jq -r .access_token)

curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/mcp \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

The token response carries `access_token`, `token_type: "Bearer"`, `expires_in` (3600), and `scope`. The client-credentials grant returns no `refresh_token`.

**DCR agents** (Claude Code, Cursor, etc.) self-register at `POST /register`. Registered clients get `token_endpoint_auth_method: "none"` and `grant_types: ["authorization_code"]`, so they use the **authorization-code + PKCE** flow via `/authorize` — not client-credentials. On first connection the operator only assigns a display label on the consent page; **group/scope assignment is a separate admin action afterward**. Until you assign groups, a DCR agent has only `default_scopes` + `mcp:connect`.

> Agents authenticate to the gateway with `Authorization: Bearer <token>` (the `DPoP` scheme is also accepted). Any other scheme returns 401. `X-API-Key` is for upstream backend credentials only — never for agent → gateway auth.

#### The `policy` block

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

- `policy.agents` — map of agent ID → `{ secret (required), groups, grant, deny }`. The `secret` is the OAuth `client_secret`.
- `policy.groups` — map of group name → `{ scopes }`.
- `policy.default_scopes` — scopes for agents with no group membership (e.g. a freshly-registered DCR client).

Effective agent scopes = (union of group scopes, or `default_scopes` if no groups) + `grant` − `deny` + always `mcp:connect`. **Deny wins over grant.**

Scope format: `namespace:tool` (e.g. `github:create_issue`) or `namespace:*` for all tools in a namespace. The literal `*` in `grant` is the admin wildcard.

Per request, Prism validates the Bearer token, resolves effective scopes, filters `tools/list` to scoped tools, authorizes `tools/call` against the requested `namespace:tool`, and serves Protected Resource Metadata per RFC 9728.

Scopes are one half of the access decision. The other half is the **backend policy stack** (which workspace to bind, what rate-limit to apply), layered agent → group → default. Backend policies live in the KV store and are edited from the admin console.

When `policy` is omitted, the embedded server still issues tokens, but no agents or scopes are pre-defined — create them from the admin console.

### Admin auth

Set the top-level `admin_auth` block (or wire it from the console under Settings → Sign-In) to require OIDC login on the admin port. Any OIDC provider works — Google, Okta, Auth0, Keycloak, Authentik. Roles (`admin` for full access, `viewer` for read-only) are granted by matching the user's email, email domain, or group claim.

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

`AdminAuthConfig` fields:

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `issuer` | string | required | OIDC issuer URL for discovery. |
| `client_id` | string | required | OAuth client ID. |
| `client_secret` | string | required | OAuth client secret (confidential client). |
| `redirect_url` | string | required | Absolute OIDC callback URL registered with the issuer. |
| `scopes` | []string | `["openid","profile","email"]` | OAuth scopes requested. |
| `groups_claim` | string | `groups` | ID-token claim carrying group membership. |
| `session_ttl` | duration | `24h` | Logged-in session lifetime. |
| `cookie_domain` | string | none | Pins the session cookie `Domain` attribute. |
| `cookie_secure` | bool | `false` (auto-on when TLS configured) | Forces `Secure=true` on the session cookie. |
| `rules` | []object | required (≥1) | Role-granting matchers; first match wins; no match = rejected. |

Each rule (`AdminAuthRule`):

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `role` | string | required | `admin` (full) or `viewer` (read-only). |
| `emails` | []string | none | Exact email addresses to grant the role (case-insensitive). |
| `domains` | []string | none | Email domains, e.g. `example.com` (case-insensitive). |
| `groups` | []string | none | Group names from the OIDC groups claim (case-sensitive). |

Hard requirements (`Load()` errors otherwise): `issuer`, `client_id`, `client_secret`, `redirect_url`, and at least one rule. Each rule needs `role` plus at least one of `emails`/`domains`/`groups`.

When `admin_auth` is absent (or disabled from the console) the admin port runs **open** — appropriate only for trusted/local networks. Admin sessions are stored encrypted in the KV store; rotating `PRISM_KV_KEY_FILE` invalidates them.

---

## Audit logging

```json
{
  "audit": {
    "enabled": true,
    "output": "/var/log/prism/audit.json",
    "retention_days": 30
  }
}
```

`AuditConfig` fields:

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `enabled` | bool | `false` | Turns on audit logging. |
| `output` | string | `stderr` | `stderr`, `stdout`, or an absolute file path. |
| `retention_days` | int | `30` | Days to keep audit entries in the KV store. |

Each tool call — allowed or denied — produces one JSON line:

```json
{"ts":"2025-01-15T10:30:00Z","subject":"ci-bot","client_id":"ci-agent-prod","namespace":"github","tool":"create_issue","allowed":true,"latency_ms":142,"backend":"github","error":"","cred_injected":true}
```

The `cred_injected` field confirms credentials were injected without ever logging the credential value.

---

## Rate limiting

Token-bucket rate limiting. The JSON keys are **`rps`** and **`burst`** — both must be > 0 when the block is present. There is no `requests_per_second` key.

`RateLimitConfig` fields:

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `rps` | float64 | none (required > 0 if block present) | Requests per second (token-bucket refill rate). |
| `burst` | int | none (required > 0 if block present) | Token-bucket burst size. |

Global (all clients), as a top-level `rate_limit`:

```json
{
  "rate_limit": {
    "rps": 100,
    "burst": 200
  }
}
```

Per-backend, as `rate_limit` on a server entry:

```json
{
  "rate_limit": {
    "rps": 10,
    "burst": 20
  }
}
```

Per-policy (per-agent / per-group) rate limits are part of the backend policy stack, edited from the admin console.

---

## Circuit breaker

Per-backend failure isolation (`circuit_breaker` on a server entry):

```json
{
  "circuit_breaker": {
    "threshold": 5,
    "timeout": "30s",
    "max_half_open": 2
  }
}
```

`CircuitBreakerConfig` fields:

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `threshold` | int | none (required > 0 if block present) | Consecutive failures before the circuit opens. |
| `timeout` | duration | none | How long the circuit stays open before half-open probing. |
| `max_half_open` | int | none | Number of probe requests allowed in the half-open state. |

After `threshold` consecutive failures the circuit opens for `timeout`, then `max_half_open` requests are allowed through to test recovery. Only `threshold` is validated (must be > 0); `timeout` and `max_half_open` have no enforced default at config-load time.

---

## Storage

The KV store backs DCR clients, refresh tokens, admin sessions, audit retention, and encrypted upstream credentials.

`StoreConfig` fields:

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `type` | string | `bbolt` | KV backend: `bbolt` or `redis`. |
| `url` | string | none | Redis connection URL (only when `type` = `redis`). |
| `path` | string | `~/.prism/prism.db` | bbolt file path override. |

bbolt (default, single embedded file):

```json
{
  "store": {
    "type": "bbolt",
    "path": "/data/prism.db"
  }
}
```

Redis (shared/HA deployments):

```json
{
  "store": {
    "type": "redis",
    "url": "redis://redis:6379/0"
  }
}
```

Sensitive KV values (OAuth client secrets, refresh tokens, admin sessions) are encrypted at rest with the key at `PRISM_KV_KEY_FILE` (see [Environment variables](#environment-variables)).

---

## TLS

Direct HTTPS termination on the gateway listener — no reverse proxy required.

```json
{
  "tls": {
    "cert": "/etc/prism/tls/fullchain.pem",
    "key": "/etc/prism/tls/privkey.pem"
  }
}
```

`TLSConfig` fields:

| JSON key | Type | Default | Purpose |
|---|---|---|---|
| `cert` | string | required (if `tls` set) | Path to PEM cert (or chain). |
| `key` | string | required (if `tls` set) | Path to PEM private key. |

Both `cert` and `key` are required when the `tls` block is present. Setting `tls` also flips the derived `public_url`/`admin_public_url` schemes to `https` and auto-enables `admin_auth.cookie_secure`. For reverse-proxy TLS termination instead, see [Deployment](./deployment.md).

---

## Environment variables

These environment variables are part of Prism's operational contract. They are distinct from backend credential variables (e.g. `GITHUB_TOKEN`), which you name yourself and reference from `credentials.env`.

"Consumer" is the binary/mode that reads the variable. Defaults are the code-level fallback when the variable is unset/empty; container defaults set by the Dockerfile `ENV` are noted in the Purpose column.

### prism gateway (`cmd/prism`, `internal/`)

| Variable | Consumer | Default (code fallback) | Purpose |
|---|---|---|---|
| `PRISM_DATA_DIR` | `prism` | unset → `~/.prism`; container ENV sets `/data` | Base dir for persistent state; used to derive the analytics DB, binstore, and bbolt paths when their specific vars are unset. |
| `PRISM_SIGNING_KEY_FILE` | `prism` | `~/.prism/signing-key.pem` (NOT derived from `PRISM_DATA_DIR` in code; container ENV pins `/data/.prism/signing-key.pem`) | Persistent RSA signing key for embedded OAuth tokens; generated on first run, reused after. |
| `PRISM_ANALYTICS_DB` | `prism` | `$PRISM_DATA_DIR/grant_events.sqlite` if set, else `~/.prism/grant_events.sqlite` | SQLite path for grant-event analytics. |
| `PRISM_BINSTORE_DIR` | `prism` | `$PRISM_DATA_DIR/binaries` if set, else `~/.prism/binaries` | Managed-binary backend artifact store. |
| `PRISM_KV_KEY_FILE` | `prism` | `~/.prism/kv-encryption.key` (NOT derived from `PRISM_DATA_DIR` in code; container ENV pins `/data/.prism/kv-encryption.key`) | AES-256-GCM at-rest key for sensitive KV entries (OAuth client secrets, refresh tokens, admin sessions); auto-generated on first start. |
| `PRISM_WORKSPACE_TOKEN` | `prism`, `prism-bridge workspace` | unset | Shared workspace-bridge registration token. |
| `PRISM_STDIO_SPAWN_MODE` | `prism` | `auto` (config value used before this default) | Stdio spawn strategy: `auto`, `docker`, `process`, `bridge_http`. Env overrides config. |
| `PRISM_BRIDGE_URLS` | `prism` | unset; takes precedence over `PRISM_BRIDGE_URL` and config | Comma/space/newline-separated list of sidecar bridge-manager base URLs. |
| `PRISM_BRIDGE_URL` | `prism` | unset; used only if `PRISM_BRIDGE_URLS` is empty; overrides config | Single sidecar bridge-manager base URL. |
| `PRISM_IN_CONTAINER` | `prism` | unset; `1` means "running in container" (also auto-detected via `/.dockerenv`); container ENV sets `1` | Gates container-aware stdio fallback behavior. |
| `PRISM_BRIDGE_NETWORK` | `prism` | unset | Docker network (`--network`) passed to the internally spawned `prism-bridge manage`. |
| `PRISM_SANDBOX_IMAGE` | `prism` | `ghcr.io/1broseidon/prism:latest` (falls back to `BRIDGE_IMAGE_FULL` first); container ENV sets it explicitly | Default Docker image for sandboxed managed backends (`--image-full` to the internal bridge). |
| `PRISM_SANDBOX_IMAGE_NODE` | `prism` | falls back to `PRISM_SANDBOX_IMAGE` → `BRIDGE_IMAGE_FULL` → literal | Node-runtime sandbox image override (`--image-node`). |
| `PRISM_SANDBOX_IMAGE_PYTHON` | `prism` | falls back to `PRISM_SANDBOX_IMAGE` → `BRIDGE_IMAGE_FULL` → literal | Python-runtime sandbox image override (`--image-python`). |
| `BRIDGE_IMAGE_FULL` | `prism`, `prism-bridge manage` | unset; in `prism` it is the fallback after `PRISM_SANDBOX_IMAGE` and before the literal default | Read by `prism` too, not only the bridge. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `prism` | unset → tracing disabled (no-op) | When set (e.g. `http://otel-collector:4318`), Prism initializes an OTLP/HTTP tracer. The only OTEL var read directly by Prism code. |
| `OTEL_SERVICE_NAME` | `prism` (via OTEL SDK) | unset → uses the `serviceName` passed to `telemetry.Init` | Standard OTEL var; overrides the service name via `resource.WithFromEnv()`. |
| `OTEL_EXPORTER_OTLP_HEADERS` | `prism` (via OTEL SDK) | unset | Standard OTEL var read by the OTLP/HTTP exporter for extra headers. |
| `OTEL_RESOURCE_ATTRIBUTES` | `prism` (via OTEL SDK) | unset | Standard OTEL var honored via `resource.WithFromEnv()` for additional resource attributes. |
| _(other standard `OTEL_*`)_ | `prism` (via OTEL SDK) | per SDK defaults | The OTLP exporter and `resource.WithFromEnv()` honor the full standard `OTEL_*` set. |

### prism-bridge workspace mode (`cmd/prism-bridge workspace`)

Each variable has a matching CLI flag that overrides it.

| Variable | Flag | Default | Purpose |
|---|---|---|---|
| `PRISM_GATEWAY_URL` | `--gateway` | unset | Prism gateway base URL the workspace bridge dials. |
| `PRISM_WORKSPACE_TOKEN` | `--token` | unset | Shared ops-managed workspace-bridge token. |
| `PRISM_AGENT_TOKEN` | `--agent-token` | unset | Per-agent OAuth access token; takes precedence over `PRISM_WORKSPACE_TOKEN`. |
| `PRISM_WORKSPACE_ID` | `--id` | sanitized hostname (or `local` if empty) | Stable workspace ID. |
| `PRISM_WORKSPACE_BACKEND` | `--backend` | `Brainfile` | Local backend ID. |
| `PRISM_WORKSPACE_NAMESPACE` | `--namespace` | unset → effectively `<backend>-<id>` | Tool namespace registered in Prism. |
| `PRISM_WORKSPACE_ROOT` | `--root` | current working directory | Working directory exposed by the workspace bridge. |

### prism-bridge manage mode (`cmd/prism-bridge manage`)

Each env var supplies the default for a matching CLI flag, which overrides it.

| Variable | Flag(s) | Default | Purpose |
|---|---|---|---|
| `BRIDGE_IMAGE_FULL` | `--image`, `--image-full` | unset | Default/fallback Docker image for managed backends (read by both flags). |
| `BRIDGE_NETWORK` | `--network` | unset | Docker network for managed containers. |
| `BRIDGE_IMAGE_BASE` | `--image-base` | unset | Image for base/runtime-neutral backends. |
| `BRIDGE_IMAGE_NODE` | `--image-node` | unset | Image for node-based backends. |
| `BRIDGE_IMAGE_PYTHON` | `--image-python` | unset | Image for python-based backends. |

`prism-auth`, `prism-bridge serve`, and `prism-bridge tool` read no environment variables (flags only).

### Container ENV defaults

The published image bakes these defaults (set in the Dockerfiles):

```
PRISM_IN_CONTAINER=1
PRISM_DATA_DIR=/data
PRISM_KV_KEY_FILE=/data/.prism/kv-encryption.key
PRISM_SIGNING_KEY_FILE=/data/.prism/signing-key.pem
PRISM_SANDBOX_IMAGE=ghcr.io/1broseidon/prism:latest
```

Because the image already pins `PRISM_KV_KEY_FILE` and `PRISM_SIGNING_KEY_FILE` to `/data`, mounting a persistent `/data` volume keeps issued OAuth tokens, admin sessions, refresh tokens, and encrypted upstream credentials valid across restarts.

---

## See also

- [Getting Started](./getting-started.md) — Docker quickstart and connecting agent harnesses.
- [Deployment](./deployment.md) — single container, compose, systemd, Kubernetes, reverse proxy, hardening.
- [Admin API](./admin-api.md) — the admin HTTP route ledger.
- [README](../README.md) — project overview.
