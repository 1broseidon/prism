# Admin API Contract

Prism's admin listener defaults to `:9086` (configurable via the `admin` field — see [Configuration](./configuration.md#top-level-fields)). The admin UI and operator scripts use this API. To enable sign-in and roles, configure [admin authentication](./configuration.md#authentication).

## Mount layout

| Surface | Method + Path | Notes |
|---|---|---|
| JSON API | `/api/v1/*` | Versioned admin JSON endpoints. Handlers see paths without `/api/v1`. |
| Admin auth callback | `GET /auth/callback` | Root-level OIDC redirect target. Do not move under `/api/v1`. |
| Backend OAuth callback | `GET /oauth/callback` | Root-level callback for outbound OAuth to upstream backends. Registered only when an outbound OAuth callback handler is wired. |
| Liveness | `GET /health` | Root-level health probe for orchestrators. |
| Metrics | `GET /metrics` | Root-level Prometheus endpoint. Registered only when metrics are enabled. |
| Admin UI | `/` | SPA fallback for browser routes (method-agnostic). |

## Authentication model

When admin auth is configured, read routes require a signed-in admin or viewer session and mutation routes require an admin session. When admin auth is disabled, these wrappers are pass-through for local/open deployments and tests.

## Stable route ledger

All paths below are mounted under `/api/v1` unless marked root-level.

### Auth and session

These three are registered directly on the mux and are not wrapped by the session/admin middleware.

| Method | Path | Access | Purpose |
|---|---|---|---|
| GET | `/auth/me` | public | Current session/user; returns `{"auth":"open"}` when admin auth is disabled. |
| GET | `/auth/login` | public | Start admin login. A per-IP login rate limit applies even in open mode. |
| POST | `/auth/logout` | public | End admin session. |
| GET | `/auth/callback` | root-level | OIDC redirect target. |
| GET | `/oauth/callback` | root-level | Upstream backend OAuth callback when enabled. |

### Read-only status and inventory

| Method | Path | Access | Purpose |
|---|---|---|---|
| GET | `/backends` | session | List backend status. |
| GET | `/backends/{id}...` | session | Backend detail/subresource dispatch. |
| GET | `/info` | session | Server/admin info. |
| GET | `/agents` | session | List agents. |
| GET | `/agents/roles` | session | List agent roles. |
| GET | `/agents/{prism_id}/policy-resolution` | session | Explain effective policy for an agent. |
| GET | `/agents/{prism_id}...` | session | Agent detail compatibility route. |
| GET | `/agents/policy-summary` | session | Cached policy summary for all agents. |
| GET | `/events` | session | Recent audit/admin events. |
| GET | `/groups` | session | List groups. |
| GET | `/groups/{id}...` | session | Group detail compatibility route. |
| GET | `/defaults` | session | Default policy settings. |
| GET | `/workspaces` | session | List registered workspaces. |
| GET | `/workspaces/{id}` | session | Workspace detail. |
| GET | `/identity` | session | List identity records. |
| GET | `/identity/{kind-or-id}...` | session | Identity subresource dispatch. |
| GET | `/binaries/{hash}` | session | Inspect uploaded/fetched binary metadata. |

### Configuration

| Method | Path | Access | Purpose |
|---|---|---|---|
| GET | `/config/admin-auth` | admin | Read admin auth config. |
| PUT | `/config/admin-auth` | admin | Replace admin auth config. |
| POST | `/config/admin-auth/test` | admin | Test admin auth discovery/config. |
| POST | `/config/admin-auth/enable` | admin | Enable admin auth. |
| DELETE | `/config/admin-auth/enable` | admin | Disable admin auth. |
| GET | `/config/network` | admin | Read runtime network settings. |
| PUT | `/config/network` | admin | Update runtime network settings. |
| GET | `/config/workspace-bridge` | admin | Read workspace bridge settings. |
| PUT | `/config/workspace-bridge` | admin | Update workspace bridge settings. |

### Mutations

| Method | Path | Access | Purpose |
|---|---|---|---|
| PUT | `/agents/{prism_id}/backend-policies` | admin | Set agent backend policies. |
| PUT | `/agents/{prism_id}...` | admin | Create/update agent compatibility route. |
| DELETE | `/agents/stale` | admin | Remove stale agents. |
| DELETE | `/agents/{prism_id}...` | admin | Delete agent compatibility route. |
| PUT | `/groups/{name}/backend-policies` | admin | Set group backend policies. |
| PUT | `/groups/{id}...` | admin | Create/update group. |
| DELETE | `/groups/{id}...` | admin | Delete group. |
| PUT | `/defaults/backend-policies` | admin | Set default backend policies. |
| PUT | `/defaults` | admin | Set default policy settings. |
| POST | `/workspaces` | admin | Register/create workspace. |
| DELETE | `/workspaces/{id}...` | admin | Delete workspace. |
| POST | `/identity` | admin | Create identity record. |
| PUT | `/identity/{kind-or-id}...` | admin | Update identity subresource. |
| DELETE | `/identity/{kind-or-id}...` | admin | Delete identity subresource. |

### Backends, OpenAPI, and binaries

| Method | Path | Access | Purpose |
|---|---|---|---|
| POST | `/backends/preview-openapi` | admin | Stateless OpenAPI parse/preview. |
| POST | `/openapi/scaffold-from-curl` | admin | Convert a curl command into starter OpenAPI 3.1 YAML. |
| POST | `/backends/{id}` | admin | Add/update backend. |
| POST | `/backends/{id}/reconnect` | admin | Reconnect backend. |
| POST | `/backends/{id}/workspace-changes/refresh` | admin | Refresh workspace change detection. |
| POST | `/backends/{id}/workspace-changes/apply` | admin | Apply workspace changes. |
| POST | `/backends/{id}/workspace-changes/discard` | admin | Discard workspace changes. |
| POST | `/backends/{id}/openapi-diff` | admin | Diff a new OpenAPI source against stored backend spec. |
| POST | `/backends/{id}/reimport` | admin | Apply OpenAPI reimport. |
| PATCH | `/backends/{id}...` | admin | Patch backend compatibility route. |
| DELETE | `/backends/{id}...` | admin | Remove backend compatibility route. |
| POST | `/binaries/upload` | admin | Upload a binary backend artifact. |
| POST | `/binaries/fetch` | admin | Fetch a binary backend artifact by URL. |

The `reconnect`, `workspace-changes/*`, `openapi-diff`, and `reimport` POST routes share the single registered `POST /backends/` pattern and are dispatched by URL suffix. Likewise the `GET /backends/{id}...` detail routes (auth-status, workspace-changes, openapi-source) are suffix-dispatched off the one `GET /backends/` pattern.

### Grants, policy, and analytics

| Method | Path | Access | Purpose |
|---|---|---|---|
| GET | `/grant-templates` | session | List grant templates. |
| POST | `/grant-templates` | admin | Create grant template. |
| GET | `/grant-templates/{id}...` | session | Read grant template/history/hash routes. |
| PUT | `/grant-templates/{id}...` | admin | Update grant template. |
| DELETE | `/grant-templates/{id}...` | admin | Delete grant template. |
| GET | `/grant-bindings` | session | List grant bindings. |
| POST | `/grant-bindings` | admin | Create grant binding. |
| GET | `/grant-bindings/{id}...` | session | Read grant binding. |
| PUT | `/grant-bindings/{id}...` | admin | Update grant binding. |
| DELETE | `/grant-bindings/{id}...` | admin | Delete grant binding. |
| GET | `/analytics/status` | session | Analytics subsystem status. |
| GET | `/analytics/events` | session | Query analytics events. |
| GET | `/analytics/events/tail` | session | Tail analytics events. |
| GET | `/analytics/templates` | session | Analytics by template. |
| GET | `/analytics/templates/{id}...` | session | Analytics for one template. |

### Policy builder

Registered by the policy route module; mounted under `/api/v1` with the same session/admin split.

| Method | Path | Access | Purpose |
|---|---|---|---|
| GET | `/policy/verbs` | session | List policy verbs. |
| GET | `/policy/verbs/{id}...` | session | Resolve a single verb. |
| GET | `/policy/subjects/{id}...` | session | Read a policy subject. |
| POST | `/policy/subjects/{id}...` | admin | Create a policy subject. |
| PUT | `/policy/subjects/{id}...` | admin | Update a policy subject. |
| DELETE | `/policy/subjects/{id}...` | admin | Delete a policy subject. |
| GET | `/policy/health` | session | Policy subsystem health. |
| GET | `/policy/access` | session | Resolve access for a backend (requires `backend` query param; optional `tool` filter; 24h window). |

For the config fields behind these routes (agents, groups, scopes, credentials), see [Configuration](./configuration.md#authentication). To connect an agent against the gateway, see [Getting Started](./getting-started.md).
