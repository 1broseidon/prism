# Changelog

All notable changes to Prism are documented in this file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-05-27

First public release.

### Added

- Embedded OAuth 2.1 authorization server (always-on): Dynamic Client
  Registration, DPoP, Rich Authorization Requests (RAR), step-up
  authentication, identity migration, grants DSL and bindings store.
- Five backend sources: native HTTP, bridged stdio (sandboxed), tool
  functions, OpenAPI 3 specs (URL / inline / `curl`-scaffolded), and
  managed binary stdio MCP servers via the binstore.
- Admin console (Preact SPA embedded in the gateway): agents, groups,
  roles, identity unification, policy builder, grant templates and
  bindings, analytics (SQLite-backed + SSE ring buffer), per-tool
  enable/disable per backend, OpenAPI inline editor, workspace bridge
  controls, settings (network, sign-in, storage).
- Admin SSO over OIDC (Google, Okta, Auth0, Keycloak, Authentik) with
  email / domain / group role mapping.
- Workspace bridge (`prism-bridge workspace`): per-agent sidecar that
  exposes a workspace directory as scoped MCP tools with snapshot
  copies and sandbox-only / stage / auto-apply write-back.
- KV store with at-rest encryption for sensitive entries (OAuth client
  secrets, refresh tokens, admin sessions) via `PRISM_KV_KEY_FILE`.
- OpenTelemetry tracing via `OTEL_EXPORTER_OTLP_ENDPOINT`.
- Three static binaries: `prism` (gateway + admin), `prism-bridge`
  (stdio↔HTTP + manage + workspace modes), `prism-auth` (standalone
  OAuth server for separated deployments).
- GitHub Actions CI (build + race tests + lint + fmt-check) and release
  workflow (multi-arch Docker images to `ghcr.io/1broseidon/prism` and
  `ghcr.io/1broseidon/prism-bridge`, plus `linux/darwin × amd64/arm64`
  tarball binaries attached to the GitHub release).
- Apache-2.0 licensed.

### Fixed

- DPoP `htm` claim is matched byte-equal (per RFC 9449 §4.3 and RFC
  7230 §3.1.1); the previous `strings.EqualFold` comparison accepted
  case-mismatched HTTP methods.

[0.1.0]: https://github.com/1broseidon/prism/releases/tag/v0.1.0
