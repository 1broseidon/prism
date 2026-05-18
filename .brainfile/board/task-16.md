---
id: task-16
title: OpenAPI gateway dispatcher
column: done
position: 1
priority: high
tags:
  - openapi
  - gateway
parentId: epic-1
contract:
  status: delivered
  deliverables:
    - type: file
      path: internal/gateway/dispatcher.go
      description: ToolDispatcher interface and MCP-session impl
    - type: file
      path: internal/gateway/dispatcher_openapi.go
      description: OpenAPI HTTP dispatcher
    - type: file
      path: internal/gateway/gateway.go
      description: persistedBackend extension + Backend.Dispatcher wiring + routeToolCall refactor
    - type: file
      path: internal/gateway/manage.go
      description: ConnectOpenAPIBackend method + restart restore path
    - type: test
      path: internal/gateway/dispatcher_openapi_test.go
      description: Dispatcher unit tests (truncation, errors, auth injection)
  validation:
    commands:
      - go test ./internal/gateway/... -tags mcp_go_client_oauth
      - golangci-lint run --build-tags mcp_go_client_oauth ./internal/gateway/...
  constraints:
    - blocks on task-15 (parser package must land first)
    - routeToolCall hot path stays identical above the dispatcher boundary — no auth/policy/audit changes
    - Existing MCP backends must continue to work with zero behavior change
    - Backend.Session = nil only legal when Backend.Dispatcher != nil
  metrics:
    readyAt: "2026-05-17T23:49:42.582Z"
    pickedUpAt: "2026-05-17T23:50:26.207Z"
    reworkCount: 0
    deliveredAt: "2026-05-18T00:03:40.379Z"
    duration: 794
createdAt: "2026-05-17T23:28:48.673Z"
updatedAt: "2026-05-18T00:03:40.379Z"
---

## Description
Wire the OpenAPI parser into the gateway as a new backend transport. No admin/UI surface yet — this PR lands when an OpenAPI-backed backend can be created via direct KV manipulation in tests and its tools route correctly through the existing policy stack.

## Dispatcher refactor

Extract a \`ToolDispatcher\` interface from \`routeToolCall\` in \`internal/gateway/gateway.go\`. The MCP-session-backed call path becomes one implementation; the OpenAPI HTTP path is another.

\`\`\`go
type ToolDispatcher interface {
    Dispatch(ctx context.Context, toolName string, args map[string]any) (*mcp.CallToolResult, error)
    Tools() []BackendToolInfo // for Status()
}
\`\`\`

Everything in \`routeToolCall\` above the final dispatch (claims → policy stack → workspace resolution → audit attach → circuit breaker entry → disabled-tool check) stays untouched. Only the leaf call to \`b.Session.CallTool\` becomes \`b.Dispatcher.Dispatch\`.

## OpenAPI dispatcher

\`internal/gateway/dispatcher_openapi.go\`:

\`\`\`go
type OpenAPIDispatcher struct {
    spec       *openapi.Spec
    baseURL    string
    httpClient *http.Client
    credResolver func(ctx context.Context) (header string, value string)
    logger     *slog.Logger
}
\`\`\`

\`Dispatch\` flow:
1. Look up operation by \`toolName\` in \`spec.Operations\`
2. Split flat args back into path/query/header/body by parameter location (parser preserves a side map)
3. Build URL: \`baseURL + path\` with \`{var}\` substituted
4. Marshal body as JSON if op has a request body
5. Set Content-Type and Accept to \`application/json\`
6. Inject credential via \`credResolver\` (Authorization: Bearer ... or operator-named header)
7. \`http.Client.Do\` with a 30s timeout
8. Read response, truncate to 32KB hard limit
9. Format as MCP \`CallToolResult\`:
   - 2xx → \`{Content: [TextContent{Text: header + body + truncation note if any}]}\`
   - 4xx/5xx → \`{IsError: true, Content: [TextContent{Text: header + body}]}\`
10. Return

## Persistence

Extend \`persistedBackend\` in \`internal/gateway/gateway.go\`:

\`\`\`go
type persistedBackend struct {
    // ...existing fields...
    OpenAPISpecRaw   []byte `json:"openapi_spec,omitempty"`     // verbatim source
    OpenAPISourceURL string `json:"openapi_source_url,omitempty"` // for re-import
    OpenAPIBaseURL   string `json:"openapi_base_url,omitempty"`   // override or extracted
    OpenAPISecurityScheme string `json:"openapi_security_scheme,omitempty"`
}
\`\`\`

On gateway start, restored OpenAPI-typed backends re-parse \`OpenAPISpecRaw\` and register tools synthetically. Parsing failures → backend marked \`disconnected\` with reason in logs (don't fail the whole gateway).

## Backend struct

\`Backend.Session\` becomes nullable. Add:

\`\`\`go
Dispatcher  ToolDispatcher           // unified call path
OpenAPI     *OpenAPIDispatcher       // typed access for Status() etc.
\`\`\`

\`Status()\` includes OpenAPI backends with the same \`Tools\` annotation (Disabled flag, MCP method hints) as everything else. \`transport\` field in BackendStatus learns the value \`"openapi"\`.

## ConnectOpenAPIBackend

New public method \`Gateway.ConnectOpenAPIBackend(ctx, id string, spec *openapi.Spec, baseURL, securityScheme string)\`:
- Builds OpenAPIDispatcher
- Registers each non-disabled operation as a tool via \`g.server.AddTool\` with a handler that funnels through \`routeToolCall\`
- Puts the Backend in \`g.backends\` with \`Session: nil, Dispatcher: <openapi>\`

## Tests

- Dispatcher routes happy path (200 → tool result with body)
- Truncation at exactly 32KB
- 4xx response → IsError with body
- 5xx response → IsError with body
- Network timeout → IsError with timeout reason
- Credential injection: bearer adds Authorization, apiKey adds named header
- Disabled tool in DisabledTools → routeToolCall rejects before dispatch (regression)
- Restart restores OpenAPI backend from persisted bytes

## Out of scope for this task

- Admin HTTP endpoints — task-17
- Frontend — task-18
- Preview / re-import endpoints — task-17
