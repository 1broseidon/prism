# Changelog

All notable changes to Prism are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow [SemVer](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-09-05

### Added
- Tray app for macOS, Windows and Linux hosting a local Streamable HTTP MCP gateway on `127.0.0.1:9086`.
- Aggregates stdio MCP servers behind one endpoint; tools are exposed as `{server}__{tool}`.
- Held calls: a panel card with a two-minute countdown, allow once, allow for 30 minutes, always allow this tool, or allow everything on a server. Denial is a first-class answer.
- Agent approval and OAuth 2.1 with dynamic client registration and PKCE; approval is the consent screen. Manual bearer tokens for clients without OAuth.
- Postures per agent (supervised, first use, guided, trusted), attention levels, rules with globs and time boxes, do-not-disturb, hold timeout and a rate tripwire.
- Server launch arguments and environment stored in the OS credential store (Keychain, Credential Manager, Secret Service).
- Rotating, redacted audit log with 30-day retention.
- Loopback-only HTTP with Host and Origin checks, request limits and a strict panel CSP.

[Unreleased]: https://github.com/1broseidon/prism/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/1broseidon/prism/releases/tag/v0.1.0
