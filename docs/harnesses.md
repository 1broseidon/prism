# Harness setup and coverage

Choose **Agents → Connect an agent**. Setup adds the local `/mcp` endpoint and an observer globally; it does not change project configuration, approve MCP access or trust a hook. Complete sign-in in the client and approve the connection in Prism. Restart the client to load changed plugins.

| Harness | Global MCP configuration | Observation | Connect |
| --- | --- | --- | --- |
| Claude Code | `~/.claude.json` | HTTP `PreToolUse` in `~/.claude/settings.json` | `/mcp` in Claude Code |
| Codex | `~/.codex/config.toml` | Command `PreToolUse` in `~/.codex/hooks.json` | `codex mcp login prism`; review hooks with `/hooks` |
| Cursor | `~/.cursor/mcp.json` | Command `preToolUse` in `~/.cursor/hooks.json` | Cursor MCP settings or `cursor-agent mcp login prism` |
| OpenCode V1 | `~/.config/opencode/opencode.json` or `opencode.jsonc` | Bundled `plugins/prism.js`, `tool.execute.before` | `opencode mcp auth prism` |
| Goose 1.49+ | `~/.config/goose/config.yaml` | `PreToolUseResult` plugin under `~/.agents/plugins/prism-goose/` | Connect the Prism extension in Goose |
| Antigravity CLI | `~/.gemini/config/mcp_config.json` | Named `PostToolUse` hook in `~/.gemini/config/hooks.json` | Connect Prism in its MCP settings |

`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, OpenCode's `XDG_CONFIG_HOME`, `OPENCODE_CONFIG` and `OPENCODE_CONFIG_DIR`, and Goose's `XDG_CONFIG_HOME`/`GOOSE_PATH_ROOT` are respected. Goose's Windows config is `%APPDATA%\Block\goose\config\config.yaml`. Goose and Antigravity command hooks require `sh` (for example Git Bash on Windows) and curl. Cursor uses the platform shell and curl; OpenCode uses its built-in JavaScript runtime. Prism installs no runtime packages.

Existing disabled settings stay disabled. A malformed file, duplicate key, conflicting `prism` entry or modified foreign plugin stops setup before any file changes. JSONC comments and unrelated YAML text survive edits; YAML constructs that cannot be safely spliced are rejected with a repair message. Removal deletes only owned entries/files and retains private backups beside configuration files.

## Reading the status

- **Configured**: the expected MCP entry or observer exists for this gateway. It does not prove the client loaded it or completed OAuth.
- **Connected**: an authenticated MCP connection is active.
- **Receiving**: an actual native event arrived after the relevant config/plugin files changed.
- **Off**: native observation or the harness's hooks are disabled.

No event is sent merely to make setup appear successful. A client that needs an update, a restart or hook trust can remain configured without receiving.

Claude Code, Codex, Cursor and OpenCode report proposals before execution. Goose reports its own pre-tool hook decision, including denied proposals; Antigravity reports after execution and includes a success/error result. Prism's watch-list match is separate from those outcomes. None of these observers changes the harness's permission decision or proves an action is safe.

Delivery is best effort. New command observers return within their two-second host timeout; OpenCode aborts delivery after 750 ms. Failures do not queue private tool inputs or change tool arguments. Payloads over 64 KiB are dropped. Stored records contain a bounded, redacted subject, not prompts, file contents or raw results. Unknown tools retain their names without storing arbitrary arguments. Missing working-directory evidence does not become a guessed workspace root.

Known MCP namespaces are matched against the current tool catalog to avoid counting the same routed call twice. Cursor and Antigravity events without enough provider identity are retained rather than guessing that they came through Prism; counts may include both observations in that case. Duplicate deliveries with the same harness, session and call ID are suppressed within a bounded recent-event window.

## Compatibility and verification

Checked September 8, 2026:

- **Cursor CLI 2026.09.02-c22c1a3, Linux:** reads generated global MCP configuration. The observer transport is tested with stopped Prism and missing curl. Desktop UI and actual Cursor tool-hook execution still need a native-client pass.
- **OpenCode 1.17.13, Linux:** reads generated global MCP configuration and discovers the plugin. A real CLI tool loop using a local model fixture executed shell, write and read calls; all arrived once with session/call/cwd identity and no file content. V2 uses a different recipe and is not covered by this adapter.
- **Goose 1.49.0, Linux:** a real CLI session using the generated plugin and a local model fixture executed a shell call. Exactly one `PreToolUseResult` arrived with the correct session, call, working directory and host decision. File-tool shapes were verified against its advertised catalog. The system's older 1.48.0 installation was left unchanged.
- **Antigravity:** targets the current CLI's global customization schema. Installed CLI 1.0.12 processes the generated MCP and hook configuration; command transport is tested separately. Its config validator does not deeply validate hook definitions. An authenticated CLI session and the older IDE 1.107.0 need separate verification.

These are local-machine integrations. Cursor cloud agents, remote OpenCode backends, Goose ACP providers and other nested/remote runtimes do not gain native coverage merely by sharing a config file. Antigravity is not a Gemini CLI alias. macOS and Windows installer fixtures do not substitute for native client execution tests.

Developer checks:

```sh
cargo test -p prism-desktop --lib harness --no-default-features
cargo test -p prism-desktop --lib installed_clients_read_generated_setup --no-default-features -- --ignored
pnpm --dir apps/desktop test
python3 apps/desktop/tests/opencode-live.py
```

The real-client checks use temporary homes and do not modify personal harness configuration. See the [Goose and Antigravity fixture notes](../apps/desktop/src-tauri/src/fixtures/harness_extra/SOURCES.md) for optional client checks and the verified Goose binary digest.

Format references: [Cursor hooks](https://cursor.com/docs/hooks), [Cursor MCP](https://cursor.com/docs/mcp), [OpenCode plugins](https://opencode.ai/docs/plugins/), [OpenCode MCP](https://opencode.ai/docs/mcp-servers/), [Goose 1.49 hooks](https://github.com/aaif-goose/goose/blob/v1.49.0/documentation/docs/guides/context-engineering/hooks.md), [Goose configuration](https://goose-docs.ai/docs/guides/config-files/), [Antigravity MCP](https://antigravity.google/docs/mcp), [Antigravity hooks](https://antigravity.google/docs/hooks).
