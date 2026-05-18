---
id: task-12
title: Resolve Linear OAuth persistence and Brainfile Docker bridge UX
description: |-
  Current status as of 2026-05-16:

  - Linear OAuth persistence is fixed. The old `backend/oauth/Linear`
    ciphertext was unrecoverable because it had been encrypted with a lost
    ephemeral key, but after fresh reauthorization Prism logged:
    `OAuth callback received`, `OAuth token exchange successful`, and
    `backend connected via OAuth`.
  - Prism now pins both `public_url` and `admin_public_url` to
    `https://mcp.dfam.one` in deployed config, and logs the exact backend OAuth
    `callback_url` plus callback receipt for future diagnosis.
  - Docker compose pins key material into the persistent `/data` volume:
    `PRISM_KV_KEY_FILE=/data/.prism/kv-encryption.key` and
    `PRISM_SIGNING_KEY_FILE=/data/.prism/signing-key.pem`.
  - Linear now restores cleanly after container restart:
    `restored persisted OAuth credential`, then `restored persisted backend`.
  - Brainfile stdio through the Docker bridge is working. Exact bridge smoke
    for `npx @brainfile/cli mcp` returned Brainfile tools, so node/python image
    selection was not the failing layer.
  - The browser `Failed to fetch` / `ERR_NETWORK_CHANGED` symptom was treated
    as a UI/API timing problem: Chrome can abort an in-flight admin POST when
    Docker creates the sandbox veth. Stdio adds now return `202 connecting`
    before spawning in the background.
  - The Servers UI now shows an optimistic pending row for async stdio adds
    with a pulsing status dot, elapsed timer, skeleton bars, and
    `connecting`/`taking longer` state until the real backend appears.
  - The deployed placeholder `example` backend was removed because `echo
    placeholder` is not an MCP server and delayed startup by about 30 seconds.
  - If Prism reuses an existing bridge backend and MCP connection fails, it now
    deletes the stale bridge backend and spawns/connects once more before
    surfacing an error.
priority: high
tags:
  - oauth
  - bridge
  - docker
  - handoff
createdAt: "2026-05-16T22:10:00Z"
completedAt: "2026-05-16T22:21:00Z"
---

## Handoff

Decisions made:

- Keep stdio MCP servers sandboxed in Docker containers via `prism-bridge`
  manage mode.
- Treat stdio adds as asynchronous admin operations instead of blocking the
  browser request until Docker spawn/tool discovery completes.
- Keep the UI optimistic and visible during async work with pending rows rather
  than a hidden background operation.
- Use the full `prism-bridge:full` image for node/python/full runtimes for now;
  it contains node/npm/python/uv and makes the sandbox reproducible.
- Keep the old orphan `prism-bridge-1` container out of the active path. Prism
  uses the compose service `prism-bridge` at `http://prism-bridge:3001`.

Current deployed bundle after the pending-row work was `index-Y0UKj8E6.js`.
