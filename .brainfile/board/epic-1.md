---
id: epic-1
title: OpenAPI backend transport
type: epic
column: done
position: 2
createdAt: "2026-05-17T23:27:48.310Z"
---

## Description
Auto-translate an OpenAPI spec (file or URL) into MCP tools so any REST API with a published spec becomes reachable through Prism without writing a per-vendor MCP server.

## Locked decisions

- **Scope**: OpenAPI 3.0 + 3.1 only. GraphQL deferred to a separate epic.
- **Spec sources**: file upload AND URL. URL-sourced specs persist the URL; "re-import" re-fetches on demand. No background polling.
- **Auth in v1**: HTTP \`bearer\` and \`apiKey\` (header location) only. Operations that require any other scheme — basic, apiKey-in-query/cookie, oauth2, openIdConnect, mutualTLS — are skipped at parse time and surfaced in the preview as unsupported with reason.
- **Curation flow**: preview-then-save. \`POST /api/v1/backends/preview-openapi\` parses and returns operations stateless. Save endpoint re-parses server-side from the same source. Client cannot inject ops the spec doesn't have.
- **Response handling**: truncate to 32KB fixed. Append "...response truncated (showed X of Y bytes)" footer. 4xx/5xx returned as MCP tool error (\`isError: true\`) with body included so the LLM can recover from validation failures.
- **Input schema**: flat — path/query/header params and body schema properties are lifted to one top-level object. Param-name collisions across locations cause the operation to be skipped at parse with a reason.
- **Naming**: \`operationId\` if present, otherwise generated \`{method}_{path}\` with braces stripped and segments snake_cased. No operator-chosen aliases in v1.
- **External \$refs**: rejected at parse time. Internal \$refs are mandatory and resolved.
- **Content types**: \`application/json\` only for requests and responses. Other content types skip the operation.
- **Hard limits**: spec body 5MB max, hard reject above. Operation count > 500 emits a warning at parse time but does not block — internal APIs hit this and per-tool toggles handle curation.
- **SSRF guard on URL imports**: reject \`localhost\`, \`127.0.0.0/8\`, link-local, RFC1918 private ranges unless an operator-configured allowlist permits the host.
- **Tool annotations**: emit MCP \`readOnlyHint\` for GET/HEAD/OPTIONS; \`destructiveHint\` for POST/PUT/PATCH/DELETE; \`idempotentHint\` for GET/HEAD/PUT/DELETE; \`openWorldHint\` always true (external system).

## Architecture

New backend transport type \`openapi\` alongside \`http\` and \`stdio\`. Sits behind the same policy stack, audit trail, per-tool toggles, rate limits, and scope mapping as every other backend.

**Persistence**: \`persistedBackend\` gains \`OpenAPISpecRaw []byte\` (verbatim source for re-display and re-import) and \`OpenAPIBaseURL string\` (extracted or operator-overridden). Optional \`OpenAPISourceURL string\` when URL-sourced. The parsed normalized form is rebuilt from \`OpenAPISpecRaw\` on every gateway start so we never persist a derived shape that could drift from the source.

**Dispatcher refactor**: extract a \`ToolDispatcher\` interface from \`routeToolCall\`. The existing MCP-session-backed path becomes one implementation; the OpenAPI HTTP-backed path is another. Everything above the dispatch (claims → policy stack → workspace resolution → audit → circuit breaker) stays identical. Adding GraphQL or gRPC later means new dispatchers, no \`routeToolCall\` changes.

**Operation fingerprint** (hash of \`method + path + sorted input schema keys + response schema digest\`) persisted alongside the disabled-tools list as \`{name, fingerprint}\` entries. On re-import, the diff classifies each operation as \`unchanged\`, \`signature_changed\`, \`added\`, \`removed\`, or \`renamed\` (same fingerprint, new name). Operator confirms before apply.

**Parser library**: \`getkin/kin-openapi\` (most maintained Go OpenAPI parser, handles 3.0 + 3.1, decent \$ref resolution).

**Synthetic backend**: \`Backend.Session\` becomes nullable. OpenAPI backends populate \`Backend.OpenAPI *OpenAPIDispatcher\` instead. Tool registration happens directly from the parsed spec — no remote \`tools/list\` round-trip.

## API surface

- \`POST /api/v1/backends/preview-openapi\` — body \`{source: {file: "<base64>"} | {url: "<...>"}}\`. Returns \`{base_url, security_schemes: [...], operations: [...], skipped: [{name, reason}], spec_warnings: [...]}\`. Stateless.
- \`POST /api/v1/backends/{id}\` (extended) — accepts \`{type: "openapi", source: {...}, base_url_override?: "...", security_scheme: "...", credential: {...}, disabled_tools: [...]}\`. Re-parses server-side.
- \`POST /api/v1/backends/{id}/openapi-diff\` — body \`{source: {...}}\` re-fetches/re-parses and returns the diff without applying.
- \`POST /api/v1/backends/{id}/reimport\` — applies the diff. Body \`{source: {...}, disabled_tools_resolution: "preserve" | "default_enabled"}\`.

## UI flow

1. Operator picks "OpenAPI" in connect → enters URL or uploads file
2. Preview call → server returns operations grouped by tags + skipped list
3. Operator chooses security scheme (if multiple available) and provides credential
4. Optional base URL override
5. Bulk select per tag (default all checked), skipped ops shown read-only with reasons
6. Save → backend created with \`disabled_tools\` = unchecked items

Re-import lives on the server detail page: button → fetches diff → modal shows add/remove/rename/signature-change → operator confirms → backend updated.

## Phases

1. **openapi-parser** — standalone Go package \`internal/openapi\`. Parse spec, emit normalized \`OperationSpec\` slice + \`SpecMeta\`, fingerprint each op, classify skips/warnings, no Prism deps.
2. **openapi-gateway** — \`ToolDispatcher\` interface extracted, OpenAPI dispatcher implemented, \`Backend.OpenAPI\` field, persisted shape + restart reload, synthetic tool registration. Internal-only — no admin/UI surface yet.
3. **openapi-admin-api** — three new admin endpoints (preview, diff, reimport) and the extended save body. Re-parse server-side, never trust client-supplied operation lists.
4. **openapi-ui** — new "OpenAPI" option in connect flow, two-step preview + curate UI, re-import diff modal on server detail.

Each phase ships as one PR. Phases 1+2 land together (parser is useless without dispatch); phase 3 ships when API endpoints + tests are in; phase 4 wraps the operator experience.
