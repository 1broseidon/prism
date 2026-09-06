# Prism

**A local MCP gateway that lives in your system tray.** Point Claude Code, Cursor, Codex or any MCP client at one endpoint. Prism runs your MCP servers, aggregates their tools, and holds any call your rules mark as *ask* until you allow or deny it from the panel. Nothing leaves the machine.

<p align="center">
  <img src="docs/intro.gif" alt="An agent asks for a tool, the tray turns amber, the panel opens and you allow or deny the call" width="960">
</p>

<p align="center">
  <a href="https://github.com/1broseidon/prism/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/1broseidon/prism?display_name=tag&color=e9a23b&labelColor=1c1917"></a>
  <a href="https://github.com/1broseidon/prism/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/1broseidon/prism/ci.yml?branch=main&labelColor=1c1917"></a>
  <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-lightgrey?labelColor=1c1917"></a>
</p>

- **One endpoint.** Every agent connects to `http://127.0.0.1:9086/mcp`. Prism spawns your real MCP servers over stdio and exposes their tools as `{server}__{tool}`.
- **Held calls.** A call that needs you shows up as a card with a two-minute countdown. Allow once, allow for 30 minutes, always allow this tool, or allow everything on that server. Denial is a first-class answer with a clear refusal to the agent.
- **Approval is consent.** Prism is its own OAuth 2.1 authorization server. A new agent registers, a browser parks on the consent step, and the approve card in the panel is that consent. Clients without OAuth get a manual bearer token.
- **Secrets stay in the keychain.** Server arguments and environment go into the OS credential store, never into a config file. The audit log is redacted and rotated.

## Install

Download the build for your machine from the [latest release](https://github.com/1broseidon/prism/releases/latest). Every asset has a line in `checksums.txt`.

| Platform | Asset | Notes |
| --- | --- | --- |
| macOS, Apple silicon | `prism_<version>_darwin_arm64.dmg` | Drag Prism to Applications. |
| macOS, Intel | `prism_<version>_darwin_x86_64.dmg` | Same. |
| Windows | `prism_<version>_windows_x86_64.msi` or `-setup.exe` | MSI for managed machines, the setup exe for a per-user install. |
| Linux, x86_64 | `.AppImage`, `.deb`, `.rpm` | AppImage needs `chmod +x`. The deb and rpm declare their WebKitGTK and AppIndicator dependencies. |
| Linux, arm64 | `.AppImage`, `.deb`, `.rpm` | Same. |

**macOS.** Builds are signed with a Developer ID certificate and notarized by Apple, so the app opens without a Gatekeeper prompt. If you installed an earlier unsigned build, replace it with the current download.

**Linux.** The deb and rpm install the binary as `prism-desktop` and add a **Prism** entry under Development in the application menu. Launching it puts the icon in the tray and nothing else on screen.

**Linux requirements.** Prism needs a session D-Bus and an unlocked Secret Service provider such as GNOME Keyring or KWallet, because that is where server credentials live. GNOME users also need an AppIndicator extension for the tray icon to appear. Ubuntu and Fedora desktops ship both.

A Homebrew cask via `1broseidon/tap` is coming. Installed copies update themselves; see [Updates](#updates).

### From source

Requires Rust stable, Node 24 and pnpm 10. On Linux, also the Tauri build dependencies:

```sh
sudo apt-get install -y libwebkit2gtk-4.1-dev libayatana-appindicator3-dev librsvg2-dev \
  libdbus-1-dev libssl-dev libxdo-dev pkg-config build-essential
```

Then:

```sh
cd apps/desktop
pnpm install
pnpm tauri build          # bundles land in target/release/bundle
```

## First run

Prism starts in the tray and stays there. Click the icon on macOS and Windows, or pick **Open Prism** from the menu on Linux. `Ctrl+Alt+P` toggles the panel from anywhere; set `panel_shortcut` in `prism.json` to change it (`"Super+Shift+P"`, say) or to `""` to turn it off.

The gateway listens on `127.0.0.1:9086`. Change it with `listen_port` in `prism.json`, which is written on first launch:

| OS | Configuration | Audit log |
| --- | --- | --- |
| Linux | `~/.config/dev.prism.gateway/prism.json` | `~/.local/share/dev.prism.gateway/audit.jsonl` |
| macOS | `~/Library/Application Support/dev.prism.gateway/prism.json` | `~/Library/Application Support/dev.prism.gateway/audit.jsonl` |
| Windows | `%APPDATA%\dev.prism.gateway\prism.json` | `%APPDATA%\dev.prism.gateway\audit.jsonl` |

Linux honours `XDG_CONFIG_HOME` and `XDG_DATA_HOME`. Directories are `0700` and files `0600` on Unix; Windows gets a DACL limited to you and SYSTEM. Configuration writes are atomic, so a crash mid-save cannot truncate the live file.

The panel has four tabs. **Now** holds anything waiting for you plus the recent feed. **Servers**, **Agents** and **Rules** are the three things you configure. The sliders icon opens operator settings.

## Add a server

**Servers → Add server.** Give it a name, the executable, its arguments, and any environment variables. Prism does not install servers; it launches whatever executable you name, wherever your package manager put it.

Arguments and environment values go straight into the OS credential store: macOS Keychain, Windows Credential Manager, or Secret Service on Linux. Prism protects *all* of them rather than guessing which ones are secrets, since tokens have a way of ending up in URLs and positional arguments. `prism.json` keeps only the name, executable, enabled flag and an opaque credential reference. Copying `prism.json` to another machine does not copy credentials; add the servers again there.

Servers receive a small environment allowlist (PATH, HOME, locale, temp and XDG directories, the platform's display and profile variables) plus what you configured. Your shell's other tokens are not inherited. Server stderr is discarded so a chatty server cannot echo a credential into a log.

If the credential store is locked when Prism starts, affected servers show as failed and the panel stays usable. Unlock the store and restart the server.

## Connect an agent

**Agents → Connect an agent** shows both options with copy buttons.

**Clients with OAuth support** (Claude Code, Cursor, Codex and most current MCP clients) only need the URL:

```json
{ "mcpServers": { "prism": { "url": "http://127.0.0.1:9086/mcp" } } }
```

The client registers itself, opens a browser, and the browser waits. Prism flips the tray amber and shows a **wants to connect** card. Approve, and the client gets a one-hour access token and a thirty-day rotating refresh token; the browser is sent back and the tools appear. Deny, and no token is ever issued.

Later sign-ins for an approved agent also ask, as a **wants to sign in again** card. A public client id proves nothing, so if nothing on your side asked to sign in, refuse it. Refusing leaves the agent's existing approval and tokens alone.

**Clients without OAuth support** get a manual token: **Connect an agent → Manual token**, name the agent, and copy the token or the generated settings into the client. It must send `Authorization: Bearer <token>`. There is one manual token per agent, shown only once, with no expiry. **Replace token** rotates it without touching permissions; **Revoke token** or **Revoke access** kill it immediately, including on open sessions.

Identity is the token, never the name a client announces about itself. Every session is bound to the identity that opened it, so one agent's token cannot ride another agent's session.

## Decide what runs

Three things are independent for every call: the **decision**, how loudly Prism **tells you**, and how long the answer **lasts**.

Each agent has a **posture**, the default when no rule matches:

| Posture | Behaviour |
| --- | --- |
| Supervised | Every call asks. |
| First use (default) | Asks once per tool, then remembers your answer. |
| Guided | Tools the server annotates as read-only pass; everything else asks. Trusts the server's own annotations. |
| Trusted | Everything passes and is logged. |

Each agent also has an **attention** level for calls that resolve without asking: silent, badge (tray lights until you open the panel), notify, or open the panel.

**Rules** sit on top of the posture. A rule matches an agent, a server and a tool (exact name or a glob such as `create_*`), decides allow, deny or ask, and can carry its own attention level and a time box. The most specific rule wins; deny beats ask beats allow on a tie; an exact name beats a glob. Expired time boxes prune themselves.

Every answer you give from a held-call card becomes one of these: allow once resolves the call, the other three write a rule. Tap an agent to see its posture, per-server access (All / Ask / None), per-tool overrides, and every remembered grant with its countdown.

**Operator settings**, behind the sliders icon:

- **Do not disturb.** Held calls resolve on their own; new agents still ask.
- **When nobody answers.** Deny, or allow if the tool is read-only.
- **Hold timeout.** How long a call waits for you. Default two minutes.
- **Rate tripwire.** When an agent runs hot, its allowed calls turn into asks until it calms down.

## Updates

Prism checks the [latest release](https://github.com/1broseidon/prism/releases/latest) shortly after launch and every six hours. When something newer exists, the settings icon shows an amber dot and **Settings → Updates** has the notes and an **Install and restart** button. Nothing installs on its own, and the only thing sent is the request for the release manifest.

Every update file is signed with Prism's minisign key and checked against the public key built into the app before it is installed, on top of Apple notarization on macOS. The DMG, AppImage, deb, rpm, MSI and setup exe can all update in place. Deb and rpm installs ask for your password through `pkexec`. A copy built from source, or installed by some other route, gets a link to the release page instead.

## Native actions

MCP is only part of what an agent does. Claude Code and Codex run shell commands, edit files and fetch pages on their own, and none of that passes through the gateway. Prism can observe those too.

**Agents → Claude Code → Write it for me** adds an HTTP hook to `~/.claude/settings.json`; **Agents → Codex → Write it for me** adds a command hook to `~/.codex/hooks.json` (a backup of the previous file is kept either way, and every other hook in it is left alone). From then on the host reports each action to Prism just before it runs and carries on regardless of the answer. Claude Code posts directly; Codex has no HTTP hook type, so its entry is a one-line `curl` with tight timeouts: a loopback post takes milliseconds, and a stopped Prism refuses the connection at once. Prism adds nothing to the host's own permission flow: no card, no prompt, no block. If Prism is not running, the hook fails silently and the host proceeds. Codex reviews every new or changed hook before running it: after Prism writes the entry, open `/hooks` in a Codex session and trust it, and again after rotating the token. The Codex host screen shows **review in Codex** until that is done; Prism reads Codex's trust state and never writes it.

What the record keeps is one line per action, never the raw input: the redacted command for a shell call, the path for a file read or write (for a Codex `apply_patch`, the file paths named in the patch and nothing of its content), the origin for a fetch, the tool name for anything else. Bearer tokens, key-like assignments, URL passwords and long opaque strings are replaced before the line is stored.

The **Now** feed shows native rows beside MCP calls with a filter for each. The **Agents** tab shows the host's coverage: **observed** once actions are arriving, **MCP only** before the hook is set up or when observation is switched off. A short deny list runs in shadow and marks the actions it would have held, such as a recursive delete outside the working directory, a forced push, a pipe from curl into a shell, or a write under `~/.ssh`. Nothing is held; **Settings → Native actions** counts them for the week and can export the entries. That count is what decides whether a real gate is worth building.

Native action coverage depends on the agent host honouring its own hooks. Prism shows what it can see and labels it; it does not sandbox anything, and a process that bypasses the host is outside what a tray app can see.

## What the audit log keeps

Agent, tool, timestamp, verdict and what decided it (you, a rule, the posture, do-not-disturb, or a timeout). Tool arguments and results are never persisted, and raw error text is dropped because servers echo credentials. The current file is capped at 5 MiB with three archives, and entries older than 30 days are removed at startup and hourly. The panel shows the last 1,000.

## What Prism protects, and what it does not

Everything binds to loopback. Every request must carry a loopback `Host`, and MCP and OAuth POSTs reject foreign or `null` browser origins, so a web page cannot drive the gateway from a tab. Registration is open to anything on the machine but grants nothing on its own. Pending sign-ins are limited (one per agent, sixteen overall, ten-minute expiry), request bodies are capped, and the OAuth routes are rate limited with `429` and `Retry-After`.

Prism does not sandbox the servers it launches. A server necessarily receives its own credentials, and any process running as your user can read what your user can read. The boundary Prism draws is between *agents* and *tools*: which agent may call what, when, and with your say-so.

## Troubleshooting

- **Port already in use.** Set `listen_port` in `prism.json` and restart. Update the URL in your clients.
- **No tray icon on GNOME.** Install an AppIndicator extension, then log out and back in.
- **Servers show "failed" on Linux at login.** The keyring was still locked when Prism started. Unlock it and restart the server from the Servers tab.
- **The agent sees no tools.** It is pending. Open the panel and approve it; Prism pushes a `tools/list_changed` notification so the client refetches.
- **The panel opens in the wrong corner on Linux.** Set `panel_anchor` in `prism.json` to `top-right`, `top-left`, `bottom-right` or `bottom-left`. `auto` follows the cursor when opened from the tray and otherwise picks the corner the desktop's reserved bar points at, top right when nothing is reserved.

## Development

```sh
cd apps/desktop
pnpm install
cargo tauri dev           # full app with the tray
pnpm dev                  # panel only, in a browser, against a fixture backend
```

The browser mode serves http://localhost:1420 with `src/mock.ts` as the backend. `#servers`, `#agents` and `#rules` pick a tab; `?scheme=light` or `?scheme=dark` overrides the colour scheme. `PRISM_SHOW_PANEL=1` opens the panel on launch of the real app.

```sh
cargo test -p prism-core                                   # gateway, policy, OAuth, storage
cargo test -p prism-core native_store_round_trip -- --ignored   # real keychain smoke test
```

- `crates/prism-core` is the headless gateway: policy, backends, approvals, OAuth, audit, storage.
- `apps/desktop` is the Tauri v2 tray app. Preact and Vite on the panel side, a thin Rust host on the other. `src/tokens.css` is the design system; every colour in `src/styles.css` goes through it.
- `docs/intro.html` is the animated walkthrough at the top of this page, self-contained.

## Releasing

Versions live in three places and must agree: the workspace `Cargo.toml`, `apps/desktop/src-tauri/tauri.conf.json` and `apps/desktop/package.json`. Bump them, add a `## [x.y.z]` section to `CHANGELOG.md`, commit, then tag:

```sh
git tag vX.Y.Z && git push --tags
```

The release workflow checks the three versions against the tag, builds the DMG, MSI, NSIS installer, AppImage, deb and rpm for five targets, writes `checksums.txt`, and publishes a GitHub release with the changelog section as its notes. macOS signing and notarization use six repository secrets: `APPLE_CERTIFICATE` (base64 Developer ID Application p12), `APPLE_CERTIFICATE_PASSWORD`, `APPLE_SIGNING_IDENTITY`, and the App Store Connect key as `APPLE_API_KEY`, `APPLE_API_ISSUER`, `APPLE_API_KEY_P8`. Without the certificate the bundle is ad-hoc signed.

Update files are signed with the minisign key in `TAURI_SIGNING_PRIVATE_KEY` and `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`; its public half is `plugins.updater.pubkey` in `tauri.conf.json`. The release job writes `latest.json` next to the assets, which is what installed copies poll. Losing that private key means shipping a release that existing installs refuse, so keep it with the Apple material.

## License

[MIT](LICENSE)
