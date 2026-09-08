# Adapter format evidence

Verified 2026-09-08. All client probes use temporary homes and working directories.

## Goose

The target is **Goose 1.49 or newer**, pinned for implementation review to
[v1.49.0 / 71fc4be](https://github.com/aaif-goose/goose/tree/71fc4be1ed729e26b1dc0a4466abdd03be548a53).
The system executable reports 1.48.0 and was left unchanged. A separately
downloaded official 1.49.0 executable delivered a real `PreToolUseResult` through
the generated plugin in the isolated local-model fixture below. Installation
alone never establishes event evidence or host trust.

- [Configuration guide at v1.49.0](https://github.com/aaif-goose/goose/blob/v1.49.0/documentation/docs/guides/config-files.md): global Unix `~/.config/goose/config.yaml`; Windows `%APPDATA%/Block/goose/config/config.yaml`; `extensions` mapping with `streamable_http`, `uri`, `name`, and `enabled`.
- [Path implementation](https://github.com/aaif-goose/goose/blob/v1.49.0/crates/goose/src/config/paths.rs): absolute `GOOSE_PATH_ROOT` overrides the config and plugin roots. Otherwise plugins live in `~/.agents/plugins`.
- [Discovery implementation](https://github.com/aaif-goose/goose/blob/v1.49.0/crates/goose/src/plugins/discovery.rs): YAML `plugins[absolute plugin path].enabled` and `~/.config/goose/settings.json` `disabledPlugins` both affect loading. Neither is changed by the adapter. Project overrides remain host-owned.
- [Native hooks implementation](https://github.com/aaif-goose/goose/blob/v1.49.0/crates/goose/src/hooks/mod.rs): `hooks/hooks.json` event groups; commands invoke `sh` on every OS; the neutral `PreToolUseResult` event follows permission evaluation and includes the final allow/deny result. The raw Goose envelope is passed to core for strict validation.

YAML is parsed with `serde_yaml_ng`, never serialized wholesale. Only ordinary
block mapping ranges are spliced; the reparsed result must exactly match the
intended semantic change. Uneditable mappings, duplicate keys, and conflicts fail
before returning any edits. Comments, unrelated values, and disabled states survive.

## Antigravity

The verified surface is the **current Antigravity CLI global customization
format**, documented for CLI v1.1.25 and corroborated by paths/events in the
installed `agy` 1.0.12 executable. The separately installed legacy IDE executable
`/usr/bin/antigravity` reports 1.107.0. It was not validated as a consumer of this
global surface, so configuring this adapter must not claim legacy IDE coverage.

- [MCP documentation](https://antigravity.google/docs/mcp): `~/.gemini/config/mcp_config.json`, `mcpServers`, remote `serverUrl`, and `disabled`. Gemini CLI's settings and `url`/`httpUrl` schema are not aliases.
- [Hooks documentation](https://antigravity.google/docs/hooks): global `~/.gemini/config/hooks.json` maps names to event groups, with optional `enabled`. `PostToolUse` consumes native camelCase `toolCall`, `stepIdx`, and conversation metadata and returns `{}`. `PreToolUse` has permission-changing decisions, so this adapter uses only `PostToolUse`.
- [CLI plugins documentation](https://antigravity.google/docs/cli/plugins): plugins are a separate surface under `~/.gemini/antigravity-cli/plugins`. This adapter installs native global hooks directly and does not fabricate a plugin registration.

The helper wraps unchanged stdin in
`{"hook_event_name":"PostToolUse","payload":<native JSON>}`. Core validates the
wrapper and original fields. Both observers disable curl config/proxy use, limit
connection time to one second and HTTP time to one second, discard responses,
return `{}` even with the observer stopped, and never emit a permission decision.
Hook runtime timeout is two seconds; path quoting handles spaces and apostrophes.

Native Windows execution has not been validated. On Windows, both adapters require a `sh` executable from Git for Windows (Git Bash) on PATH; installation reports a concrete requirement when it is absent. No executable permission changes are made.

## Validation

`cargo test --locked -p prism-desktop --lib harness::extra --no-default-features`
passes 19 tests on Linux, with two additional client checks ignored by default.
Both optional client checks were also run successfully. The Antigravity check runs
`agy plugin validate` on the generated MCP and native hook documents in an
isolated fixture plugin, with a cleared environment and temporary HOME/XDG
directories. Agy 1.0.12 reported one MCP server and one hook processed. A negative
control rejected an invalid MCP map, but accepted an invalid hook definition;
its validation does **not** establish deep hook schema correctness, authenticated
global hook execution, or legacy IDE support. Other tests run the installed
shell/curl against a fixture HTTP server and verify raw Goose and wrapped
Antigravity payloads, neutral responses, offline/missing/noisy curl, and a stalled
gateway. Filesystem tests cover preservation, collisions, duplicate keys,
disabled settings, idempotent repair/removal, symlinks, and evidence invalidation.

The additional opt-in `installed_goose_149_emits_native_tool_observation` test
passed using the real installer to prepare the plugin and an explicit isolated
environment. The command `printf PRISM_GOOSE_OBSERVER_OK` actually executed,
its result reached the next local model request, and exactly one native
`PreToolUseResult` arrived with `decision: allow`, `policy_evaluated: false`,
correct `working_dir`, and nonempty session/call identifiers. No API account,
user credentials, cloud model, or global client upgrade was involved.

Captured evidence:

- [Native event](goose-1.49-native-event.json): actual field shape, with only the ephemeral session and working directory replaced by stable fixture values.
- [Advertised tool schemas](goose-1.49-tool-catalog.json): from the same model request, with descriptions/defaults/examples omitted. Native names include `shell(command, timeout_secs)`, `edit(path, before, after)`, `write(path, content)`, and `tree(path, depth)`. No `text_editor` was advertised. Only `shell` was executed; the file tools were verified as advertised.
- [Local model driver](goose-live.py): receives artifacts generated by `edits()`, binds loopback only, and replaces the fixture observer port. Subprocess timeout is 35 seconds.

Reproduce on Linux x86_64, from the repository root:

```sh
probe_dir="$(mktemp -d /tmp/prism-goose-149.XXXXXX)"
curl --fail --location --output "$probe_dir/goose.tar.gz" \
  https://github.com/aaif-goose/goose/releases/download/v1.49.0/goose-x86_64-unknown-linux-musl.tar.gz
printf '%s  %s\n' \
  3815ac601bd5fb44dbde6e63e69fb2756b79e51fd712174bc199c46aa829517b \
  "$probe_dir/goose.tar.gz" | sha256sum --check
tar -xzf "$probe_dir/goose.tar.gz" -C "$probe_dir" ./goose
PRISM_GOOSE_149_BIN="$probe_dir/goose" cargo test --locked -p prism-desktop \
  --lib harness::extra::tests::installed_goose_149_emits_native_tool_observation \
  --no-default-features -- --ignored --nocapture
```

The SHA-256 was checked against the asset digest in GitHub's official
[v1.49.0 release metadata](https://api.github.com/repos/aaif-goose/goose/releases/tags/v1.49.0).
The regular suite requires no downloaded client; the Goose live test is ignored
unless explicitly selected. The Antigravity check can be selected separately with
the `installed_antigravity_cli_validates_generated_native_configs` filter and
`-- --ignored`.
