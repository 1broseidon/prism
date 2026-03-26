---
id: task-1
title: PrismID policy binding + admin group management
description: |-
  Consolidates old task-7 and task-1. PrismID becomes the policy target for DCR agents. Config agents keep name=client_id (no PrismID needed).

  Key invariant: the gateway stays stateless — scopes live in the JWT, resolved at token mint time by the auth server. PrismID never reaches the gateway.

  Work:
  1. KV schema: `policy/agent/{prism_id}` → `{groups: [], grant: [], deny: []}` (same shape as config AgentConfig)
  2. `resolveAgentScopesByPrismID()` — reads KV, runs through existing group → grant → deny logic
  3. `handleRefreshToken` and `handleAuthCodeExchange` re-resolve scopes at mint time via PrismID lookup (not stale ClientConfig.AllowedScopes)
  4. Admin API: `PUT /admin/agents/{prism_id}/policy` (set groups/grant/deny), `GET /admin/agents` (list with identity + current policy)
  5. Replace `UpdateAgentScopes(clientID, scopes)` with PrismID-based group assignment
  6. Optional: `prism_id` custom JWT claim for audit enrichment (gateway ignores it)

  Traps to avoid:
  - Do NOT put PrismID in JWT `sub` (breaks OAuth 2.1 spec)
  - Do NOT add gateway-side PrismID lookups (breaks stateless design)
  - Do NOT force PrismIDs onto config agents (they use name=client_id)
priority: critical
tags:
  - policy
  - identity
  - admin-api
relatedFiles:
  - internal/authserver/oauth.go
  - internal/authserver/persist.go
  - internal/authserver/authserver.go
  - internal/config/config.go
  - internal/admin/admin.go
createdAt: "2026-03-26T03:23:52.214Z"
contract:
  status: delivered
  deliverables:
    - type: file
      path: internal/authserver/policy.go
      description: PrismID-based scope resolution and KV policy read/write
    - type: file
      path: internal/admin/agents.go
      description: Admin API endpoints for agent listing and policy CRUD
    - type: test
      path: internal/authserver/policy_test.go
      description: Tests for scope resolution by PrismID
    - type: test
      path: internal/admin/agents_test.go
      description: Tests for admin API agent/policy endpoints
    - type: file
      path: internal/authserver/authserver.go
      description: Remove UpdateAgentScopes, wire PrismID resolution
    - type: file
      path: internal/authserver/oauth.go
      description: Re-resolve scopes at token mint time for DCR agents
    - type: file
      path: internal/auth/token.go
      description: Add optional prism_id claim to JWT
    - type: file
      path: internal/audit/log.go
      description: Add PrismID to audit Entry struct
  validation:
    commands:
      - cd /home/george/Projects/personal/prism && go build ./...
      - cd /home/george/Projects/personal/prism && go test ./...
      - cd /home/george/Projects/personal/prism && go vet ./...
  constraints:
    - Gateway stays stateless - no PrismID lookups at gateway
    - Do NOT put PrismID in JWT sub claim
    - Do NOT force PrismIDs onto config agents
    - Use existing KV store interface and persistence patterns
    - Config agents continue using config-defined scopes unchanged
    - Scope resolution happens only at auth server at token mint time
  metrics:
    readyAt: "2026-03-26T03:26:48.437Z"
    pickedUpAt: "2026-03-26T03:26:51.275Z"
    reworkCount: 0
    deliveredAt: "2026-03-26T03:36:27.191Z"
    duration: 576
updatedAt: "2026-03-26T13:59:06.103Z"
completedAt: "2026-03-26T13:59:06.103Z"
---

## Description
Consolidates old task-7 and task-1. PrismID becomes the policy target for DCR agents. Config agents keep name=client_id (no PrismID needed).

Key invariant: the gateway stays stateless — scopes live in the JWT, resolved at token mint time by the auth server. PrismID never reaches the gateway.

Work:
1. KV schema: `policy/agent/{prism_id}` → `{groups: [], grant: [], deny: []}` (same shape as config AgentConfig)
2. `resolveAgentScopesByPrismID()` — reads KV, runs through existing group → grant → deny logic
3. `handleRefreshToken` and `handleAuthCodeExchange` re-resolve scopes at mint time via PrismID lookup (not stale ClientConfig.AllowedScopes)
4. Admin API: `PUT /admin/agents/{prism_id}/policy` (set groups/grant/deny), `GET /admin/agents` (list with identity + current policy)
5. Replace `UpdateAgentScopes(clientID, scopes)` with PrismID-based group assignment
6. Optional: `prism_id` custom JWT claim for audit enrichment (gateway ignores it)

Traps to avoid:
- Do NOT put PrismID in JWT `sub` (breaks OAuth 2.1 spec)
- Do NOT add gateway-side PrismID lookups (breaks stateless design)
- Do NOT force PrismIDs onto config agents (they use name=client_id)
