---
id: task-2
title: "SIGHUP reload: merge config + KV state correctly"
description: |-
  Consolidates old task-3 and task-6. Both are symptoms of the same bug: SIGHUP only diffs mcpServers, ignoring policy and DCR state.

  After task-1 lands, the auth server has two client sources:
  - Static: config `policy.agents` → expandPolicy() → ClientConfig entries
  - Dynamic: KV store → DCR clients with PrismID-based group assignments

  On SIGHUP:
  1. Reload and re-validate config
  2. Rebuild static clients from `policy.agents` (re-run expandPolicy)
  3. Re-read `default_scopes` and update on auth server
  4. Call `loadPersistedState()` to restore dynamic clients from KV
  5. Merge: static clients from config + dynamic clients from KV → s.clients map
  6. Dynamic group assignments survive inherently (they're in KV, not config)

  Must not blow away in-memory DCR registrations or their PrismID-based policy assignments.
priority: high
tags:
  - bug
  - policy
  - reliability
relatedFiles:
  - cmd/prism/main.go
  - internal/authserver/authserver.go
  - internal/authserver/persist.go
  - internal/config/config.go
createdAt: "2026-03-26T03:24:02.649Z"
contract:
  status: delivered
  deliverables:
    - type: file
      path: internal/authserver/authserver.go
      description: Add ReloadPolicy method to auth server
    - type: file
      path: cmd/prism/main.go
      description: Update SIGHUP handler to reload policy and merge clients
    - type: test
      path: internal/authserver/authserver_test.go
      description: Tests for reload preserving dynamic clients and updating scopes
  validation:
    commands:
      - cd /home/george/Projects/personal/prism && go test ./...
      - cd /home/george/Projects/personal/prism && go vet ./...
  constraints:
    - DCR registrations must survive SIGHUP
    - Dynamic client KV policy assignments must be preserved
    - Refresh tokens must survive SIGHUP
    - Group removal means zero scopes from that group, not an error
    - Must re-resolve dynamic client scopes from fresh group definitions
    - Must continue diffing mcpServers for backend add/remove
  metrics:
    readyAt: "2026-03-26T03:38:06.207Z"
    pickedUpAt: "2026-03-26T03:38:09.325Z"
    reworkCount: 0
    deliveredAt: "2026-03-26T03:42:20.833Z"
    duration: 252
updatedAt: "2026-03-26T13:59:06.582Z"
completedAt: "2026-03-26T13:59:06.582Z"
---

## Description
Consolidates old task-3 and task-6. Both are symptoms of the same bug: SIGHUP only diffs mcpServers, ignoring policy and DCR state.

After task-1 lands, the auth server has two client sources:
- Static: config `policy.agents` → expandPolicy() → ClientConfig entries
- Dynamic: KV store → DCR clients with PrismID-based group assignments

On SIGHUP:
1. Reload and re-validate config
2. Rebuild static clients from `policy.agents` (re-run expandPolicy)
3. Re-read `default_scopes` and update on auth server
4. Call `loadPersistedState()` to restore dynamic clients from KV
5. Merge: static clients from config + dynamic clients from KV → s.clients map
6. Dynamic group assignments survive inherently (they're in KV, not config)

Must not blow away in-memory DCR registrations or their PrismID-based policy assignments.
