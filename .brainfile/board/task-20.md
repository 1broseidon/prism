---
id: task-20
title: "Binary backend: upload + URL-fetch (Go MCP servers via prism-managed sandbox)"
column: done
position: 4
priority: high
tags:
  - backend
  - sandbox
  - frontend
  - usability
contract:
  status: delivered
  deliverables:
    - type: file
      path: internal/binstore/binstore.go
      description: Content-addressed binary store (sha256-keyed) backed by a managed directory; Put(reader)/Get(hash)/Exists(hash)/Stat/Prune; size cap + total-disk cap enforced
    - type: file
      path: internal/binstore/binstore_test.go
      description: Put/Get roundtrip, dedup by hash, size cap rejection, prune unused
    - type: file
      path: internal/binstore/archive.go
      description: Detect+extract zip/tar.gz/tgz/tar archives; locate ELF binary inside (single-binary auto-detect, multi-binary requires operator-supplied path); reject non-ELF / non-linux-amd64 with clear errors
    - type: file
      path: internal/binstore/archive_test.go
      description: zip + tar.gz fixtures, single-binary auto-detect, multi-binary disambiguation, non-ELF rejection, arch mismatch rejection
    - type: file
      path: internal/admin/binaries.go
      description: POST /binaries/upload (multipart, raw binary or archive), POST /binaries/fetch (URL, optional archive_binary_path), GET /binaries/{hash} (metadata), all under admin auth. 64MB upload cap. Returns {hash, size, name, detected_binary_path}
    - type: file
      path: internal/admin/binaries_test.go
      description: Upload raw binary, upload archive with auto-detected binary, archive with multiple binaries requires path, URL fetch, URL fetch+extract, size cap, non-ELF rejection, arch mismatch
    - type: file
      path: internal/admin/admin.go
      description: Route registration for /binaries endpoints
    - type: file
      path: internal/admin/backends.go
      description: Extend AddBackendBody + persisted plumbing to accept binary_hash + args (string parsed shell-style); validation rejects binary_hash combined with command/url/openapi_spec; mounts the binary into the sandbox via SandboxMount and sets command=/opt/prism/bin/<name>
    - type: file
      path: internal/gateway/gateway.go
      description: Add BinaryHash, BinaryName, BinarySource (url or upload), BinarySourceURL, BinaryArchivePath fields to persistedBackend; reconnectPersistedBinaryBackend resolves hash → host path and spawns via existing stdio path with synthesized SandboxMount
    - type: file
      path: internal/gateway/manage.go
      description: reconnectPersistedBinaryBackend (mirror of reconnectPersistedOpenAPIBackend pattern); refuses to start backend if hash no longer resolves in binstore (with clear log)
    - type: file
      path: internal/gateway/binary_test.go
      description: End-to-end-ish: create binary backend (mock binstore), spawn produces docker spec with mount, args parsed correctly from "recoil mcp serve" → ["mcp","serve"], blank args produces no args
    - type: file
      path: internal/admin/web/src/api/types.ts
      description: BinaryUploadResponse, BinaryFetchRequest, BinaryFetchResponse, extend AddBackendBody with binary_hash + binary_args + binary_name; add binary fields to Backend type for display
    - type: file
      path: internal/admin/web/src/pages/Servers.tsx
      description: Fourth backend mode "binary" alongside stdio/url/openapi; sub-tabs Upload | URL; upload mode = file picker (accepts binary or zip/tar.gz); URL mode = url textbox + optional "binary path inside archive" field; "command" textbox below source picker (placeholder "recoil mcp"); preview shows detected binary name + hash + size before save
    - type: file
      path: internal/admin/web/src/styles/app.css
      description: Reuse existing source-tab + config-input styles; small additions only if needed for binary metadata display
  validation:
    commands:
      - go test ./internal/binstore/... ./internal/admin/... ./internal/gateway/... -tags mcp_go_client_oauth
      - golangci-lint run --build-tags mcp_go_client_oauth ./internal/binstore/... ./internal/admin/... ./internal/gateway/...
      - cd internal/admin/web && npx tsc --noEmit
      - cd internal/admin/web && npm run build
  constraints:
    - Binary store path is configurable but defaults to a managed location inside the prism data dir; the host operator never installs the binary
    - All binary execution happens inside the existing sandbox (docker container with the binstore directory bind-mounted read-only at /opt/prism/bin); no host-side exec path
    - Only linux/amd64 ELF binaries accepted in v1 — validate ELF header (e_ident magic + EI_CLASS=ELFCLASS64 + e_machine=EM_X86_64) and reject everything else with a clear error message naming the detected arch
    - Archive extraction supports .zip, .tar.gz, .tgz, .tar — detected by magic bytes, not filename; reject other formats
    - Archive with exactly one ELF binary → auto-select; archive with multiple ELF binaries → require operator to supply archive_binary_path; archive with zero ELF binaries → reject
    - URL fetch uses the same SSRF guard already used by OpenAPI URL imports (block localhost, RFC1918, link-local); 64MB download cap; follows redirects with a max of 5
    - "MCP command" field is parsed shell-style (whitespace split, basic quote handling — reuse shell parser if one exists, otherwise simple split); empty field is allowed and produces zero args. First token is informational only — the actual binary path is /opt/prism/bin/<name> regardless of what the operator types as token zero
    - Hash-based dedup — two backends pointing at the same uploaded binary share storage; pruning only removes binaries with zero referencing backends
    - Binary store enforces a per-binary cap (64MB) and a total cap (default 1GB, configurable); upload over either cap is rejected before write
    - On gateway restart, missing binary in binstore disables the backend with a clear status message rather than crashing — operator can re-upload to recover
    - URL-sourced binary backends support re-fetch (mirror of OpenAPI reimport diff flow); upload-sourced backends support replace-upload. Out of scope for this task — note in code comments but do not implement
    - Document in admin UI (hint text under the upload/URL field): "Linux x86_64 binary required. Static builds recommended (CGO_ENABLED=0). Archives: .zip, .tar.gz."
    - Persisted backend discriminator: BinaryHash != "" identifies a binary backend (parallels OpenAPISpecRaw for openapi backends)
  metrics: {}
createdAt: "2026-05-18T03:00:00.000Z"
updatedAt: "2026-05-17T12:00:00.000Z"
---

## Description

Add a third backend transport type (alongside stdio command and OpenAPI) for **prism-managed binary MCP servers**. The operator provides a pre-built Linux/x86_64 binary either by uploading it directly or by URL (typically a GitHub release asset). Prism stores the binary in a content-addressed binstore and runs it inside the existing sandbox container by mounting the binstore directory in. The operator's host never needs the binary installed.

Two ergonomics that this unlocks:

1. **No host install** — operator uploads `cymbal` or `recoil` once via admin UI, prism owns it from there. The agent connects through prism, prism enforces auth/scopes/audit, the binary only ever runs in the sandbox.
2. **No Dockerfile required** — binaries get reused inside the existing prism-bridge sandbox image (which already has libc + ca-certs), so most static Go binaries Just Work.

## Why

Today, getting a Go MCP server (cymbal, recoil, anything you built yourself) running through prism means either:
- Installing it on the host (defeats the gatekeeper story — the user can skip prism entirely)
- Building a custom Docker image with the binary baked in (high friction; not realistic for "I just want to try this MCP server")
- Bind-mounting the host binary into the sandbox (operator has to install on host anyway)

None of these match prism's positioning as the security gatekeeper. The operator should upload a binary to prism, and from that point on the binary lives inside prism's controlled environment.

## Sources accepted

**Upload:** multipart POST of a file. Accepts:
- Raw ELF binary
- `.zip` archive (extract, find ELF inside)
- `.tar.gz` / `.tgz` / `.tar` archive (extract, find ELF inside)

**URL:** GET of a remote URL. Accepts the same content types. Archive detection is by magic bytes, not URL suffix.

In both modes, if the archive contains multiple ELF binaries (e.g., `cymbal` plus `cymbal-helper`), the operator must specify `archive_binary_path` to disambiguate. Single-binary archives auto-detect.

## MCP command field

A text input on the add-backend form (placeholder: `recoil mcp`). Parsed shell-style into argv. Examples:

| operator types | parsed args | invocation |
|---|---|---|
| (blank) | `[]` | `/opt/prism/bin/recoil` |
| `mcp` | `["mcp"]` | `/opt/prism/bin/recoil mcp` |
| `recoil mcp` | `["mcp"]` (first token dropped — informational) | `/opt/prism/bin/recoil mcp` |
| `recoil mcp serve` | `["mcp","serve"]` | `/opt/prism/bin/recoil mcp serve` |

First token is treated as the binary name for display purposes (so the operator can paste the whole command line they'd use locally). The actual binary path is always the binstore-mounted path under `/opt/prism/bin/<name>`.

## Sandbox integration

The binstore directory is bind-mounted into the sandbox container as `/opt/prism/bin` read-only. The spawn request sets `command=/opt/prism/bin/<name>` and `args=[parsed args]`. Everything else (network policy, workspace, scope enforcement, audit logging) flows through the existing sandbox path unchanged.

No new docker image variant. The existing base/full image already has libc + ca-certs, which covers any reasonable Go binary regardless of CGO mode.

## Out of scope (this task)

- Re-fetch / replace-upload flow (mirror of OpenAPI reimport) — note it in code, implement next
- Auto-discovery from GitHub release pages (operator pastes release URL, prism picks the right asset) — phase 2
- Non-amd64 architectures — document the constraint, reject with clear error
- Signature verification / sigstore — future
- Binary registry / curated catalog — far future
