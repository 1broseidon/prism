---
id: task-8
title: Per-container backend isolation in bridge manage mode
description: |-
  Extend the bridge manager (task-7) to spawn each MCP backend in its own container instead of as a child process. This provides full isolation: separate env vars, filesystem, PID namespace, and network namespace per backend.

  ## Context

  Task-7 delivers a bridge management API that spawns child processes. This works but has a security gap: all backends share the bridge container's environment. A malicious MCP server could read env vars intended for other backends, access the shared filesystem, or interfere with other processes.

  This task adds a `--runtime docker` flag to `manage` mode. When set, the bridge creates a new container per backend instead of forking a child process. Each container is created from the bridge's own image (or a configurable image) with a `serve -- <command>` entrypoint. The bridge proxies MCP traffic to the container's HTTP endpoint.

  ## Topology

  ```
  ┌─ bridge container (manage mode) ──────────────────────┐
  │  Management API  (:3001/manage/*)                      │
  │  MCP Proxy       (:3001/mcp/{id})                      │
  │         │                    │                          │
  │         ▼                    ▼                          │
  │  ┌─ container: github ─┐  ┌─ container: postgres ─┐   │
  │  │ prism-bridge serve   │  │ prism-bridge serve     │  │
  │  │ -- npx ...           │  │ -- uvx ...             │  │
  │  │ :0 (dynamic port)    │  │ :0 (dynamic port)     │  │
  │  └─────────────────────┘  └────────────────────────┘   │
  └────────────────────────────────────────────────────────┘
  ```

  Actually, the spawned containers are siblings, not nested. The bridge talks to them over the Docker network:

  ```
  Docker network (private)
  ├── bridge (manage mode, :3001)
  │     ├── POST /manage/spawn → docker create + start
  │     └── /mcp/{id} → proxy to container's :3001/mcp
  ├── prism-managed-github (:3001, not published)
  │     └── prism-bridge serve -- npx @modelcontextprotocol/server-github
  └── prism-managed-postgres (:3001, not published)
        └── prism-bridge serve -- uvx postgres-mcp
  ```

  ## Command Changes

  ```
  prism-bridge manage --runtime docker [flags]
  ```

  New flags:
  - `--runtime <process|docker>` — backend isolation mode (default: process)
  - `--image <image>` — container image for spawned backends (default: same image as bridge, detected from /proc/self/cgroup or configurable)
  - `--network <network>` — Docker network to attach spawned containers to (default: bridge's own network)
  - `--label-prefix <prefix>` — label prefix for spawned containers (default: "prism.bridge")

  ## Spawn Flow (Docker Runtime)

  When `POST /manage/spawn` is received in docker runtime mode:

  1. Validate request (same as process mode)
  2. Build container config:
     - Image: `--image` flag value
     - Command: `["serve", "--port", "3001", "--", <command>, <args...>]`
     - Env: only the env vars specified in the spawn request — NOT the bridge's own env
     - Network: `--network` flag value
     - Labels: `prism.bridge.id={id}`, `prism.bridge.managed=true`
     - Name: `prism-managed-{id}` (configurable prefix)
     - No published ports — internal network only
  3. Create container via Docker API
  4. Start container
  5. Wait for health check (`GET :3001/health`) with backoff (max 30s)
  6. Proxy `/mcp/{id}` to `http://prism-managed-{id}:3001/mcp`
  7. Return tool list and endpoint to caller

  ## Stop Flow (Docker Runtime)

  When `DELETE /manage/{id}` is received:

  1. Stop the container (SIGTERM, 10s timeout, then SIGKILL)
  2. Remove the container
  3. Remove proxy route
  4. Clean up from managed backends map

  ## Manager Changes

  ### Runtime interface
  ```go
  // Runtime abstracts how the manager spawns and stops backends.
  type Runtime interface {
      // Spawn starts a backend and returns its MCP endpoint URL.
      Spawn(ctx context.Context, req SpawnRequest) (*SpawnResult, error)
      // Stop terminates a backend.
      Stop(ctx context.Context, id string) error
      // Status returns the current status of a backend.
      Status(ctx context.Context, id string) (BackendStatus, error)
      // Cleanup removes all managed backends (called on bridge shutdown).
      Cleanup(ctx context.Context) error
  }

  type SpawnRequest struct {
      ID      string
      Command string
      Args    []string
      Env     map[string]string
  }

  type SpawnResult struct {
      Endpoint string   // e.g., "http://prism-managed-github:3001/mcp"
      Tools    []string // discovered tool names
  }
  ```

  ### ProcessRuntime
  Refactored from task-7's direct child spawning. Same behavior, just behind the Runtime interface.

  ### DockerRuntime
  Implements Runtime using the Docker Engine API (`github.com/docker/docker/client`).

  ```go
  type DockerRuntime struct {
      client    *client.Client
      image     string
      network   string
      prefix    string  // container name prefix
      logger    *slog.Logger
  }
  ```

  ### Manager refactored
  ```go
  type Manager struct {
      mu          sync.RWMutex
      backends    map[string]*ManagedBackend
      runtime     Runtime
      maxBackends int
      logger      *slog.Logger
  }
  ```

  The Manager no longer cares how backends are spawned. It delegates to the Runtime interface. The management API handlers and MCP proxy dispatcher remain unchanged.

  ### ManagedBackend changes
  In docker mode, the ManagedBackend no longer holds a Session or Server directly. Instead it holds:
  ```go
  type ManagedBackend struct {
      ID          string
      Command     string
      Args        []string
      Env         map[string]string
      Endpoint    string         // upstream URL (container endpoint)
      Handler     http.Handler   // reverse proxy to the container
      Tools       []string
      StartedAt   time.Time
      ContainerID string         // Docker container ID (docker runtime only)
      PID         int            // child PID (process runtime only)
      Status      string         // "running", "exited", "stopped"
  }
  ```

  The MCP proxy for docker mode is a simple HTTP reverse proxy to the container's endpoint, not an in-process MCP server. This is important: the bridge doesn't do MCP protocol handling for docker-spawned backends, it just proxies HTTP.

  ## Environment Isolation

  Critical security property: each spawned container receives ONLY the env vars from the spawn request. The bridge's own env vars (which may contain secrets for other backends or the bridge itself) are never passed to spawned containers.

  ```go
  // In DockerRuntime.Spawn:
  containerConfig := &container.Config{
      Image: d.image,
      Cmd:   append([]string{"serve", "--port", "3001", "--"}, req.Command, req.Args...),
      Env:   envFromMap(req.Env), // ONLY spawn request env, not os.Environ()
  }
  ```

  ## Health Checking

  After starting a container, the bridge polls `GET /health` on the container's internal endpoint:
  - Retry with exponential backoff: 100ms, 200ms, 400ms, 800ms, 1.6s, 3.2s, 6.4s, 12.8s (max 30s total)
  - On timeout: stop and remove the container, return 500 to spawn caller
  - On success: proceed with tool discovery (GET the MCP endpoint, or just trust the health check since tools are discovered by the serve subprocess)

  Since each container runs `prism-bridge serve`, tool discovery happens inside the container. The managing bridge just needs to know the container is healthy and proxy traffic to it.

  ## Bridge Shutdown

  On SIGINT/SIGTERM to the managing bridge:
  1. Stop accepting new spawn requests
  2. For each managed container: stop (SIGTERM, 10s wait, SIGKILL) and remove
  3. Shutdown the management API HTTP server
  4. Exit

  Containers are labeled with `prism.bridge.managed=true`. On startup, the bridge can optionally clean up orphaned containers from a previous crash:
  ```
  docker ps -a --filter label=prism.bridge.managed=true -q | xargs docker rm -f
  ```

  ## Docker Compose Integration

  Updated example:
  ```yaml
  services:
    prism:
      image: prism
      ports: ["8080:8080", "9090:9090"]
      volumes:
        - ./config.json:/etc/prism/config.json:ro
      depends_on:
        bridge:
          condition: service_healthy

    bridge:
      image: prism-bridge
      command: ["manage", "--runtime", "docker", "--network", "prism_default"]
      volumes:
        - /var/run/docker.sock:/var/run/docker.sock
      healthcheck:
        test: ["CMD", "wget", "-q", "--spider", "http://localhost:3001/health"]
        interval: 10s
      networks:
        - default
  ```

  Prism config:
  ```json
  {
    "bridge_url": "http://bridge:3001",
    "mcpServers": { ... }
  }
  ```

  Operator adds MCP server via admin UI → Prism tells bridge → bridge creates container → tools available. No manifest editing.

  ## Files to create/modify

  ### New files
  - `cmd/prism-bridge/runtime.go` — Runtime interface definition
  - `cmd/prism-bridge/runtime_process.go` — ProcessRuntime (refactored from task-7)
  - `cmd/prism-bridge/runtime_docker.go` — DockerRuntime implementation
  - `cmd/prism-bridge/runtime_docker_test.go` — DockerRuntime tests (requires Docker daemon or mock)

  ### Modified files
  - `cmd/prism-bridge/manage.go` — refactor Manager to use Runtime interface, add --runtime flag
  - `cmd/prism-bridge/main.go` — pass --runtime/--image/--network flags through
  - `go.mod` — add `github.com/docker/docker` dependency

  ## Test Plan
  - Unit test: DockerRuntime.Spawn creates container with correct config (env isolation, labels, network)
  - Unit test: DockerRuntime.Stop removes container
  - Unit test: DockerRuntime.Cleanup removes all managed containers
  - Unit test: ProcessRuntime still works (regression)
  - Integration test: spawn via docker runtime → health check passes → proxy works → stop removes container
  - Security test: spawned container's env does NOT contain bridge's own env vars
  - Shutdown test: bridge shutdown cleans up all managed containers
priority: high
tags:
  - bridge
  - containers
  - security
  - docker
  - phase-2
dependsOn:
  - task-7
relatedFiles:
  - cmd/prism-bridge/manage.go
  - cmd/prism-bridge/main.go
  - cmd/prism-bridge/Dockerfile
createdAt: "2026-03-27T09:30:00.000Z"
contract:
  status: draft
  deliverables:
    - type: file
      path: cmd/prism-bridge/runtime.go
      description: Runtime interface definition
    - type: file
      path: cmd/prism-bridge/runtime_process.go
      description: ProcessRuntime — child process spawning (refactored)
    - type: file
      path: cmd/prism-bridge/runtime_docker.go
      description: DockerRuntime — per-container backend isolation
    - type: file
      path: cmd/prism-bridge/runtime_docker_test.go
      description: DockerRuntime tests
    - type: file
      path: cmd/prism-bridge/manage.go
      description: Refactored Manager using Runtime interface
  validation:
    commands:
      - cd /home/george/Projects/personal/prism && go build ./...
      - cd /home/george/Projects/personal/prism && go test ./...
  constraints:
    - Spawned containers receive ONLY the env vars from the spawn request, never the bridge's own env
    - Runtime interface allows process and docker modes without changing the Manager or API
    - Docker runtime requires Docker socket access — document as a known requirement
    - Orphaned containers from previous crashes are cleaned up on bridge startup
    - Container names use prism-managed- prefix and prism.bridge.managed=true label
    - Health check with exponential backoff, 30s max before failing the spawn
completedAt: "2026-03-27T20:07:45.908Z"
updatedAt: "2026-03-27T20:07:45.908Z"
---

## Description
Extend the bridge manager (task-7) to spawn each MCP backend in its own container instead of as a child process. This provides full isolation: separate env vars, filesystem, PID namespace, and network namespace per backend. A malicious MCP server cannot read env vars or interfere with other backends.

## Security Model
The core security property: each MCP server runs in its own container with only the environment variables explicitly provided for that server. The bridge's env, filesystem, and process space are completely separate from any spawned backend.
