# Changelog

All notable changes to Prism are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow [SemVer](https://semver.org/).

## [Unreleased]

### Changed
- A harness is one agent. Every OAuth client that names Claude Code or Codex, from user settings or any project, now joins that harness's single entry alongside its hooks: one posture, one attention level, one rule set. The Agents tab is one list, a harness row shows its connections and hook coverage, and its screen lists each registration under **Connections** with **Forget** per registration and **Sign out everywhere**. A further registration of an approved harness asks once, as a **wants to connect from a new place** card. Agents that earlier versions made per registration are folded into the harness entry on first start, with their tokens, rules, posture and attention. Each client records where it registered from, so a harness on another machine will be its own entry when remote access lands.
- The page the browser lands on after an OAuth sign-in matches the panel: the Prism mark centred with a green facet, "Signed in.", the server's name, and "You can close this tab." A refused sign-in shows the same page in red, a stale one in muted ink. It loads nothing from the network.

## [0.3.0] - 2026-09-06

### Added
- Remote MCP servers. **Servers → Add server → URL** connects to a Streamable HTTP server with no auth, an API key header, or OAuth 2.1. OAuth discovers the server's settings from its 401 challenge, registers Prism as a public client with dynamic client registration, signs in through your browser with PKCE, and takes the code back on a one-off loopback listener; tokens refresh on their own. Keys, the registered client and tokens live in the OS keyring under an opaque reference, never in `prism.json`. A signed-out OAuth server shows *needs sign-in*, and its row offers **Sign in** and **Sign out**. URLs must be https, except plain http to this machine.
- Native actions, observed. Claude Code and Codex can report every shell command, file edit and fetch to Prism through their hooks; **Agents → Claude Code** writes an HTTP hook into `~/.claude/settings.json` and **Agents → Codex** writes a `curl` hook into `~/.codex/hooks.json`, which Codex asks you to trust in `/hooks` before it runs. Each action becomes a redacted one-line audit entry (a Codex patch is recorded by the paths it touches, never its content), and a short watch list marks the risky ones: a recursive delete outside the project, a forced push, curl piped into a shell, sudo, a read of keys or a `.env` file, a write under `~/.ssh` or outside the project. The Now summary counts those as needed attention. Nothing is held or changed in this phase.

### Changed
- The Now tab sums up the last seven days instead of listing every call: actions, how many needed a person, a bar per day, and the busiest agents. Every number is a door: the attention count, a day's bar, an agent's row or a watch-list pattern opens the action list narrowed to exactly those rows, with each narrowing shown as a chip that can be dropped. A row in the list unfolds to show what it had to cut.
- Less prose everywhere. Cards, hints and empty states say what they must and no more; the hook snippet is folded away behind **Show snippet**; the MCP verdict line and the Settings "This week" row are gone, since the numbers above them already say it.
- Destructive buttons (revoke, forget, sign out, remove, delete, refuse) ask once, on the button itself: tap, then tap again within three seconds.

### Fixed
- The panel shortcut was `Ctrl+Shift+Space` (`Cmd+Shift+Space` on macOS), which 1Password takes on every platform. It is now `Ctrl+Alt+P`, and `panel_shortcut` in `prism.json` overrides it.
- Scrollbars no longer show on ordinary screens; only the action log keeps one.
- The panel opened wherever the window manager put it when it was not opened from a tray click, such as from the keyboard shortcut. It now lands in a fixed corner of the tray's monitor: below a top bar, above a bottom one, top right when nothing is reserved, and always inside the work area. Where the desktop reserves nothing for its bar, the last tray click (remembered between runs) says which edge the bar is on and the panel keeps clear of it. The position is reapplied after the window maps for window managers that place it themselves.
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
