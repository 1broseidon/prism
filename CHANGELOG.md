# Changelog

All notable changes to Prism are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow [SemVer](https://semver.org/).

## [Unreleased]

### Added
- Native actions, observed. Claude Code can post every shell command, file edit and fetch to Prism through an HTTP hook; **Agents → Claude Code** writes the hook into `~/.claude/settings.json`. Each action becomes a redacted one-line audit entry, the Now feed shows them beside MCP calls, and a short deny list runs in shadow to count what a real gate would have held. Nothing is held or changed in this phase.

### Fixed
- Update notes in Settings showed raw changelog markdown. Headings, bullets, code and bold now render, and the card links to the full release notes.

## [0.2.1] - 2026-09-06

### Fixed
- macOS and Linux: launched from the menu, Prism only saw the session PATH, so a server installed by `go install`, Homebrew, cargo or pnpm was "not found" even though it ran from a terminal. Prism now asks the login shell for its PATH at startup and uses that to find servers.
- Linux: the deb and rpm desktop entry had no category, so menus that group by category (Cinnamon, KDE) did not list Prism. It now sits under Development.

## [0.2.0] - 2026-09-06

### Added
- Built-in updater. Prism checks the latest release after launch and every six hours, marks the settings icon when something newer exists, and **Settings → Updates** installs it in place and restarts. Update files are minisign-signed and verified against the key built into the app. Works for the DMG, AppImage, deb, rpm, MSI and setup exe; other installs get a link to the release page.
- Releases publish `latest.json` and a `.sig` beside every installer.

### Fixed
- Windows: the panel opened below the screen from a bottom taskbar. It now anchors to the tray icon's rectangle, above a bottom bar or below a top bar, and stays inside the work area on every edge. The positioner plugin is gone.

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

[Unreleased]: https://github.com/1broseidon/prism/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/1broseidon/prism/releases/tag/v0.2.1
[0.2.0]: https://github.com/1broseidon/prism/releases/tag/v0.2.0
[0.1.0]: https://github.com/1broseidon/prism/releases/tag/v0.1.0
