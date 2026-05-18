---
id: task-13
title: Align Prism MCP OAuth discovery with GitHub/VS Code remote MCP behavior
description: |-
  Current status as of 2026-05-16:

  - GitHub's hosted MCP endpoint responds to unauthenticated initialize with
    `401` and `WWW-Authenticate: Bearer ... resource_metadata=...`.
  - Its protected-resource metadata declares resource
    `https://api.githubcopilot.com/mcp/` and authorization server
    `https://github.com/login/oauth`.
  - GitHub's authorization-server metadata does not expose
    `registration_endpoint`, so generic clients cannot DCR themselves. VS Code's
    smooth OAuth UX comes from VS Code/GitHub being a supported, registered MCP
    host, not from a server-specific rule that Prism should copy.
  - Prism now advertises its MCP resource as `https://mcp.dfam.one/mcp` in
    protected-resource metadata and includes
    `resource_metadata="https://mcp.dfam.one/.well-known/oauth-protected-resource/mcp"`
    on `401` from `/mcp`.
  - Caddy routes both `/.well-known/oauth-protected-resource` and
    `/.well-known/oauth-protected-resource/*` to the gateway, so path-specific
    resource metadata works publicly.
  - Upstream OAuth backend flows now keep separate backend URL and OAuth
    resource URL fields. Authorization/token `resource` parameters use the
    resource declared by protected-resource metadata, while backend connection
    and persistence keep the operator-entered backend URL.
  - Upstream protected-resource lookup accepts canonical trailing-slash variants
    before falling back to origin-level resources, which matches real GitHub MCP
    behavior without adding a GitHub-specific rule.
priority: high
tags:
  - oauth
  - mcp
  - vscode
  - github
  - handoff
createdAt: "2026-05-16T23:08:00Z"
completedAt: "2026-05-16T23:08:00Z"
---

## Handoff

Decisions made:

- Keep OAuth backend onboarding generic: follow RFC 9728 protected-resource
  discovery, RFC 8414 authorization-server discovery, PKCE, DCR when available,
  and manual client credentials only when the provider does not support DCR.
- Do not add per-MCP-server rules for GitHub. GitHub remote MCP requires a
  registered host app/OAuth app for OAuth, or PAT auth, because it does not
  publish a dynamic registration endpoint.
- Next UX improvement should be provider-neutral: add reusable OAuth client
  profiles keyed by authorization server issuer, and consider Client ID Metadata
  Document support for providers that advertise it.

Verification:

- `go test ./...`
- `go test -tags mcp_go_client_oauth ./...`
- Live `https://mcp.dfam.one/mcp` now returns a `401` with the path-specific
  `resource_metadata` URL.
- Live `https://mcp.dfam.one/.well-known/oauth-protected-resource/mcp` returns
  JSON with `resource: "https://mcp.dfam.one/mcp"`.
