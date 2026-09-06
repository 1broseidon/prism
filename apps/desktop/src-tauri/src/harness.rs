//! Global client configuration. A write never implies OAuth consent or hook trust.
use serde::Serialize;
use serde_json::{json, Value};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::Mutex,
    time::SystemTime,
};
use toml_edit::{value, DocumentMut, Item, Table};

#[path = "harness_state.rs"]
mod hook_state;

static CONFIG_WRITE: Mutex<()> = Mutex::new(());

#[derive(Clone)]
pub struct Paths {
    pub mcp: PathBuf,
    pub hooks: PathBuf,
    pub codex: bool,
}

impl Paths {
    pub fn for_host(host: &str) -> Result<Self, String> {
        let home = super::home_path()?;
        match host {
            "claude-code" => {
                let dir = std::env::var_os("CLAUDE_CONFIG_DIR").map(PathBuf::from);
                Ok(Self {
                    mcp: dir
                        .as_ref()
                        .map(|p| p.join(".claude.json"))
                        .unwrap_or_else(|| home.join(".claude.json")),
                    hooks: dir
                        .unwrap_or_else(|| home.join(".claude"))
                        .join("settings.json"),
                    codex: false,
                })
            }
            "codex" => {
                let dir = std::env::var_os("CODEX_HOME")
                    .map(PathBuf::from)
                    .unwrap_or_else(|| home.join(".codex"));
                Ok(Self {
                    mcp: dir.join("config.toml"),
                    hooks: dir.join("hooks.json"),
                    codex: true,
                })
            }
            _ => Err("Unknown harness".into()),
        }
    }
}

#[derive(Serialize)]
pub struct Setup {
    pub host: String,
    pub settings_path: String,
    pub mcp_path: String,
    pub mcp_configured: bool,
    pub hook_installed: bool,
    pub setup_present: bool,
    pub hooks_disabled: bool,
    /// Evidence for this configuration, not a guessed trust hash.
    pub events_received: bool,
    pub problem: Option<String>,
}

#[derive(Serialize)]
pub struct Changes {
    pub paths: Vec<String>,
    pub backups: Vec<String>,
}

fn read(path: &Path) -> Result<Option<Vec<u8>>, String> {
    match fs::read(path) {
        Ok(bytes) => Ok(Some(bytes)),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(_) => Err(format!("Could not read {}", path.display())),
    }
}

fn json_config(bytes: Option<&[u8]>, path: &Path) -> Result<Value, String> {
    let bytes = bytes.unwrap_or(b"{}");
    let v: Value = serde_json::from_slice(bytes)
        .map_err(|_| format!("Fix invalid JSON in {} first", path.display()))?;
    if !v.is_object() {
        return Err(format!("{} must contain a JSON object", path.display()));
    }
    Ok(v)
}

fn toml_config(bytes: Option<&[u8]>, path: &Path) -> Result<DocumentMut, String> {
    std::str::from_utf8(bytes.unwrap_or(b""))
        .ok()
        .and_then(|s| s.parse().ok())
        .ok_or_else(|| format!("Fix invalid TOML in {} first", path.display()))
}

fn local_url(url: &str, suffix: &str) -> bool {
    ["http://127.0.0.1:", "http://localhost:", "http://[::1]:"]
        .iter()
        .any(|prefix| {
            url.strip_prefix(prefix)
                .and_then(|s| s.split_once('/'))
                .is_some_and(|(port, path)| port.parse::<u16>().is_ok() && path == suffix)
        })
}

pub fn hook_entry(host: &str, url: &str) -> Result<Value, String> {
    match host {
        "claude-code" => Ok(json!({"type":"http", "url":url, "timeout":5})),
        "codex" => {
            let post = |exe, null| {
                format!("{exe} -s --connect-timeout 1 -m 3 -o {null} -X POST -H Content-Type:application/json --data-binary @- {url}")
            };
            Ok(
                json!({"type":"command", "command":post("curl", "/dev/null"),
                "commandWindows":post("curl.exe", "NUL"), "timeout":5, "statusMessage":"Prism"}),
            )
        }
        _ => Err("Unknown harness".into()),
    }
}

fn owned_hook(hook: &Value, host: &str) -> bool {
    let url = if host == "claude-code" {
        if hook["type"] != "http" {
            return false;
        }
        hook["url"].as_str()
    } else {
        if hook["type"] != "command" {
            return false;
        }
        hook["command"].as_str().and_then(|s| s.strip_prefix("curl -s --connect-timeout 1 -m 3 -o /dev/null -X POST -H Content-Type:application/json --data-binary @- "))
    };
    url.is_some_and(|url| {
        let Some((prefix, token)) = url.rsplit_once('/') else {
            return false;
        };
        !token.is_empty()
            && token
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_')
            && local_url(prefix, &format!("hooks/{host}"))
    })
}

fn hook_is_current(config: &Value, host: &str, url: &str) -> bool {
    let Ok(expected) = hook_entry(host, url) else {
        return false;
    };
    config
        .pointer("/hooks/PreToolUse")
        .and_then(Value::as_array)
        .is_some_and(|groups| {
            groups.iter().any(|group| {
                let matches_all = group
                    .get("matcher")
                    .is_none_or(|v| matches!(v.as_str(), Some("" | "*" | ".*")));
                matches_all
                    && group["hooks"]
                        .as_array()
                        .is_some_and(|hooks| hooks.iter().any(|h| h == &expected))
            })
        })
}

pub fn inspect(
    paths: &Paths,
    host: &str,
    url: &str,
    hook_url: &str,
    last_event: Option<chrono::DateTime<chrono::Utc>>,
) -> Setup {
    let mut setup = Setup {
        host: host.into(),
        settings_path: paths.hooks.display().to_string(),
        mcp_path: paths.mcp.display().to_string(),
        mcp_configured: false,
        hook_installed: false,
        setup_present: false,
        hooks_disabled: false,
        events_received: false,
        problem: None,
    };
    let result = (|| {
        let bytes = read(&paths.mcp)?;
        setup.mcp_configured = if paths.codex {
            let doc = toml_config(bytes.as_deref(), &paths.mcp)?;
            let entry = doc.get("mcp_servers").and_then(|m| m.get("prism"));
            setup.setup_present = entry
                .and_then(|e| e.get("url"))
                .and_then(Item::as_str)
                .is_some_and(|u| local_url(u, "mcp"));
            entry.is_some_and(|e| {
                e.get("url").and_then(Item::as_str) == Some(url)
                    && e.get("enabled").and_then(Item::as_bool) != Some(false)
            })
        } else {
            let doc = json_config(bytes.as_deref(), &paths.mcp)?;
            setup.setup_present = doc
                .pointer("/mcpServers/prism/url")
                .and_then(Value::as_str)
                .is_some_and(|u| local_url(u, "mcp"));
            doc.pointer("/mcpServers/prism/url").and_then(Value::as_str) == Some(url)
                && doc
                    .pointer("/mcpServers/prism/type")
                    .and_then(Value::as_str)
                    == Some("http")
        };
        let config = json_config(read(&paths.hooks)?.as_deref(), &paths.hooks)?;
        setup.hook_installed = hook_is_current(&config, host, hook_url);
        setup.setup_present |= config
            .pointer("/hooks/PreToolUse")
            .and_then(Value::as_array)
            .is_some_and(|groups| {
                groups.iter().any(|group| {
                    group["hooks"]
                        .as_array()
                        .is_some_and(|hooks| hooks.iter().any(|hook| owned_hook(hook, host)))
                })
            });
        setup.hooks_disabled = config["disableAllHooks"] == true;
        // A previous event doesn't prove a changed hook still works. Trust is owned by the host.
        let modified = [&paths.hooks, &paths.mcp]
            .into_iter()
            .filter_map(|p| fs::metadata(p).ok()?.modified().ok())
            .max();
        setup.events_received = setup.hook_installed
            && !setup.hooks_disabled
            && last_event.is_some_and(|at| modified.is_some_and(|m| SystemTime::from(at) >= m));
        Ok::<_, String>(())
    })();
    if let Err(e) = result {
        setup.problem = Some(e);
    }
    setup
}

fn edit_hooks(
    config: &mut Value,
    host: &str,
    url: &str,
    remove: bool,
) -> Result<Vec<(usize, usize, Option<usize>)>, String> {
    let mut moves = vec![];
    if remove && config.get("hooks").is_none() {
        return Ok(moves);
    }
    let hooks = config
        .as_object_mut()
        .unwrap()
        .entry("hooks")
        .or_insert(json!({}));
    let hooks = hooks.as_object_mut().ok_or("hooks must be an object")?;
    if remove && !hooks.contains_key("PreToolUse") {
        return Ok(moves);
    }
    let groups = hooks
        .entry("PreToolUse")
        .or_insert(json!([]))
        .as_array_mut()
        .ok_or("PreToolUse must be an array")?;
    // Preserve group positions where possible: other hosts key trust by group index.
    let mut placed = false;
    for (group_index, group) in groups.iter_mut().enumerate() {
        let all = group
            .get("matcher")
            .is_none_or(|v| matches!(v.as_str(), Some("" | "*" | ".*")));
        if let Some(list) = group.get_mut("hooks").and_then(Value::as_array_mut) {
            let mut updated = Vec::with_capacity(list.len());
            for (old_index, hook) in list.drain(..).enumerate() {
                let new_index = updated.len();
                if !owned_hook(&hook, host) {
                    if old_index != new_index {
                        moves.push((group_index, old_index, Some(new_index)));
                    }
                    updated.push(hook);
                } else if all && !remove && !placed {
                    updated.push(hook_entry(host, url)?);
                    placed = true;
                } else {
                    moves.push((group_index, old_index, None));
                }
            }
            *list = updated;
        }
    }
    if !remove && !placed {
        groups.push(json!({"hooks":[hook_entry(host, url)?]}));
    }
    Ok(moves)
}

fn edit_json_mcp(config: &mut Value, url: &str, remove: bool) -> Result<(), String> {
    if remove && config.get("mcpServers").is_none() {
        return Ok(());
    }
    let servers = config
        .as_object_mut()
        .unwrap()
        .entry("mcpServers")
        .or_insert(json!({}))
        .as_object_mut()
        .ok_or("mcpServers must be an object")?;
    if let Some(existing) = servers.get("prism") {
        if !existing
            .get("url")
            .and_then(Value::as_str)
            .is_some_and(|u| local_url(u, "mcp"))
        {
            return Err(
                "The name prism already points elsewhere. Rename that entry in the client first."
                    .into(),
            );
        }
    }
    if remove {
        servers.remove("prism");
    } else {
        let entry = servers
            .entry("prism")
            .or_insert(json!({}))
            .as_object_mut()
            .ok_or("prism must be an object")?;
        entry.insert("url".into(), json!(url));
        entry.insert("type".into(), json!("http"));
    }
    Ok(())
}

fn edit_toml_mcp(config: &mut DocumentMut, url: &str, remove: bool) -> Result<(), String> {
    if remove && config.get("mcp_servers").is_none() {
        return Ok(());
    }
    if config.get("mcp_servers").is_none() {
        config["mcp_servers"] = Item::Table(Table::new());
    }
    let servers = config["mcp_servers"]
        .as_table_like_mut()
        .ok_or("mcp_servers must be a table")?;
    if let Some(existing) = servers.get("prism") {
        if !existing
            .get("url")
            .and_then(Item::as_str)
            .is_some_and(|u| local_url(u, "mcp"))
        {
            return Err(
                "The name prism already points elsewhere. Rename that entry in the client first."
                    .into(),
            );
        }
    }
    if remove {
        servers.remove("prism");
    } else {
        if !servers.contains_key("prism") {
            servers.insert("prism", Item::Table(Table::new()));
        }
        let entry = servers
            .get_mut("prism")
            .unwrap()
            .as_table_like_mut()
            .ok_or("prism must be a table")?;
        entry.insert("url", value(url));
        if entry.get("enabled").and_then(Item::as_bool) == Some(false) {
            entry.insert("enabled", value(true));
        }
    }
    Ok(())
}

struct Edit {
    path: PathBuf,
    old: Option<Vec<u8>>,
    new: Vec<u8>,
}

fn commit(edits: Vec<Edit>) -> Result<Changes, String> {
    commit_with(edits, prism_core::write_client_config)
}

fn commit_with(
    edits: Vec<Edit>,
    mut write: impl FnMut(&Path, &[u8]) -> std::io::Result<()>,
) -> Result<Changes, String> {
    let edits: Vec<_> = edits
        .into_iter()
        .filter(|e| e.old.as_deref() != Some(e.new.as_slice()))
        .collect();
    let mut out = Changes {
        paths: vec![],
        backups: vec![],
    };
    for e in &edits {
        if fs::symlink_metadata(&e.path).is_ok_and(|m| m.file_type().is_symlink()) {
            return Err(format!(
                "{} is a symlink; update its target manually",
                e.path.display()
            ));
        }
        if read(&e.path)? != e.old {
            return Err("Client settings changed. Try again.".into());
        }
    }
    for (i, e) in edits.iter().enumerate() {
        let apply = (|| {
            if read(&e.path)? != e.old {
                return Err("Client settings changed. Try again.".to_string());
            }
            if let Some(old) = &e.old {
                let backup = e.path.with_file_name(format!(
                    "{}.prism-{}.bak",
                    e.path.file_name().unwrap().to_string_lossy(),
                    chrono::Utc::now().timestamp_nanos_opt().unwrap_or_default()
                ));
                write(&backup, old).map_err(|_| "Could not back up client settings".to_string())?;
                out.backups.push(backup.display().to_string());
            }
            write(&e.path, &e.new).map_err(|_| format!("Could not save {}", e.path.display()))?;
            out.paths.push(e.path.display().to_string());
            Ok::<_, String>(())
        })();
        if let Err(err) = apply {
            let mut unrestored = vec![];
            // A replacement may succeed before the directory sync fails. Check this edit too.
            for previous in edits[..=i].iter().rev() {
                match read(&previous.path) {
                    Ok(current) if current == previous.old => {}
                    Ok(current) if current.as_deref() == Some(previous.new.as_slice()) => {
                        let restored = match &previous.old {
                            Some(old) => write(&previous.path, old),
                            None => fs::remove_file(&previous.path),
                        };
                        if restored.is_err() {
                            unrestored.push(previous.path.display().to_string());
                        }
                    }
                    // Preserve concurrent user edits and report that restoration is incomplete.
                    _ => unrestored.push(previous.path.display().to_string()),
                }
            }
            let recovery = if unrestored.is_empty() {
                String::new()
            } else {
                format!(" Could not restore: {}.", unrestored.join(", "))
            };
            return Err(format!(
                "{err}.{recovery} Backups are beside the client files."
            ));
        }
    }
    Ok(out)
}

/// Both documents are parsed before either is written. Existing non-Prism settings survive.
pub fn configure(
    paths: &Paths,
    host: &str,
    url: &str,
    hook_url: &str,
    remove: bool,
    hooks_only: bool,
) -> Result<Changes, String> {
    let _guard = CONFIG_WRITE
        .lock()
        .map_err(|_| "Client settings are busy")?;
    let old = read(&paths.hooks)?;
    let mut hooks = json_config(old.as_deref(), &paths.hooks)?;
    let moves = edit_hooks(&mut hooks, host, hook_url, remove)?;
    let mut edits = vec![];
    if old.is_some() || !remove {
        edits.push(Edit {
            path: paths.hooks.clone(),
            old,
            new: format!(
                "{}\n",
                serde_json::to_string_pretty(&hooks).map_err(|_| "Could not format hooks")?
            )
            .into_bytes(),
        });
    }
    if !hooks_only || (paths.codex && !moves.is_empty()) {
        let old = read(&paths.mcp)?;
        let new = if paths.codex {
            let mut config = toml_config(old.as_deref(), &paths.mcp)?;
            hook_state::remap(&mut config, &paths.hooks, &moves)?;
            if !hooks_only {
                edit_toml_mcp(&mut config, url, remove)?;
            }
            config.to_string().into_bytes()
        } else {
            let mut config = json_config(old.as_deref(), &paths.mcp)?;
            edit_json_mcp(&mut config, url, remove)?;
            format!(
                "{}\n",
                serde_json::to_string_pretty(&config)
                    .map_err(|_| "Could not format MCP settings")?
            )
            .into_bytes()
        };
        if old.is_some() || !remove {
            edits.push(Edit {
                path: paths.mcp.clone(),
                old,
                new,
            });
        }
    }
    commit(edits)
}

#[cfg(test)]
mod tests {
    use super::*;
    const URL: &str = "http://127.0.0.1:9086/mcp";
    const HOOK: &str = "http://127.0.0.1:9086/hooks/codex/test-token";
    fn paths(dir: &Path, codex: bool) -> Paths {
        Paths {
            mcp: dir.join(if codex { "config.toml" } else { ".claude.json" }),
            hooks: dir.join("hooks.json"),
            codex,
        }
    }

    #[test]
    fn codex_install_repair_remove_preserves_settings_and_comments() {
        let dir = tempfile::tempdir().unwrap();
        let p = paths(dir.path(), true);
        fs::write(
            &p.mcp,
            "# keep me\nmodel = 'custom'\n[mcp_servers.other]\ncommand = 'other'\n",
        )
        .unwrap();
        fs::write(
            &p.hooks,
            r#"{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo other"}]}]}}"#,
        )
        .unwrap();
        let changes = configure(&p, "codex", URL, HOOK, false, false).unwrap();
        assert_eq!(changes.backups.len(), 2);
        assert!(fs::read_to_string(&p.mcp).unwrap().contains("# keep me"));
        assert!(inspect(&p, "codex", URL, HOOK, None).mcp_configured);
        assert!(!inspect(&p, "codex", URL, HOOK, None).events_received);
        assert!(configure(&p, "codex", URL, HOOK, false, false)
            .unwrap()
            .paths
            .is_empty());
        configure(&p, "codex", URL, HOOK, true, false).unwrap();
        assert!(fs::read_to_string(&p.hooks).unwrap().contains("echo other"));
        assert!(fs::read_to_string(&p.mcp)
            .unwrap()
            .contains("mcp_servers.other"));
        assert!(!inspect(&p, "codex", URL, HOOK, None).hook_installed);
    }

    #[test]
    fn claude_keeps_project_scopes_and_unrelated_hooks() {
        let dir = tempfile::tempdir().unwrap();
        let p = paths(dir.path(), false);
        fs::write(&p.mcp, r#"{"projects":{"/project":{"mcpServers":{"prism":{"url":"https://remote/mcp"}}}},"other":42}"#).unwrap();
        let hook = HOOK.replace("codex", "claude-code");
        configure(&p, "claude-code", URL, &hook, false, false).unwrap();
        let doc = json_config(read(&p.mcp).unwrap().as_deref(), &p.mcp).unwrap();
        assert_eq!(
            doc["projects"]["/project"]["mcpServers"]["prism"]["url"],
            "https://remote/mcp"
        );
        assert_eq!(doc["other"], 42);
        configure(&p, "claude-code", URL, &hook, true, false).unwrap();
        assert!(fs::read_to_string(&p.mcp)
            .unwrap()
            .contains("https://remote/mcp"));
    }

    #[test]
    fn malformed_or_conflicting_mcp_never_writes_hook() {
        for config in [
            "[broken",
            "[mcp_servers.prism]\nurl='https://elsewhere/mcp'\n",
        ] {
            let dir = tempfile::tempdir().unwrap();
            let p = paths(dir.path(), true);
            fs::write(&p.mcp, config).unwrap();
            assert!(configure(&p, "codex", URL, HOOK, false, false).is_err());
            assert!(!p.hooks.exists());
            assert_eq!(fs::read_to_string(&p.mcp).unwrap(), config);
        }
    }

    #[test]
    fn changing_hook_invalidates_observation_evidence() {
        let dir = tempfile::tempdir().unwrap();
        let p = paths(dir.path(), true);
        configure(&p, "codex", URL, HOOK, false, false).unwrap();
        let after = chrono::Utc::now() + chrono::Duration::seconds(1);
        assert!(inspect(&p, "codex", URL, HOOK, Some(after)).events_received);
        assert!(
            !inspect(
                &p,
                "codex",
                URL,
                &HOOK.replace("test-token", "new-token"),
                Some(after)
            )
            .events_received
        );
    }

    #[test]
    fn stale_endpoint_and_token_still_offer_removal() {
        for codex in [true, false] {
            let dir = tempfile::tempdir().unwrap();
            let p = paths(dir.path(), codex);
            let host = if codex { "codex" } else { "claude-code" };
            let hook = HOOK.replace("codex", host);
            configure(&p, host, URL, &hook, false, false).unwrap();
            let new_url = URL.replace("9086", "9087");
            let new_hook = hook
                .replace("9086", "9087")
                .replace("test-token", "rotated-token");
            let status = inspect(&p, host, &new_url, &new_hook, None);
            assert!(status.setup_present);
            assert!(!status.mcp_configured);
            assert!(!status.hook_installed);
            configure(&p, host, &new_url, &new_hook, true, false).unwrap();
            assert!(!inspect(&p, host, &new_url, &new_hook, None).setup_present);
        }
    }

    #[test]
    fn rollback_includes_replacement_that_failed_after_rename() {
        for existing in [true, false] {
            let dir = tempfile::tempdir().unwrap();
            let first = dir.path().join("first.json");
            let second = dir.path().join("second.json");
            fs::write(&first, "original first").unwrap();
            let second_old = existing.then(|| b"original second".to_vec());
            if let Some(old) = &second_old {
                fs::write(&second, old).unwrap();
            }
            let err = commit_with(
                vec![
                    Edit {
                        path: first.clone(),
                        old: Some(b"original first".to_vec()),
                        new: b"replacement".to_vec(),
                    },
                    Edit {
                        path: second.clone(),
                        old: second_old.clone(),
                        new: b"replacement".to_vec(),
                    },
                ],
                |path, bytes| {
                    prism_core::write_client_config(path, bytes)?;
                    if path == second && bytes == b"replacement" {
                        return Err(std::io::Error::other("directory sync failed after rename"));
                    }
                    Ok(())
                },
            )
            .err()
            .unwrap();
            assert!(!err.contains("Could not restore"));
            assert_eq!(fs::read(&first).unwrap(), b"original first");
            assert_eq!(read(&second).unwrap(), second_old);
        }
    }

    #[test]
    fn failed_restoration_is_reported_with_the_backup_kept() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("settings.json");
        fs::write(&file, "original").unwrap();
        let err = commit_with(
            vec![Edit {
                path: file.clone(),
                old: Some(b"original".to_vec()),
                new: b"replacement".to_vec(),
            }],
            |path, bytes| {
                if path == file && bytes == b"original" {
                    return Err(std::io::Error::other("restoration refused"));
                }
                prism_core::write_client_config(path, bytes)?;
                if path == file {
                    return Err(std::io::Error::other("sync failed"));
                }
                Ok(())
            },
        )
        .err()
        .unwrap();
        assert!(err.contains("Could not restore:"));
        assert!(err.contains(file.to_str().unwrap()));
        let backup = fs::read_dir(dir.path())
            .unwrap()
            .flatten()
            .map(|e| e.path())
            .find(|p| p.extension().is_some_and(|e| e == "bak"))
            .unwrap();
        assert_eq!(fs::read(backup).unwrap(), b"original");
    }

    #[test]
    fn repairing_a_mixed_group_keeps_other_hook_positions() {
        let mut config = json!({"hooks":{"PreToolUse":[{"hooks":[
            hook_entry("codex", HOOK).unwrap(), {"type":"command","command":"echo other"}
        ]}]}});
        edit_hooks(
            &mut config,
            "codex",
            &HOOK.replace("test-token", "new-token"),
            false,
        )
        .unwrap();
        assert_eq!(
            config["hooks"]["PreToolUse"][0]["hooks"][1]["command"],
            "echo other"
        );
    }

    #[test]
    fn removing_or_deduplicating_prism_preserves_other_codex_hook_state() {
        for remove in [true, false] {
            let dir = tempfile::tempdir().unwrap();
            let p = paths(dir.path(), true);
            configure(&p, "codex", URL, HOOK, false, false).unwrap();
            let prism = hook_entry("codex", HOOK).unwrap();
            let hooks = json!({"hooks":{"PreToolUse":[{"hooks":[
                prism, {"type":"command","command":"echo disabled"}, prism,
                {"type":"command","command":"echo untrusted"},
                {"type":"command","command":"echo trusted"}
            ]}]}});
            fs::write(&p.hooks, serde_json::to_vec(&hooks).unwrap()).unwrap();
            let key = |i| format!("{}:pre_tool_use:0:{i}", p.hooks.display());
            let mut config = toml_config(read(&p.mcp).unwrap().as_deref(), &p.mcp).unwrap();
            for (i, hash) in [
                (0, "prism"),
                (1, "disabled-original"),
                (2, "duplicate"),
                (4, "trusted-original"),
            ] {
                config["hooks"]["state"][&key(i)]["trusted_hash"] = value(hash);
            }
            config["hooks"]["state"][&key(1)]["enabled"] = value(false);
            fs::write(&p.mcp, config.to_string()).unwrap();
            // Hook-only repair/removal must still move state stored in config.toml.
            configure(&p, "codex", URL, HOOK, remove, true).unwrap();
            let config = toml_config(read(&p.mcp).unwrap().as_deref(), &p.mcp).unwrap();
            let first = usize::from(!remove);
            let state = &config["hooks"]["state"];
            assert_eq!(
                state[&key(first)]["trusted_hash"].as_str(),
                Some("disabled-original")
            );
            assert_eq!(state[&key(first)]["enabled"].as_bool(), Some(false));
            assert!(state.get(key(first + 1)).is_none());
            assert_eq!(
                state[&key(first + 2)]["trusted_hash"].as_str(),
                Some("trusted-original")
            );
            assert!(state.get(key(4)).is_none());
            assert_eq!(config["mcp_servers"]["prism"]["url"].as_str(), Some(URL));
        }
    }

    #[test]
    fn setup_respects_a_hosts_disabled_observation_setting() {
        let dir = tempfile::tempdir().unwrap();
        let p = paths(dir.path(), false);
        fs::write(&p.hooks, r#"{"disableAllHooks":true}"#).unwrap();
        let hook = HOOK.replace("codex", "claude-code");
        configure(&p, "claude-code", URL, &hook, false, false).unwrap();
        let status = inspect(
            &p,
            "claude-code",
            URL,
            &hook,
            Some(chrono::Utc::now() + chrono::Duration::seconds(1)),
        );
        assert!(status.mcp_configured);
        assert!(status.hook_installed);
        assert!(status.hooks_disabled);
        assert!(!status.events_received);
    }

    #[test]
    fn invalid_hooks_and_changed_file_fail_without_overwriting() {
        let dir = tempfile::tempdir().unwrap();
        let p = paths(dir.path(), true);
        fs::write(&p.hooks, "{not json").unwrap();
        assert!(configure(&p, "codex", URL, HOOK, false, false).is_err());
        assert!(!p.mcp.exists());
        let path = dir.path().join("changed.json");
        fs::write(&path, "newer user edit").unwrap();
        assert!(commit(vec![Edit {
            path: path.clone(),
            old: Some(b"old".to_vec()),
            new: b"replacement".to_vec()
        }])
        .is_err());
        assert_eq!(fs::read_to_string(&path).unwrap(), "newer user edit");
    }

    #[cfg(unix)]
    #[test]
    fn private_backups_and_no_symlink_replacement() {
        use std::os::unix::fs::{symlink, PermissionsExt};
        let dir = tempfile::tempdir().unwrap();
        let p = paths(dir.path(), true);
        fs::write(&p.mcp, "model='test'\n").unwrap();
        let changes = configure(&p, "codex", URL, HOOK, false, false).unwrap();
        for path in changes.paths.iter().chain(changes.backups.iter()) {
            assert_eq!(
                fs::metadata(path).unwrap().permissions().mode() & 0o777,
                0o600
            );
        }
        let target = dir.path().join("linked.json");
        fs::write(&target, "{}").unwrap();
        fs::remove_file(&p.hooks).unwrap();
        symlink(&target, &p.hooks).unwrap();
        assert!(configure(&p, "codex", URL, HOOK, false, false).is_err());
        assert_eq!(fs::read_to_string(target).unwrap(), "{}");
    }

    #[test]
    #[ignore = "requires installed Claude Code and Codex CLIs; isolated configs, no model calls"]
    fn installed_clients_read_generated_global_configuration() {
        for (host, codex) in [("claude-code", false), ("codex", true)] {
            let dir = tempfile::tempdir().unwrap();
            let p = paths(dir.path(), codex);
            let hook = HOOK.replace("codex", host);
            let probe_url = "http://127.0.0.1:9/mcp";
            configure(&p, host, probe_url, &hook, false, false).unwrap();
            let mut cmd = std::process::Command::new(if codex { "codex" } else { "claude" });
            cmd.args(["mcp", "get", "prism"])
                .current_dir(dir.path())
                .env(
                    if codex {
                        "CODEX_HOME"
                    } else {
                        "CLAUDE_CONFIG_DIR"
                    },
                    dir.path(),
                );
            if codex {
                cmd.arg("--json");
            }
            let output = cmd.output().expect("installed CLI");
            assert!(
                output.status.success(),
                "{host} rejected generated configuration"
            );
            assert!(
                String::from_utf8_lossy(&output.stdout).contains(probe_url),
                "{host} did not read global endpoint"
            );
        }
    }
}
