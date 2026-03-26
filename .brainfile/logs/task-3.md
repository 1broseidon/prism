---
id: task-3
title: Glob scope matching (namespace:read* patterns)
description: |-
  Extend Policy.CanAccessTool to support glob patterns in scope strings. Example: `fs:read*` matches `fs:read_file`, `fs:read_text_file`, etc.

  Reduces config verbosity when granting access to tool families. Independent of the PrismID work — operates on the scope string matching layer in internal/auth/policy.go.

  Use Go's `path.Match` or a simple prefix/suffix matcher. Keep it minimal — full regex is overkill.
priority: medium
tags:
  - policy
  - scopes
relatedFiles:
  - internal/auth/policy.go
  - internal/auth/policy_test.go
createdAt: "2026-03-26T03:24:06.792Z"
completedAt: "2026-03-26T13:59:06.622Z"
updatedAt: "2026-03-26T13:59:06.622Z"
---

## Description
Extend Policy.CanAccessTool to support glob patterns in scope strings. Example: `fs:read*` matches `fs:read_file`, `fs:read_text_file`, etc.

Reduces config verbosity when granting access to tool families. Independent of the PrismID work — operates on the scope string matching layer in internal/auth/policy.go.

Use Go's `path.Match` or a simple prefix/suffix matcher. Keep it minimal — full regex is overkill.
