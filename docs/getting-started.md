# Getting Started with Prism

This guide walks through setting up Prism end-to-end: backends behind the gateway, Prism in the middle, and agent harnesses connecting to it.

```
┌──────────────┐      ┌───────────┐      ┌─────────────────────────┐
│ Agent        │      │           │      │ Backends                │
│              │      │           │ ───→ │ bridge → github (stdio) │
│ Claude Code  │ ───→ │  Prism    │ ───→ │ bridge → check-dns (sh)│
│ Cursor       │      │  (:8080)  │ ───→ │ custom-api (native HTTP)│
│ Custom agent │      │           │      │                         │
└──────────────┘      └───────────┘      └─────────────────────────┘
```

## Fastest Start: Docker

For a homelab install, run one container. Prism stores state in `/data` and,
when the Docker socket is mounted, starts its built-in bridge manager so stdio
MCP servers such as `npx ...` and `uvx ...` run in sandbox containers.

```bash
docker run -d \
  --name prism \
  -p 8080:8080 \
  -p 9086:9086 \
  -v prism-data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/1broseidon/prism:latest
```

Open the admin UI at `http://localhost:9086` and add HTTP or stdio MCP servers
from the Servers page. Reverse proxies and sidecar bridges are optional
advanced deployment choices.

## Build From Source

```bash
git clone https://github.com/1broseidon/prism.git
cd prism
make build
# → bin/prism         (the gateway)
# → bin/prism-bridge  (the transport adapter)
```

## Step 1: Set Up Backends

Prism connects to backends over HTTP. There are three ways to set up a backend:

### Option A: Bridge a stdio MCP server (most common)

Most MCP servers speak stdio — they read/write JSON-RPC on stdin/stdout. In
the single-container Docker install, Prism manages the bridge for you and
spawns each stdio backend in a sandbox container. When running from source or
separating concerns with compose, you can run `prism-bridge` yourself.

```bash
# Wrap any stdio MCP server
prism-bridge serve --port 3001 -- npx @modelcontextprotocol/server-github
prism-bridge serve --port 3002 -- npx @modelcontextprotocol/server-filesystem /data
prism-bridge serve --port 3003 -- uvx mcp-server-postgres --connection-string "..."

# Or with Docker for isolation
docker run -p 3001:3001 prism-bridge serve --port 3001 -- npx @modelcontextprotocol/server-github
```

The bridge spawns the stdio server, discovers its tools, and re-exposes them as Streamable HTTP on `/mcp`. Prism connects to `http://localhost:3001/mcp` as if it were a native HTTP server.

**Why bridge stdio?** Prism is a gateway — it speaks HTTP. Running stdio
servers behind the bridge means:
- Each server is isolated (own process, own container, own resource limits)
- No network endpoint for the raw server (agents can't bypass Prism by curling it)
- Uniform transport — Prism can manage HTTP backends and stdio backends through
  the same gateway surface

### Option B: Write a tool as a function (simplest)

Don't want to build a full MCP server? Write a script. Any script that reads JSON from stdin and writes text to stdout is a tool:

```bash
#!/bin/bash
# check-dns.sh — reads {"hostname": "example.com"} from stdin
input=$(cat)
hostname=$(echo "$input" | grep -o '"hostname":"[^"]*"' | cut -d'"' -f4)
getent hosts "$hostname" | awk '{print $1}' | sort -u
```

```python
#!/usr/bin/env python3
# word-count.py — reads {"text": "..."} from stdin
import json, sys
data = json.load(sys.stdin)
text = data["text"]
print(f"Lines: {text.count(chr(10)) + 1}\nWords: {len(text.split())}\nCharacters: {len(text)}")
```

Deploy with the bridge in tool mode:

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

**The function contract:**

| Channel | Purpose |
|---|---|
| stdin | JSON object of tool arguments |
| stdout | Result text (returned to agent) |
| stderr | Error message (if exit code ≠ 0) |
| exit 0 | Success |
| exit 1+ | Error |

Any language works. No SDK required. No MCP knowledge required.

See `examples/tools/` for ready-to-use examples.

### Option C: Connect a native HTTP MCP server

If your MCP server already speaks Streamable HTTP, just point Prism at it directly — no bridge needed:

```bash
# Your server is already running at http://localhost:3005/mcp
# Just add it to Prism's config (Step 2)
```

### Verify backends are running

```bash
# Bridge health check
curl http://localhost:3001/health
# → {"status":"ok","tools":5}

# Native HTTP — send an MCP initialize request
curl -X POST http://localhost:3005/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}'
```

## Step 2: Configure Prism

Create `config.json` pointing to your backends. `mcpServers` is a map keyed by namespace — stdio (`command`/`args`) and HTTP (`url`) entries can coexist:

```json
{
  "listen": ":8080",
  "admin": ":9086",
  "mcpServers": {
    "github": {
      "url": "http://localhost:3001/mcp",
      "credentials": { "env": "GITHUB_TOKEN" }
    },
    "dns": {
      "url": "http://localhost:3004/mcp"
    },
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
      "dev": { "scopes": ["github:*", "dns:*", "api:*"] }
    }
  },
  "audit": {
    "enabled": true,
    "output": "stderr"
  }
}
```

**Key points:**
- Prism doesn't know or care whether a backend is bridged or native — it's all HTTP
- The map key is the namespace. Tools are exposed as `namespace__toolname` (e.g. `github__create_issue`, `dns__check-dns`)
- `credentials` are injected by Prism into outbound requests — the agent never sees them. The credential type is the field that's set (`env`, `value`, `file`, or `command`)
- `policy` defines how agents authenticate *to Prism*. When present, Prism embeds an OAuth 2.1 auth server; agents use the `secret` as their `client_secret` (client-credentials grant). Omit `policy` to run open in development.

### Start Prism

```bash
export GITHUB_TOKEN="Bearer ghp_xxxxxxxxxxxx"
./bin/prism -config config.json
```

### Verify Prism

```bash
# Gateway health
curl http://localhost:9086/health
# → {"status":"ok"}

# Connected backends
curl http://localhost:9086/backends
# → [{"id":"github","namespace":"github","url":"http://localhost:3001/mcp"}, ...]
```

## Step 3: Connect Agent Harnesses

Prism exposes a single MCP endpoint at `http://localhost:8080/mcp`. Any MCP-compatible agent connects here instead of directly to backends.

### Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "prism": {
      "transport": "streamable-http",
      "url": "http://localhost:8080/mcp",
      "headers": {
        "X-API-Key": "my-agent-key"
      }
    }
  }
}
```

Restart Claude Desktop. You should see tools from all backends in the tool picker, prefixed by namespace.

**Before Prism:** You'd configure each MCP server separately, each with its own credentials in the config. Every server's API keys sit in a plaintext JSON file on the user's machine.

**After Prism:** One endpoint, one API key. Backend credentials live on the server running Prism, never on the developer's laptop.

### Claude Code (CLI)

Add to `~/.claude/mcp_servers.json` (global) or `.claude/mcp_servers.json` (per-project):

```json
{
  "prism": {
    "transport": "streamable-http",
    "url": "http://localhost:8080/mcp",
    "headers": {
      "X-API-Key": "my-agent-key"
    }
  }
}
```

### Cursor

In Cursor settings (Settings → MCP Servers → Add):

```json
{
  "prism": {
    "transport": "streamable-http",
    "url": "http://localhost:8080/mcp",
    "headers": {
      "X-API-Key": "my-agent-key"
    }
  }
}
```

### Windsurf

In `~/.windsurf/mcp_servers.json`:

```json
{
  "prism": {
    "transport": "streamable-http",
    "url": "http://localhost:8080/mcp",
    "headers": {
      "X-API-Key": "my-agent-key"
    }
  }
}
```

### OpenAI Agents SDK (Python)

```python
from openai_agents import Agent, MCPServerStreamableHTTP

async with MCPServerStreamableHTTP(
    url="http://localhost:8080/mcp",
    headers={"X-API-Key": "my-agent-key"},
) as mcp:
    agent = Agent(
        name="my-agent",
        instructions="You have access to GitHub, DNS, and API tools.",
        mcp_servers=[mcp],
    )
    result = await agent.run("Look up the DNS records for example.com")
```

### Custom Agent (Go)

```go
client := mcp.NewClient(&mcp.Implementation{Name: "my-agent", Version: "0.1.0"}, nil)

session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
    Endpoint: "http://localhost:8080/mcp",
    HTTPClient: &http.Client{
        Transport: &headerTransport{
            base:   http.DefaultTransport,
            header: "X-API-Key",
            value:  "my-agent-key",
        },
    },
}, nil)

// List tools — you'll see github__create_issue, dns__check-dns, etc.
tools, _ := session.ListTools(ctx, &mcp.ListToolsParams{})

// Call a tool
result, _ := session.CallTool(ctx, &mcp.CallToolParams{
    Name:      "dns__check-dns",
    Arguments: map[string]any{"hostname": "example.com"},
})

// headerTransport sets the auth header on every request.
type headerTransport struct {
    base   http.RoundTripper
    header, value string
}
func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
    r.Header.Set(t.header, t.value)
    return t.base.RoundTrip(r)
}
```

### Custom Agent (Python)

```python
from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

async with streamablehttp_client(
    "http://localhost:8080/mcp",
    headers={"X-API-Key": "my-agent-key"},
) as (read, write, _):
    async with ClientSession(read, write) as session:
        await session.initialize()
        tools = await session.list_tools()
        result = await session.call_tool("dns__check-dns", {"hostname": "example.com"})
        print(result.content[0].text)
```

## Step 4: Verify End-to-End

### 1. Check tool discovery

Ask your agent: "What tools do you have available?"

Expected: namespaced tools like `github__create_issue`, `dns__check-dns`, `api__whatever`.

### 2. Check credential injection

Watch the audit log (stderr or your configured output):

```json
{"ts":"2025-01-15T10:30:00Z","namespace":"github","tool":"create_issue","allowed":true,"cred_injected":true,...}
```

`cred_injected: true` confirms Prism injected the backend credential. The agent never saw it.

### 3. Check the admin API

```bash
curl -s http://localhost:9086/backends | jq .
curl -s http://localhost:9086/health
```

## What You've Achieved

```
Before Prism:
  Agent → GitHub API  (holds ghp_xxx token)
  Agent → Postgres    (holds db password)
  Agent → Filesystem  (unrestricted access)
  No audit trail. Agent has all the keys. Can curl anything.

After Prism:
  Agent → Prism (one API key or OAuth token)
       ├→ bridge (isolated container) → GitHub stdio server
       ├→ bridge (isolated container) → check-dns.sh
       └→ native HTTP API server
  Every call audited. Credentials injected. Agent holds nothing.
  Stdio servers have no network endpoint — can't be curled.
```

## Production Setup

For production, swap API key auth for OAuth 2.1 and run bridges in containers:

```yaml
# docker-compose.yml
services:
  prism:
    build: .
    ports: ["8080:8080", "9086:9086"]
    volumes: ["./config.json:/etc/prism/config.json:ro"]

  bridge-github:
    image: ghcr.io/prism-gateway/bridge
    command: ["serve", "--port", "3001", "--", "npx", "@modelcontextprotocol/server-github"]
    environment:
      GITHUB_PERSONAL_ACCESS_TOKEN: ${GITHUB_TOKEN}

  bridge-dns:
    image: ghcr.io/prism-gateway/bridge
    command: ["tool", "--manifest", "/tools/check-dns.json", "--port", "3002", "--", "bash", "/tools/check-dns.sh"]
    volumes: ["./examples/tools:/tools:ro"]
```

Each bridge is isolated: own container, own resources, own network namespace. A buggy or compromised MCP server can't affect Prism or other backends.

## Next Steps

- **OAuth 2.1 auth**: See the [config reference](../README.md#oauth-21-production--agentic) for Keycloak, Auth0, or any OIDC provider
- **Scope enforcement**: With OAuth, agents only see tools their token grants access to
- **Deployment**: See [deployment.md](deployment.md) for systemd, Docker, Kubernetes, and production hardening
- **Credential rotation**: Use `command`-type credentials with Vault or cloud CLI for automatic rotation
- **Write your own tools**: See `examples/tools/` for bash and Python examples
