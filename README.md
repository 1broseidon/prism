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

## Quickstart

Run the published image. Port `8080` is the MCP gateway (agents connect here); port `9086` is the admin console.

```bash
docker run -d \
  --name prism \
  -p 8080:8080 \
  -p 9086:9086 \
  -v prism-data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/1broseidon/prism:latest
```

Then open the admin console:

```
http://localhost:9086
```

Add a backend from the **Servers** page (HTTP URL, stdio command, OpenAPI spec, or a managed binary). That's it — Prism is now fronting your MCP servers.

Notes:
- `prism-data` (mounted at `/data`) holds the bbolt store, signing key, KV encryption key, analytics, and binstore. The image already pins `PRISM_KV_KEY_FILE` and `PRISM_SIGNING_KEY_FILE` to this volume, so issued tokens and credentials survive restarts.
- The Docker socket is optional. With it mounted, Prism spawns sandboxed stdio backends in isolated containers. Native HTTP MCP backends work without it.
- Pin a reproducible tag with `ghcr.io/1broseidon/prism:0.1.0`.

See [docs/getting-started.md](./docs/getting-started.md) for the full walkthrough, or [docs/deployment.md](./docs/deployment.md) for production topologies.

## Connect an agent

Define an agent first — add a `policy.agents` entry (see [Configuration](./docs/configuration.md#authentication)) or create one from the admin console. Then mint its token and point your MCP client at the gateway with `Authorization: Bearer`:

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/token \
  -d "grant_type=client_credentials&client_id=my-agent&client_secret=change-me" | jq -r .access_token)

curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/mcp \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

MCP client config (e.g. Claude Desktop):

```json
{ "prism": { "type": "streamable-http", "url": "http://localhost:8080/mcp",
  "headers": { "Authorization": "Bearer TOKEN" } } }
```

DCR-capable clients (Claude Code) self-register via `POST /register` and the authorization-code + PKCE flow — omit the `headers` block and approve the agent from the admin console.

Full harness setup (Claude Desktop, Claude Code, Cursor, Windsurf, OpenAI Agents SDK, custom Go/Python) is in [docs/getting-started.md](./docs/getting-started.md).

## What you get

- **Credential brokering** — static, env var, file, or shell-command credentials injected outbound; the agent never sees raw values.
- **OAuth 2.1 + RFC 9728** — always-on embedded auth server: token validation, scope enforcement, DCR, Protected Resource Metadata discovery.
- **Scope-filtered discovery** — `tools/list` returns only tools the agent is authorized to use.
- **Namespace aggregation** — N backends appear as one MCP server; tools are prefixed (`github__create_issue`, `fs__read_file`).
- **Five backend sources** — native HTTP, bridged stdio (`npx`/`uvx`/binaries), tool functions, OpenAPI specs, and managed binaries.
- **Sandboxed stdio** — with the Docker socket, stdio backends run in isolated containers with CPU/memory/PID limits and read-only rootfs.
- **Structured audit log** — every tool call, allowed or denied, emits a single-line JSON entry for SIEM ingestion.
- **Rate limiting + circuit breaking** — global, per-backend, and per-policy token buckets; per-backend failure isolation.
- **Admin console + API** — backends, agents, groups, policies, grants, analytics, and audit history on a separate port.
- **Admin SSO** — optional OIDC sign-in (Google, Okta, Auth0, Keycloak, …) with email/domain/group role mapping.

## How it works

Prism is both an MCP server facing agents (Streamable HTTP) and multiple MCP clients facing backends. Native HTTP MCP servers connect directly; stdio servers and tool functions are wrapped as HTTP by `prism-bridge`, each in its own isolated container. Tools from all backends are aggregated under namespace prefixes — `tools/list` returns the scope-filtered union, and `tools/call` routes to the right backend by prefix.

```
                    ┌─────────────────────────────────────┐
                    │              Prism                   │
Agent ──Bearer──→   │  Auth ─→ Scope Filter ─→ Router      │
                    │         ┌─────────────────┼────────┐ │
                    │         │  Credential     │ Audit  │ │
                    │         │  Store          │ Logger │ │
                    │         └────┬────────────┴────────┘ │
                    └──────────────┼────────────────────────┘
                                   │
              ┌────────────────────┼────────────────────┐
              ▼                    ▼                     ▼
     ┌──────────────┐    ┌──────────────┐     ┌──────────────┐
     │ Bridge       │    │ Bridge       │     │ Native HTTP  │
     │ stdio→HTTP   │    │ func→HTTP    │     │              │
     │ npx github   │    │ python       │     │ custom-api   │
     └──────────────┘    └──────────────┘     └──────────────┘
```

Prism builds three static binaries: `prism` (the gateway), `prism-bridge` (the stdio↔HTTP adapter), and `prism-auth` (a standalone OAuth server for separated deployments — most setups use the auth server embedded in `prism`). The published container images bundle `prism` and `prism-bridge`; `prism-auth` ships only as a release binary. Built on the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk).

## Documentation

- [Getting Started](./docs/getting-started.md) — Docker quickstart, adding backends, connecting every agent harness.
- [Configuration](./docs/configuration.md) — the full config reference: fields, backends, credentials, auth, audit, rate limits, storage, TLS, environment variables.
- [Deployment](./docs/deployment.md) — Docker, systemd, Kubernetes, reverse proxies, production hardening.
- [Admin API](./docs/admin-api.md) — the admin route ledger (`:9086`, JSON under `/api/v1`).

## Build from source

Docker is the recommended path; build from source when you need a local toolchain.

```bash
git clone https://github.com/1broseidon/prism.git
cd prism
make build    # bin/prism, bin/prism-bridge, bin/prism-auth
make test
```

Requires Go 1.26+ (and golangci-lint for `make lint`).

## License

Apache 2.0
