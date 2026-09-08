//! Cursor and OpenCode local adapters. These never edit workspace configuration.
use super::{json_edit, local_url, read, Edit, Paths, Setup};
use serde_json::{json, Value};
use std::{fs, path::Path, time::SystemTime};

const OPENCODE_OBSERVER: &str = include_str!("observers/opencode.js");
const OWNED: &str = "// Prism-owned observer v1.";

/// A different installed major must never receive a V1 config/plugin recipe.
/// An absent CLI is allowed: the desktop client also loads this global directory.
fn check_opencode_version() -> Result<(), String> {
    use std::{
        io::Read,
        process::{Command, Stdio},
        time::{Duration, Instant},
    };
    let mut child = match Command::new("opencode")
        .arg("--version")
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
    {
        Ok(child) => child,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(_) => return Err("Could not check the OpenCode version".into()),
    };
    let start = Instant::now();
    loop {
        match child.try_wait() {
            Ok(Some(status)) if status.success() => break,
            Ok(Some(_)) => return Err("Could not check the OpenCode version".into()),
            Ok(None) if start.elapsed() < Duration::from_secs(3) => {
                std::thread::sleep(Duration::from_millis(20))
            }
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return Err("OpenCode version check timed out".into());
            }
        }
    }
    let mut output = String::new();
    if let Some(stdout) = child.stdout.take() {
        let _ = stdout.take(128).read_to_string(&mut output);
    }
    if output.trim().trim_start_matches('v').split('.').next() != Some("1") {
        return Err(
            "This setup supports OpenCode V1. Use manual MCP setup for other versions.".into(),
        );
    }
    Ok(())
}

pub(super) fn paths(host: &str, home: &Path) -> Result<Paths, String> {
    match host {
        "cursor" => Ok(Paths {
            mcp: home.join(".cursor/mcp.json"),
            hooks: home.join(".cursor/hooks.json"),
            codex: false,
        }),
        "opencode" => {
            let global = std::env::var_os("XDG_CONFIG_HOME")
                .map(std::path::PathBuf::from)
                .unwrap_or_else(|| home.join(".config"))
                .join("opencode");
            let dir = std::env::var_os("OPENCODE_CONFIG_DIR")
                .map(std::path::PathBuf::from)
                .unwrap_or(global);
            // JSONC is loaded last by V1; update that file if both exist.
            let mcp = std::env::var_os("OPENCODE_CONFIG")
                .map(std::path::PathBuf::from)
                .unwrap_or_else(|| {
                    dir.join(if dir.join("opencode.jsonc").exists() {
                        "opencode.jsonc"
                    } else {
                        "opencode.json"
                    })
                });
            if !dir.is_absolute() || !mcp.is_absolute() {
                return Err("OpenCode config paths must be absolute".into());
            }
            Ok(Paths {
                mcp,
                hooks: dir.join("plugins/prism.js"),
                codex: false,
            })
        }
        _ => Err("Unknown harness".into()),
    }
}

fn cursor_command(url: &str, windows: bool) -> String {
    if windows {
        format!("cmd /D /S /C \"curl.exe --disable -s --noproxy * --proto =http --connect-timeout 1 -m 1 -o NUL -X POST -H Content-Type:application/json --data-binary @- {url} 2>NUL & echo {{}} & exit /B 0\"")
    } else {
        format!("sh -c 'curl --disable -s --noproxy \"*\" --proto =http --connect-timeout 1 -m 1 -o /dev/null -X POST -H Content-Type:application/json --data-binary @- {url} 2>/dev/null; printf \"{{}}\"; exit 0'")
    }
}

fn cursor_hook(url: &str) -> Value {
    json!({"type":"command","command":cursor_command(url,cfg!(windows)),"timeout":2,"failClosed":false})
}

fn owned_cursor(hook: &Value) -> bool {
    let Some(command) = hook["command"].as_str() else {
        return false;
    };
    let Some(url) = command
        .split_whitespace()
        .find(|s| s.starts_with("http://"))
    else {
        return false;
    };
    let Some((base, token)) = url.rsplit_once('/') else {
        return false;
    };
    !token.is_empty()
        && token
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
        && local_url(base, "hooks/cursor")
        && [false, true]
            .into_iter()
            .any(|w| command == cursor_command(url, w))
}

fn edit_cursor_hooks(doc: &mut Value, hook_url: &str, remove: bool) -> Result<(), String> {
    if doc.get("version").is_some_and(|v| v != 1) {
        return Err("Unsupported Cursor hook version".into());
    }
    if remove && doc.get("hooks").is_none() {
        return Ok(());
    }
    let hooks = doc
        .as_object_mut()
        .unwrap()
        .entry("hooks")
        .or_insert(json!({}))
        .as_object_mut()
        .ok_or("hooks must be an object")?;
    if remove && !hooks.contains_key("preToolUse") {
        return Ok(());
    }
    let entries = hooks
        .entry("preToolUse")
        .or_insert(json!([]))
        .as_array_mut()
        .ok_or("preToolUse must be an array")?;
    let mut placed = false;
    entries.retain_mut(|entry| {
        if !owned_cursor(entry) {
            return true;
        }
        if remove || placed {
            return false;
        }
        let disabled = entry.get("enabled").filter(|v| **v == false).cloned();
        *entry = cursor_hook(hook_url);
        if let Some(v) = disabled {
            entry["enabled"] = v;
        }
        placed = true;
        true
    });
    if !remove && !placed {
        entries.push(cursor_hook(hook_url));
    }
    if !remove {
        doc["version"] = json!(1);
    }
    Ok(())
}

fn server<'a>(doc: &'a Value, host: &str) -> Option<&'a Value> {
    doc.get(if host == "cursor" {
        "mcpServers"
    } else {
        "mcp"
    })?
    .get("prism")
}

fn edit_mcp(doc: &mut Value, host: &str, url: &str, remove: bool) -> Result<(), String> {
    let key = if host == "cursor" {
        "mcpServers"
    } else {
        "mcp"
    };
    if host == "opencode" && doc.pointer("/mcp/servers").is_some() {
        return Err("OpenCode V2 uses a different setup. This adapter supports V1.".into());
    }
    if remove && doc.get(key).is_none() {
        return Ok(());
    }
    let servers = doc
        .as_object_mut()
        .unwrap()
        .entry(key)
        .or_insert(json!({}))
        .as_object_mut()
        .ok_or("MCP settings must be an object")?;
    if let Some(old) = servers.get("prism") {
        if !old
            .get("url")
            .and_then(Value::as_str)
            .is_some_and(|u| local_url(u, "mcp"))
            || old.get("command").is_some()
        {
            return Err("The name prism already points elsewhere. Rename that entry first.".into());
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
        if host == "opencode" {
            entry.insert("type".into(), json!("remote"));
        }
        // A user's disabled setting and auth/custom headers are not setup consent to change them.
    }
    Ok(())
}

fn observer(url: &str) -> String {
    OPENCODE_OBSERVER.replace("__PRISM_HOOK_URL__", url)
}

pub(super) fn snippet(host: &str, url: &str) -> Result<String, String> {
    match host {
        "cursor" => serde_json::to_string_pretty(
            &json!({"version":1,"hooks":{"preToolUse":[cursor_hook(url)]}}),
        )
        .map_err(|e| e.to_string()),
        "opencode" => Ok(observer(url)),
        _ => Err("Unknown harness".into()),
    }
}

pub(super) fn edits(
    paths: &Paths,
    host: &str,
    url: &str,
    hook_url: &str,
    remove: bool,
    hooks_only: bool,
) -> Result<Vec<Edit>, String> {
    if host == "opencode" && !remove && !cfg!(test) {
        check_opencode_version()?;
    }
    let mut edits = vec![];
    if !hooks_only {
        let old = read(&paths.mcp)?;
        let mut doc = json_edit::value(old.as_deref(), &paths.mcp)?;
        edit_mcp(&mut doc, host, url, remove)?;
        if old.is_some() || !remove {
            let new = Some(json_edit::update(old.as_deref(), &paths.mcp, &doc)?);
            edits.push(Edit {
                path: paths.mcp.clone(),
                old,
                new,
            });
        }
    }
    let old = read(&paths.hooks)?;
    if host == "cursor" {
        let mut doc = json_edit::value(old.as_deref(), &paths.hooks)?;
        edit_cursor_hooks(&mut doc, hook_url, remove)?;
        if old.is_some() || !remove {
            let new = Some(json_edit::update(old.as_deref(), &paths.hooks, &doc)?);
            edits.push(Edit {
                path: paths.hooks.clone(),
                old,
                new,
            });
        }
    } else {
        if old
            .as_deref()
            .is_some_and(|b| !b.starts_with(OWNED.as_bytes()))
        {
            return Err(
                "plugins/prism.js already exists and is not managed by Prism. Rename it first."
                    .into(),
            );
        }
        edits.push(Edit {
            path: paths.hooks.clone(),
            old,
            new: (!remove).then(|| observer(hook_url).into_bytes()),
        });
    }
    Ok(edits)
}

pub(super) fn inspect(
    paths: &Paths,
    host: &str,
    url: &str,
    hook_url: &str,
    last: Option<chrono::DateTime<chrono::Utc>>,
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
        let doc = json_edit::value(read(&paths.mcp)?.as_deref(), &paths.mcp)?;
        let entry = server(&doc, host);
        setup.setup_present = entry
            .and_then(|e| e["url"].as_str())
            .is_some_and(|u| local_url(u, "mcp"));
        setup.mcp_configured = entry.is_some_and(|e| {
            e["url"] == url
                && e.get("command").is_none()
                && e["enabled"] != false
                && e["disabled"] != true
                && (host == "cursor" || e["type"] == "remote")
        });
        let hooks = read(&paths.hooks)?;
        if host == "cursor" {
            let doc = json_edit::value(hooks.as_deref(), &paths.hooks)?;
            setup.hooks_disabled = doc["disableAllHooks"] == true || doc["enabled"] == false;
            if let Some(entries) = doc.pointer("/hooks/preToolUse").and_then(Value::as_array) {
                setup.setup_present |= entries.iter().any(owned_cursor);
                setup.hook_installed = entries.iter().any(|e| {
                    let mut expected = cursor_hook(hook_url);
                    if e["enabled"] == false {
                        expected["enabled"] = json!(false);
                        setup.hooks_disabled = true;
                    }
                    *e == expected
                });
            }
        } else {
            setup.setup_present |= hooks
                .as_deref()
                .is_some_and(|b| b.starts_with(OWNED.as_bytes()));
            setup.hook_installed = hooks.as_deref() == Some(observer(hook_url).as_bytes());
            if doc.pointer("/mcp/servers").is_some() {
                return Err("OpenCode V2 needs a different adapter".to_string());
            }
            if hooks
                .as_deref()
                .is_some_and(|b| !b.starts_with(OWNED.as_bytes()))
            {
                return Err("plugins/prism.js belongs to another plugin".into());
            }
        }
        let modified = [&paths.mcp, &paths.hooks]
            .into_iter()
            .filter_map(|p| fs::metadata(p).ok()?.modified().ok())
            .max();
        setup.events_received = setup.hook_installed
            && !setup.hooks_disabled
            && last.is_some_and(|at| modified.is_some_and(|m| SystemTime::from(at) >= m));
        Ok::<_, String>(())
    })();
    if let Err(e) = result {
        setup.problem = Some(e);
    }
    setup
}

#[cfg(test)]
mod tests {
    use super::*;
    const URL: &str = "http://127.0.0.1:9086/mcp";
    fn fixture(home: &Path, host: &str) -> Paths {
        Paths {
            mcp: home.join("settings.jsonc"),
            hooks: home.join(if host == "cursor" {
                "hooks.json"
            } else {
                "plugins/prism.js"
            }),
            codex: false,
        }
    }
    #[test]
    fn install_repair_remove_preserve_user_config_and_comments() {
        for host in ["cursor", "opencode"] {
            let dir = tempfile::tempdir().unwrap();
            let p = fixture(dir.path(), host);
            fs::write(&p.mcp, "{\n // preference\n \"theme\":\"dark\",\n}\n").unwrap();
            if host == "cursor" {
                fs::write(
                    &p.hooks,
                    r#"{"hooks":{"preToolUse":[{ /* keep */ "command":"other"}]}}"#,
                )
                .unwrap();
            }
            let hook = format!("http://127.0.0.1:9086/hooks/{host}/token");
            super::super::configure(&p, host, URL, &hook, false, false).unwrap();
            let status = inspect(&p, host, URL, &hook, None);
            assert!(status.mcp_configured && status.hook_installed && !status.events_received);
            assert!(fs::read_to_string(&p.mcp)
                .unwrap()
                .contains("// preference"));
            assert!(super::super::configure(&p, host, URL, &hook, false, false)
                .unwrap()
                .paths
                .is_empty());
            let rotated = hook.replace("/token", "/rotated");
            assert!(!inspect(&p, host, URL, &rotated, Some(chrono::Utc::now())).events_received);
            super::super::configure(&p, host, URL, &rotated, false, false).unwrap();
            super::super::configure(&p, host, URL, &rotated, true, false).unwrap();
            assert!(!inspect(&p, host, URL, &rotated, None).setup_present);
            if host == "cursor" {
                assert!(fs::read_to_string(&p.hooks).unwrap().contains("/* keep */"));
            } else {
                assert!(!p.hooks.exists());
            }
        }
    }
    #[test]
    fn conflict_never_partially_writes_and_disabled_settings_remain() {
        for host in ["cursor", "opencode"] {
            let dir = tempfile::tempdir().unwrap();
            let p = fixture(dir.path(), host);
            let key = if host == "cursor" {
                "mcpServers"
            } else {
                "mcp"
            };
            let hook = format!("http://127.0.0.1:9086/hooks/{host}/test");
            let old = json!({key:{"prism":{"url":"https://other/mcp"}}}).to_string();
            fs::write(&p.mcp, &old).unwrap();
            assert!(super::super::configure(&p, host, URL, &hook, false, false).is_err());
            assert!(!p.hooks.exists());
            assert_eq!(fs::read_to_string(&p.mcp).unwrap(), old);
            fs::write(
                &p.mcp,
                json!({key:{"prism":{"url":URL,"enabled":false,"disabled":true}}}).to_string(),
            )
            .unwrap();
            super::super::configure(&p, host, URL, &hook, false, false).unwrap();
            assert!(!inspect(&p, host, URL, &hook, None).mcp_configured);
        }
    }
    #[cfg(unix)]
    #[test]
    fn cursor_observer_is_neutral_when_prism_or_curl_is_absent() {
        use std::process::{Command, Stdio};
        for path in ["/usr/bin:/bin", "/nonexistent"] {
            let full = cursor_command("http://127.0.0.1:9/hooks/cursor/test", false);
            let script = full
                .strip_prefix("sh -c '")
                .unwrap()
                .strip_suffix('\'')
                .unwrap();
            let start = std::time::Instant::now();
            let result = Command::new("/bin/sh")
                .args(["-c", script])
                .env("PATH", path)
                .stdin(Stdio::null())
                .output()
                .unwrap();
            assert!(result.status.success());
            assert_eq!(result.stdout, b"{}");
            assert!(result.stderr.is_empty());
            assert!(start.elapsed() < std::time::Duration::from_secs(3));
        }
    }

    #[test]
    #[ignore = "requires installed Cursor and OpenCode CLIs; isolated homes, no model calls"]
    fn installed_clients_read_generated_setup() {
        for (host, binary, args) in [
            ("cursor", "cursor-agent", vec!["mcp", "list"]),
            ("opencode", "opencode", vec!["debug", "config"]),
        ] {
            let temp = tempfile::tempdir().unwrap();
            let home = temp.path().canonicalize().unwrap();
            let p = if host == "cursor" {
                Paths {
                    mcp: home.join(".cursor/mcp.json"),
                    hooks: home.join(".cursor/hooks.json"),
                    codex: false,
                }
            } else {
                Paths {
                    mcp: home.join(".config/opencode/opencode.json"),
                    hooks: home.join(".config/opencode/plugins/prism.js"),
                    codex: false,
                }
            };
            let endpoint = "http://127.0.0.1:9/mcp";
            let hook = format!("http://127.0.0.1:9/hooks/{host}/fixture");
            super::super::configure(&p, host, endpoint, &hook, false, false).unwrap();
            let output = std::process::Command::new(binary)
                .args(args)
                .current_dir(&home)
                .env("PWD", &home)
                .env("HOME", &home)
                .env("USERPROFILE", &home)
                .env("XDG_CONFIG_HOME", home.join(".config"))
                .env("XDG_DATA_HOME", home.join(".local/share"))
                .env("XDG_CACHE_HOME", home.join(".cache"))
                .env("XDG_STATE_HOME", home.join(".state"))
                .env_remove("OPENCODE_CONFIG")
                .env_remove("OPENCODE_CONFIG_DIR")
                .env_remove("OPENCODE_CONFIG_CONTENT")
                .env_remove("CURSOR_CONFIG_DIR")
                .output()
                .expect("installed CLI");
            assert!(
                output.status.success(),
                "{host} rejected generated configuration"
            );
            let text = String::from_utf8_lossy(&output.stdout);
            assert!(
                text.contains("prism"),
                "{host} did not read Prism's global entry"
            );
            if host == "opencode" {
                assert!(text.contains(endpoint));
                assert!(
                    text.contains("plugins/prism.js"),
                    "OpenCode did not discover the bundled observer"
                );
            }
        }
    }
}
