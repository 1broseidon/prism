---
name: restart-prism
description: Rebuild and restart the prism docker-compose stack (gateway + bridge + caddy), then verify the gateway is reachable. Use after backend changes when the running container needs to pick them up.
---

# /restart-prism

Repeatable workflow for rebuilding and restarting prism after backend edits.

## What it does

1. `docker compose up -d --build` — rebuilds whichever images changed (typically `prism:dev`), recreates the prism container, leaves bridge + caddy alone if unchanged
2. Lists the prism-* container statuses so you can spot a failure
3. Hits the admin /health endpoint on :9086 to confirm the gateway came back up

## When to use

- After committing backend Go changes (`internal/gateway/**`, `cmd/prism/**`, `internal/admin/**`, etc.)
- After UI changes that rebuilt `internal/admin/web/dist/` (the dev container ships dist as static assets)
- After config changes to `deploy/config.container.json`

## When NOT to use

- For pure frontend dev — use `cd internal/admin/web && npm run dev` against a running gateway instead
- When only `prism-bridge` changed — it has its own image; let compose figure it out (this skill still works)

## Commands

```bash
docker compose up -d --build
docker ps --filter "name=prism" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
curl -fsS http://localhost:9086/health && echo " — admin healthy"
```

If `docker compose up` reports an image build failure, surface the failing stage to the user — do NOT retry blindly. If `/health` returns 503 or hangs, tail logs with `docker logs --tail 100 prism` before suggesting next steps.

## Common pitfalls

- The prism container takes a few seconds after "Started" to actually serve traffic. Brief retry on the health curl is fine.
- The bridge container's healthcheck is on port 3001 internally; if compose says "Waiting" → "Healthy" before recreating prism, that's normal.
- `bind mount source not found` errors usually mean `deploy/config.container.json` was deleted or moved.
