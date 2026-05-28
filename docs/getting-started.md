# Getting Started with Prism

This is the fast path: run Prism in Docker, add a backend from the admin
console, mint a token, and connect an agent. Source build and the lower-level
backend mechanics come after.

```
┌──────────────┐      ┌───────────┐      ┌─────────────────────────┐
│ Agent        │      │           │      │ Backends                │
│              │      │           │ ───→ │ bridge → github (stdio) │
│ Claude Code  │ ───→ │  Prism    │ ───→ │ bridge → check-dns (sh) │
│ Cursor       │      │  (:8080)  │ ───→ │ custom-api (native HTTP)│
│ Custom agent │      │           │      │                         │
└──────────────┘      └───────────┘      └─────────────────────────┘
```

For the full configuration reference (every field, default, and env var), see
[configuration.md](./configuration.md). This guide links to it instead of
repeating it.

## 1. Run Prism (Docker)

One container. Prism stores all state under `/data`. When the Docker socket is
mounted, Prism starts its built-in bridge manager so stdio MCP servers (`npx
...`, `uvx ...`) run in sandbox containers.

```bash
docker run -d \
  --name prism \
  -p 8080:8080 \
  -p 9086:9086 \
  -v prism-data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/1broseidon/prism:latest
```

- Port `8080` = the MCP gateway (agents connect here). Port `9086` = the admin UI/API.
- The `prism-data` volume holds the bbolt DB, signing key, KV encryption key, analytics, and binstore.
- The Docker socket is optional. Without it, native HTTP MCP backends still work; only sandboxed stdio backends need it.

The published image already pins the at-rest key paths to `/data`, so issued
OAuth tokens, admin sessions, and stored upstream credentials survive restarts.
For a reproducible deploy, pin the version tag (`ghcr.io/1broseidon/prism:0.1.0`).
See [configuration.md#environment-variables](./configuration.md#environment-variables)
for the key-file env vars.

Confirm it's up:

```bash
curl http://localhost:9086/health
# → {"status":"ok"}
```

## 2. Add a backend from the console

Open `http://localhost:9086` and go to the Servers page. The console handles all
five backend sources — no config file edit required:

- **HTTP** — paste a URL to a Streamable HTTP MCP server.
- **stdio** — paste a `command` and `args` (e.g. `npx @modelcontextprotocol/server-github`); Prism sandboxes it in a Docker container.
- **OpenAPI** — paste an OpenAPI 3 spec URL, drop in inline JSON/YAML, or paste a `curl` command and let Prism scaffold a spec for you.
- **Binary** — upload a static stdio MCP binary (or paste a fetch URL); Prism stores and sandboxes it.
- **Workspace** — sandbox a stdio backend against a snapshot of a workspace registered by `prism-bridge workspace`.

The map key (server ID) becomes the tool namespace. Tools are exposed to agents
as `namespace__toolname` (e.g. `github__create_issue`). Backends added in the
console are persisted to the KV store, so they survive restarts.

For HTTP backends you can attach outbound credentials — Prism injects them into
upstream requests and the agent never sees them. There are four credential
types (`static`, `env`, `file`, `command`); see
[configuration.md#credentials](./configuration.md#credentials). Credentials
apply to HTTP/`url` backends only — they are rejected for stdio/`command` backends.

Verify the backend connected:

```bash
curl -s http://localhost:9086/api/v1/backends | jq .
```

## 3. Mint a token and connect an agent

The gateway exposes a single MCP endpoint at `http://localhost:8080/mcp`. The
embedded OAuth 2.1 server is always on (it can't be disabled) and is served on
the same `:8080` listener, exposing `POST /token`, `POST /register`,
`GET /authorize`, and the `.well-known` discovery documents.

Agents authenticate with `Authorization: Bearer <token>`. There are two paths:

- **Static client credentials** — define the agent under `policy.agents` (config) or add it from the console. The agent exchanges `client_id`/`client_secret` at `POST /token` (`grant_type=client_credentials`) for a Bearer access token (no refresh token), then sends it to `/mcp`.
- **Dynamic Client Registration (RFC 7591)** — clients like Claude Code and Cursor discover the gateway via RFC 9728, self-register at `POST /register`, and run the authorization-code + PKCE (S256) flow. DCR clients are registered with `token_endpoint_auth_method=none`, so they do *not* use client_credentials. On first connect, an admin names/approves the agent from the console; you then assign groups separately to grant tool access. Until then the agent has only `default_scopes` plus `mcp:connect`.

Mint a token from the shell for testing (static-client path):

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/token \
  -d "grant_type=client_credentials&client_id=my-agent&client_secret=change-me" | jq -r .access_token)

curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/mcp \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

> Agents always authenticate to the gateway with `Authorization: Bearer`.
> `X-API-Key` is only ever a *backend* credential header (Prism → upstream
> server), never an agent → gateway header.

### Claude Code (CLI)

Claude Code supports OAuth discovery: point it at the gateway and it self-registers a DCR client.

```json
// ~/.claude/mcp_servers.json
{
  "prism": {
    "type": "streamable-http",
    "url": "http://localhost:8080/mcp"
  }
}
```

The first connection prompts in the console for an admin to name/approve the
agent. To skip DCR and use a static token instead, add an
`Authorization: Bearer …` header as in the Claude Desktop example.

### Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "prism": {
      "type": "streamable-http",
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_TOKEN_HERE"
      }
    }
  }
}
```

Restart Claude Desktop. Tools from all backends appear in the tool picker, prefixed by namespace.

### Cursor

In Cursor settings (Settings → MCP Servers → Add):

```json
{
  "prism": {
    "type": "streamable-http",
    "url": "http://localhost:8080/mcp",
    "headers": {
      "Authorization": "Bearer YOUR_TOKEN_HERE"
    }
  }
}
```

### Windsurf

In `~/.windsurf/mcp_servers.json`:

```json
{
  "prism": {
    "type": "streamable-http",
    "url": "http://localhost:8080/mcp",
    "headers": {
      "Authorization": "Bearer YOUR_TOKEN_HERE"
    }
  }
}
```

### OpenAI Agents SDK (Python)

```python
from openai_agents import Agent, MCPServerStreamableHTTP

async with MCPServerStreamableHTTP(
    url="http://localhost:8080/mcp",
    headers={"Authorization": f"Bearer {TOKEN}"},
) as mcp:
    agent = Agent(
        name="my-agent",
        instructions="You have access to GitHub, DNS, and API tools.",
        mcp_servers=[mcp],
    )
    result = await agent.run("Look up the DNS records for example.com")
```

### Custom agent (Go)

```go
client := mcp.NewClient(&mcp.Implementation{Name: "my-agent", Version: "0.1.0"}, nil)

session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
    Endpoint: "http://localhost:8080/mcp",
    HTTPClient: &http.Client{
        Transport: &bearerTransport{base: http.DefaultTransport, token: TOKEN},
    },
}, nil)

// List tools — you'll see github__create_issue, dns__check-dns, etc.
tools, _ := session.ListTools(ctx, &mcp.ListToolsParams{})

// Call a tool
result, _ := session.CallTool(ctx, &mcp.CallToolParams{
    Name:      "dns__check-dns",
    Arguments: map[string]any{"hostname": "example.com"},
})

type bearerTransport struct {
    base  http.RoundTripper
    token string
}
func (t *bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
    r.Header.Set("Authorization", "Bearer "+t.token)
    return t.base.RoundTrip(r)
}
```

### Custom agent (Python)

```python
from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

async with streamablehttp_client(
    "http://localhost:8080/mcp",
    headers={"Authorization": f"Bearer {TOKEN}"},
) as (read, write, _):
    async with ClientSession(read, write) as session:
        await session.initialize()
        tools = await session.list_tools()
        result = await session.call_tool("dns__check-dns", {"hostname": "example.com"})
        print(result.content[0].text)
```

## 4. Verify end-to-end

**Tool discovery.** Ask your agent: "What tools do you have available?" Expect
namespaced tools like `github__create_issue`, `dns__check-dns`.

**Credential injection.** Watch the audit log (output is `stderr` in the
container by default). A tool call produces a one-line JSON entry; `cred_injected: true`
confirms Prism injected the backend credential and the agent never saw it.
Audit config lives in [configuration.md#audit-logging](./configuration.md#audit-logging).

**Admin API.** Check connected backends and health:

```bash
curl -s http://localhost:9086/api/v1/backends | jq .
curl -s http://localhost:9086/health
```

## Backends in depth

The console covers the common cases. The mechanics underneath — useful when you
run a sidecar bridge or build a backend by hand — are below. For the per-server
config fields and the five backend-source table, see
[configuration.md#backend-servers](./configuration.md#backend-servers).

### Bridge a stdio MCP server

Most MCP servers speak stdio (JSON-RPC over stdin/stdout). In the
single-container Docker install, Prism manages the bridge for you and spawns
each stdio backend in a sandbox container. When running from source — or
separating concerns with compose — you can run `prism-bridge` yourself:

```bash
prism-bridge serve --port 3001 -- npx @modelcontextprotocol/server-github
prism-bridge serve --port 3002 -- npx @modelcontextprotocol/server-filesystem /data
prism-bridge serve --port 3003 -- uvx mcp-server-postgres --connection-string "..."
```

The bridge spawns the stdio server, discovers its tools, and re-exposes them as
Streamable HTTP on `/mcp`. Prism then connects to `http://localhost:3001/mcp` as
if it were a native HTTP server. Bridging stdio means each server is isolated
(own process/container/limits), has no network endpoint of its own (agents can't
bypass Prism), and is managed through the same gateway surface as HTTP backends.

### Write a tool as a function

Don't want a full MCP server? Any script that reads JSON from stdin and writes
text to stdout is a tool:

```bash
#!/bin/bash
# check-dns.sh — reads {"hostname": "example.com"} from stdin
input=$(cat)
hostname=$(echo "$input" | grep -o '"hostname":"[^"]*"' | cut -d'"' -f4)
getent hosts "$hostname" | awk '{print $1}' | sort -u
```

Deploy it with the bridge in tool mode:

```bash
# Quick — name and description from CLI flags
prism-bridge tool --name check-dns --description "Resolve DNS for a hostname" \
  --port 3004 -- bash check-dns.sh

# Better — full manifest with input schema (so agents know what arguments to pass)
prism-bridge tool --manifest check-dns.json --port 3004 -- bash check-dns.sh
```

A tool manifest (`check-dns.json`):

```json
{
  "name": "check-dns",
  "description": "Resolve a hostname to its IP addresses",
  "input_schema": {
    "type": "object",
    "properties": {
      "hostname": {
        "type": "string",
        "description": "The hostname to resolve"
      }
    },
    "required": ["hostname"]
  }
}
```

The function contract:

| Channel | Purpose |
|---|---|
| stdin | JSON object of tool arguments |
| stdout | Result text (returned to agent) |
| stderr | Error message (if exit code ≠ 0) |
| exit 0 | Success |
| exit 1+ | Error |

Any language works — no SDK, no MCP knowledge. See `examples/tools/` for
ready-to-run bash and Python examples.

### Connect a native HTTP MCP server

If your server already speaks Streamable HTTP, point Prism at it directly — no
bridge needed. Add the URL from the Servers page, or seed it in config under
`mcpServers` with a `url`. Verify it responds to an MCP initialize:

```bash
curl -X POST http://localhost:3005/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}'
```

### Seeding backends in config (optional)

Backends are normally managed from the console (persisted to KV). If you prefer
to seed them on first boot, add an `mcpServers` map to your config file. Each
entry is keyed by namespace; stdio (`command`/`args`) and HTTP (`url`) entries
can coexist. The full field table — `enabled`, `credentials`, `timeout`,
`rate_limit`, `circuit_breaker`, `sandbox`, `workspace` — is in
[configuration.md#backend-servers](./configuration.md#backend-servers).

A minimal seed with an HTTP backend, a credential, and a static agent:

```json
{
  "listen": ":8080",
  "admin": ":9086",
  "mcpServers": {
    "api": {
      "url": "http://localhost:3005/mcp",
      "credentials": { "header": "X-API-Key", "value": "sk_live_your_key" }
    }
  },
  "policy": {
    "agents": {
      "my-agent": { "secret": "change-me", "groups": ["dev"] }
    },
    "groups": {
      "dev": { "scopes": ["api:*"] }
    }
  },
  "audit": { "enabled": true, "output": "stderr" }
}
```

`credentials.header` defaults to `Authorization` when omitted; the credential
type is inferred from the single field you set (`value`/`env`/`file`/`command`).
The `policy` block pre-seeds agents, groups, and scopes — with no `policy`
block, the embedded OAuth server is still on, but no static agents exist (add
them via the console or rely on DCR + approval). See
[configuration.md#authentication](./configuration.md#authentication) for the full
agent/group/scope model.

## Build from source

Docker is the recommended path. Build from source when you need to develop on
Prism or run it under systemd:

```bash
git clone https://github.com/1broseidon/prism.git
cd prism
make build   # → bin/prism, bin/prism-bridge, bin/prism-auth
make test
```

Run the gateway against a config file:

```bash
./bin/prism -config config.json
```

`bin/prism` is the gateway (admin UI, embedded OAuth server, MCP gateway).
`bin/prism-bridge` is the stdio-to-HTTP adapter. `bin/prism-auth` is a
standalone OAuth server for advanced separated deployments.

## Next steps

- **Configuration reference** — every field, default, credential type, and env var: [configuration.md](./configuration.md).
- **Deployment** — Docker, systemd, Kubernetes, reverse proxy (Caddy/nginx), and the production hardening checklist: [deployment.md](./deployment.md).
- **Admin API** — the `/api/v1` route ledger, root health/metrics/callback paths, and the admin/session access split: [admin-api.md](./admin-api.md).
- **Admin SSO** — protect the admin port with OIDC sign-in via the `admin_auth` block: [configuration.md#authentication](./configuration.md#authentication).
- **Rate limiting and circuit breakers** — global, per-backend, and per-policy controls: [configuration.md#rate-limiting](./configuration.md#rate-limiting) and [configuration.md#circuit-breaker](./configuration.md#circuit-breaker).
- **Workspace bridge** — run `prism-bridge workspace` next to your editor to expose a project tree as workspace-scoped MCP tools with sandbox snapshots and staged write-back.
- **Tracing** — set `OTEL_EXPORTER_OTLP_ENDPOINT` to ship request traces to an OpenTelemetry collector ([configuration.md#environment-variables](./configuration.md#environment-variables)).
