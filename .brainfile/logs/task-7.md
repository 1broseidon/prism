---
id: task-7
title: "Bridge management API: dynamic multi-backend process manager"
description: |-
  Transform prism-bridge from a single-backend-at-startup tool into a multi-backend process manager with an HTTP management API. This enables Prism's admin UI to add/remove MCP servers dynamically in containerized deployments without editing Docker Compose or Kubernetes manifests.

  ## Context

  Today prism-bridge has two modes:
  - `serve -- <command>`: spawns one stdio MCP server, proxies it over HTTP
  - `tool -- <command>`: wraps one function as an MCP tool

  Both are one-backend-per-process, configured at CLI startup. In containerized deployments, operators must pre-deploy one bridge container per backend and wire the URLs into Prism's config or Docker Compose.

  The goal is a third mode: `manage`, where the bridge starts empty and accepts API calls from Prism to spawn/stop stdio MCP servers on demand. Each spawned backend gets its own MCP client session and is proxied through the bridge's HTTP endpoint.

  ## New Subcommand

  ```
  prism-bridge manage [flags]
  ```

  Flags:
  - `--port <port>` — management + proxy listen port (default: 3001)
  - `--host <host>` — bind address (default: 0.0.0.0)
  - `--max-backends <n>` — maximum concurrent backends (default: 20, 0 = unlimited)

  ## Management API

  All management endpoints live under `/manage/` on the same port as the MCP proxy.

  ### POST /manage/spawn
  Request:
  ```json
  {
    "id": "github",
    "command": "npx",
    "args": ["@modelcontextprotocol/server-github"],
    "env": {"GITHUB_TOKEN": "ghp_xxx"}
  }
  ```
  Response (201):
  ```json
  {
    "id": "github",
    "endpoint": "/mcp/github",
    "tools": ["create_issue", "search_repos", "..."],
    "status": "running"
  }
  ```
  Errors:
  - 409 if `id` already exists
  - 400 if `command` is missing
  - 503 if max-backends limit reached
  - 500 if spawn or tool discovery fails

  Flow:
  1. Validate request, check ID uniqueness and backend limit
  2. Spawn child process via `exec.CommandContext`
  3. Connect MCP client via `mcp.CommandTransport`
  4. Discover tools via `session.ListTools()`
  5. Build an `mcp.Server` that proxies each tool to the backend session
  6. Register the server at `/mcp/{id}` and `/mcp/{id}/` on the shared HTTP mux
  7. Return tool list and endpoint to caller

  ### DELETE /manage/{id}
  Response (200):
  ```json
  {"id": "github", "status": "stopped"}
  ```
  Errors:
  - 404 if `id` not found

  Flow:
  1. Close the MCP client session (sends SIGTERM to child process)
  2. Remove the `/mcp/{id}` routes from the mux
  3. Clean up from the managed backends map

  ### GET /manage
  Response (200):
  ```json
  {
    "backends": [
      {
        "id": "github",
        "endpoint": "/mcp/github",
        "command": "npx",
        "args": ["@modelcontextprotocol/server-github"],
        "tools": 14,
        "status": "running",
        "started_at": "2026-03-27T10:00:00Z",
        "pid": 12345
      }
    ],
    "limit": 20,
    "count": 1
  }
  ```

  ### GET /manage/{id}
  Response (200): single backend status object (same shape as above)
  Errors:
  - 404 if `id` not found

  ### GET /health
  Response (200):
  ```json
  {"status": "ok", "mode": "manage", "backends": 3}
  ```
  Already exists for serve/tool modes; extend for manage mode.

  ## Internal Architecture

  ### ManagedBackend struct
  ```go
  type ManagedBackend struct {
      ID        string
      Command   string
      Args      []string
      Env       map[string]string
      Session   *mcp.ClientSession
      Server    *mcp.Server
      Tools     []string
      Cancel    context.CancelFunc  // cancels the backend context, kills child
      StartedAt time.Time
      PID       int                 // child process PID for status reporting
  }
  ```

  ### Manager struct
  ```go
  type Manager struct {
      mu         sync.RWMutex
      backends   map[string]*ManagedBackend
      mux        *http.ServeMux
      maxBackends int
      logger     *slog.Logger
  }
  ```

  The Manager owns the HTTP mux. Management API routes are registered at startup. MCP proxy routes (`/mcp/{id}`, `/mcp/{id}/`) are dynamically registered/deregistered as backends are spawned/stopped.

  ### Dynamic route registration
  Go's `http.ServeMux` does not support deregistration. Two options:
  1. Use a custom mux that supports dynamic routes
  2. Use a delegating handler that looks up the backend by ID on each request

  Option 2 is simpler: register a single `/mcp/` catch-all handler that extracts the backend ID from the path and dispatches to the correct `mcp.Server`. This avoids mux mutation entirely.

  ```go
  // Registered once at startup:
  mux.HandleFunc("/mcp/", manager.handleMCPProxy)

  func (m *Manager) handleMCPProxy(w http.ResponseWriter, r *http.Request) {
      // Extract backend ID from path: /mcp/{id} or /mcp/{id}/...
      id := extractBackendID(r.URL.Path)
      m.mu.RLock()
      backend, ok := m.backends[id]
      m.mu.RUnlock()
      if !ok {
          http.NotFound(w, r)
          return
      }
      // Delegate to the backend's MCP server handler
      handler := mcp.NewStreamableHTTPHandler(
          func(_ *http.Request) *mcp.Server { return backend.Server },
          nil,
      )
      // Strip the /mcp/{id} prefix before passing to the MCP handler
      http.StripPrefix("/mcp/"+id, handler).ServeHTTP(w, r)
  }
  ```

  Note: `mcp.NewStreamableHTTPHandler` should be created once per backend at spawn time and stored in `ManagedBackend`, not on every request.

  ### Child process lifecycle
  - Each backend gets its own `context.WithCancel` derived from the manager's context
  - On DELETE, cancel the context → CommandTransport sends SIGTERM → waits TerminateDuration (5s) → SIGKILL
  - On bridge shutdown (SIGINT/SIGTERM), cancel all backend contexts in parallel, wait for graceful shutdown
  - Monitor child process exit: if a child dies unexpectedly, mark the backend status as "exited" and log a warning. Do NOT auto-restart — let Prism detect the failure via health check and decide.

  ## Prism Gateway Integration

  ### Config change
  Add to `config.Config`:
  ```go
  // BridgeURL is the URL of a prism-bridge running in manage mode.
  // When set, command-type backends added via the admin UI are delegated
  // to the bridge instead of spawned locally by the gateway.
  BridgeURL string `json:"bridge_url,omitempty"`
  ```
  Propagate to `config.Loaded`.

  ### Admin backend add flow
  In `gateway.AddBackend()` (internal/gateway/manage.go):
  1. If `cfg.Command != ""` AND `g.bridgeURL != ""`:
     - POST to `{bridgeURL}/manage/spawn` with the command/args/env
     - On success, extract the endpoint path from response
     - Rewrite the backend as an HTTP backend: `sc.URL = bridgeURL + endpoint`
     - Clear `sc.Command` — Prism now treats it as a normal HTTP backend
  2. If `cfg.Command != ""` AND `g.bridgeURL == ""`:
     - Current behavior: spawn locally via CommandTransport
  3. If `cfg.URL != ""`:
     - Current behavior: connect to HTTP backend directly

  ### Admin backend remove flow
  In `gateway.RemoveBackend()`:
  1. If the backend was delegated to the bridge (track via metadata):
     - DELETE `{bridgeURL}/manage/{id}`
     - Then proceed with normal disconnect
  2. Otherwise: current behavior

  ### Persistence
  The `persistedBackend` struct already stores command/args/env/url. No changes needed.
  On restart, `LoadPersistedBackends` will re-delegate command backends to the bridge.

  ## Files to create/modify

  ### New files
  - `cmd/prism-bridge/manage.go` — Manager struct, management API handlers, MCP proxy dispatcher, child lifecycle
  - `cmd/prism-bridge/manage_test.go` — unit tests for Manager

  ### Modified files
  - `cmd/prism-bridge/main.go` — add `manage` subcommand dispatch, update usage text
  - `internal/config/config.go` — add `BridgeURL` to Config and Loaded
  - `internal/gateway/manage.go` — bridge delegation logic in AddBackend/RemoveBackend
  - `internal/gateway/gateway.go` — store bridgeURL, add BridgeURL accessor

  ## Test Plan
  - Unit test: Manager.Spawn with a simple echo MCP server (use `cmd/prism-bridge` itself in serve mode as the child, or a mock)
  - Unit test: Manager.Stop cleans up correctly
  - Unit test: concurrent spawn/stop operations
  - Unit test: max-backends limit enforcement
  - Unit test: MCP proxy routing dispatches to correct backend
  - Integration: admin UI adds a command backend → bridge spawns it → tools appear in Prism
  - Integration: admin UI removes backend → bridge stops it → tools disappear
priority: high
tags:
  - bridge
  - containers
  - admin-ui
  - phase-1
relatedFiles:
  - cmd/prism-bridge/main.go
  - cmd/prism-bridge/serve.go
  - cmd/prism-bridge/tool.go
  - internal/gateway/manage.go
  - internal/gateway/gateway.go
  - internal/config/config.go
  - internal/admin/backends.go
createdAt: "2026-03-27T09:30:00.000Z"
contract:
  status: draft
  deliverables:
    - type: file
      path: cmd/prism-bridge/manage.go
      description: Manager struct, management API, MCP proxy dispatcher, child lifecycle
    - type: file
      path: cmd/prism-bridge/manage_test.go
      description: Unit tests for Manager
    - type: file
      path: cmd/prism-bridge/main.go
      description: Add manage subcommand dispatch and updated usage
    - type: file
      path: internal/config/config.go
      description: Add BridgeURL field
    - type: file
      path: internal/gateway/manage.go
      description: Bridge delegation in AddBackend/RemoveBackend
    - type: file
      path: internal/gateway/gateway.go
      description: Store and expose bridgeURL
  validation:
    commands:
      - cd /home/george/Projects/personal/prism && go build ./...
      - cd /home/george/Projects/personal/prism && go test ./...
  constraints:
    - Dynamic route dispatch via catch-all /mcp/ handler, not mux mutation
    - Child processes get SIGTERM on stop, SIGKILL after 5s
    - Max-backends limit enforced at spawn time
    - Bridge delegation is transparent to the admin UI — same UX for local and containerized
    - Persisted backends survive bridge and Prism restarts
    - Do NOT auto-restart dead child processes — mark as exited and let Prism detect
completedAt: "2026-03-27T20:07:44.203Z"
updatedAt: "2026-03-27T20:07:44.203Z"
---

## Description
Transform prism-bridge from a single-backend-at-startup tool into a multi-backend process manager with an HTTP management API. This enables Prism's admin UI to add/remove MCP servers dynamically in containerized deployments without editing Docker Compose or Kubernetes manifests.

## Operator Experience

In containerized mode, the operator experience is identical to local mode:

1. Open admin UI at `http://prism:9090`
2. Click "+ Connect", enter name and command (e.g., `npx @modelcontextprotocol/server-github`)
3. Backend appears with tools — operator doesn't know or care that it's running inside a bridge container

The only deployment difference: one bridge container is running alongside Prism, and `bridge_url` is set in Prism's config.

## Security Note

This phase runs all backends as child processes in the same container. They share the container's env vars, filesystem, and network. Phase 2 (task-8) adds per-container isolation. This phase is suitable for trusted operators and dev/staging environments.
