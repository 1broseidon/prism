# Changelog

All notable changes to Prism are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow [SemVer](https://semver.org/).

## [Unreleased]

### Added
- The listen port is a setting (**Settings → Network**). Changing it moves the listener at once; agents keep their tokens but need the new address, and the field says so.
- A port that is already taken is reported instead of hidden. The panel says which port, names another copy of Prism when that is what holds it, and offers **Retry**. A free port is suggested but never chosen for you: your agents dial the port you configured.

- **Reachable from** (Settings → Network) opens the listener to the local network so agents on other machines can connect. The Connect screen shows the address they use, the OAuth issuer follows whichever address a client dialed, and the setting carries the warning it deserves: plain HTTP, readable by anything on the network. Loopback only stays the default.

- Rules can carry a condition on the call's arguments: a path prefix, a host list or scope, an argument value, or a program name. A held write or delete offers **Under ~/that/folder** next to Allow, and a call that names a host offers **For that.host**; either one writes an allow rule for exactly that. A rule whose condition Prism cannot read is marked **Inert** and never matches.
- Every held call and audit row lists what the call touches (paths with the access Prism inferred, hosts with their reach), and each audit row's detail says in one line what decided it: which rule and clause, the agent's posture, the rate tripwire, Do not disturb, or a timeout.

### Changed
- Policy settings moved from Settings to the Rules tab; Settings keeps panel, network, observation and updates.

### Fixed
- Prism no longer starts with no listener when its port is taken. Before, the bind failure was one log line and the tray looked healthy while agents could not connect.

## [0.7.2] - 2026-09-08

### Added
- Tool lists stay current. When a server announces new or changed tools, Prism re-reads that server's list and tells connected agents; hiding or exposing a tool tells them too. A server that cannot announce changes still needs a restart.
- **Sign out** asks the provider to revoke the tokens when it offers revocation, then forgets them here; the local sign-out completes even when the provider cannot be reached. Removing a server does the same. A sign-in whose saved record has no issuer gets local sign-out only until you sign in again.

### Changed
- Agents' tool calls are routed by the exact `{server}__{tool}` name. Two tools that would share one public name are kept off the list until the clash is resolved.

### Fixed
- Rapid flips of a tool's exposure switch no longer overlap: the switch waits for the save to land, and a failed refresh cannot undo a saved change.

## [0.7.1] - 2026-09-08

The first published build of 0.7. Version 0.7.0 was tagged the same day but its Windows build failed its tests, so no installers were published for it; everything below ships here.

### Added
- Global MCP and native observation setup for Cursor, OpenCode V1, Goose 1.49+ and the current Antigravity CLI configuration. Each harness groups its MCP registrations and tool observations into one agent entry. Setup preserves unrelated configuration, comments and disabled settings, with private backups, repair and owned-file removal.
- Per-harness event parsing and bounded, neutral observers. OpenCode patch/file contents are omitted before transport; Goose decisions and Antigravity completion results remain distinct from Prism policy. Observation readiness requires a real event after configuration changes.
- Per-server tool exposure. A server's row opens its own screen, which lists every tool with a switch. A hidden tool is not listed to any agent and is refused if called; the Servers list shows how many are hidden. Remove, Sign out and Restart live on that screen, and recovery (Sign in, Retry) stays on the row.
- **Settings › Panel** chooses where the panel opens, Auto or a fixed corner, and shows the shortcut (Ctrl+Alt+P unless `panel_shortcut` says otherwise).

### Changed
- Now at rest opens with **All clear.** and the server and agent counts above the seven-day chart and the busiest agents, as many whole rows as fit. **All actions** stays pinned bottom-right. The header no longer shows a Ready dot; it says Offline or Checking only when the gateway is not listening.
- One way to grow a long list. Every list that outgrows the screen scrolls, with a thin thumb that appears only while scrolling, and a **Show 20 more** row appends the next slice without moving what is already shown. Previous/Next paging is gone from Rules, Tools, Servers, Agents, Connect and the agent subscreens; Actions keeps reading its slices against one snapshot.
- The panel opens in the same place every time, from the tray, the shortcut or a pending call; the pointer no longer decides. On a Linux desktop that reserves no space for its bar, Auto reads the tray icon's position to find the bar and keeps the panel clear of it. macOS and Windows keep the panel on the tray icon. Explicit corners stay inside the desktop's work area.
- Setup gives each harness its own sign-in instructions. Observer source is available to copy from a separate details view. Routine status on rows is plain text, and only OAuth servers offer Sign in.

### Fixed
- A hook whose working directory is a POSIX path (`/home/...`) is accepted on Windows and keeps its forward slashes, so a harness running on a POSIX host can report to a Windows Prism instead of being refused. This also made the test suite pass on Windows.
- Agents lists a known harness only once it has a gateway record or a global setup on disk; Connect an agent offers the rest plus Other agent. Setup creates the host record, so a fresh harness is listed before its first contact, and a finished setup no longer offers Resume.
- The back arrow sits centred in its button.

## [0.6.0] - 2026-09-07

### Changed
- The panel is a quick-decision tray. Reopening always lands on Now, or on the approval queue when something waits. An unfinished setup or a one-time token is parked in memory and offered back on Now as **Resume** or **Discard**, never written to disk. A request that arrives while a detail screen is open no longer replaces it; the header shows **N waiting** and jumps to the queue.
- One request at a time. Now shows the current agent, sign-in or tool call with *1 of 3 waiting* and Previous/Next. A and D decide only the visible request, key repeat and double clicks are ignored for 400 ms, and **Inspect** opens the full arguments with Allow and Deny pinned to the footer. The activity dashboard returns when the queue empties.
- No hidden scrolling. Rules, Tools, Servers and Agents page through Previous/Next in the footer. Actions shows 20 rows a page that replace each other against one snapshot, with **Open log** and **Export**, which writes the filtered rows as JSONL beside a metadata file in Downloads and opens it with the default application. Agent detail is a hub with Connections, Setup, Servers and Grants; Settings pushes Observation and Updates, and release notes are a short summary with a link to the full notes.

### Fixed
- A history page answered after the filter changed no longer overwrites the newer view, and Inspect leaves a request that expired or was decided elsewhere.

## [0.5.0] - 2026-09-06

### Added

- Global Claude Code and Codex setup for MCP and native observation, with backups, repair/removal, and readiness based on configuration and observed events.
- Browser sign-in progress and confirmed outcomes, including the incoming approval wait.
- Retained-history activity queries and exports with matching filters and pagination.
- MCP 2026-07-28 over the same `/mcp` endpoint: authenticated stateless discovery and tool calls, private tool-list cache metadata, and `subscriptions/listen` for tool-list changes. Older clients continue to initialize and use bound sessions automatically. HTTP upstream connections also negotiate modern discovery with a legacy fallback.

### Fixed
- Modern Claude Code connections no longer fail after OAuth because of missing session initialization or notification support. Identity comes from each request's bearer token; approval rules and audit records apply in both protocol modes.
- Disconnecting a held request removes its approval card and records cancellation. Expired or revoked tokens close modern notification streams. Tool results from legacy upstreams receive the modern response fields when needed.

## [0.4.1] - 2026-09-06

### Fixed
- Claude Code can sign in again. The protected-resource metadata now advertises the gateway origin (`http://127.0.0.1:PORT/`) instead of `/mcp`, which is what its SDK compares against; `/authorize` still accepts either form.

### Changed
- On the page after an OAuth sign-in, the mark's second facet now refracts before it settles: a spectral band sweeps through it, the verdict colour rises where the light left, then the pool behind the mark lights and the words follow. Reduced motion skips straight to the settled page.

## [0.4.0] - 2026-09-06

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

[Unreleased]: https://github.com/1broseidon/prism/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/1broseidon/prism/releases/tag/v0.5.0
[0.4.1]: https://github.com/1broseidon/prism/releases/tag/v0.4.1
[0.4.0]: https://github.com/1broseidon/prism/releases/tag/v0.4.0
[0.3.0]: https://github.com/1broseidon/prism/releases/tag/v0.3.0
[0.2.1]: https://github.com/1broseidon/prism/releases/tag/v0.2.1
[0.2.0]: https://github.com/1broseidon/prism/releases/tag/v0.2.0
[0.1.0]: https://github.com/1broseidon/prism/releases/tag/v0.1.0
