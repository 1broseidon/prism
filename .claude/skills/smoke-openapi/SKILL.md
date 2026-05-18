---
name: smoke-openapi
description: Smoke-test an OpenAPI-backed prism backend end-to-end. Posts the spec URL through the admin API, waits for tools to register, fires a representative tool call, and verifies the audit log captured it. Use to validate changes to the OpenAPI transport against a real spec.
---

# /smoke-openapi

End-to-end smoke test for the OpenAPI backend transport. Catches integration bugs that unit tests in `internal/openapi/` miss — relative server URLs, content-type quirks, $ref inlining edge cases, scalar formatting (e.g. integers serialized as scientific notation).

## Inputs

- `$1` (spec URL or path) — required. Examples:
  - `https://petstore3.swagger.io/api/v3/openapi.json` (auth-free, mutating)
  - `https://api.weather.gov/openapi.json` (auth-free, read-only, `application/geo+json` responses)
  - `./testdata/spec.yaml` (local file — pass as inline source instead)

## Steps

1. **Resolve admin endpoint**: `${PRISM_ADMIN:-http://localhost:9086}`. If unreachable, surface the failure — do NOT proceed.

2. **Create backend via admin API**:
   ```bash
   curl -fsS -X POST "$PRISM_ADMIN/api/v1/backends" \
     -H 'content-type: application/json' \
     -d "{\"openapi_source_url\": \"$1\", \"enabled\": true}"
   ```
   Capture the returned backend ID.

3. **Wait for tool registration** (max ~10s, poll /backends/{id}):
   ```bash
   for i in {1..10}; do
     count=$(curl -fsS "$PRISM_ADMIN/api/v1/backends/$ID" | jq '.tools | length')
     [ "$count" -gt 0 ] && break
     sleep 1
   done
   ```
   Fail if tools never appear. Report `skipped_operations` from the backend if non-empty — those indicate spec features the parser rejected.

4. **Pick a representative read-only tool** from the backend's tool list. Prefer:
   - A GET endpoint with no required path/query params (smallest blast radius)
   - Falls back to a GET with simple integer or string path param (substitute a plausible value — `1` for IDs)

5. **Grant scope** if the chosen tool's scope isn't already on the test agent. Surface this clearly to the user — scope policy is the most common silent failure mode.

6. **Fire the tool call** via the gateway MCP endpoint and confirm:
   - Response is non-empty
   - `isError` is false
   - Audit log shows the call (`GET /api/v1/audit?limit=1`)

7. **Cleanup** (optional, only if `$2 == --cleanup`): `DELETE /api/v1/backends/$ID`.

## Output

Report concisely:
- Backend ID + tool count
- Any skipped_operations (with reason codes)
- The tool that was invoked
- Pass/fail per step
- Audit row ID

## What this catches

- Relative `servers[].url` resolution (commit `8f152a0`)
- Integer scalar formatting in path/query (commit `08d6e76`)
- `application/*+json` content type acceptance (commit `6c299d1`)
- `$ref` strings leaking into tool input schemas (commit `2eeaac9`)
- Scope policy gotchas where tools register but are invisible until granted

## What this does NOT catch

- OAuth-protected APIs (no flow automated here — operator must wire credentials in admin UI first)
- Mutating operations (test only fires GETs to keep idempotent)
- Multi-step workflows
