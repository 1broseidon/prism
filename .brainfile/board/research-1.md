---
id: research-1
title: GraphQL backend transport (parked research)
type: research
column: todo
position: 1
priority: low
tags:
  - graphql
  - research
  - parked
createdAt: "2026-05-18T02:17:26.880Z"
---

## Description
Parked design research for adding GraphQL as a backend transport type alongside the shipped OpenAPI work (epic-1). Not ready to start — captures the design space, open decisions, and what reuses from OpenAPI so we can pick this up when there's bandwidth.

## Why GraphQL needs its own design pass (not a port of OpenAPI)

OpenAPI describes endpoints with fixed request/response shapes — one endpoint maps to one tool, mechanically. GraphQL describes a *schema* exposed at a single endpoint, where the client decides what to fetch via a selection set in the query string. The selection set is GraphQL's superpower and the translation problem.

Three operation kinds: Query (read), Mutation (write), Subscription (long-lived stream). Subscriptions don't map to MCP tools — drop in v1.

## Three strategies considered

**A. Per-field tools, canned selection.** Each top-level Query/Mutation field becomes a tool with a server-side hard-coded selection set (e.g. "all scalars at depth 1"). Closest to OpenAPI's mental model; reuses all existing UX (preview, per-tool toggles, tag groups). Loses GraphQL's "ask for exactly what you need" — every call to \`users\` returns all scalars whether the LLM wanted them or not.

**B. Single \`graphql_query\` tool.** One tool taking \`query\` and \`variables\`. Schema digest in the description or as an MCP resource. LLM composes its own query. Honors GraphQL's design; one tool definition regardless of schema size. Smaller models compose invalid queries; schema-in-description balloons tool definitions; schema-as-resource needs the LLM to fetch it.

**C. Hybrid — per-field tool with optional \`_fields\` selector.** Each top-level field becomes a tool with its normal args PLUS an optional \`_fields: string[]\` parameter. Empty defaults to "all immediate scalars"; explicit \`[\"name\", \"address.city\"]\` gets composed into the selection set server-side. Per-tool familiarity for LLMs that don't want to think about projection; real GraphQL power when they do. Recommended starting point.

## What reuses cleanly from epic-1

- \`ToolDispatcher\` interface (already extracted)
- \`persistedBackend\` extension pattern
- Preview-then-save admin flow with curation
- Per-tool toggles + tag grouping in UI
- 32KB truncation with footer
- Bearer + apiKey-header auth (most GraphQL APIs use bearer)
- Scope mapping
- Audit pipeline

## New pieces

- GraphQL SDL parser. Candidates: \`graphql-go/graphql\` or \`wundergraph/graphql-go-tools\`. Needs research.
- Introspection client (POST \`__schema\` query, parse response).
- Selection-set composer: \`_fields: [\"name\", \"address.city\"]\` → \`{ name address { city } }\`.
- Type-system translator: GraphQL types → JSON Schema. Scalars/lists/enums map straight. Custom scalars become strings + description. Unions/interfaces need \`__typename\` injected and emit as \`oneOf\`.
- Error handler: GraphQL returns HTTP 200 with \`{ data, errors }\` even on logical failures. Detect non-empty \`errors\` array and flag as \`IsError\` while still surfacing \`data\`.

## Open decisions (need to lock before planning code)

1. Selection strategy: A, B, or C? (lean C)
2. Schema source: SDL upload, introspection URL, or both? (introspection commonly disabled for security; SDL is reliable)
3. Default field selection (for C): depth-1 scalars, or scalars + nested objects' scalars?
4. Subscriptions: confirm skip with reason \`unsupported_subscription\`?
5. Mutations marked with \`destructiveHint: true\` like POST/DELETE in OpenAPI?
6. Union/interface handling: inject \`__typename\` into default selection, emit \`oneOf\` in description?
7. Error semantics: non-empty \`errors\` array → \`IsError: true\` while including both \`data\` and \`errors\` in body?

## Implementation shape (when started)

Same four-phase structure as epic-1: parser → gateway dispatcher → admin API → frontend. Should be smaller than the OpenAPI epic since the dispatcher abstraction and admin flow patterns are already in place.

## Why parked

OpenAPI work just landed and is stabilizing. GraphQL isn't a forcing function yet — no specific backend the operator wants to expose. Bring back when there's a real need or when there's bandwidth to research the GraphQL parsing libraries.

## When to unpark

- A real GraphQL backend an operator wants to expose
- Or: 1-2 days of slack to research \`graphql-go/graphql\` vs \`wundergraph/graphql-go-tools\` and prototype the selection-set composer
