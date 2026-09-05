# Prism

A local MCP gateway that lives in the system tray. AI agents (Claude Code, Cursor, Codex) connect to Prism as a single Streamable HTTP MCP server. Prism spawns your real MCP servers over stdio, aggregates their tools, and holds any call the policy marks as Ask until you allow or deny it from the panel.

## Run

From the repo root you only need the desktop app; it hosts `prism-core`.

```bash
cd apps/desktop
pnpm install
cargo tauri dev
```

The first launch writes `prism.json` to the Tauri app-config directory and `audit.jsonl` to the app-data directory. The gateway listens on `127.0.0.1:9086` by default (`listen_port` in `prism.json`).

### Storage and credentials

| OS | Configuration | Audit log |
| --- | --- | --- |
| Linux | `~/.config/dev.prism.gateway/prism.json` | `~/.local/share/dev.prism.gateway/audit.jsonl` |
| macOS | `~/Library/Application Support/dev.prism.gateway/prism.json` | `~/Library/Application Support/dev.prism.gateway/audit.jsonl` |
| Windows | `%APPDATA%\dev.prism.gateway\prism.json` | `%APPDATA%\dev.prism.gateway\audit.jsonl` |

Linux honors `XDG_CONFIG_HOME` and `XDG_DATA_HOME`. Windows uses Roaming AppData. These are per-user locations; Prism does not relocate or install server executables. They remain wherever the configured executable or package manager puts them.

Prism sets its storage directories to `0700` and files to `0600` on Linux/macOS, including existing files. On Windows it applies a protected DACL granting access to the current user and SYSTEM. Symlink/reparse-point storage targets are refused. Configuration updates use a private temporary file in the same directory and an atomic replacement, so a failed write does not truncate the live configuration. Applications embedding `prism-core` must supply paths in an application-owned directory, because Prism tightens that directory's permissions.

All configured server **argument and environment values** go into the OS credential store: macOS Keychain, Windows Credential Manager, or a Linux Secret Service provider such as GNOME Keyring or KWallet. This deliberately protects arbitrary names, positional arguments, and credentials embedded in URLs, rather than guessing which fields are secrets. `prism.json` retains the server name, executable, enabled state, and an opaque `credential_ref`; `args` and `env` are empty. Values are retrieved when starting/restarting the server and are not returned in desktop server snapshots. Long values are split into verified credential entries under service `dev.prism.gateway.servers` to accommodate Windows' per-entry limit.

On startup, legacy plaintext launch settings are copied into the credential store and read back for verification **before** the configuration is replaced. No plaintext backup is created. If the store is locked/unavailable during migration, startup reports an error and preserves the original configuration for recovery; unlock the store and retry. There is no plaintext fallback. For already-migrated servers, unavailable credentials mark the affected server as failed while leaving the panel available. New servers are protected before saving, and removing a server removes its credential entries. If cleanup fails after removal, the OS credential manager can be used to remove the remaining entry. Copying `prism.json` to another machine does not copy credentials; re-add the servers there. Existing backups or filesystem snapshots may still contain the old plaintext and are not rewritten by Prism.

Linux requires a running session D-Bus and an unlocked Secret Service provider; development builds also require the D-Bus development package (`libdbus-1-dev` on Debian/Ubuntu). No secret service is contacted for a configuration with no stored launch values.

Audit logs keep agent/tool identifiers, timestamps, verdicts, and decision sources. Raw error details are omitted because a server can echo credentials; backend stderr is discarded, and rmcp diagnostic logging is disabled in Prism's subscriber. Existing audit files are rewritten to remove legacy error details. Tool arguments/results are not persisted in the audit log. The current file is capped at **5 MiB**, with **three archives** (`audit.jsonl.1` through `.3`) for at most approximately **20 MiB** total. Records older than **30 days** are removed at startup and hourly while running; cleanup also bounds oversized legacy files. The panel retains at most 1,000 recent entries in memory.

Agent approvals, rules, OAuth client registrations, and token hashes remain in private JSON. Raw OAuth access/refresh tokens are never persisted by Prism. OS credential storage protects data at rest; the launched server necessarily receives its credentials, command-line arguments can still be visible through OS process inspection, and applications running as the same user are not sandboxed by file permissions.

Storage tests run with `cargo test -p prism-core` (the Unix child-process fixture requires Python 3). The explicit native-store smoke test uses disposable credentials and requires an unlocked store: `cargo test -p prism-core native_store_round_trip -- --ignored`.

### Frontend only

`pnpm dev` inside `apps/desktop` serves the panel at http://localhost:1420 with a fixture backend (`src/mock.ts`), so the UI can be iterated in a normal browser without Tauri. `#servers`, `#agents`, `#rules` pick the tab; `?scheme=light` or `?scheme=dark` overrides the colour scheme.

On Linux Prism anchors the panel to the cursor when you pick Open Prism from the tray menu, below a top bar or above a bottom bar, and remembers that spot for later auto-opens. Before the first tray open it falls back to the screen edge that reserves a work-area strut, else top-right. Override with `panel_anchor` in `prism.json`: `auto`, `top-right`, `top-left`, `bottom-right`, or `bottom-left`. `PRISM_SHOW_PANEL=1` opens the panel on launch, handy during development. `Ctrl+Shift+Space` (or `Cmd+Shift+Space` on macOS) also toggles the panel.

## Connect an agent

For clients with OAuth support, point the client at the gateway URL, `http://127.0.0.1:9086/mcp`, or add it to the client's `mcp.json`:

```json
{ "mcpServers": { "prism": { "url": "http://127.0.0.1:9086/mcp" } } }
```

The panel shows both, with copy buttons, on the "Connect an agent" screen behind the button at the bottom of the Agents tab.

The gateway is an OAuth 2.1 authorization server as well as the MCP resource, following the MCP authorization spec. A client that hits `/mcp` without a token gets a 401 pointing at `/.well-known/oauth-protected-resource`, registers itself at `/register` (RFC 7591 dynamic registration, open to anyone on the machine), and opens `/authorize` in a browser with PKCE S256. Registration grants nothing. The browser parks there while Prism registers the agent as **pending**, flips the tray icon, and shows the approve/deny card in the panel: approval is the consent screen. Approve, and the browser is redirected back with a code the client swaps at `/token` for a one-hour access token and a thirty-day rotating refresh token. Deny, and the redirect carries `access_denied` and no token is ever issued. Tokens are opaque and stored only as SHA-256 hashes in `prism.json`.

Identity is the token. A session's announced `clientInfo.name` is recorded but never used to pick the agent, so a process cannot borrow another agent's approval by announcing its name. Each client registration is bound to exactly one agent; a client that loses its registration shows up as a new agent and asks again. Every sign-in waits for you, including sign-ins for an agent you already approved: those show as a "wants to sign in again" card, because a public `client_id` is not proof of who is asking. Refusing it issues nothing and leaves the agent's approval and existing tokens alone. An MCP session is also bound to the identity that opened it, so a valid token for one agent cannot be used on another agent's session id, on any method. Revoking an agent, denying it, or pressing **Sign out** on its screen deletes its tokens, and its next request is a 401. When you approve, Prism also sends `notifications/tools/list_changed` to every open session for that agent so it refetches and sees the tools.

For clients without OAuth support, open **Agents → Connect an agent → Manual token**, enter a name, and create a token. This approves that agent with the default first-use tool permissions. Copy the token or the generated connection settings into your client; it must send an `Authorization: Bearer <token>` HTTP header, either through a bearer-token setting or custom headers. Manual provisioning is available only in the desktop panel, not through an open HTTP endpoint.

There is one manual token per agent. Prism shows it only when created or replaced and stores only its SHA-256 hash. It has no automatic expiry; **Revoke token**, **Replace token**, **Revoke access**, and forgetting the agent invalidate it. Replacing a token preserves the agent's permissions. The old token stops working even on an existing MCP session. Manual tokens cannot be exchanged for OAuth tokens.

Anonymous connections and the old Settings switch have been removed. Existing legacy agents keep their names, approval state, and rules, but appear as **Token needed**. Open an approved agent and choose **Create token**, then update its client settings. Pending or denied agents need approval first. Old `allow_unauthenticated` settings are ignored and removed on save; announcing a name never grants access.

## Local security boundaries

MCP servers receive a small environment allowlist, followed by their explicitly configured values. The allowlist covers executable lookup, home/user and locale, temporary directories, XDG/cache locations, and platform launch variables (Windows system/profile paths and Linux display settings). Unrelated desktop credentials and tokens are not inherited. If a server needs another variable, configure it for that server; its value goes into OS credential storage. Server stderr is discarded.

Every HTTP route requires a loopback Host (`127.0.0.1`, `localhost`, or `[::1]`) at the listener's port. MCP requests and OAuth POSTs reject foreign or `null` browser Origins. Native clients without an Origin continue to work, and `/authorize` permits browser navigation from other origins.

OAuth allows one pending sign-in per agent and 16 overall. A separate request receives `temporarily_unavailable` and must retry; it cannot share another request's consent. Pending requests expire after ten minutes. There can be 64 unused registrations; abandoned registrations expire after 24 hours and are removed on startup or the next registration. Approved and denied identities are preserved. Per-minute limits are 30 registrations, 30 authorizations, 120 token requests, and 60 revocations, with HTTP 429 and `Retry-After` when exceeded. OAuth request bodies are limited to 32 KiB and registration allows at most eight redirect URIs of 2 KiB each.

The panel's production CSP permits bundled scripts, fonts, styles and images, plus Tauri IPC. Inline scripts, external connections, frames, plugins and form submissions are blocked; inline styles remain allowed for component styling. Development additionally permits the local Vite hot-reload socket. The panel capability grants only event listening/unlistening; window controls, shortcuts and notifications run in Rust. Agent names and tool arguments remain escaped text. This policy provides an additional layer of protection against injection.

## Permissions

Three things are independent for every call: the **decision**, how loudly Prism **tells you**, and how long the answer **lasts**.

Each agent has a **posture**, the default when no rule matches:

| Posture | Behaviour |
| --- | --- |
| Supervised | Every call asks. |
| First use (default) | Asks once per tool, then remembers your answer. |
| Guided | Tools the server marks read-only pass; anything else asks. Trusts the server's own annotations. |
| Trusted | Everything passes and is logged. |

Each agent also has an **attention** level for calls resolved without asking: silent, badge (tray icon lights until you open the panel), notify, or open the panel. Rules can override it.

**Rules** sit on top of the posture. A rule matches an agent, a server, and a tool (exact name or a glob such as `create_*`), decides allow, deny, or ask, and can carry its own attention and a time box. The most specific rule wins; deny beats ask beats allow on a tie; an exact tool name beats a glob. Expired time boxes are pruned automatically.

From the held-call card you can allow once, allow for 30 minutes, always allow this tool, or allow everything on that server for this agent. Tapping an agent opens its screen: posture, attention, per-server access (All / Ask / None), per-tool overrides behind each server, and every remembered grant with its countdown.

Operator settings live behind the sliders icon: do not disturb (held calls resolve on their own, new agents still ask), what happens when nobody answers (deny, or allow if read-only), how long a call is held, and a rate tripwire that turns an agent's allowed calls into asks once it runs hot.

## Tray

The tray mark is a standing prism, two facets with a gutter between them (`apps/desktop/src-tauri/icons/prism-mark.svg`). Idle is a pale glyph on dark bars and a macOS template icon; pending is the same shape in amber. On Windows a light taskbar gets the ink variant. The PNGs and the app icon set are rendered from those polygons, not hand-drawn, so regenerate rather than edit.

Prism uses Tauri's standard tray integration. On Linux, click the icon and choose **Open Prism** or **Quit** from the menu. On macOS and Windows, left-click toggles the panel directly. The existing `Ctrl+Shift+Space` shortcut (`Cmd+Shift+Space` on macOS) also toggles the panel when available.

## Workspace

- `crates/prism-core` — headless gateway library (`cargo test -p prism-core`)
- `apps/desktop` — Tauri v2 tray app (Preact + Vite frontend, Rust host)
  - `src/tokens.css` is the design system: every colour and font in `src/styles.css` references a token, light and dark via `light-dark()`
  - `src/screens/*` one file per tab, `src/ui.tsx` the shared primitives

Tool names exposed to agents are `{server_name}__{tool}`.
