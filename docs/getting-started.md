# Getting Started with Prism

This guide walks through setting up Prism end-to-end: MCP servers behind the gateway, Prism in the middle, and agent harnesses connecting to it.

```
┌──────────────┐      ┌───────────┐      ┌──────────────────┐
│ Agent        │      │           │      │ MCP Servers       │
│              │      │           │ ───→ │ github (port 3001)│
│ Claude Code  │ ───→ │  Prism    │ ───→ │ filesystem (3002) │
│ Cursor       │      │  (:8080)  │ ───→ │ postgres (3003)   │
│ Custom agent │      │           │      │                   │
└──────────────┘      └───────────┘      └──────────────────┘
```

## Step 1: Set Up MCP Servers

Prism connects to any MCP server that speaks Streamable HTTP. Here are common ways to run them.

### Using npx (quickest)

Many MCP servers are published as npm packages:

```bash
# GitHub MCP server
npx @modelcontextprotocol/server-github --port 3001

# Filesystem MCP server
npx @modelcontextprotocol/server-filesystem --port 3002 /path/to/allowed/dir

# PostgreSQL MCP server
npx @modelcontextprotocol/server-postgres --port 3003 \
  "postgresql://user:pass@localhost:5432/mydb"
```

### Using Docker

```bash
# GitHub MCP server
docker run -p 3001:3001 \
  -e GITHUB_PERSONAL_ACCESS_TOKEN=ghp_xxx \
  ghcr.io/modelcontextprotocol/server-github

# Filesystem server
docker run -p 3002:3002 \
  -v /data:/data:ro \
  ghcr.io/modelcontextprotocol/server-filesystem /data
```

### Writing Your Own (Go)

A minimal MCP server using the Go SDK:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "net/http"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
    server := mcp.NewServer(&mcp.Implementation{
        Name:    "my-server",
        Version: "0.1.0",
    }, nil)

    server.AddTool(&mcp.Tool{
        Name:        "greet",
        Description: "Say hello",
        InputSchema: mcp.ToolInputSchema{
            Type: "object",
            Properties: map[string]*mcp.Property{
                "name": {Type: "string", Description: "Name to greet"},
            },
            Required: []string{"name"},
        },
    }, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        name, _ := req.Arguments["name"].(string)
        return &mcp.CallToolResult{
            Content: []mcp.Content{{Type: "text", Text: fmt.Sprintf("Hello, %s!", name)}},
        }, nil
    })

    handler := mcp.NewStreamableHTTPHandler(
        func(r *http.Request) *mcp.Server { return server },
        nil,
    )

    http.Handle("/mcp", handler)
    http.Handle("/mcp/", handler)
    log.Fatal(http.ListenAndServe(":3001", nil))
}
```

### Writing Your Own (Python)

```python
from mcp.server import Server
from mcp.server.transports import StreamableHTTPTransport

server = Server("my-server")

@server.tool("greet", description="Say hello")
async def greet(name: str) -> str:
    return f"Hello, {name}!"

transport = StreamableHTTPTransport(host="0.0.0.0", port=3001)
server.run(transport)
```

### Verify Your Backends

Before configuring Prism, make sure each backend is reachable:

```bash
# Quick health check — send an MCP initialize request
curl -X POST http://localhost:3001/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}'
```

You should get a JSON-RPC response with the server's capabilities.

## Step 2: Configure Prism

Create `config.json` pointing to your running backends:

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
    },
    {
      "id": "filesystem",
      "url": "http://localhost:3002/mcp",
      "namespace": "fs"
    },
    {
      "id": "postgres",
      "url": "http://localhost:3003/mcp",
      "namespace": "db",
      "credentials": {
        "type": "static",
        "header": "Authorization",
        "value": "Bearer db-access-token"
      }
    }
  ],
  "auth": {
    "header": "X-API-Key",
    "valid_keys": ["my-agent-key"]
  },
  "audit": {
    "enabled": true,
    "output": "stderr"
  }
}
```

**Key points:**
- Each backend gets a `namespace`. Tools are exposed as `namespace__toolname` (e.g. `github__create_issue`)
- `credentials` are injected by Prism into outbound requests — the agent never sees them
- `auth` defines how agents authenticate *to Prism* (API key for dev, OAuth for production)

### Start Prism

```bash
export GITHUB_TOKEN="Bearer ghp_xxxxxxxxxxxx"
./prism -config config.json
```

### Verify Prism

```bash
# Gateway health
curl http://localhost:9090/health
# → {"status":"ok"}

# Connected backends
curl http://localhost:9090/backends
# → [{"id":"github","status":"connected"}, ...]

# List tools through the gateway (with auth)
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "X-API-Key: my-agent-key" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}'
```

## Step 3: Connect Agent Harnesses

Prism exposes a single MCP endpoint at `http://localhost:8080/mcp`. Any MCP-compatible agent connects here instead of directly to backend servers.

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

Restart Claude Desktop. You should see tools from all backends in the tool picker, prefixed by namespace (e.g. `github__create_issue`, `fs__read_file`, `db__query`).

**Before Prism:** You'd configure each MCP server separately, each with its own credentials in the config. Every server's API keys sit in a plaintext JSON file on the user's machine.

**After Prism:** One endpoint, one API key. Backend credentials live on the server running Prism, never on the developer's laptop.

### Claude Code (CLI)

Claude Code reads from `~/.claude/mcp_servers.json`:

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

Or set it per-project in `.claude/mcp_servers.json` at the repo root.

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
        instructions="You have access to GitHub, filesystem, and database tools.",
        mcp_servers=[mcp],
    )
    result = await agent.run("Create a GitHub issue about the bug in main.py")
```

### Custom Agent (Go)

```go
package main

import (
    "context"
    "fmt"
    "net/http"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
    client := mcp.NewClient(&mcp.Implementation{
        Name:    "my-agent",
        Version: "0.1.0",
    }, nil)

    transport := &mcp.StreamableClientTransport{
        Endpoint: "http://localhost:8080/mcp",
        HTTPClient: &http.Client{
            Transport: &headerTransport{
                base:   http.DefaultTransport,
                header: "X-API-Key",
                value:  "my-agent-key",
            },
        },
    }

    session, err := client.Connect(context.Background(), transport, nil)
    if err != nil {
        panic(err)
    }
    defer session.Close()

    // List all available tools
    tools, err := session.ListTools(context.Background(), nil)
    if err != nil {
        panic(err)
    }
    for _, t := range tools.Tools {
        fmt.Printf("  %s — %s\n", t.Name, t.Description)
    }

    // Call a tool
    result, err := session.CallTool(context.Background(), &mcp.CallToolRequest{
        Name:      "github__create_issue",
        Arguments: map[string]any{
            "title": "Bug in main.py",
            "body":  "Found a null pointer on line 42",
        },
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Content[0].Text)
}

type headerTransport struct {
    base   http.RoundTripper
    header string
    value  string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    req.Header.Set(t.header, t.value)
    return t.base.RoundTrip(req)
}
```

### Custom Agent (Python)

```python
import httpx
from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

async with streamablehttp_client(
    "http://localhost:8080/mcp",
    headers={"X-API-Key": "my-agent-key"},
) as (read, write, _):
    async with ClientSession(read, write) as session:
        await session.initialize()

        # List tools
        tools = await session.list_tools()
        for tool in tools.tools:
            print(f"  {tool.name} — {tool.description}")

        # Call a tool
        result = await session.call_tool(
            "github__create_issue",
            arguments={"title": "Bug", "body": "Details here"},
        )
        print(result.content[0].text)
```

## Step 4: Verify End-to-End

Once everything is wired up:

### 1. Check tool discovery

Your agent should see namespaced tools. Ask it: "What tools do you have available?"

Expected: tools like `github__create_issue`, `fs__read_file`, `db__query`.

### 2. Check credential injection

Watch the audit log (stderr or your configured output):

```bash
# You should see entries like:
{"ts":"2025-01-15T10:30:00Z","namespace":"github","tool":"create_issue","allowed":true,"cred_injected":true,...}
```

`cred_injected: true` confirms Prism injected the backend credential. The agent never saw it.

### 3. Check the admin API

```bash
# All backends connected?
curl -s http://localhost:9090/backends | jq .

# Gateway healthy?
curl -s http://localhost:9090/health
```

## What You've Achieved

```
Before Prism:
  Agent → GitHub API  (holds ghp_xxx token)
  Agent → Postgres    (holds db password)
  Agent → Filesystem  (unrestricted access)
  No audit trail. Agent has all the keys.

After Prism:
  Agent → Prism (one API key or OAuth token)
       → Prism injects GitHub token    → GitHub MCP
       → Prism injects DB credential   → Postgres MCP  
       → Prism passes through          → Filesystem MCP
  Every call audited. Agent holds nothing.
```

## Next Steps

- **Production auth**: Switch from API keys to [OAuth 2.1](../README.md#oauth-21-production--agentic) with Keycloak, Auth0, or any OIDC provider
- **Scope enforcement**: With OAuth, agents only see tools their token grants access to
- **Deployment**: See [deployment.md](deployment.md) for systemd, Docker, Kubernetes, and production hardening
- **Credential rotation**: Use `command`-type credentials with Vault or cloud CLI for automatic rotation
