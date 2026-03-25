# MCPGate

A lightweight MCP (Model Context Protocol) gateway that aggregates multiple backend MCP servers behind a single endpoint.

Connect N MCP servers, expose them as one. Clients see a unified tool/resource/prompt namespace.

## Features

- **Tool aggregation** — `tools/list` merges tools from all backends with namespace prefixes
- **Tool routing** — `tools/call` routes to the correct backend by prefix
- **Resource & prompt aggregation** — same pattern for resources and prompts
- **Per-server circuit breaking** — isolate backend failures
- **Per-client rate limiting** — token bucket per API key
- **API key auth** — simple header-based client authentication
- **Per-server auth injection** — each backend can have its own credentials
- **Hot reload** — add/remove servers via config reload (SIGHUP or admin API)
- **Admin API** — tool inventory, server health, call metrics, reload status
- **Single binary** — one JSON config, no dependencies beyond the binary

## Quick Start

```bash
go build -o mcpgate ./cmd/mcpgate
./mcpgate -config config.json
```

## Config

```json
{
  "listen_addr": ":8080",
  "admin_addr": ":9090",
  "servers": [
    {
      "id": "github",
      "url": "http://localhost:3001/mcp",
      "namespace": "github",
      "auth": {
        "header": "Authorization",
        "value": "Bearer $GITHUB_TOKEN"
      }
    },
    {
      "id": "filesystem",
      "url": "http://localhost:3002/mcp",
      "namespace": "fs"
    }
  ],
  "auth": {
    "header": "X-API-Key",
    "valid_keys": ["my-secret-key"]
  },
  "rate_limit": {
    "requests_per_second": 100,
    "burst": 200
  }
}
```

## Architecture

MCPGate acts as both:
- An **MCP server** (Streamable HTTP) facing clients
- Multiple **MCP clients** connecting to backend servers

```
Client → [MCPGate Server] → [Client A] → Backend MCP Server A
                           → [Client B] → Backend MCP Server B
                           → [Client C] → Backend MCP Server C
```

Built on the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk).

## License

Apache 2.0
