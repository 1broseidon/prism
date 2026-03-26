---
id: task-6
title: OAuth client flow for upstream MCP server authentication
column: review
position: 0
description: |-
  Prism needs to act as an OAuth client when connecting to upstream MCP servers that require OAuth 2.1 authentication. Today Prism only supports static credentials (API keys, env vars, commands). OAuth-protected backends require the full authorization code + PKCE flow.

  ## Operator Flow

  1. Operator clicks "+ Connect" in dashboard, enters name + URL
  2. Prism probes the URL → gets 401 with WWW-Authenticate header
  3. Dashboard detects OAuth requirement → shows "Authenticate" button
  4. Operator clicks "Authenticate" → opens new browser tab to auth server's consent page
  5. Operator authorizes → auth server redirects to Prism's callback URL
  6. Prism exchanges auth code for tokens via PKCE
  7. Tokens stored in KV (encrypted at rest)
  8. Backend connects with token → tools discovered → done

  ## Architecture

  ```
  Dashboard                    Prism Gateway                   Upstream MCP Server
  POST /backends/github        │                                │
  {url: "https://..."}         │                                │
  ────────────────────────────>│ GET /mcp                       │
                               │────────────────────────────────>│
                               │ 401 + WWW-Authenticate         │
                               │<────────────────────────────────│
  {status: "auth_required",   │                                │
   auth_url: "https://..."}   │                                │
  <────────────────────────────│                                │
                               │                                │
  [Operator clicks Authenticate, opens browser]                 │
                               │                                │
  Callback: GET /oauth/callback?code=xxx                        │
                               │ POST /token (code + PKCE)      │
                               │────────────────────────────────>│
                               │ {access_token, refresh_token}  │
                               │<────────────────────────────────│
                               │ [Store tokens in KV]           │
                               │ GET /mcp (Bearer token)        │
                               │────────────────────────────────>│
                               │ 200 + tools                    │
                               │<────────────────────────────────│
  {status: "ok", tools: 14}   │                                │
  <────────────────────────────│                                │
  ```

  ## Implementation Pieces (in order)

  ### 1. OAuth credential type
  New `credentials.OAuth` that wraps `oauth2.TokenSource`. Implements `Credential` interface — resolves to `Authorization: Bearer {token}` on each call. Auto-refreshes via oauth2 library.

  ### 2. OAuth probe on backend add
  When `POST /backends/{id}` receives a URL, probe it first. If 401 + `WWW-Authenticate` with `resource_metadata`:
  - Discover protected resource metadata (RFC 9728)
  - Discover auth server metadata
  - Register as client via DCR (RFC 7591) 
  - Return `{status: "auth_required", auth_url: "...", state: "...", backend_id: "..."}` to the dashboard
  - Store pending auth flow in memory (keyed by state parameter)

  ### 3. Auth flow manager
  In-memory map of pending OAuth flows keyed by `state` parameter:
  ```go
  type PendingAuthFlow struct {
      BackendID     string
      Config        *oauth2.Config
      CodeVerifier  string
      State         string
      ResourceURL   string
      CreatedAt     time.Time
  }
  ```
  Flows expire after 10 minutes. Completed flows are cleaned up.

  ### 4. Callback endpoint
  `GET /oauth/callback` on admin port (9090):
  - Receives `?code=xxx&state=yyy`
  - Looks up pending flow by state
  - Exchanges code for tokens using PKCE verifier
  - Stores tokens in KV under `backend/oauth/{backend_id}`
  - Creates `credentials.OAuth` and registers with credential store
  - Connects the backend
  - Returns HTML page that closes the popup: `<script>window.close()</script>`

  ### 5. Dashboard UX
  When backend add probe returns `auth_required`:
  - Show "Authenticate" button in the inline form
  - On click: `window.open(auth_url, '_blank', 'width=600,height=700')`
  - Poll `GET /backends/{id}/auth-status` every 2 seconds
  - When status changes to "connected", refresh backends list
  - Timeout after 5 minutes with "Authentication timed out"

  ### 6. Token KV persistence
  KV key: `backend/oauth/{backend_id}`
  ```json
  {
    "access_token": "...",
    "refresh_token": "...",
    "token_type": "Bearer",
    "expiry": "2026-03-27T00:00:00Z",
    "client_id": "...",
    "client_secret": "...",
    "token_url": "...",
    "auth_style": 1
  }
  ```
  On startup, `LoadPersistedBackends` checks for OAuth tokens and creates `oauth2.TokenSource` with auto-refresh. If the refresh token is expired, the backend connects but tools may 401 — dashboard shows "re-authenticate" button.

  ### 7. Security considerations
  - OAuth tokens in KV: bbolt is a local file, protected by filesystem permissions. For production, consider encrypting at rest.
  - State parameter: cryptographic random, single-use, expires in 10 minutes.
  - PKCE: S256 challenge method, required.
  - Redirect URI: `http://localhost:9090/oauth/callback` — admin port is internal-only.
  - Client credentials from DCR: persisted alongside OAuth tokens for refresh.

  ## Key constraints
  - Do NOT use the SDK's build-tagged `AuthorizationCodeHandler` — implement using `oauthex` package directly (no build tag needed, full control)
  - The `oauthex` package provides: `GetProtectedResourceMetadata`, `GetAuthServerMeta`, `RegisterClient`, `ParseWWWAuthenticate`
  - Use standard `golang.org/x/oauth2` for token exchange and refresh
  - The callback endpoint must be on the admin port (9090), not the MCP port (8080)
  - Tokens auto-refresh via `oauth2.TokenSource` — Prism should never need operator re-auth unless refresh token expires
  - Backend config persistence (already built) handles the URL/command — OAuth tokens are a separate KV entry

  ## Files to create/modify
  - `internal/credentials/oauth.go` (new) — OAuth credential type
  - `internal/gateway/oauth.go` (new) — auth flow manager, probe, callback
  - `internal/gateway/gateway.go` — wire callback endpoint, load OAuth creds on startup  
  - `internal/admin/admin.go` — mount callback route on admin mux
  - `internal/admin/backends.go` — extend BackendConfig for OAuth, auth-status endpoint
  - `internal/admin/ui.html` — Authenticate button, polling, status display
priority: high
tags:
  - oauth
  - backends
  - security
relatedFiles:
  - internal/credentials/store.go
  - internal/gateway/gateway.go
  - internal/gateway/manage.go
  - internal/admin/backends.go
  - internal/admin/ui.html
createdAt: "2026-03-26T22:01:59.682Z"
contract:
  status: delivered
  deliverables:
    - type: file
      path: internal/credentials/oauth.go
      description: OAuth credential type wrapping oauth2.TokenSource
    - type: file
      path: internal/gateway/oauth.go
      description: Auth flow manager - probe, pending flows, callback, token persistence
    - type: file
      path: internal/gateway/gateway.go
      description: Wire OAuth callback, load OAuth creds on startup
    - type: file
      path: internal/admin/admin.go
      description: Mount callback route on admin mux
    - type: file
      path: internal/admin/backends.go
      description: Extend for OAuth probe response and auth-status endpoint
    - type: file
      path: internal/admin/ui.html
      description: Authenticate button, polling, popup UX
  validation:
    commands:
      - cd /home/george/Projects/personal/prism && go build ./...
      - cd /home/george/Projects/personal/prism && go test ./...
  constraints:
    - Do NOT use SDK build-tagged AuthorizationCodeHandler - use oauthex directly
    - PKCE S256 required for all OAuth flows
    - Callback on admin port 9090 only
    - Tokens auto-refresh via oauth2.ReuseTokenSource
    - "State parameter: cryptographic random, single-use, 10-min expiry"
    - "Redirect URI: http://localhost:9090/oauth/callback"
    - "DCR metadata: client_name Prism Gateway, grant_types authorization_code, token_endpoint_auth_method none"
  metrics:
    readyAt: "2026-03-26T22:58:26.455Z"
    pickedUpAt: "2026-03-26T22:58:29.300Z"
    reworkCount: 0
    deliveredAt: "2026-03-26T23:12:10.783Z"
    duration: 821
updatedAt: "2026-03-26T23:12:10.783Z"
---

## Description
Prism needs to act as an OAuth client when connecting to upstream MCP servers that require OAuth 2.1 authentication. Today Prism only supports static credentials (API keys, env vars, commands). OAuth-protected backends require the full authorization code + PKCE flow.

## Operator Flow

1. Operator clicks "+ Connect" in dashboard, enters name + URL
2. Prism probes the URL → gets 401 with WWW-Authenticate header
3. Dashboard detects OAuth requirement → shows "Authenticate" button
4. Operator clicks "Authenticate" → opens new browser tab to auth server's consent page
5. Operator authorizes → auth server redirects to Prism's callback URL
6. Prism exchanges auth code for tokens via PKCE
7. Tokens stored in KV (encrypted at rest)
8. Backend connects with token → tools discovered → done

## Architecture

```
Dashboard                    Prism Gateway                   Upstream MCP Server
POST /backends/github        │                                │
{url: "https://..."}         │                                │
────────────────────────────>│ GET /mcp                       │
                             │────────────────────────────────>│
                             │ 401 + WWW-Authenticate         │
                             │<────────────────────────────────│
{status: "auth_required",   │                                │
 auth_url: "https://..."}   │                                │
<────────────────────────────│                                │
                             │                                │
[Operator clicks Authenticate, opens browser]                 │
                             │                                │
Callback: GET /oauth/callback?code=xxx                        │
                             │ POST /token (code + PKCE)      │
                             │────────────────────────────────>│
                             │ {access_token, refresh_token}  │
                             │<────────────────────────────────│
                             │ [Store tokens in KV]           │
                             │ GET /mcp (Bearer token)        │
                             │────────────────────────────────>│
                             │ 200 + tools                    │
                             │<────────────────────────────────│
{status: "ok", tools: 14}   │                                │
<────────────────────────────│                                │
```

## Implementation Pieces (in order)

### 1. OAuth credential type
New `credentials.OAuth` that wraps `oauth2.TokenSource`. Implements `Credential` interface — resolves to `Authorization: Bearer {token}` on each call. Auto-refreshes via oauth2 library.

### 2. OAuth probe on backend add
When `POST /backends/{id}` receives a URL, probe it first. If 401 + `WWW-Authenticate` with `resource_metadata`:
- Discover protected resource metadata (RFC 9728)
- Discover auth server metadata
- Register as client via DCR (RFC 7591) 
- Return `{status: "auth_required", auth_url: "...", state: "...", backend_id: "..."}` to the dashboard
- Store pending auth flow in memory (keyed by state parameter)

### 3. Auth flow manager
In-memory map of pending OAuth flows keyed by `state` parameter:
```go
type PendingAuthFlow struct {
    BackendID     string
    Config        *oauth2.Config
    CodeVerifier  string
    State         string
    ResourceURL   string
    CreatedAt     time.Time
}
```
Flows expire after 10 minutes. Completed flows are cleaned up.

### 4. Callback endpoint
`GET /oauth/callback` on admin port (9090):
- Receives `?code=xxx&state=yyy`
- Looks up pending flow by state
- Exchanges code for tokens using PKCE verifier
- Stores tokens in KV under `backend/oauth/{backend_id}`
- Creates `credentials.OAuth` and registers with credential store
- Connects the backend
- Returns HTML page that closes the popup: `<script>window.close()</script>`

### 5. Dashboard UX
When backend add probe returns `auth_required`:
- Show "Authenticate" button in the inline form
- On click: `window.open(auth_url, '_blank', 'width=600,height=700')`
- Poll `GET /backends/{id}/auth-status` every 2 seconds
- When status changes to "connected", refresh backends list
- Timeout after 5 minutes with "Authentication timed out"

### 6. Token KV persistence
KV key: `backend/oauth/{backend_id}`
```json
{
  "access_token": "...",
  "refresh_token": "...",
  "token_type": "Bearer",
  "expiry": "2026-03-27T00:00:00Z",
  "client_id": "...",
  "client_secret": "...",
  "token_url": "...",
  "auth_style": 1
}
```
On startup, `LoadPersistedBackends` checks for OAuth tokens and creates `oauth2.TokenSource` with auto-refresh. If the refresh token is expired, the backend connects but tools may 401 — dashboard shows "re-authenticate" button.

### 7. Security considerations
- OAuth tokens in KV: bbolt is a local file, protected by filesystem permissions. For production, consider encrypting at rest.
- State parameter: cryptographic random, single-use, expires in 10 minutes.
- PKCE: S256 challenge method, required.
- Redirect URI: `http://localhost:9090/oauth/callback` — admin port is internal-only.
- Client credentials from DCR: persisted alongside OAuth tokens for refresh.

## Key constraints
- Do NOT use the SDK's build-tagged `AuthorizationCodeHandler` — implement using `oauthex` package directly (no build tag needed, full control)
- The `oauthex` package provides: `GetProtectedResourceMetadata`, `GetAuthServerMeta`, `RegisterClient`, `ParseWWWAuthenticate`
- Use standard `golang.org/x/oauth2` for token exchange and refresh
- The callback endpoint must be on the admin port (9090), not the MCP port (8080)
- Tokens auto-refresh via `oauth2.TokenSource` — Prism should never need operator re-auth unless refresh token expires
- Backend config persistence (already built) handles the URL/command — OAuth tokens are a separate KV entry

## Files to create/modify
- `internal/credentials/oauth.go` (new) — OAuth credential type
- `internal/gateway/oauth.go` (new) — auth flow manager, probe, callback
- `internal/gateway/gateway.go` — wire callback endpoint, load OAuth creds on startup  
- `internal/admin/admin.go` — mount callback route on admin mux
- `internal/admin/backends.go` — extend BackendConfig for OAuth, auth-status endpoint
- `internal/admin/ui.html` — Authenticate button, polling, status display
