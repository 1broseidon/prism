---
id: task-11
title: Persist OAuth backend credentials across container recreates
description: |-
  Current status as of 2026-05-16:

  - Root cause for Linear after reboot was confirmed in container logs:
    `failed to decrypt persisted OAuth token` for `backend/oauth/Linear`,
    followed by an unauthenticated reconnect and `Unauthorized`.
  - Docker compose now pins Prism's encryption and signing key paths into the
    persistent `/data` volume:
    - `PRISM_KV_KEY_FILE=/data/.prism/kv-encryption.key`
    - `PRISM_SIGNING_KEY_FILE=/data/.prism/signing-key.pem`
  - `ensureSigningKey` now honors `PRISM_SIGNING_KEY_FILE`.
  - OAuth token sources now persist rotated access/refresh tokens back to KV,
    so providers that rotate refresh tokens during normal use do not leave a
    stale refresh token in KV for the next restart.
  - Added `POST /backends/{id}/reconnect` to reconnect a KV-persisted backend
    without deleting its stored config or OAuth state.
  - Admin UI now marks disconnected backends explicitly and shows reconnect
    plus reauthorize actions on the server detail page.

  Runtime result:
  - The new key env is active in the Prism container.
  - The existing Linear ciphertext still cannot decrypt with `/data/.prism/kv-encryption.key`,
    which means it was encrypted by the previous ephemeral `/root/.prism` key
    and is not recoverable after the container recreate. Linear needs one fresh
    OAuth reauthorization; future tokens will persist.

  Verification passed:
  - `go test ./...`
  - `go test -tags mcp_go_client_oauth ./...`
  - `golangci-lint run ./...`
  - `npm --prefix internal/admin/web run build`
  - `git diff --check`
priority: high
tags:
  - oauth
  - persistence
  - admin-ui
  - handoff
createdAt: "2026-05-16T21:45:00Z"
completedAt: "2026-05-16T21:45:00Z"
---

## Handoff

Do not expect the currently stored Linear token to recover automatically. The
operator should reauthorize Linear once, then restart/recreate Prism to confirm
the new token decrypts from the `/data` key and reconnects.
