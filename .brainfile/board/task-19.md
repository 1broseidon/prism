---
id: task-19
title: "OpenAPI usability: inline spec editor + curl scaffold"
column: done
position: 3
priority: high
tags:
  - openapi
  - usability
  - frontend
  - backend
contract:
  status: delivered
  deliverables:
    - type: file
      path: internal/openapi/scaffold.go
      description: Curl parser + OpenAPI 3.1 stub generator
    - type: file
      path: internal/openapi/scaffold_test.go
      description: Table-driven tests covering curl flag variants, JSON body schema inference, auth detection, query/header param extraction
    - type: file
      path: internal/admin/openapi_scaffold.go
      description: POST /openapi/scaffold-from-curl handler
    - type: file
      path: internal/admin/admin.go
      description: Route registration for scaffold endpoint
    - type: file
      path: internal/admin/openapi_scaffold_test.go
      description: Handler tests
    - type: file
      path: internal/admin/backends_openapi.go
      description: Extend preview/save/diff/reimport to accept source.inline
    - type: file
      path: internal/admin/backends_openapi_test.go
      description: Coverage for inline source across all four endpoints
    - type: file
      path: internal/admin/web/src/api/types.ts
      description: OpenAPISpecSource inline variant + scaffold request/response types
    - type: file
      path: internal/admin/web/src/pages/Servers.tsx
      description: Three-mode source picker (url/file/inline) + 'Generate from curl' button
    - type: file
      path: internal/admin/web/src/components/ReimportDiffModal.tsx
      description: Inline editor for re-import on file/inline-sourced backends
    - type: file
      path: internal/admin/web/src/styles/app.css
      description: Source-mode tabs + textarea styling
  validation:
    commands:
      - go test ./internal/openapi/... ./internal/admin/... -tags mcp_go_client_oauth
      - golangci-lint run --build-tags mcp_go_client_oauth ./internal/openapi/... ./internal/admin/...
      - cd internal/admin/web && npx tsc --noEmit
      - cd internal/admin/web && npm run build
  constraints:
    - Inline source bytes never normalize through the parser before persistence — store verbatim like file/URL sourced specs
    - 5MB spec cap applies to inline source too
    - "Curl parser is best-effort: unrecognized flags emit a warning, parsing continues"
    - Path-template detection from curl URLs is explicitly out of scope
    - Existing URL/file source paths must continue to work with zero behavior change
    - Re-import flow defaults to original source mode (URL stays URL by default, file/inline default to inline editor)
  metrics:
    readyAt: "2026-05-18T02:23:09.730Z"
    pickedUpAt: "2026-05-18T02:24:48.639Z"
    reworkCount: 1
    deliveredAt: "2026-05-18T02:40:11.084Z"
    duration: 922
createdAt: "2026-05-18T02:23:09.739Z"
updatedAt: "2026-05-18T02:40:11.084Z"
---

## Description
Two related additions to the OpenAPI backend flow shipped in epic-1:

1. **Inline spec source** — third option alongside URL / file upload. Operator pastes YAML/JSON directly into a textarea, previews, saves. Removes the "save to file first" step for hand-rolled specs.

2. **Generate stub from curl** — sub-flow on the inline source mode. Operator pastes a curl command, server parses it and returns an OpenAPI 3.1 fragment for that one endpoint. The fragment populates the inline editor; operator reviews/edits, then saves like any other inline spec.

These ship together because curl scaffolding feeds the inline editor.

## Why

For operators wrapping APIs that don't publish OpenAPI specs (most SaaS / internal APIs), the v1 workflow is "go write a YAML file somewhere, then upload it." Two friction points removed:
- Inline editor: skip the round-trip to a text editor for short specs.
- Curl scaffold: skip the "what does the OpenAPI shape even look like" research entirely — paste a curl you already have working, get a starting point.

## Architecture

### Inline source

Wire shape currently is \`source: {file: base64} | {url: string}\`. Add third variant: \`source: {inline: string}\` carrying raw YAML/JSON spec text.

- Preview endpoint, save endpoint, diff endpoint, reimport endpoint all gain inline support. Server-side parsing treats the string as raw bytes; SSRF guard doesn't apply.
- \`persistedBackend\` already stores raw bytes verbatim — inline-sourced backends persist the same way file-sourced ones do.
- Re-import flow: if the original source was inline, the diff modal shows an inline editor pre-filled with the persisted spec. File-uploaded specs also get the inline editor (file re-upload remains as an alternative). URL-sourced specs keep URL re-fetch as primary with inline-override available.

### Curl scaffold

New endpoint \`POST /api/v1/openapi/scaffold-from-curl\` (under the same admin auth as other openapi endpoints). Body: \`{curl: string}\`. Response: \`{spec: string}\` — YAML text ready to paste into the inline editor.

Parser must handle:
- Method (\`-X\`, defaults to GET; \`-d\`/\`--data\`/\`--data-raw\`/\`--data-binary\` implies POST when -X absent)
- URL (positional)
- Headers (\`-H 'Key: Value'\`)
- Body (\`-d\`/\`--data\`/\`--data-raw\`/\`--data-binary\`)
- Multi-line continuations (\`\\\\\` at end of line)
- Single vs double quoted args
- \`@filename\` body references — skip with a comment in the generated spec

Output spec:
- \`openapi: 3.1.0\`
- \`info.title\`: derived from URL host
- \`info.version\`: "1.0"
- \`servers\`: \`[{url: scheme://host}]\` (no port unless non-default)
- \`paths\`: one entry for the URL path
- Method matched against input
- Auth header detection:
  - \`Authorization: Bearer ...\` → securityScheme \`bearerAuth\` (type: http, scheme: bearer)
  - Any non-Authorization header that looks like an API key by name (\`X-API-Key\`, \`Api-Key\`, etc.) → securityScheme \`apiKeyAuth\` (type: apiKey, in: header)
  - Other Authorization headers (Basic, custom) → emit as a regular header parameter with a comment noting "unsupported auth scheme; v1 of Prism accepts bearer or apiKey-in-header"
- Other custom headers → parameters with \`in: header\`
- Body bytes:
  - If JSON-parseable → infer schema (recursively detect object/array/scalar types from a single example)
  - Else → \`type: object\` placeholder with a comment
- Query params (from URL) → parameters with \`in: query\`, type inferred from value
- Path params NOT auto-detected — the URL is preserved verbatim. Operator can edit \`{id}\` segments themselves; comment in the output explains how.

### Frontend changes

\`Servers.tsx\` AddOpenAPI flow gains a source-mode picker with three options (URL / file / inline). Inline mode shows a textarea (plain monospace textarea with autoresize is fine for v1 — no need for a real code editor).

On inline mode, a "Generate from curl" button toggles a second textarea for the curl input. "Generate" button calls \`/openapi/scaffold-from-curl\`, fills the spec editor with the response. Operator reviews and clicks Preview as normal.

\`ReimportDiffModal.tsx\` learns to render an inline editor pre-filled with the persisted spec for re-imports. URL-sourced backends still default to URL re-fetch but expose "edit inline instead" as an alternative.

## Locked decisions

- Inline source is plain text (YAML or JSON; the parser content-sniffs)
- 5MB spec cap still applies to inline source (matches file/URL)
- Curl scaffold lives at \`POST /api/v1/openapi/scaffold-from-curl\`
- Curl parser handles the listed flag set; unrecognized flags emit a warning in the response but parsing continues with what it understood
- Inferred body schemas use the simplest possible representation (no \`oneOf\`/\`enum\` detection from a single sample)
- Path-template detection from a curl URL is out of scope — too many false positives (every UUID/number in a URL could be a path param)
- Generated specs are YAML (more human-readable for editing); operator can paste JSON too

## Out of scope

- Real code editor (CodeMirror/Monaco) — plain textarea ships first
- Live syntax validation in the editor — server-side preview catches errors
- Multi-endpoint scaffolds from multiple curls — one curl at a time
- Other ingest formats (HAR files, Postman collections) — separate task if wanted
