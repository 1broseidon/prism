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

### Frontend only

`pnpm dev` inside `apps/desktop` serves the panel at http://localhost:1420 with a fixture backend (`src/mock.ts`), so the UI can be iterated in a normal browser without Tauri. `#servers`, `#agents`, `#rules` pick the tab; `?scheme=light` or `?scheme=dark` overrides the colour scheme.

On Linux the tray cannot report its own position, so Prism anchors the panel to the cursor at the moment you pick Open Prism from the tray menu (below a top bar, above a bottom bar, centred on the icon) and remembers that spot for later auto-opens. Before the first tray open it falls back to the screen edge that reserves a work-area strut, else top-right. Override with `panel_anchor` in `prism.json`: `auto`, `top-right`, `top-left`, `bottom-right`, or `bottom-left`. `PRISM_SHOW_PANEL=1` opens the panel on launch, handy during development. `Ctrl+Shift+Space` (or `Cmd+Shift+Space` on macOS) also toggles the panel.

## Connect an agent

There are no API keys. Point any MCP client at the gateway URL, `http://127.0.0.1:9086/mcp`, or add it to the client's `mcp.json`:

```json
{ "mcpServers": { "prism": { "url": "http://127.0.0.1:9086/mcp" } } }
```

The panel's Agents tab shows both with copy buttons.

The first time a client connects, Prism reads the name it announces in MCP `initialize` (`clientInfo.name`), registers it as **pending**, flips the tray icon, and shows an approve/deny card in the panel. Until you approve, that client sees an empty tool list and any call it attempts is refused with a message telling it to get approved. When you approve, Prism sends `notifications/tools/list_changed` to every open session for that agent so it refetches and sees the tools. Deny or revoke does the reverse. Approved agents still go through your rules on every call.

Identity is by announced client name for the HTTP transport. Process-level identity comes with the stdio shim in a later phase.

## Tray

Left-click the tray icon to open the panel on macOS and Windows. Linux trays (StatusNotifierItem) do not deliver click events to the app, so on Linux any click shows the menu; pick **Open Prism**. The panel opens next to where you clicked (see below).

## Workspace

- `crates/prism-core` — headless gateway library (`cargo test -p prism-core`)
- `apps/desktop` — Tauri v2 tray app (Preact + Vite frontend, Rust host)
  - `src/tokens.css` is the design system: every colour and font in `src/styles.css` references a token, light and dark via `light-dark()`
  - `src/screens/*` one file per tab, `src/ui.tsx` the shared primitives

Tool names exposed to agents are `{server_name}__{tool}`.
