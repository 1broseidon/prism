---
id: task-5
title: Prometheus metrics endpoint
description: |-
  Expose metrics on admin port: request counts, latencies (histogram), error rates, active connections, token validations, scope denials.

  Use prometheus/client_golang. Expose at /admin/metrics. No-op middleware when metrics are disabled in config.
priority: medium
tags:
  - observability
  - roadmap
relatedFiles:
  - internal/admin/admin.go
  - internal/gateway/gateway.go
  - internal/auth/middleware.go
createdAt: "2026-03-26T03:24:13.330Z"
completedAt: "2026-03-26T13:59:07.462Z"
updatedAt: "2026-03-26T13:59:07.462Z"
---

## Description
Expose metrics on admin port: request counts, latencies (histogram), error rates, active connections, token validations, scope denials.

Use prometheus/client_golang. Expose at /admin/metrics. No-op middleware when metrics are disabled in config.
