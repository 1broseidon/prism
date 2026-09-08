//! Wire contracts checked 2026-09-08:
//! - https://cursor.com/docs/hooks (preToolUse; installed CLI 2026.09.02 also inspected)
//! - https://github.com/anomalyco/opencode/blob/dev/packages/plugin/src/index.ts
//!   and packages/opencode/src/{tool,mcp} (stable V1 adapter, not the V2 plugin API)
//! - https://github.com/aaif-goose/goose/tree/v1.49.0/crates/goose/src
//!   hooks and agents/platform_extensions/developer (PreToolUseResult requires a supporting host)
//! - https://antigravity.google/docs/hooks (PostToolUse only; original JSON wrapped by our helper)
//!
//! These schemas establish parsing compatibility, not live CLI/desktop execution coverage.

use super::*;
use crate::audit::AuditVerdict;
use serde_json::json;

const ID_MAX_BYTES: usize = 256;
const PATH_MAX_BYTES: usize = 4096;
const PATCH_MAX_PATHS: usize = 512;

// No error contains any input, including serde's potentially sensitive error context.
#[derive(Debug)]
pub(crate) struct InvalidHook;
type Parsed<T> = std::result::Result<T, InvalidHook>;

pub(crate) struct Observation {
    pub event: HookEvent,
    pub call_id: Option<String>,
    pub normalized_tool: Option<&'static str>,
    pub verdict: AuditVerdict,
    mcp_server: Option<String>,
}

impl Observation {
    pub fn policy_tool(&self) -> &str {
        self.normalized_tool.unwrap_or(&self.event.tool_name)
    }

    /// Compare the entire tool name with the name actually advertised by Prism. In particular,
    /// `mcp__elsewhere__server__tool` must not disappear merely because its suffix matches.
    pub fn matches_prism_tool(&self, host: &str, server: &str, tool: &str) -> bool {
        let advertised = format!("{server}__{tool}");
        let expected = match host {
            HOST_CLAUDE_CODE | HOST_CODEX => format!("mcp__prism__{advertised}"),
            HOST_CURSOR => {
                if self.mcp_server.as_deref() != Some("prism") {
                    return false;
                }
                // Cursor's generic hook supplies MCP:<server-advertised tool name>. It does not
                // include the MCP provider key (unlike beforeMCPExecution). Preserve the row
                // when that evidence is absent, even for an exact catalog match: a different
                // provider can advertise the same tool. No second lifecycle is subscribed to
                // fill this gap, as that would count native calls twice.
                format!("MCP:{advertised}")
            }
            HOST_OPENCODE => {
                // McpCatalog.toolName: sanitize(client) + '_' + sanitize(server tool name).
                let sanitized: String = advertised
                    .chars()
                    .map(|c| {
                        if c.is_ascii_alphanumeric() || matches!(c, '_' | '-') {
                            c
                        } else {
                            '_'
                        }
                    })
                    .collect();
                format!("prism_{sanitized}")
            }
            HOST_GOOSE => format!("prism__{advertised}"),
            // No verified MCP namespace in the Antigravity hook contract. Preserve observations
            // until an exact identity can be established rather than guessing from a suffix.
            _ => return false,
        };
        self.event.tool_name == expected
    }
}

/// Select exactly one lifecycle per harness. An unknown lifecycle never creates an agent,
/// consumes the event budget, or advances its coverage timestamp.
pub(crate) fn parse_hook(host: &str, body: Value) -> Parsed<Option<Observation>> {
    if !body.is_object() || !HOSTS.contains(&host) {
        return Err(InvalidHook);
    }
    let (payload, session_key, cwd_key, call_key) = match host {
        HOST_CLAUDE_CODE | HOST_CODEX => {
            // Retain compatibility with older Prism helpers which omitted the event marker.
            if body.get("hook_event_name").is_some()
                && string(&body, "hook_event_name")? != "PreToolUse"
            {
                return Ok(None);
            }
            if body.get("event").is_some() || body.get("conversation_id").is_some() {
                return Err(InvalidHook);
            }
            (&body, "session_id", "cwd", "tool_use_id")
        }
        HOST_CURSOR => {
            if string(&body, "hook_event_name")? != "preToolUse" {
                return Ok(None);
            }
            string(&body, "conversation_id")?;
            workspace_paths(&body, "workspace_roots")?;
            (&body, "conversation_id", "cwd", "tool_use_id")
        }
        HOST_OPENCODE => {
            if string(&body, "hook_event_name")? != "PreToolUse" {
                return Ok(None);
            }
            // This is our V1 tool.execute.before projection, not a raw plugin or Claude event.
            required_id(&body, "session_id")?;
            required_id(&body, "tool_use_id")?;
            directory(&body, "cwd")?.ok_or(InvalidHook)?;
            if body.get("event").is_some() || body.get("conversation_id").is_some() {
                return Err(InvalidHook);
            }
            (&body, "session_id", "cwd", "tool_use_id")
        }
        HOST_GOOSE => {
            if string(&body, "event")? != "PreToolUseResult" {
                return Ok(None);
            }
            // .agents/plugins is shared across runtimes. Neither the install location nor a
            // Claude-shaped PreToolUse payload is evidence of a Goose observation.
            if body.get("hook_event_name").is_some()
                || body.get("conversation_id").is_some()
                || body.get("toolCall").is_some()
            {
                return Err(InvalidHook);
            }
            required_id(&body, "session_id")?;
            required_id(&body, "tool_call_id")?;
            directory(&body, "working_dir")?.ok_or(InvalidHook)?;
            if !matches!(string(&body, "decision")?, "allow" | "deny") {
                return Err(InvalidHook);
            }
            (&body, "session_id", "working_dir", "tool_call_id")
        }
        HOST_ANTIGRAVITY => {
            // PreToolUse requires permission output. The neutral observer installs PostToolUse
            // and wraps stdin because native pre/post payloads have no lifecycle discriminator.
            if string(&body, "hook_event_name")? != "PostToolUse" {
                return Ok(None);
            }
            let payload = body
                .get("payload")
                .filter(|v| v.is_object())
                .ok_or(InvalidHook)?;
            required_id(payload, "conversationId")?;
            payload
                .get("stepIdx")
                .and_then(Value::as_u64)
                .ok_or(InvalidHook)?;
            workspace_paths(payload, "workspacePaths")?;
            optional_string(payload, "error")?;
            (&body["payload"], "conversationId", "", "")
        }
        _ => return Err(InvalidHook),
    };
    let (tool_name, input) = if host == HOST_ANTIGRAVITY {
        let call = payload
            .get("toolCall")
            .filter(|v| v.is_object())
            .ok_or(InvalidHook)?;
        (string(call, "name")?, call.get("args").ok_or(InvalidHook)?)
    } else {
        (
            string(payload, "tool_name")?,
            payload.get("tool_input").unwrap_or(&Value::Null),
        )
    };
    identifier(tool_name)?;
    let legacy = matches!(host, HOST_CLAUDE_CODE | HOST_CODEX);
    if !(input.is_object() || legacy && input.is_null()) {
        return Err(InvalidHook);
    }
    let mut cwd = directory(payload, cwd_key)?;
    // An explicitly selected tool working directory is stronger evidence than session cwd.
    let tool_cwd_key = match (host, tool_name) {
        (HOST_CURSOR, "Shell") => Some("working_directory"),
        (HOST_OPENCODE, "bash") => Some("workdir"),
        (HOST_CODEX, "Bash" | "shell" | "local_shell" | "exec_command") => Some("workdir"),
        (HOST_ANTIGRAVITY, "run_command") => Some("Cwd"),
        _ => None,
    };
    if let Some(key) = tool_cwd_key {
        if let Some(dir) = directory(input, key)? {
            cwd = Some(dir);
        }
    }
    // workspace_roots/workspacePaths are deliberately never used as cwd, even with one entry.
    let session_id = optional_id(payload, session_key)?;
    let call_id = if host == HOST_ANTIGRAVITY {
        Some(payload["stepIdx"].as_u64().ok_or(InvalidHook)?.to_string())
    } else {
        optional_id(payload, call_key)?
    };
    let verdict = if host == HOST_GOOSE && payload["decision"] == "deny" {
        AuditVerdict::Denied
    } else if host == HOST_ANTIGRAVITY
        && optional_string(payload, "error")?.is_some_and(|s| !s.is_empty())
    {
        AuditVerdict::Error
    } else {
        AuditVerdict::Allowed
    };
    let (normalized_tool, tool_input) = normalize(host, tool_name, input)?;
    Ok(Some(Observation {
        event: HookEvent {
            session_id,
            cwd,
            hook_event_name: None,
            tool_name: tool_name.to_owned(),
            tool_input,
            agent_id: None,
            agent_type: if legacy {
                optional_id(payload, "agent_type")?
            } else {
                None
            },
        },
        normalized_tool,
        call_id,
        verdict,
        mcp_server: if host == HOST_CURSOR {
            optional_id(payload, "mcp_server_name")?
        } else {
            None
        },
    }))
}

fn string<'a>(value: &'a Value, key: &str) -> Parsed<&'a str> {
    value.get(key).and_then(Value::as_str).ok_or(InvalidHook)
}

fn optional_string<'a>(value: &'a Value, key: &str) -> Parsed<Option<&'a str>> {
    match value.get(key) {
        None | Some(Value::Null) => Ok(None),
        Some(Value::String(s)) => Ok(Some(s)),
        _ => Err(InvalidHook),
    }
}

fn identifier(s: &str) -> Parsed<()> {
    if s.is_empty()
        || s.len() > ID_MAX_BYTES
        || !s
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"_.:-".contains(&b))
    {
        return Err(InvalidHook);
    }
    Ok(())
}

fn optional_id(value: &Value, key: &str) -> Parsed<Option<String>> {
    optional_string(value, key)?
        .filter(|s| !s.is_empty())
        .map(|s| {
            identifier(s)?;
            Ok(s.to_owned())
        })
        .transpose()
}

fn required_id(value: &Value, key: &str) -> Parsed<String> {
    optional_id(value, key)?.ok_or(InvalidHook)
}

fn path(s: &str) -> Parsed<&str> {
    if s.trim().is_empty() || s.len() > PATH_MAX_BYTES || s.chars().any(char::is_control) {
        return Err(InvalidHook);
    }
    Ok(s)
}

fn directory(value: &Value, key: &str) -> Parsed<Option<String>> {
    optional_string(value, key)?
        .filter(|s| !s.is_empty())
        .map(|s| {
            path(s)?;
            // A harness on a POSIX host (WSL, a container) may post to a Windows Prism. Its
            // paths are absolute where they came from, and stay spelled the way they arrived.
            let posix = s.starts_with('/');
            if !posix && !Path::new(s).is_absolute() {
                return Err(InvalidHook);
            }
            let resolved = shadow::resolve(s, None, None)
                .to_string_lossy()
                .into_owned();
            Ok(if posix {
                resolved.replace('\\', "/")
            } else {
                resolved
            })
        })
        .transpose()
}

fn workspace_paths(value: &Value, key: &str) -> Parsed<()> {
    if let Some(paths) = value.get(key) {
        let paths = paths
            .as_array()
            .filter(|paths| paths.len() <= 64)
            .ok_or(InvalidHook)?;
        for p in paths {
            path(p.as_str().ok_or(InvalidHook)?)?;
        }
    }
    Ok(())
}

fn normalize(host: &str, tool: &str, input: &Value) -> Parsed<(Option<&'static str>, Value)> {
    // Map verified argument names only. A third-party extension ending in __shell is not the
    // developer shell. Unrecognized tools retain only their name, never their input or output.
    let mapping = match (host, tool) {
        (HOST_CLAUDE_CODE | HOST_CODEX, "Bash" | "shell" | "local_shell" | "exec_command") => {
            Some(("Bash", "command"))
        }
        (HOST_CLAUDE_CODE | HOST_CODEX, "apply_patch") => Some(("apply_patch", "command")),
        (HOST_CLAUDE_CODE | HOST_CODEX, "Read") => Some(("Read", "file_path")),
        (HOST_CLAUDE_CODE | HOST_CODEX, "Write" | "Edit" | "MultiEdit") => {
            Some(("Write", "file_path"))
        }
        (HOST_CLAUDE_CODE | HOST_CODEX, "NotebookEdit") => Some(("Write", "notebook_path")),
        (HOST_CLAUDE_CODE | HOST_CODEX, "Glob" | "Grep") => Some(("Grep", "path")),
        (HOST_CLAUDE_CODE | HOST_CODEX, "WebFetch") => Some(("WebFetch", "url")),
        (HOST_CLAUDE_CODE | HOST_CODEX, "WebSearch") => Some(("WebSearch", "")),
        (HOST_CURSOR, "Shell") => Some(("Bash", "command")),
        (HOST_CURSOR, "Read") => Some(("Read", "file_path")),
        (HOST_CURSOR, "Write" | "Delete") => Some(("Write", "file_path")),
        (HOST_CURSOR, "Grep") => Some(("Grep", "path")),
        (HOST_OPENCODE, "bash") => Some(("Bash", "command")),
        (HOST_OPENCODE, "read") => Some(("Read", "filePath")),
        (HOST_OPENCODE, "write" | "edit") => Some(("Write", "filePath")),
        (HOST_OPENCODE, "apply_patch") => Some(("apply_patch", "patchText")),
        (HOST_OPENCODE, "glob" | "grep") => Some(("Grep", "path")),
        (HOST_OPENCODE, "webfetch") => Some(("WebFetch", "url")),
        (HOST_OPENCODE, "websearch") => Some(("WebSearch", "")),
        // Goose 1.49's built-in developer registry uses unprefixed names. A real local-model
        // probe confirmed emitted `shell` and advertised write/edit/tree argument schemas.
        // Native fixtures retain that evidence; file tools were not executed by the probe.
        (HOST_GOOSE, "shell") => Some(("Bash", "command")),
        (HOST_GOOSE, "write" | "edit") => Some(("Write", "path")),
        (HOST_GOOSE, "tree") => Some(("Grep", "path")),
        // Keep the exact extension-prefixed forms supported by Goose's extension manager;
        // never classify arbitrary third-party names merely because they end in __shell/edit.
        (HOST_GOOSE, "developer__shell") => Some(("Bash", "command")),
        (HOST_GOOSE, "developer__write" | "developer__edit") => Some(("Write", "path")),
        (HOST_GOOSE, "developer__tree") => Some(("Grep", "path")),
        (HOST_ANTIGRAVITY, "run_command") => Some(("Bash", "CommandLine")),
        (HOST_ANTIGRAVITY, "view_file") => Some(("Read", "AbsolutePath")),
        (
            HOST_ANTIGRAVITY,
            "write_to_file" | "replace_file_content" | "multi_replace_file_content",
        ) => Some(("Write", "TargetFile")),
        (HOST_ANTIGRAVITY, "list_dir") => Some(("Grep", "DirectoryPath")),
        (HOST_ANTIGRAVITY, "find_by_name") => Some(("Grep", "SearchDirectory")),
        (HOST_ANTIGRAVITY, "grep_search") => Some(("Grep", "SearchPath")),
        (HOST_ANTIGRAVITY, "read_url_content") => Some(("WebFetch", "Url")),
        (HOST_ANTIGRAVITY, "search_web") => Some(("WebSearch", "")),
        _ => None,
    };
    let Some((kind, key)) = mapping else {
        return Ok((None, json!({})));
    };
    let legacy = matches!(host, HOST_CLAUDE_CODE | HOST_CODEX);
    let data = match kind {
        "Bash" => {
            let command = if legacy {
                command_text(input)
            } else {
                optional_string(input, key)?.map(str::to_owned)
            };
            match command {
                Some(command) if command.len() <= MAX_BODY_BYTES => json!({"command": command}),
                None if legacy => json!({}),
                _ => return Err(InvalidHook),
            }
        }
        "apply_patch" => {
            let patch = optional_string(input, key)?;
            if !legacy && patch.is_none() {
                return Err(InvalidHook);
            }
            let paths = patch_paths(patch.unwrap_or_default());
            if paths.len() > PATCH_MAX_PATHS {
                return Err(InvalidHook);
            }
            for p in &paths {
                path(p)?;
            }
            json!({"paths": paths})
        }
        "Read" | "Write" => match optional_string(input, key)? {
            Some(p) => json!({"file_path": path(p)?}),
            None if legacy => json!({}),
            None => return Err(InvalidHook),
        },
        "Grep" => match optional_string(input, key)? {
            Some(p) => json!({"path": path(p)?}),
            None => json!({}),
        },
        "WebFetch" => match optional_string(input, key)? {
            Some(url) => json!({"url": origin_of(url)}),
            None if legacy => json!({}),
            None => return Err(InvalidHook),
        },
        _ => json!({}),
    };
    Ok((Some(kind), data))
}
