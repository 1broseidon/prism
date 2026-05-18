---
id: task-10
title: Restore Docker-managed bridge runtime for stdio MCP servers
description: |-
  Current status as of 2026-05-16:

  - Restored `prism-bridge manage` from the old clean-slate stash onto the current worktree.
  - Added process and Docker runtimes for dynamic backend spawn/delete through `/manage/spawn`, `/manage/{id}`, `/manage`, and `/health`.
  - Wired `compose.yml` with a reproducible `prism-bridge` service using `prism-bridge:full`, Docker runtime, the `prism_default` network, and Docker socket access only in the manager container.
  - Set `deploy/config.json` `bridge_url` to `http://prism-bridge:3001`.
  - Updated Prism startup so config-defined stdio backends route through the bridge when `bridge_url` is set, instead of attempting to exec `npx`/`uvx` inside the Prism container.
  - Added Docker runtime hardening for spawned backend containers: no published ports, request-scoped environment only, `CapDrop: ALL`, and `no-new-privileges:true`.
  - Verified direct bridge spawn/delete for `npx -y @brainfile/cli mcp` and `uvx mcp-server-time --local-timezone UTC`; both ran in Docker-managed sibling containers and cleaned up.
  - Full verification passed: `go test ./...`, `go test -tags mcp_go_client_oauth ./...`, `golangci-lint run ./...`, `npm --prefix internal/admin/web run build`, and `git diff --check`.

  Notes:
  - The old orphan `prism-bridge-1` container from the previous `docker-compose.yml` still exists and is healthy, but Prism is configured to use the new compose-managed `prism-prism-bridge-1` service via DNS name `prism-bridge`.
  - The checked-in `deploy/config.json` still has an `example` stdio backend that runs `echo placeholder`; it correctly goes through the bridge now, but it is not a real MCP server and fails the 30 second health probe.
priority: high
tags:
  - bridge
  - docker
  - stdio
  - handoff
createdAt: "2026-05-16T21:00:00Z"
completedAt: "2026-05-16T21:00:00Z"
---

## Handoff

The proper bridge restoration is now in the working tree, not just in the old orphan image. Continue from the dirty worktree carefully: many unrelated OAuth, admin, UI, Caddy, and test files were already modified before this bridge work.
