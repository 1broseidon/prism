//! Native actions, phase 1: observe what an agent host does outside MCP.
//!
//! Claude Code posts every `PreToolUse` event to `/hooks/claude-code/{token}` on the loopback
//! listener. Each becomes an audit entry with a one-line redacted subject. The route always answers
//! `200 {}` in this phase, so the host's own permission flow is untouched. A short curated deny list
//! runs in shadow and marks entries it *would* have held; that count decides whether enforcement
//! ever ships. See `.brainfile/plans/native-actions.md`.

use std::collections::VecDeque;
use std::path::{Component, Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, Instant};

use axum::extract::{DefaultBodyLimit, Path as RoutePath, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::routing::post;
use axum::{Json, Router};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use tracing::warn;

use crate::gateway::Gateway;

/// Host ids; each is also the suffix of the host's agent record id (`host:<id>`).
pub const HOST_CLAUDE_CODE: &str = "claude-code";
pub const HOST_CODEX: &str = "codex";
pub const HOSTS: &[&str] = &[HOST_CLAUDE_CODE, HOST_CODEX];

/// The harness a self-declared OAuth client name belongs to, if Prism knows it. Matching is
/// anchored at the start and blind to case and punctuation, so "Claude Code", "claude-code" and
/// "Claude Code 2.1" all land on the same entry.
pub fn harness_for_client_name(name: &str) -> Option<&'static str> {
    let key: String = name
        .chars()
        .filter(|c| c.is_ascii_alphanumeric())
        .collect::<String>()
        .to_ascii_lowercase();
    if key.starts_with("claudecode") {
        Some(HOST_CLAUDE_CODE)
    } else if key.starts_with("codex") {
        Some(HOST_CODEX)
    } else {
        None
    }
}

pub fn harness_display_name(host: &str) -> &str {
    match host {
        HOST_CLAUDE_CODE => "Claude Code",
        HOST_CODEX => "Codex",
        other => other,
    }
}

/// The agent record id for a harness. One per harness on this machine; a harness reaching the
/// gateway from another machine gets its own record keyed by where it came from, so remote
/// copies are never folded into the local one.
pub fn harness_agent_id(host: &str, origin: Option<&str>) -> String {
    match origin {
        Some(origin) if !origin.is_empty() => format!("host:{host}@{origin}"),
        _ => format!("host:{host}"),
    }
}
pub const MAX_BODY_BYTES: usize = 64 * 1024;
const SUBJECT_MAX_CHARS: usize = 240;
const EVENTS_PER_MINUTE: usize = 1000;

/// The fields Prism reads from a Claude Code hook event. Everything else is ignored.
#[derive(Debug, Clone, Deserialize)]
pub struct HookEvent {
    #[serde(default)]
    pub session_id: Option<String>,
    #[serde(default)]
    pub cwd: Option<String>,
    #[serde(default)]
    pub hook_event_name: Option<String>,
    pub tool_name: String,
    #[serde(default)]
    pub tool_input: Value,
    #[serde(default)]
    pub agent_id: Option<String>,
    #[serde(default)]
    pub agent_type: Option<String>,
}

/// One row of the shadow deny list, as the panel shows it.
#[derive(Debug, Clone, Serialize)]
pub struct ShadowRule {
    pub id: &'static str,
    pub summary: &'static str,
}

/// What the panel needs for the Agents coverage label and the Settings "This week" line.
#[derive(Debug, Clone, Serialize)]
pub struct NativeStatus {
    pub observe_native: bool,
    /// Most recent event across hosts.
    pub last_event_at: Option<DateTime<Utc>>,
    pub actions_7d: usize,
    pub would_hold_7d: usize,
    pub by_reason: Vec<ReasonCount>,
    pub rules: Vec<ShadowRule>,
    pub hosts: Vec<HostStatus>,
    pub window: crate::audit::AuditWindow,
}

/// One host's share of the record.
#[derive(Debug, Clone, Serialize)]
pub struct HostStatus {
    pub host: String,
    pub hook_url: String,
    pub last_event_at: Option<DateTime<Utc>>,
    pub actions_7d: usize,
    /// Shadow reasons for this local host only, matching its agent-id drilldown.
    pub by_reason: Vec<ReasonCount>,
}

#[derive(Debug, Clone, Serialize)]
pub struct ReasonCount {
    pub reason: String,
    pub count: usize,
}

/// Sliding one-minute window so a runaway session cannot fill the log. Over the limit the route
/// still answers `200 {}` and drops the event: slowing Claude down is never the right outcome.
#[derive(Default)]
pub(crate) struct EventBudget {
    stamps: VecDeque<Instant>,
}

impl EventBudget {
    pub(crate) fn admit(&mut self) -> bool {
        let now = Instant::now();
        while self
            .stamps
            .front()
            .is_some_and(|t| now.duration_since(*t) > Duration::from_secs(60))
        {
            self.stamps.pop_front();
        }
        if self.stamps.len() >= EVENTS_PER_MINUTE {
            return false;
        }
        self.stamps.push_back(now);
        true
    }
}

pub(crate) fn router(gateway: Arc<Gateway>) -> Router {
    Router::new()
        .route("/hooks/{host}/{token}", post(host_hook))
        .layer(DefaultBodyLimit::max(MAX_BODY_BYTES))
        .with_state(gateway)
}

async fn host_hook(
    State(gateway): State<Arc<Gateway>>,
    RoutePath((host, token)): RoutePath<(String, String)>,
    body: Result<Json<HookEvent>, axum::extract::rejection::JsonRejection>,
) -> Response {
    let Some(host) = HOSTS.iter().copied().find(|h| *h == host) else {
        return (StatusCode::NOT_FOUND, "").into_response();
    };
    if !gateway.hook_token_matches(&token) {
        return (StatusCode::NOT_FOUND, "").into_response();
    }
    let Ok(Json(event)) = body else {
        return (StatusCode::BAD_REQUEST, "").into_response();
    };
    match gateway.record_native(host, event).await {
        Ok(()) => Json(serde_json::json!({})).into_response(),
        Err(crate::Error::NotFound(_)) => (StatusCode::FORBIDDEN, "").into_response(),
        Err(err) => {
            warn!(%err, "native event not recorded");
            Json(serde_json::json!({})).into_response()
        }
    }
}

/// Constant-time comparison so the URL token cannot be guessed byte by byte.
pub(crate) fn token_eq(a: &str, b: &str) -> bool {
    let (a, b) = (a.as_bytes(), b.as_bytes());
    if a.len() != b.len() {
        return false;
    }
    a.iter().zip(b).fold(0u8, |acc, (x, y)| acc | (x ^ y)) == 0
}

pub(crate) fn new_token() -> String {
    use base64::Engine;
    use rand::RngCore;
    let mut bytes = [0u8; 32];
    rand::rng().fill_bytes(&mut bytes);
    base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(bytes)
}

// ----- subject -------------------------------------------------------------------------------

fn str_field<'a>(input: &'a Value, key: &str) -> Option<&'a str> {
    input.get(key).and_then(Value::as_str)
}

/// The shell command as one line. Codex may send `command` as an argv array.
fn command_text(input: &Value) -> Option<String> {
    match input.get("command")? {
        Value::String(s) => Some(s.clone()),
        Value::Array(items) => Some(
            items
                .iter()
                .filter_map(Value::as_str)
                .collect::<Vec<_>>()
                .join(" "),
        ),
        _ => None,
    }
}

/// File paths named by an `apply_patch` body: the `*** Add/Update/Delete File:` headers only.
/// The patch content itself is never kept.
pub fn patch_paths(patch: &str) -> Vec<String> {
    patch
        .lines()
        .filter_map(|line| {
            let line = line.trim_start();
            ["*** Add File: ", "*** Update File: ", "*** Delete File: "]
                .iter()
                .find_map(|prefix| line.strip_prefix(prefix))
        })
        .map(|p| p.trim().to_string())
        .filter(|p| !p.is_empty())
        .collect()
}

/// The one line the record keeps about an action. Never the raw input.
pub fn subject(tool: &str, input: &Value, cwd: Option<&Path>, home: Option<&Path>) -> String {
    let text = match tool {
        "Bash" | "shell" | "local_shell" | "exec_command" => {
            command_text(input).map(|c| redact(&c)).unwrap_or_default()
        }
        "apply_patch" => command_text(input)
            .map(|body| {
                patch_paths(&body)
                    .iter()
                    .map(|p| display_path(p, cwd, home))
                    .collect::<Vec<_>>()
                    .join(", ")
            })
            .unwrap_or_default(),
        "Read" | "Write" | "Edit" | "MultiEdit" | "NotebookEdit" => str_field(input, "file_path")
            .or_else(|| str_field(input, "notebook_path"))
            .map(|p| display_path(p, cwd, home))
            .unwrap_or_default(),
        "Glob" | "Grep" => str_field(input, "path")
            .map(|p| display_path(p, cwd, home))
            .or_else(|| cwd.map(|c| display_path(&c.to_string_lossy(), None, home)))
            .unwrap_or_default(),
        "WebFetch" => str_field(input, "url").map(origin_of).unwrap_or_default(),
        "WebSearch" => "web search".to_string(),
        _ => String::new(),
    };
    let text = if text.is_empty() {
        tool.to_string()
    } else {
        text
    };
    cap(&text, SUBJECT_MAX_CHARS)
}

fn cap(text: &str, max: usize) -> String {
    if text.chars().count() <= max {
        return text.to_string();
    }
    let mut out: String = text.chars().take(max - 1).collect();
    out.push('…');
    out
}

fn display_path(path: &str, cwd: Option<&Path>, home: Option<&Path>) -> String {
    if let Some(cwd) = cwd {
        if let Ok(rest) = Path::new(path).strip_prefix(cwd) {
            let rest = rest.to_string_lossy();
            if !rest.is_empty() {
                return rest.into_owned();
            }
        }
    }
    if let Some(home) = home {
        if let Ok(rest) = Path::new(path).strip_prefix(home) {
            let rest = rest.to_string_lossy();
            return if rest.is_empty() {
                "~".into()
            } else {
                format!("~/{rest}")
            };
        }
    }
    path.to_string()
}

fn origin_of(url: &str) -> String {
    let (scheme, rest) = match url.split_once("://") {
        Some(parts) => parts,
        None => return "url".into(),
    };
    let host = rest.split(['/', '?', '#']).next().unwrap_or("");
    let host = host.rsplit('@').next().unwrap_or(host);
    format!("{scheme}://{host}")
}

/// Strip the shapes secrets take on a command line. Tokens after `bearer`, values of key-ish
/// assignments, URL userinfo, and long opaque runs are replaced. Everything else stays readable.
pub fn redact(command: &str) -> String {
    let mut out = Vec::new();
    let mut prev_bearer = false;
    for raw in command.split_whitespace() {
        // Keep closing quotes and punctuation so the line still reads as a command.
        let trail_at = raw.trim_end_matches(['\'', '"', ';', ')', ',', '`']).len();
        let (token, trail) = raw.split_at(trail_at);
        let lower = token.to_ascii_lowercase();
        let replaced = if prev_bearer {
            "***".to_string()
        } else if let Some((key, _)) = token.split_once('=') {
            let k = key.to_ascii_lowercase();
            if ["key", "token", "secret", "password", "passwd", "pwd"]
                .iter()
                .any(|needle| k.contains(needle))
            {
                format!("{key}=***")
            } else {
                token.to_string()
            }
        } else if let Some((scheme, rest)) = token.split_once("://") {
            match rest.split_once('@') {
                Some((userinfo, tail)) if userinfo.contains(':') => {
                    format!("{scheme}://***@{tail}")
                }
                _ => token.to_string(),
            }
        } else if token.len() >= 32
            && !token.starts_with(['/', '~', '.'])
            && token
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '/' | '=' | '-' | '_'))
        {
            "***".to_string()
        } else {
            token.to_string()
        };
        prev_bearer = lower == "bearer";
        out.push(format!("{replaced}{trail}"));
    }
    out.join(" ")
}

// ----- shadow deny list --------------------------------------------------------------------

pub mod shadow {
    use super::*;

    pub const RULES: &[ShadowRule] = &[
        ShadowRule {
            id: "rm_outside_cwd",
            summary: "Recursive delete of a path outside the working directory",
        },
        ShadowRule {
            id: "git_force",
            summary: "git push --force, reset --hard, or clean -f",
        },
        ShadowRule {
            id: "pipe_to_shell",
            summary: "curl or wget piped into a shell or interpreter",
        },
        ShadowRule {
            id: "sudo",
            summary: "A command run through sudo or doas",
        },
        ShadowRule {
            id: "secret_read",
            summary: "Reading SSH keys, cloud credentials, or a .env file",
        },
        ShadowRule {
            id: "sensitive_write",
            summary: "Writing under ~/.ssh, ~/.aws, ~/.config/gh, or a shell rc file",
        },
        ShadowRule {
            id: "write_outside_cwd",
            summary: "Writing a file outside the working directory",
        },
    ];

    /// The first rule the action trips, by id. `None` is the common case.
    pub fn evaluate(
        tool: &str,
        input: &Value,
        cwd: Option<&Path>,
        home: Option<&Path>,
    ) -> Option<&'static str> {
        match tool {
            "Bash" | "shell" | "local_shell" | "exec_command" => {
                let command = command_text(input)?;
                evaluate_command(&command, cwd, home)
            }
            "apply_patch" => {
                let body = command_text(input)?;
                let mut outside = false;
                for raw in patch_paths(&body) {
                    let path = resolve(&raw, cwd, home);
                    if is_sensitive_write(&path, home) {
                        return Some("sensitive_write");
                    }
                    outside |= is_outside_cwd(&path, cwd) && !is_temp(&path);
                }
                outside.then_some("write_outside_cwd")
            }
            "Read" => {
                let path = resolve(str_field(input, "file_path")?, cwd, home);
                is_secret_path(&path, home).then_some("secret_read")
            }
            "Write" | "Edit" | "MultiEdit" | "NotebookEdit" => {
                let raw =
                    str_field(input, "file_path").or_else(|| str_field(input, "notebook_path"))?;
                let path = resolve(raw, cwd, home);
                if is_sensitive_write(&path, home) {
                    Some("sensitive_write")
                } else if is_outside_cwd(&path, cwd) && !is_temp(&path) {
                    Some("write_outside_cwd")
                } else {
                    None
                }
            }
            _ => None,
        }
    }

    fn evaluate_command(
        command: &str,
        cwd: Option<&Path>,
        home: Option<&Path>,
    ) -> Option<&'static str> {
        let segments: Vec<Vec<&str>> = split_segments(command)
            .into_iter()
            .map(|s| s.split_whitespace().collect())
            .collect();
        for (i, seg) in segments.iter().enumerate() {
            let Some(&head) = seg.first() else { continue };
            let bin = basename(head);
            if bin == "sudo" || bin == "doas" {
                return Some("sudo");
            }
            if bin == "rm"
                && seg[1..].iter().any(|t| {
                    t.starts_with('-') && !t.starts_with("--") && t.contains(['r', 'R'])
                        || *t == "--recursive"
                })
            {
                for arg in seg[1..].iter().filter(|t| !t.starts_with('-')) {
                    let path = resolve(arg, cwd, home);
                    if (is_outside_cwd(&path, cwd) && !is_temp(&path))
                        || cwd.is_some_and(|c| path == c)
                    {
                        return Some("rm_outside_cwd");
                    }
                }
            }
            if bin == "git" {
                let sub = seg.get(1).copied().unwrap_or("");
                let flags = &seg[2..];
                let forced = |names: &[&str]| flags.iter().any(|f| names.contains(f));
                if (sub == "push" && forced(&["--force", "-f"]))
                    || (sub == "reset" && forced(&["--hard"]))
                    || (sub == "clean"
                        && flags.iter().any(|f| {
                            f.starts_with('-') && !f.starts_with("--") && f.contains('f')
                                || *f == "--force"
                        }))
                {
                    return Some("git_force");
                }
            }
            if (bin == "curl" || bin == "wget")
                && segments
                    .get(i + 1)
                    .and_then(|next| next.first())
                    .is_some_and(|n| {
                        matches!(
                            basename(n),
                            "sh" | "bash" | "zsh" | "fish" | "python" | "python3" | "node" | "perl"
                        )
                    })
            {
                return Some("pipe_to_shell");
            }
            if matches!(bin, "cat" | "less" | "more" | "head" | "tail" | "bat") {
                for arg in seg[1..].iter().filter(|t| !t.starts_with('-')) {
                    if is_secret_path(&resolve(arg, cwd, home), home) {
                        return Some("secret_read");
                    }
                }
            }
        }
        None
    }

    /// Split on the shell operators that start a new command, in order. Pipes count as a
    /// boundary too, which is what `pipe_to_shell` looks across.
    fn split_segments(command: &str) -> Vec<&str> {
        let mut out = Vec::new();
        let mut start = 0;
        let bytes = command.as_bytes();
        let mut i = 0;
        while i < bytes.len() {
            let two = &command[i..(i + 2).min(command.len())];
            let (cut, width) = if two == "&&" || two == "||" {
                (true, 2)
            } else if matches!(bytes[i], b';' | b'|' | b'\n') {
                (true, 1)
            } else {
                (false, 1)
            };
            if cut {
                out.push(&command[start..i]);
                i += width;
                start = i;
            } else {
                i += 1;
            }
        }
        out.push(&command[start..]);
        out.into_iter()
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .collect()
    }

    fn basename(token: &str) -> &str {
        token.rsplit('/').next().unwrap_or(token)
    }

    /// Lexical resolution: `~`, relative to `cwd`, then `.` and `..` folded. No filesystem access.
    pub(super) fn resolve(raw: &str, cwd: Option<&Path>, home: Option<&Path>) -> PathBuf {
        let raw = raw.trim_matches(|c| c == '"' || c == '\'');
        let expanded: PathBuf = if raw == "~" {
            home.map(Path::to_path_buf)
                .unwrap_or_else(|| PathBuf::from(raw))
        } else if let Some(rest) = raw.strip_prefix("~/") {
            home.map(|h| h.join(rest))
                .unwrap_or_else(|| PathBuf::from(raw))
        } else {
            PathBuf::from(raw)
        };
        let joined = if expanded.is_absolute() {
            expanded
        } else if let Some(cwd) = cwd {
            cwd.join(expanded)
        } else {
            expanded
        };
        let mut out = PathBuf::new();
        for component in joined.components() {
            match component {
                Component::CurDir => {}
                Component::ParentDir => {
                    out.pop();
                }
                other => out.push(other.as_os_str()),
            }
        }
        out
    }

    fn is_outside_cwd(path: &Path, cwd: Option<&Path>) -> bool {
        match cwd {
            Some(cwd) => !path.starts_with(cwd),
            None => false,
        }
    }

    fn is_temp(path: &Path) -> bool {
        path.starts_with(std::env::temp_dir())
            || path.starts_with("/tmp")
            || path.starts_with("/var/tmp")
    }

    fn under_home(path: &Path, home: Option<&Path>, rel: &str) -> bool {
        home.is_some_and(|h| path.starts_with(h.join(rel)))
    }

    fn is_secret_path(path: &Path, home: Option<&Path>) -> bool {
        let name = path.file_name().and_then(|n| n.to_str()).unwrap_or("");
        if under_home(path, home, ".ssh") {
            // known_hosts, config and public keys are not secrets; everything else in there is.
            return !(matches!(name, "known_hosts" | "config" | "authorized_keys")
                || name.ends_with(".pub"));
        }
        if under_home(path, home, ".aws") {
            return true;
        }
        if let Some(home) = home {
            if let Ok(rest) = path.strip_prefix(home.join(".config")) {
                let mut parts = rest.components();
                let _app = parts.next();
                if parts
                    .next()
                    .and_then(|c| c.as_os_str().to_str())
                    .is_some_and(|n| n.starts_with("credentials"))
                {
                    return true;
                }
            }
        }
        (name == ".env" || name.starts_with(".env."))
            && !name.ends_with(".example")
            && !name.ends_with(".sample")
            && !name.ends_with(".template")
    }

    fn is_sensitive_write(path: &Path, home: Option<&Path>) -> bool {
        if under_home(path, home, ".ssh")
            || under_home(path, home, ".aws")
            || under_home(path, home, ".config/gh")
        {
            return true;
        }
        let name = path.file_name().and_then(|n| n.to_str()).unwrap_or("");
        home.is_some_and(|h| path.parent() == Some(h))
            && matches!(
                name,
                ".bashrc"
                    | ".zshrc"
                    | ".profile"
                    | ".zprofile"
                    | ".bash_profile"
                    | ".zshenv"
                    | ".bash_login"
            )
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn home() -> PathBuf {
        PathBuf::from("/home/u")
    }
    fn cwd() -> PathBuf {
        PathBuf::from("/home/u/proj")
    }
    fn bash(cmd: &str) -> Option<&'static str> {
        shadow::evaluate(
            "Bash",
            &serde_json::json!({ "command": cmd }),
            Some(&cwd()),
            Some(&home()),
        )
    }
    fn file(tool: &str, path: &str) -> Option<&'static str> {
        shadow::evaluate(
            tool,
            &serde_json::json!({ "file_path": path }),
            Some(&cwd()),
            Some(&home()),
        )
    }

    #[test]
    fn subject_per_tool() {
        let s = |tool: &str, input: Value| subject(tool, &input, Some(&cwd()), Some(&home()));
        assert_eq!(
            s("Bash", serde_json::json!({"command": "npm test"})),
            "npm test"
        );
        assert_eq!(
            s(
                "Write",
                serde_json::json!({"file_path": "/home/u/proj/a.rs"})
            ),
            "a.rs"
        );
        assert_eq!(
            s("Read", serde_json::json!({"file_path": "/etc/hosts"})),
            "/etc/hosts"
        );
        assert_eq!(
            s("Grep", serde_json::json!({"pattern": "AKIA[0-9]+"})),
            "~/proj"
        );
        assert_eq!(
            s(
                "WebFetch",
                serde_json::json!({"url": "https://u:p@api.example.com/v1?key=1"})
            ),
            "https://api.example.com"
        );
        assert_eq!(
            s("WebSearch", serde_json::json!({"query": "how to leak"})),
            "web search"
        );
        assert_eq!(
            s("mcp__prism__ketch__x", serde_json::json!({"a": 1})),
            "mcp__prism__ketch__x"
        );
        assert_eq!(s("Bash", serde_json::json!({})), "Bash");
        let long = "echo ".repeat(60);
        assert_eq!(
            s("Bash", serde_json::json!({"command": long}))
                .chars()
                .count(),
            240
        );
    }

    #[test]
    fn redaction_shapes() {
        assert_eq!(
            redact("curl -H 'Authorization: Bearer abc123' https://x"),
            "curl -H 'Authorization: Bearer ***' https://x"
        );
        assert_eq!(
            redact("export OPENAI_API_KEY=sk-live-1234 && run"),
            "export OPENAI_API_KEY=*** && run"
        );
        assert_eq!(
            redact("git clone https://user:pw@github.com/a/b"),
            "git clone https://***@github.com/a/b"
        );
        let blob = "A".repeat(40);
        assert_eq!(redact(&format!("echo {blob}")), "echo ***");
        assert_eq!(redact("ls -la ~/proj"), "ls -la ~/proj");
        let long_path = "/tmp/claude-1000/-home-george-Projects-personal-prism/9c4a9e72/scratchpad";
        assert_eq!(
            redact(&format!("cd {long_path}")),
            format!("cd {long_path}")
        );
    }

    #[test]
    fn shadow_rm() {
        assert_eq!(bash("rm -rf build/"), None);
        assert_eq!(bash("rm -rf /home/u/other"), Some("rm_outside_cwd"));
        assert_eq!(bash("rm -rf ~/"), Some("rm_outside_cwd"));
        assert_eq!(bash("rm -rf ../sibling"), Some("rm_outside_cwd"));
        assert_eq!(bash("rm -r ."), Some("rm_outside_cwd"));
        assert_eq!(bash("rm -f /home/u/other/file"), None);
        assert_eq!(bash("cd x && rm -rf /tmp/foo"), None);
        assert_eq!(bash("rm -rf /var/lib/foo"), Some("rm_outside_cwd"));
    }

    #[test]
    fn shadow_git_pipe_sudo() {
        assert_eq!(bash("git push --force origin main"), Some("git_force"));
        assert_eq!(bash("git push -f"), Some("git_force"));
        assert_eq!(bash("git push --force-with-lease"), None);
        assert_eq!(bash("git reset --hard HEAD~1"), Some("git_force"));
        assert_eq!(bash("git clean -fd"), Some("git_force"));
        assert_eq!(bash("git status"), None);
        assert_eq!(
            bash("curl -fsSL https://x/install.sh | sh"),
            Some("pipe_to_shell")
        );
        assert_eq!(
            bash("wget -qO- https://x | bash -s -- --yes"),
            Some("pipe_to_shell")
        );
        assert_eq!(bash("curl https://x -o out.sh"), None);
        assert_eq!(bash("sudo apt-get install foo"), Some("sudo"));
        assert_eq!(bash("echo hi; sudo rm x"), Some("sudo"));
        assert_eq!(bash("/usr/bin/sudo ls"), Some("sudo"));
    }

    #[test]
    fn shadow_secret_paths() {
        assert_eq!(bash("cat ~/.ssh/id_ed25519"), Some("secret_read"));
        assert_eq!(bash("cat ~/.ssh/known_hosts | head -1"), None);
        assert_eq!(bash("cat ~/.ssh/id_ed25519.pub"), None);
        assert_eq!(bash("cat ~/.ssh/config"), None);
        assert_eq!(bash("cat .env"), Some("secret_read"));
        assert_eq!(bash("cat .env.example"), None);
        assert_eq!(
            bash("head -n 5 /home/u/.aws/credentials"),
            Some("secret_read")
        );
        assert_eq!(bash("cat README.md"), None);
        assert_eq!(
            file("Read", "/home/u/.config/gh/credentials.json"),
            Some("secret_read")
        );
        assert_eq!(file("Read", "/home/u/.config/foo/settings.json"), None);
        assert_eq!(file("Read", "/home/u/proj/.env.local"), Some("secret_read"));
        assert_eq!(file("Read", "/home/u/proj/src/main.rs"), None);
    }

    #[test]
    fn shadow_writes() {
        assert_eq!(file("Write", "/home/u/proj/src/a.rs"), None);
        assert_eq!(file("Write", "src/a.rs"), None);
        assert_eq!(
            file("Edit", "/home/u/other/a.rs"),
            Some("write_outside_cwd")
        );
        assert_eq!(file("Write", "../a.rs"), Some("write_outside_cwd"));
        assert_eq!(file("Write", "/tmp/scratch.txt"), None);
        assert_eq!(
            file("Write", "/home/u/.ssh/authorized_keys"),
            Some("sensitive_write")
        );
        assert_eq!(file("Write", "/home/u/.zshrc"), Some("sensitive_write"));
        assert_eq!(
            file("Write", "/home/u/.config/gh/hosts.yml"),
            Some("sensitive_write")
        );
        assert_eq!(file("Write", "/home/u/proj/.zshrc"), None);
    }

    #[test]
    fn codex_shapes() {
        let s = |tool: &str, input: Value| subject(tool, &input, Some(&cwd()), Some(&home()));
        let e =
            |tool: &str, input: Value| shadow::evaluate(tool, &input, Some(&cwd()), Some(&home()));
        assert_eq!(
            s(
                "Bash",
                serde_json::json!({"command": ["bash", "-lc", "ls -la"]})
            ),
            "bash -lc ls -la"
        );
        let patch = "*** Begin Patch\n*** Update File: src/main.rs\n@@\n-a\n+b\n*** Add File: /home/u/.zshrc\n+export X=1\n*** End Patch";
        assert_eq!(
            s("apply_patch", serde_json::json!({"command": patch})),
            "src/main.rs, ~/.zshrc"
        );
        let absolute = format!(
            "*** Begin Patch\n*** Update File: {}/notes.txt\n*** End Patch",
            cwd().display()
        );
        assert_eq!(
            s("apply_patch", serde_json::json!({"command": absolute})),
            "notes.txt"
        );
        let inside_cwd = serde_json::json!({"file_path": cwd().join("src/x.rs")});
        assert_eq!(s("Write", inside_cwd), "src/x.rs");
        assert_eq!(
            e("apply_patch", serde_json::json!({"command": patch})),
            Some("sensitive_write")
        );
        let outside = "*** Begin Patch\n*** Update File: ../other/x.rs\n*** End Patch";
        assert_eq!(
            e("apply_patch", serde_json::json!({"command": outside})),
            Some("write_outside_cwd")
        );
        let inside = "*** Begin Patch\n*** Update File: src/x.rs\n*** End Patch";
        assert_eq!(
            e("apply_patch", serde_json::json!({"command": inside})),
            None
        );
        assert_eq!(
            e("Bash", serde_json::json!({"command": ["sudo", "ls"]})),
            Some("sudo")
        );
    }

    #[test]
    fn budget_and_token() {
        let mut b = EventBudget::default();
        assert!((0..1000).all(|_| b.admit()));
        assert!(!b.admit());
        assert!(token_eq("abc", "abc"));
        assert!(!token_eq("abc", "abd"));
        assert!(!token_eq("abc", "ab"));
        assert_ne!(new_token(), new_token());
        assert_eq!(new_token().len(), 43);
    }
}
