---
id: task-4
title: OpenTelemetry tracing across full request path
description: |-
  Trace: agent → prism-auth → gateway → bridge → backend. Instrument the key spans:
  - Auth middleware (token validation)
  - Scope enforcement (allowed/denied)
  - Backend routing + tool call
  - Credential injection (fact, not value)

  Use OTEL SDK with OTLP exporter. Configurable via env vars (OTEL_EXPORTER_OTLP_ENDPOINT). No-op when unconfigured.
priority: medium
tags:
  - observability
  - roadmap
relatedFiles:
  - internal/gateway/gateway.go
  - internal/auth/middleware.go
  - internal/authserver/authserver.go
createdAt: "2026-03-26T03:24:11.128Z"
contract:
  status: delivered
  deliverables:
    - type: file
      path: internal/telemetry/telemetry.go
      description: OTEL init package with no-op when unconfigured
    - type: file
      path: internal/auth/middleware.go
      description: Add prism.auth.validate span to token validation
    - type: file
      path: internal/gateway/gateway.go
      description: Add prism.gateway.tool_call and prism.gateway.scope_filter spans
    - type: file
      path: cmd/prism/main.go
      description: Wire telemetry.Init for prism binary
    - type: file
      path: cmd/prism-auth/main.go
      description: Wire telemetry.Init for prism-auth binary
  validation:
    commands:
      - cd /home/george/Projects/personal/prism && go build ./...
      - cd /home/george/Projects/personal/prism && go test ./...
      - cd /home/george/Projects/personal/prism && go vet ./...
  constraints:
    - Zero overhead when OTEL_EXPORTER_OTLP_ENDPOINT is not set
    - No tracing config in config.json — standard OTEL env vars only
    - Keep telemetry package thin — no custom metrics
    - Use go.opentelemetry.io/otel/trace for span creation via global tracer provider
    - Run go test ./... to verify no regressions
  metrics:
    readyAt: "2026-03-26T03:46:14.991Z"
    pickedUpAt: "2026-03-26T03:46:17.797Z"
    reworkCount: 0
    deliveredAt: "2026-03-26T03:49:09.397Z"
    duration: 172
updatedAt: "2026-03-26T13:59:07.091Z"
completedAt: "2026-03-26T13:59:07.091Z"
---

## Description
Trace: agent → prism-auth → gateway → bridge → backend. Instrument the key spans:
- Auth middleware (token validation)
- Scope enforcement (allowed/denied)
- Backend routing + tool call
- Credential injection (fact, not value)

Use OTEL SDK with OTLP exporter. Configurable via env vars (OTEL_EXPORTER_OTLP_ENDPOINT). No-op when unconfigured.
