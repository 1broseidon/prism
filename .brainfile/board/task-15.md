---
id: task-15
title: OpenAPI parser package
column: done
position: 0
priority: high
tags:
  - openapi
  - parser
parentId: epic-1
contract:
  status: delivered
  deliverables:
    - type: file
      path: internal/openapi/parser.go
      description: Parser and normalized Spec type
    - type: file
      path: internal/openapi/fetcher.go
      description: URL fetcher with SSRF guard
    - type: file
      path: internal/openapi/schema.go
      description: JSON Schema flattening + annotation emission
    - type: file
      path: internal/openapi/parser_test.go
      description: Unit tests covering parse rules, skip reasons, fingerprint stability
    - type: test
      path: internal/openapi
      description: Coverage > 80% on the parser package
  validation:
    commands:
      - go test ./internal/openapi/...
      - go vet ./internal/openapi/...
      - golangci-lint run ./internal/openapi/...
  constraints:
    - No imports from internal/admin, internal/gateway, internal/auth — parser must be reusable
    - kin-openapi version pinned via go.mod
    - All locked decisions in epic-1 enforced; deviations require updating the epic first
  metrics:
    readyAt: "2026-05-17T23:28:14.030Z"
    pickedUpAt: "2026-05-17T23:39:07.384Z"
    reworkCount: 0
    deliveredAt: "2026-05-17T23:47:27.453Z"
    duration: 500
createdAt: "2026-05-17T23:28:14.046Z"
updatedAt: "2026-05-17T23:47:27.453Z"
subtasks:
  - id: task-15-1
    title: sub-karen
    completed: true
---

## Description
Standalone Go package \`internal/openapi\` that turns an OpenAPI 3.0/3.1 document into a normalized internal representation. No Prism deps — pure parsing.

## Inputs

- \`Parse([]byte) (*Spec, error)\` — verbatim spec bytes (JSON or YAML, content-sniffed)
- \`Fetch(ctx, url) ([]byte, error)\` — URL fetcher with SSRF guard (reject localhost/127.0.0.0/8/link-local/RFC1918 unless host allowlisted via config)

## Outputs

\`\`\`go
type Spec struct {
    Title       string
    Version     string
    BaseURL     string             // first servers[] entry
    Operations  []OperationSpec    // accepted ops
    Skipped     []SkippedOperation // ops we won't expose with reasons
    Warnings    []string           // soft issues (e.g. >500 ops)
    SecuritySchemes map[string]SecurityScheme // bearer/apiKey-header only
}

type OperationSpec struct {
    Name        string          // operationId or generated
    Method      string
    Path        string
    Summary     string
    Description string
    Tags        []string
    Deprecated  bool
    InputSchema json.RawMessage // flat JSON Schema of all params + body
    Security    []string        // names of acceptable schemes from SecuritySchemes
    Fingerprint string          // sha256 of method+path+sorted input keys+response shape
}
\`\`\`

## Parsing rules (locked)

- OpenAPI 3.0 and 3.1 supported; reject Swagger 2 / older with a clear error
- External \$refs → reject the entire spec
- Internal \$refs → resolved
- Spec > 5MB → reject
- > 500 operations → \`Warnings\` entry, parse continues
- Content types other than \`application/json\` request/response → operation goes to \`Skipped\` with reason
- Security schemes other than bearer or apiKey-in-header → scheme dropped from \`SecuritySchemes\`; any op requiring only-dropped schemes → \`Skipped\`
- Param name collision across path/query/header/body → operation \`Skipped\` with reason
- Name from \`operationId\` if present; otherwise generate from \`{method}_{path}\` with braces stripped and segments snake_cased
- HTTP method → MCP annotations: GET/HEAD/OPTIONS = readOnly; POST/PUT/PATCH/DELETE = destructive; GET/HEAD/PUT/DELETE = idempotent; openWorld = always true (emit in InputSchema's \`x-mcp-annotations\` or separate field — picker's call)

## Library

\`github.com/getkin/kin-openapi/openapi3\` — already the de facto Go OpenAPI 3 parser.

## Constraints

- No HTTP calls during parsing (Fetch is separate and SSRF-guarded)
- Deterministic output for the same input bytes (test the fingerprint stability)
- Skipping an operation never fails the whole parse — only spec-level violations (size, external \$refs, OpenAPI version) do
