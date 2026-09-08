//! Goose 1.49 and the current Antigravity CLI global customization surface.
//! This module only plans edits. The parent transaction owns writes and rollback.
//! See fixtures/harness_extra/SOURCES.md for the versioned format evidence.
use super::{json_edit, local_url, read, Edit, Paths, Setup};
use chrono::{DateTime, Utc};
use serde_json::{json, Value};
use serde_yaml_ng::{Mapping, Value as Yaml};
use std::{
    fs,
    ops::Range,
    path::{Path, PathBuf},
    time::SystemTime,
};

const GOOSE_PLUGIN: &str = "prism-goose";
const OBSERVER: &str = "prism-observer";

pub(super) fn paths(host: &str, home: &Path) -> Result<Paths, String> {
    resolve_paths(
        host,
        home,
        cfg!(windows),
        std::env::var_os("GOOSE_PATH_ROOT").map(PathBuf::from),
        std::env::var_os("XDG_CONFIG_HOME").map(PathBuf::from),
        std::env::var_os("APPDATA").map(PathBuf::from),
    )
}

fn resolve_paths(
    host: &str,
    home: &Path,
    windows: bool,
    goose_root: Option<PathBuf>,
    xdg: Option<PathBuf>,
    appdata: Option<PathBuf>,
) -> Result<Paths, String> {
    let (mcp, hooks) = match host {
        "goose" => {
            let (config, root) = if let Some(root) = goose_root {
                if !root.is_absolute() {
                    return Err("GOOSE_PATH_ROOT must be absolute".into());
                }
                (root.join("config"), root)
            } else {
                let config = if windows {
                    appdata
                        .unwrap_or_else(|| home.join("AppData/Roaming"))
                        .join("Block/goose/config")
                } else {
                    xdg.filter(|p| p.is_absolute())
                        .unwrap_or_else(|| home.join(".config"))
                        .join("goose")
                };
                (config, home.to_path_buf())
            };
            (
                config.join("config.yaml"),
                root.join(".agents/plugins/prism-goose/hooks/hooks.json"),
            )
        }
        "antigravity" => (
            home.join(".gemini/config/mcp_config.json"),
            home.join(".gemini/config/hooks.json"),
        ),
        _ => return Err("Unknown extra harness".into()),
    };
    Ok(Paths {
        mcp,
        hooks,
        codex: false,
    })
}

fn plugin_root(paths: &Paths) -> Result<&Path, String> {
    paths
        .hooks
        .parent()
        .and_then(Path::parent)
        .ok_or_else(|| "Invalid Goose plugin path".into())
}

fn goose_settings(paths: &Paths) -> Result<PathBuf, String> {
    // This settings file deliberately ignores XDG_CONFIG_HOME, just as Goose does.
    let root = plugin_root(paths)?
        .parent()
        .and_then(Path::parent)
        .and_then(Path::parent)
        .ok_or("Invalid Goose plugin path")?;
    Ok(root.join(".config/goose/settings.json"))
}

fn script_path(paths: &Paths, host: &str) -> Result<PathBuf, String> {
    match host {
        "goose" => Ok(plugin_root(paths)?.join("scripts/observe.sh")),
        "antigravity" => Ok(paths
            .hooks
            .parent()
            .ok_or("Invalid hooks path")?
            .join("prism-observer.sh")),
        _ => Err("Unknown extra harness".into()),
    }
}

fn shell_quote(value: &str) -> String {
    format!("'{}'", value.replace('\'', "'\"'\"'"))
}

fn command(paths: &Paths, host: &str) -> Result<String, String> {
    let path = script_path(paths, host)?;
    let text = path.to_str().ok_or("Observer path must be UTF-8")?;
    // Goose invokes sh on Windows too. Forward slashes work in both shell families.
    let text = if cfg!(windows) {
        text.replace('\\', "/")
    } else {
        text.into()
    };
    Ok(format!("sh {}", shell_quote(&text)))
}

fn observer_runtime() -> Result<(), String> {
    #[cfg(windows)]
    {
        let present = std::env::var_os("PATH").is_some_and(|path| {
            std::env::split_paths(&path).any(|directory| {
                directory.join("sh.exe").is_file() || directory.join("sh").is_file()
            })
        });
        if !present {
            return Err("Native observation requires sh from Git for Windows (Git Bash) on PATH. Native Windows execution has not been validated.".into());
        }
    }
    Ok(())
}

fn hook_config(paths: &Paths, host: &str) -> Result<Value, String> {
    let action = json!({"type":"command", "command":command(paths, host)?, "timeout":2});
    match host {
        "goose" => Ok(json!({"hooks":{"PreToolUseResult":[{"hooks":[action]}]}})),
        "antigravity" => Ok(json!({OBSERVER:{"PostToolUse":[{"matcher":".*", "hooks":[action]}]}})),
        _ => Err("Unknown extra harness".into()),
    }
}

fn valid_hook_url(url: &str, host: &str) -> bool {
    url.rsplit_once('/').is_some_and(|(base, token)| {
        !token.is_empty()
            && token
                .bytes()
                .all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_')
            && local_url(base, &format!("hooks/{host}"))
    })
}

fn script(host: &str, url: &str) -> Result<Vec<u8>, String> {
    if !matches!(host, "goose" | "antigravity") || !valid_hook_url(url, host) {
        return Err("Invalid local observer endpoint".into());
    }
    let input = if host == "antigravity" {
        // No shell evaluation or JSON rewriting of native stdin. The server validates
        // the wrapper and the complete original camelCase payload before recording it.
        "{ printf '%s' '{\"hook_event_name\":\"PostToolUse\",\"payload\":'; cat; printf '%s' '}'; } 2>/dev/null | "
    } else {
        ""
    };
    Ok(format!("#!/bin/sh\n# Prism {host} observer v1. Managed by Prism.\nprism_curl=curl\nprism_null=/dev/null\ncase \"${{OS:-}}\" in Windows_NT) prism_curl=curl.exe; prism_null=NUL ;; esac\n{input}\"$prism_curl\" --disable --silent --connect-timeout 1 --max-time 1 --noproxy '*' --proto '=http' --output \"$prism_null\" --request POST --header 'Content-Type: application/json' --data-binary @- {} >/dev/null 2>&1 || :\nprintf '%s\\n' '{{}}'\nexit 0\n", shell_quote(url)).into_bytes())
}

fn owned_script(bytes: &[u8], host: &str) -> bool {
    let Ok(text) = std::str::from_utf8(bytes) else {
        return false;
    };
    let Some((_, suffix)) = text.split_once("--data-binary @- '") else {
        return false;
    };
    let Some((url, _)) = suffix.split_once('\'') else {
        return false;
    };
    script(host, url).is_ok_and(|expected| expected == bytes)
}

fn manifest() -> Value {
    json!({"name":GOOSE_PLUGIN, "version":"1.0.0", "description":"Prism native tool observation for Goose 1.49 or later"})
}

fn encoded(value: &Value) -> Vec<u8> {
    format!(
        "{}\n",
        serde_json::to_string_pretty(value).expect("JSON value is serializable")
    )
    .into_bytes()
}

pub(super) fn snippet(host: &str, hook_url: &str) -> Result<String, String> {
    let paths = paths(host, &super::super::home_path()?)?;
    // Include the actual generated script as well: a hook pointing to an absent
    // helper alone is not a usable manual setup recipe.
    let manifest = if host == "goose" {
        format!(
            "{}\n{}\n",
            plugin_root(&paths)?.join("plugin.json").display(),
            String::from_utf8(encoded(&manifest())).unwrap()
        )
    } else {
        String::new()
    };
    Ok(format!(
        "{manifest}{}\n\n{}\n{}\n{}",
        paths.hooks.display(),
        String::from_utf8(encoded(&hook_config(&paths, host)?)).unwrap(),
        script_path(&paths, host)?.display(),
        String::from_utf8(script(host, hook_url)?).unwrap()
    ))
}

fn checked_read(path: &Path) -> Result<Option<Vec<u8>>, String> {
    for ancestor in path.ancestors() {
        match fs::symlink_metadata(ancestor) {
            Ok(meta) if meta.file_type().is_symlink() => {
                return Err(format!(
                    "{} is a symlink; update its target manually",
                    ancestor.display()
                ))
            }
            Ok(_) => (),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
            Err(_) => return Err(format!("Could not inspect {}", ancestor.display())),
        }
    }
    read(path)
}

fn edit(out: &mut Vec<Edit>, path: PathBuf, old: Option<Vec<u8>>, new: Option<Vec<u8>>) {
    if old != new {
        out.push(Edit { path, old, new });
    }
}

fn owned_file(
    out: &mut Vec<Edit>,
    path: PathBuf,
    expected: Vec<u8>,
    remove: bool,
    is_owned: impl FnOnce(&[u8]) -> bool,
) -> Result<(), String> {
    let old = checked_read(&path)?;
    if old.as_deref().is_some_and(|b| !is_owned(b)) {
        return Err(format!(
            "{} conflicts with Prism's observer; preserve or rename it first",
            path.display()
        ));
    }
    edit(out, path, old, (!remove).then_some(expected));
    Ok(())
}

fn goose_artifacts(paths: &Paths, url: &str, remove: bool) -> Result<Vec<Edit>, String> {
    let root = plugin_root(paths)?;
    if !remove && checked_read(&root.join("plugin.json"))?.is_none() {
        // Do not claim an unrelated pre-existing plugin directory. Known orphaned
        // artifacts are repairable; transaction backups are deliberately retained.
        for (directory, allowed) in [
            (root.to_path_buf(), &["hooks", "scripts", "plugin.json"][..]),
            (root.join("hooks"), &["hooks.json"][..]),
            (root.join("scripts"), &["observe.sh"][..]),
        ] {
            match fs::read_dir(&directory) {
                Ok(entries) => {
                    for entry in entries {
                        let entry =
                            entry.map_err(|_| "Could not inspect Goose plugin directory")?;
                        let name = entry.file_name();
                        let name = name.to_str().ok_or("Conflicting Goose plugin artifact")?;
                        let backup = allowed.iter().any(|allowed| {
                            name.starts_with(&format!("{allowed}.prism-")) && name.ends_with(".bak")
                        });
                        if !allowed.contains(&name) && !backup {
                            return Err("The prism-goose directory contains an unrelated plugin; preserve or rename it first".into());
                        }
                    }
                }
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(_) => return Err("Could not inspect Goose plugin directory".into()),
            }
        }
    }
    let mut out = vec![];
    let manifest = encoded(&manifest());
    let hooks = encoded(&hook_config(paths, "goose")?);
    owned_file(
        &mut out,
        root.join("plugin.json"),
        manifest.clone(),
        remove,
        |b| b == manifest,
    )?;
    owned_file(&mut out, paths.hooks.clone(), hooks.clone(), remove, |b| {
        b == hooks
    })?;
    owned_file(
        &mut out,
        script_path(paths, "goose")?,
        script("goose", url)?,
        remove,
        |b| owned_script(b, "goose"),
    )?;
    Ok(out)
}

fn json_mcp(doc: &mut Value, url: &str, remove: bool) -> Result<(), String> {
    if remove && doc.get("mcpServers").is_none() {
        return Ok(());
    }
    let servers = doc
        .as_object_mut()
        .ok_or("MCP config must be an object")?
        .entry("mcpServers")
        .or_insert(json!({}))
        .as_object_mut()
        .ok_or("mcpServers must be an object")?;
    if let Some(entry) = servers.get("prism") {
        validate_ag_mcp(entry)?;
    }
    if remove {
        servers.remove("prism");
    } else {
        let entry = servers
            .entry("prism")
            .or_insert(json!({}))
            .as_object_mut()
            .ok_or("prism must be an object")?;
        entry.insert("serverUrl".into(), json!(url));
    }
    Ok(())
}

fn validate_ag_mcp(entry: &Value) -> Result<(), String> {
    if !entry["serverUrl"]
        .as_str()
        .is_some_and(|u| local_url(u, "mcp"))
        || ["command", "url", "httpUrl"]
            .iter()
            .any(|k| entry.get(k).is_some())
    {
        return Err(
            "The name prism has a conflicting Antigravity MCP configuration; rename it first"
                .into(),
        );
    }
    if entry.get("disabled").is_some_and(|v| !v.is_boolean()) {
        return Err("Antigravity prism.disabled must be a boolean".into());
    }
    Ok(())
}

fn validate_ag_hook(entry: &Value, expected: &Value) -> Result<(), String> {
    let mut entry = entry.clone();
    let object = entry
        .as_object_mut()
        .ok_or("Prism observer must be an object")?;
    if object.remove("enabled").is_some_and(|v| !v.is_boolean()) {
        return Err("Prism observer enabled must be a boolean".into());
    }
    if &entry != expected {
        return Err(
            "The prism-observer hook conflicts with Prism; preserve or rename it first".into(),
        );
    }
    Ok(())
}

/// Preflight every document/artifact before returning any work to the transaction.
pub(super) fn edits(
    paths: &Paths,
    host: &str,
    url: &str,
    hook_url: &str,
    remove: bool,
    hooks_only: bool,
) -> Result<Vec<Edit>, String> {
    if !remove {
        observer_runtime()?;
    }
    if !local_url(url, "mcp") {
        return Err("Invalid local MCP endpoint".into());
    }
    let mut out = match host {
        "goose" => {
            // A disabled plugin is deliberate; repair must not reactivate it.
            let old = checked_read(&paths.mcp)?;
            let doc = yaml_config(old.as_deref(), &paths.mcp)?;
            goose_disabled(paths, &doc)?;
            let mut out = goose_artifacts(paths, hook_url, remove)?;
            if !hooks_only && (old.is_some() || !remove) {
                let new = goose_mcp(old.as_deref(), &paths.mcp, url, remove)?;
                edit(&mut out, paths.mcp.clone(), old, Some(new));
            }
            out
        }
        "antigravity" => {
            let old = checked_read(&paths.hooks)?;
            let mut doc = json_edit::value(old.as_deref(), &paths.hooks)?;
            let expected = hook_config(paths, host)?;
            if let Some(entry) = doc.get(OBSERVER) {
                validate_ag_hook(entry, &expected[OBSERVER])?;
            }
            if remove {
                doc.as_object_mut().unwrap().remove(OBSERVER);
            } else if doc.get(OBSERVER).is_none() {
                doc[OBSERVER] = expected[OBSERVER].clone();
            }
            let mut out = vec![];
            if old.is_some() || !remove {
                let new = json_edit::update(old.as_deref(), &paths.hooks, &doc)?;
                edit(&mut out, paths.hooks.clone(), old, Some(new));
            }
            owned_file(
                &mut out,
                script_path(paths, host)?,
                script(host, hook_url)?,
                remove,
                |b| owned_script(b, host),
            )?;
            if !hooks_only {
                let old = checked_read(&paths.mcp)?;
                let mut doc = json_edit::value(old.as_deref(), &paths.mcp)?;
                json_mcp(&mut doc, url, remove)?;
                if old.is_some() || !remove {
                    let new = json_edit::update(old.as_deref(), &paths.mcp, &doc)?;
                    edit(&mut out, paths.mcp.clone(), old, Some(new));
                }
            }
            out
        }
        _ => return Err("Unknown extra harness".into()),
    };
    out.sort_by(|a, b| a.path.cmp(&b.path));
    Ok(out)
}

fn goose_disabled(paths: &Paths, doc: &Yaml) -> Result<bool, String> {
    let mut disabled = false;
    if let Some(plugins) = doc.get("plugins") {
        let entries = plugins
            .as_mapping()
            .ok_or("Goose plugins must be a mapping")?;
        let key = plugin_root(paths)?
            .to_str()
            .ok_or("Goose plugin path must be UTF-8")?;
        if let Some(entry) = entries.get(Yaml::String(key.into())) {
            disabled = !entry
                .get("enabled")
                .and_then(Yaml::as_bool)
                .ok_or("Goose plugin enabled must be a boolean")?;
        }
    }
    let path = goose_settings(paths)?;
    let bytes = checked_read(&path)?;
    let settings = json_edit::value(bytes.as_deref(), &path)?;
    for field in ["enabledPlugins", "disabledPlugins"] {
        if let Some(value) = settings.get(field) {
            let names = value
                .as_array()
                .filter(|v| v.iter().all(Value::is_string))
                .ok_or("Goose plugin settings must contain arrays of names")?;
            if field == "disabledPlugins" {
                disabled |= names.iter().any(|v| v == GOOSE_PLUGIN);
            }
        }
    }
    Ok(disabled)
}

pub(super) fn inspect(
    paths: &Paths,
    host: &str,
    url: &str,
    hook_url: &str,
    last: Option<DateTime<Utc>>,
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
        let mut evidence = vec![
            paths.mcp.clone(),
            paths.hooks.clone(),
            script_path(paths, host)?,
        ];
        let old = checked_read(&paths.mcp)?;
        match host {
            "goose" => {
                let doc = yaml_config(old.as_deref(), &paths.mcp)?;
                if let Some(entry) = goose_entry(&doc)? {
                    validate_goose_entry(entry)?;
                    setup.setup_present = true;
                    setup.mcp_configured = entry.get("uri").and_then(Yaml::as_str) == Some(url)
                        && entry.get("type").and_then(Yaml::as_str) == Some("streamable_http")
                        && entry.get("enabled").and_then(Yaml::as_bool) == Some(true);
                }
                setup.hooks_disabled = goose_disabled(paths, &doc)?;
                let manifest_path = plugin_root(paths)?.join("plugin.json");
                evidence.extend([manifest_path.clone(), goose_settings(paths)?]);
                let manifest_bytes = checked_read(&manifest_path)?;
                let hook_bytes = checked_read(&paths.hooks)?;
                let script_bytes = checked_read(&script_path(paths, host)?)?;
                let manifest_ok =
                    manifest_bytes.as_deref() == Some(encoded(&manifest()).as_slice());
                let hooks_ok =
                    hook_bytes.as_deref() == Some(encoded(&hook_config(paths, host)?).as_slice());
                setup.setup_present |= manifest_ok
                    || hooks_ok
                    || script_bytes
                        .as_deref()
                        .is_some_and(|b| owned_script(b, host));
                // Same preflight detects modified/foreign artifacts without changing them.
                goose_artifacts(paths, hook_url, false)?;
                setup.hook_installed = manifest_ok
                    && hooks_ok
                    && script_bytes.as_deref() == Some(script(host, hook_url)?.as_slice());
            }
            "antigravity" => {
                let doc = json_edit::value(old.as_deref(), &paths.mcp)?;
                if doc.get("mcpServers").is_some_and(|v| !v.is_object()) {
                    return Err("mcpServers must be an object".into());
                }
                if let Some(entry) = doc.pointer("/mcpServers/prism") {
                    validate_ag_mcp(entry)?;
                    setup.setup_present = true;
                    setup.mcp_configured = entry["serverUrl"] == url && entry["disabled"] != true;
                }
                let doc = json_edit::value(checked_read(&paths.hooks)?.as_deref(), &paths.hooks)?;
                let script_bytes = checked_read(&script_path(paths, host)?)?;
                if let Some(bytes) = &script_bytes {
                    if !owned_script(bytes, host) {
                        return Err("Antigravity observer script conflicts with Prism".into());
                    }
                    setup.setup_present = true;
                }
                if let Some(entry) = doc.get(OBSERVER) {
                    validate_ag_hook(entry, &hook_config(paths, host)?[OBSERVER])?;
                    setup.setup_present = true;
                    setup.hooks_disabled = entry["enabled"] == false;
                    setup.hook_installed =
                        script_bytes.as_deref() == Some(script(host, hook_url)?.as_slice());
                }
            }
            _ => return Err("Unknown extra harness".into()),
        }
        // Every document that can change loading or enablement participates in the
        // evidence boundary, including the plugin manifest and observer source.
        let mut modified = None;
        for path in evidence {
            match fs::metadata(&path) {
                Ok(metadata) => {
                    let at = metadata
                        .modified()
                        .map_err(|_| "Could not establish observer configuration age")?;
                    modified = Some(modified.map_or(at, |previous: SystemTime| previous.max(at)));
                }
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
                Err(_) => return Err("Could not establish observer configuration age".into()),
            }
        }
        if setup.hook_installed {
            observer_runtime()?;
        }
        setup.events_received = setup.hook_installed
            && !setup.hooks_disabled
            && last.is_some_and(|at| modified.is_some_and(|m| SystemTime::from(at) >= m));
        Ok::<_, String>(())
    })();
    if let Err(error) = result {
        setup.problem = Some(error);
        setup.events_received = false;
    }
    setup
}

fn yaml_config(bytes: Option<&[u8]>, path: &Path) -> Result<Yaml, String> {
    let text =
        std::str::from_utf8(bytes.unwrap_or(b"")).map_err(|_| "Goose config must be UTF-8")?;
    if text
        .lines()
        .all(|l| l.trim().is_empty() || l.trim_start().starts_with('#'))
    {
        return Ok(Yaml::Mapping(Mapping::new()));
    }
    let value: Yaml = serde_yaml_ng::from_str(text)
        .map_err(|_| format!("Fix invalid or duplicate YAML in {} first", path.display()))?;
    if !value.is_mapping() {
        return Err("Goose config must contain a mapping".into());
    }
    Ok(value)
}

fn goose_entry(doc: &Yaml) -> Result<Option<&Yaml>, String> {
    let Some(entries) = doc.get("extensions") else {
        return Ok(None);
    };
    let map = entries
        .as_mapping()
        .ok_or("Goose extensions must be a mapping")?;
    for (key, entry) in map {
        let key = key
            .as_str()
            .ok_or("Goose extension names must be strings")?;
        let normal = |s: &str| {
            s.chars()
                .filter(|c| !c.is_whitespace())
                .map(|c| {
                    if c.is_ascii_alphanumeric() || c == '_' || c == '-' {
                        c.to_ascii_lowercase()
                    } else {
                        '_'
                    }
                })
                .collect::<String>()
        };
        if key != "prism"
            && (normal(key) == "prism"
                || entry
                    .get("name")
                    .and_then(Yaml::as_str)
                    .is_some_and(|n| normal(n) == "prism"))
        {
            return Err(
                "Another Goose extension already uses the Prism name; rename it first".into(),
            );
        }
    }
    Ok(entries.get("prism"))
}

fn validate_goose_entry(entry: &Yaml) -> Result<(), String> {
    if !entry
        .get("uri")
        .and_then(Yaml::as_str)
        .is_some_and(|u| local_url(u, "mcp"))
        || entry
            .get("type")
            .is_some_and(|v| v.as_str() != Some("streamable_http"))
        || entry
            .get("name")
            .is_some_and(|v| v.as_str() != Some("prism"))
        || ["cmd", "command", "url"]
            .iter()
            .any(|k| entry.get(*k).is_some())
    {
        return Err("The name prism has a conflicting Goose extension; rename it first".into());
    }
    if entry.get("enabled").is_some_and(|v| !v.is_bool()) {
        return Err("Goose prism.enabled must be a boolean".into());
    }
    Ok(())
}

#[derive(Clone)]
struct YamlLine {
    start: usize,
    end: usize,
    indent: usize,
    code: Range<usize>,
    comment: Option<usize>,
}

fn yaml_lines(text: &str) -> Vec<YamlLine> {
    let mut start = 0;
    text.split_inclusive('\n')
        .map(|line| {
            let end = start + line.len();
            let body = line.trim_end_matches(['\r', '\n']);
            let indent = body.bytes().take_while(|c| *c == b' ').count();
            let comment = yaml_separator(body, b'#')
                .filter(|i| *i == 0 || body.as_bytes()[i - 1].is_ascii_whitespace());
            let code_end = body[..comment.unwrap_or(body.len())].trim_end().len();
            let out = YamlLine {
                start,
                end,
                indent,
                code: (start + indent).min(start + code_end)..start + code_end,
                comment: comment.map(|c| start + c),
            };
            start = end;
            out
        })
        .collect()
}

// This is only a locator for ordinary single-line mapping keys, never a YAML
// parser. Both the entire input and the result go through serde_yaml_ng.
fn yaml_separator(text: &str, needle: u8) -> Option<usize> {
    let bytes = text.as_bytes();
    let (mut quote, mut i) = (0, 0);
    while i < bytes.len() {
        let c = bytes[i];
        if quote == b'"' && c == b'\\' {
            i += 2;
            continue;
        }
        if c == quote && quote != 0 {
            if quote == b'\'' && bytes.get(i + 1) == Some(&quote) {
                i += 2;
                continue;
            }
            quote = 0;
        } else if quote == 0 {
            if matches!(c, b'\'' | b'"') {
                quote = c;
            } else if c == needle
                && (needle != b'#' || i == 0 || bytes[i - 1].is_ascii_whitespace())
            {
                return Some(i);
            }
        }
        i += 1;
    }
    None
}

#[derive(Clone)]
struct BlockEntry {
    key: String,
    line: usize,
    end: usize,
    value: Range<usize>,
}

fn block_entries(
    text: &str,
    lines: &[YamlLine],
    range: Range<usize>,
    indent: usize,
) -> Result<Vec<BlockEntry>, String> {
    let mut entries: Vec<BlockEntry> = vec![];
    for i in range.clone() {
        let line = &lines[i];
        if line.code.is_empty() {
            continue;
        }
        if line.indent < indent {
            return Err("Unsupported Goose YAML indentation".into());
        }
        if line.indent > indent {
            continue;
        }
        let code = &text[line.code.clone()];
        if indent == 0 && code == "---" && entries.is_empty() {
            continue;
        }
        let colon = yaml_separator(code, b':')
            .ok_or("Use ordinary block mappings for Goose extensions before editing")?;
        let key: Yaml =
            serde_yaml_ng::from_str(&code[..colon]).map_err(|_| "Unsupported Goose mapping key")?;
        let key = key
            .as_str()
            .ok_or("Goose mapping keys must be strings")?
            .to_string();
        if key == "<<" {
            return Err("YAML merge keys cannot be safely edited here".into());
        }
        if let Some(previous) = entries.last_mut() {
            previous.end = i;
        }
        let value_start = line.code.start + colon + 1;
        let offset = text[value_start..line.code.end].len()
            - text[value_start..line.code.end].trim_start().len();
        entries.push(BlockEntry {
            key,
            line: i,
            end: range.end,
            value: value_start + offset..line.code.end,
        });
    }
    Ok(entries)
}

fn child_entries(
    text: &str,
    lines: &[YamlLine],
    entry: &BlockEntry,
) -> Result<Vec<BlockEntry>, String> {
    if !entry.value.is_empty() {
        return Err("Use a block mapping for Goose extensions/prism before editing; flow mappings and aliases are preserved".into());
    }
    let indent = (entry.line + 1..entry.end)
        .filter(|i| !lines[*i].code.is_empty())
        .map(|i| lines[i].indent)
        .min()
        .unwrap_or(lines[entry.line].indent + 2);
    if indent <= lines[entry.line].indent {
        return Err("Unsupported Goose mapping indentation".into());
    }
    block_entries(text, lines, entry.line + 1..entry.end, indent)
}

fn goose_mcp(
    bytes: Option<&[u8]>,
    path: &Path,
    url: &str,
    remove: bool,
) -> Result<Vec<u8>, String> {
    let mut desired = yaml_config(bytes, path)?;
    if let Some(entry) = goose_entry(&desired)? {
        validate_goose_entry(entry)?;
    }
    let mut text = std::str::from_utf8(bytes.unwrap_or(b""))
        .map_err(|_| "Goose config must be UTF-8")?
        .to_string();
    let original = desired.clone();
    if remove {
        if let Some(extensions) = desired.get_mut("extensions").and_then(Yaml::as_mapping_mut) {
            extensions.remove(Yaml::String("prism".into()));
        }
    } else {
        let root = desired.as_mapping_mut().unwrap();
        let extensions = root
            .entry(Yaml::String("extensions".into()))
            .or_insert(Yaml::Mapping(Mapping::new()))
            .as_mapping_mut()
            .ok_or("Goose extensions must be a mapping")?;
        let entry = extensions
            .entry(Yaml::String("prism".into()))
            .or_insert(Yaml::Mapping(Mapping::new()))
            .as_mapping_mut()
            .ok_or("Goose prism must be a mapping")?;
        for (key, value) in [
            ("name", Yaml::String("prism".into())),
            ("type", Yaml::String("streamable_http".into())),
            ("uri", Yaml::String(url.into())),
        ] {
            entry.insert(Yaml::String(key.into()), value);
        }
        entry
            .entry(Yaml::String("enabled".into()))
            .or_insert(Yaml::Bool(true));
    }
    if desired == original {
        return Ok(text.into_bytes());
    }
    let newline = if text.contains("\r\n") { "\r\n" } else { "\n" };
    let new_entry = |indent: usize| {
        format!("{}prism:{newline}{}name: prism{newline}{}type: streamable_http{newline}{}uri: {url}{newline}{}enabled: true{newline}", " ".repeat(indent), " ".repeat(indent+2), " ".repeat(indent+2), " ".repeat(indent+2), " ".repeat(indent+2))
    };
    if text.trim() == "{}" {
        text.clear();
    }
    let lines = yaml_lines(&text);
    let roots = block_entries(&text, &lines, 0..lines.len(), 0)?;
    let mut replacements: Vec<(Range<usize>, String)> = vec![];
    if let Some(extensions) = roots.iter().find(|e| e.key == "extensions") {
        if &text[extensions.value.clone()] == "{}"
            && original
                .get("extensions")
                .and_then(Yaml::as_mapping)
                .is_some_and(Mapping::is_empty)
        {
            replacements.push((extensions.value.clone(), String::new()));
            let end = lines[extensions.line].end;
            let prefix = if text[..end].ends_with('\n') {
                ""
            } else {
                newline
            };
            replacements.push((end..end, format!("{prefix}{}", new_entry(2))));
        } else {
            let entries = child_entries(&text, &lines, extensions)?;
            if let Some(prism) = entries.iter().find(|e| e.key == "prism") {
                let fields = child_entries(&text, &lines, prism)?;
                if remove {
                    let end = lines.get(prism.end).map_or(text.len(), |l| l.start);
                    let mut comments = String::new();
                    for line in &lines[prism.line..prism.end] {
                        if line.code.is_empty() {
                            comments.push_str(&text[line.start..line.end]);
                        } else if let Some(comment) = line.comment {
                            comments.push_str(&" ".repeat(line.indent));
                            comments.push_str(&text[comment..line.end]);
                        }
                    }
                    replacements.push((lines[prism.line].start..end, comments));
                    if entries.len() == 1 {
                        replacements.push((extensions.value.clone(), " {}".into()));
                    }
                } else {
                    let indent = fields
                        .first()
                        .map_or(lines[prism.line].indent + 2, |e| lines[e.line].indent);
                    let mut missing = String::new();
                    for (key, value) in [
                        ("name", "prism"),
                        ("type", "streamable_http"),
                        ("uri", url),
                        ("enabled", "true"),
                    ] {
                        if let Some(field) = fields.iter().find(|e| e.key == key) {
                            if key != "enabled"
                                && original["extensions"]["prism"].get(key)
                                    != desired["extensions"]["prism"].get(key)
                            {
                                if field.value.is_empty()
                                    || lines[field.line + 1..field.end]
                                        .iter()
                                        .any(|l| !l.code.is_empty())
                                {
                                    return Err("Use single-line scalar fields for Goose prism before editing".into());
                                }
                                replacements.push((field.value.clone(), value.into()));
                            }
                        } else {
                            missing.push_str(&format!(
                                "{}{key}: {value}{newline}",
                                " ".repeat(indent)
                            ));
                        }
                    }
                    if !missing.is_empty() {
                        let pos = lines[prism.line].end;
                        let prefix = if text[..pos].ends_with('\n') {
                            ""
                        } else {
                            newline
                        };
                        replacements.push((pos..pos, format!("{prefix}{missing}")));
                    }
                }
            } else {
                let indent = entries.first().map_or(2, |e| lines[e.line].indent);
                let pos = lines[extensions.line].end;
                let prefix = if text[..pos].ends_with('\n') {
                    ""
                } else {
                    newline
                };
                replacements.push((pos..pos, format!("{prefix}{}", new_entry(indent))));
            }
        }
    } else {
        let prefix = if text.is_empty() || text.ends_with('\n') {
            ""
        } else {
            newline
        };
        replacements.push((
            text.len()..text.len(),
            format!("{prefix}extensions:{newline}{}", new_entry(2)),
        ));
    }
    replacements.sort_by(|a, b| b.0.start.cmp(&a.0.start));
    for (range, value) in replacements {
        text.replace_range(range, &value);
    }
    if yaml_config(Some(text.as_bytes()), path)? != desired {
        return Err(
            "Cannot safely splice this Goose YAML layout; use ordinary block mappings first".into(),
        );
    }
    Ok(text.into_bytes())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{
        io::{Read, Write},
        net::TcpListener,
        process::{Command, Stdio},
        time::{Duration, Instant},
    };

    const URL: &str = "http://127.0.0.1:39701/mcp";
    fn hook(host: &str) -> String {
        format!("http://127.0.0.1:39701/hooks/{host}/fixture_token")
    }
    fn fixture(host: &str) -> (tempfile::TempDir, Paths) {
        let home = tempfile::Builder::new()
            .prefix("Prism fixture's home ")
            .tempdir()
            .unwrap();
        let canonical_home = home.path().canonicalize().unwrap();
        let paths = resolve_paths(host, &canonical_home, false, None, None, None).unwrap();
        (home, paths)
    }
    fn put(path: &Path, bytes: &[u8]) {
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, bytes).unwrap();
    }
    fn apply(edits: Vec<Edit>) {
        // Tests only, in fixture homes. Production delegates this to the parent
        // transaction, whose backup/rollback behavior has its own coverage.
        for change in edits {
            assert_eq!(read(&change.path).unwrap(), change.old);
            if let Some(bytes) = change.new {
                put(&change.path, &bytes);
            } else {
                fs::remove_file(change.path).unwrap();
            }
        }
    }
    fn install(paths: &Paths, host: &str) {
        apply(edits(paths, host, URL, &hook(host), false, false).unwrap());
    }

    #[test]
    fn documented_paths_and_environment_roots() {
        // Absolute on the host running the test: an XDG root counts only when it is.
        let home = if cfg!(windows) {
            Path::new(r"C:\fixture home")
        } else {
            Path::new("/fixture home")
        };
        let unix = resolve_paths("goose", home, false, None, None, None).unwrap();
        assert_eq!(unix.mcp, home.join(".config/goose/config.yaml"));
        assert_eq!(
            unix.hooks,
            home.join(".agents/plugins/prism-goose/hooks/hooks.json")
        );
        let windows = resolve_paths(
            "goose",
            home,
            true,
            None,
            None,
            Some(home.join("Roaming Data")),
        )
        .unwrap();
        assert_eq!(
            windows.mcp,
            home.join("Roaming Data/Block/goose/config/config.yaml")
        );
        let xdg = resolve_paths("goose", home, false, None, Some(home.join("xdg")), None).unwrap();
        assert_eq!(xdg.mcp, home.join("xdg/goose/config.yaml"));
        assert_eq!(
            goose_settings(&xdg).unwrap(),
            home.join(".config/goose/settings.json")
        );
        let root = resolve_paths(
            "goose",
            home,
            false,
            Some(home.join("isolated")),
            None,
            None,
        )
        .unwrap();
        assert_eq!(root.mcp, home.join("isolated/config/config.yaml"));
        assert_eq!(
            root.hooks,
            home.join("isolated/.agents/plugins/prism-goose/hooks/hooks.json")
        );
        assert_eq!(
            goose_settings(&root).unwrap(),
            home.join("isolated/.config/goose/settings.json")
        );
        assert!(resolve_paths("goose", home, false, Some("relative".into()), None, None).is_err());
        let ag = resolve_paths("antigravity", home, false, None, None, None).unwrap();
        assert_eq!(ag.mcp, home.join(".gemini/config/mcp_config.json"));
        assert_eq!(ag.hooks, home.join(".gemini/config/hooks.json"));
        assert!(resolve_paths("gemini", home, false, None, None, None).is_err());
    }

    #[test]
    fn goose_install_repairs_and_removes_without_losing_comments_or_settings() {
        let (_home, paths) = fixture("goose");
        let source = include_bytes!("fixtures/harness_extra/goose.yaml");
        put(&paths.mcp, source);
        let original = yaml_config(Some(source), &paths.mcp).unwrap();
        let planned = edits(&paths, "goose", URL, &hook("goose"), false, false).unwrap();
        assert!(!paths.hooks.exists(), "planning must not write artifacts");
        assert_eq!(read(&paths.mcp).unwrap().unwrap(), source);
        apply(planned);
        let installed = fs::read_to_string(&paths.mcp).unwrap();
        for line in std::str::from_utf8(source).unwrap().lines() {
            assert!(installed.contains(line), "Lost source line: {line}");
        }
        let setup = inspect(&paths, "goose", URL, &hook("goose"), None);
        assert!(setup.mcp_configured && setup.hook_installed && setup.setup_present);
        assert!(!setup.events_received && setup.problem.is_none());
        assert!(edits(&paths, "goose", URL, &hook("goose"), false, false)
            .unwrap()
            .is_empty());
        let other_file = plugin_root(&paths).unwrap().join("user-note.txt");
        put(&other_file, b"preserve this note");
        let changes = edits(&paths, "goose", URL, &hook("goose"), true, false).unwrap();
        assert_eq!(changes.iter().filter(|e| e.new.is_none()).count(), 3);
        apply(changes);
        assert_eq!(
            yaml_config(read(&paths.mcp).unwrap().as_deref(), &paths.mcp).unwrap(),
            original
        );
        assert_eq!(fs::read(other_file).unwrap(), b"preserve this note");
        assert!(!paths.hooks.exists());
        // Removal is a no-op even if user notes outlive the removed plugin.
        assert!(edits(&paths, "goose", URL, &hook("goose"), true, false)
            .unwrap()
            .is_empty());
    }

    #[test]
    fn goose_repair_preserves_disabled_state_and_inline_comments() {
        let (_home, paths) = fixture("goose");
        let source = "# keep\nextensions: # heading\n  prism: # own heading\n    uri: 'http://localhost:1111/mcp' # saved endpoint\n    type: streamable_http\n    enabled: false # deliberate\n    available_tools: [keep_this]\n  other: {name: other, enabled: false, type: builtin}\n";
        put(&paths.mcp, source.as_bytes());
        install(&paths, "goose");
        let text = fs::read_to_string(&paths.mcp).unwrap();
        assert!(text.contains("enabled: false # deliberate"));
        assert!(text.contains("# saved endpoint"));
        assert!(text.contains("available_tools: [keep_this]"));
        assert!(!inspect(&paths, "goose", URL, &hook("goose"), None).mcp_configured);
        apply(edits(&paths, "goose", URL, &hook("goose"), true, false).unwrap());
        let text = fs::read_to_string(&paths.mcp).unwrap();
        for comment in [
            "# keep",
            "# heading",
            "# own heading",
            "# saved endpoint",
            "# deliberate",
        ] {
            assert!(text.contains(comment));
        }
        assert!(
            !yaml_config(Some(text.as_bytes()), &paths.mcp).unwrap()["extensions"]
                .as_mapping()
                .unwrap()
                .contains_key("prism")
        );
    }

    #[test]
    fn goose_rejects_ambiguous_yaml_and_name_or_transport_collisions() {
        let (_home, paths) = fixture("goose");
        for source in [
            "extensions: {}\nextensions: {}\n",
            "extensions:\n  prism: {uri: 'http://127.0.0.1:1/mcp'}\n  prism: {}\n",
            "extensions: {prism: {uri: 'http://127.0.0.1:1/mcp', enabled: true, type: streamable_http}}\n",
            "extensions:\n  prism:\n    uri: https://example.com/private-fixture\n",
            "extensions:\n  prism:\n    uri: http://127.0.0.1:1/mcp\n    type: stdio\n",
            "extensions:\n  other: {name: prism, type: builtin}\n",
            "extensions:\n  PRISM: {name: other, type: builtin}\n",
            "extensions:\n  <<: {prism: {uri: 'http://127.0.0.1:1/mcp'}}\n",
            "defaults: &defs {enabled: true}\nextensions: *defs\n",
            "---\nextensions: {}\n---\nother: true\n",
            "extensions: []\n",
            "extensions:\n  prism:\n    uri: http://127.0.0.1:1/mcp\n    enabled: maybe\n",
        ] {
            put(&paths.mcp, source.as_bytes());
            let error = edits(&paths, "goose", URL, &hook("goose"), false, false).err().expect("must reject unsupported config");
            assert!(!error.contains("private-fixture"), "Errors must not echo config values");
            assert_eq!(fs::read(&paths.mcp).unwrap(), source.as_bytes());
            assert!(!paths.hooks.exists());
        }
    }

    #[test]
    fn goose_empty_mapping_crlf_and_missing_final_newline() {
        let (_home, paths) = fixture("goose");
        for source in [
            "",
            "{}",
            "# empty",
            "extensions: {} # empty\r\n",
            "theme: dark\r\nextensions:\r\n  other: {type: builtin}",
        ] {
            let installed = goose_mcp(Some(source.as_bytes()), &paths.mcp, URL, false).unwrap();
            assert!(
                yaml_config(Some(&installed), &paths.mcp).unwrap()["extensions"]
                    .get("prism")
                    .is_some()
            );
            let removed = goose_mcp(Some(&installed), &paths.mcp, URL, true).unwrap();
            let root = yaml_config(Some(&removed), &paths.mcp).unwrap();
            assert!(root["extensions"]
                .as_mapping()
                .unwrap()
                .get("prism")
                .is_none());
            if source.contains("# empty") {
                assert!(std::str::from_utf8(&removed).unwrap().contains("# empty"));
            }
            if source.contains("\r\n") {
                assert!(!std::str::from_utf8(&installed)
                    .unwrap()
                    .replace("\r\n", "")
                    .contains('\n'));
            }
        }
    }

    #[test]
    fn goose_disabled_plugin_sources_are_preserved_and_invalidate_evidence() {
        let (_home, paths) = fixture("goose");
        install(&paths, "goose");
        let settings = goose_settings(&paths).unwrap();
        put(
            &settings,
            br#"{"disabledPlugins":["prism-goose"],"enabledPlugins":["prism-goose"]}"#,
        );
        let expected = fs::read(&settings).unwrap();
        assert!(edits(&paths, "goose", URL, &hook("goose"), false, true)
            .unwrap()
            .is_empty());
        let status = inspect(
            &paths,
            "goose",
            URL,
            &hook("goose"),
            Some(Utc::now() + chrono::Duration::seconds(1)),
        );
        assert!(status.hook_installed && status.hooks_disabled && !status.events_received);
        assert_eq!(fs::read(&settings).unwrap(), expected);
        fs::remove_file(&settings).unwrap();
        let old = fs::read_to_string(&paths.mcp).unwrap();
        let plugin_key =
            serde_json::to_string(plugin_root(&paths).unwrap().to_str().unwrap()).unwrap();
        put(
            &paths.mcp,
            format!("{old}plugins:\n  {plugin_key}:\n    enabled: false\n").as_bytes(),
        );
        assert!(inspect(&paths, "goose", URL, &hook("goose"), None).hooks_disabled);
        let before = fs::read(&paths.mcp).unwrap();
        assert!(edits(&paths, "goose", URL, &hook("goose"), false, true)
            .unwrap()
            .is_empty());
        assert_eq!(fs::read(&paths.mcp).unwrap(), before);
    }

    #[test]
    fn antigravity_preserves_jsonc_and_native_hook_shape_on_install_and_remove() {
        let (_home, paths) = fixture("antigravity");
        put(
            &paths.mcp,
            include_bytes!("fixtures/harness_extra/antigravity_mcp.jsonc"),
        );
        put(
            &paths.hooks,
            include_bytes!("fixtures/harness_extra/antigravity_hooks.jsonc"),
        );
        let original_mcp =
            json_edit::value(read(&paths.mcp).unwrap().as_deref(), &paths.mcp).unwrap();
        let original_hooks =
            json_edit::value(read(&paths.hooks).unwrap().as_deref(), &paths.hooks).unwrap();
        install(&paths, "antigravity");
        let mcp = json_edit::value(read(&paths.mcp).unwrap().as_deref(), &paths.mcp).unwrap();
        assert_eq!(mcp["mcpServers"]["prism"], json!({"serverUrl":URL}));
        let hooks = json_edit::value(read(&paths.hooks).unwrap().as_deref(), &paths.hooks).unwrap();
        assert!(hooks[OBSERVER].get("PostToolUse").is_some());
        assert!(hooks[OBSERVER].get("PreToolUse").is_none());
        assert_eq!(hooks["fixture-gate"], original_hooks["fixture-gate"]);
        assert!(fs::read_to_string(&paths.hooks)
            .unwrap()
            .contains("// Keep this user-owned permission gate"));
        assert!(fs::read_to_string(&paths.mcp)
            .unwrap()
            .contains("// Existing native Antigravity"));
        assert!(edits(
            &paths,
            "antigravity",
            URL,
            &hook("antigravity"),
            false,
            false
        )
        .unwrap()
        .is_empty());
        apply(
            edits(
                &paths,
                "antigravity",
                URL,
                &hook("antigravity"),
                true,
                false,
            )
            .unwrap(),
        );
        assert_eq!(
            json_edit::value(read(&paths.mcp).unwrap().as_deref(), &paths.mcp).unwrap(),
            original_mcp
        );
        assert_eq!(
            json_edit::value(read(&paths.hooks).unwrap().as_deref(), &paths.hooks).unwrap(),
            original_hooks
        );
        assert!(!script_path(&paths, "antigravity").unwrap().exists());
        assert!(edits(
            &paths,
            "antigravity",
            URL,
            &hook("antigravity"),
            true,
            false
        )
        .unwrap()
        .is_empty());
    }

    #[test]
    fn antigravity_disabled_flags_survive_repair() {
        let (_home, paths) = fixture("antigravity");
        install(&paths, "antigravity");
        let mut hooks = hook_config(&paths, "antigravity").unwrap();
        hooks[OBSERVER]["enabled"] = json!(false);
        put(&paths.hooks, &encoded(&hooks));
        put(
            &paths.mcp,
            &encoded(
                &json!({"mcpServers":{"prism":{"serverUrl":"http://localhost:1111/mcp", "disabled":true, "disabledTools":["fixture"]}}}),
            ),
        );
        install(&paths, "antigravity");
        let status = inspect(
            &paths,
            "antigravity",
            URL,
            &hook("antigravity"),
            Some(Utc::now() + chrono::Duration::seconds(1)),
        );
        assert!(
            !status.mcp_configured
                && status.hooks_disabled
                && status.hook_installed
                && !status.events_received
        );
        let mcp = json_edit::value(read(&paths.mcp).unwrap().as_deref(), &paths.mcp).unwrap();
        assert_eq!(
            mcp["mcpServers"]["prism"]["disabledTools"],
            json!(["fixture"])
        );
        assert_eq!(
            json_edit::value(read(&paths.hooks).unwrap().as_deref(), &paths.hooks).unwrap(),
            hooks
        );
    }

    #[test]
    fn antigravity_conflicts_and_duplicate_keys_fail_before_any_write() {
        let (_home, paths) = fixture("antigravity");
        for source in [
            r#"{"mcpServers":{"prism":{"url":"http://127.0.0.1:1/mcp"}}}"#,
            r#"{"mcpServers":{"prism":{"serverUrl":"https://example.com/private-fixture"}}}"#,
            r#"{"mcpServers":{"prism":{"serverUrl":"http://127.0.0.1:1/mcp","command":"foreign"}}}"#,
            r#"{"mcpServers":{},"mcpServers":{}}"#,
            r#"{"mcpServers":{"prism":{},"prism":{}}}"#,
            r#"{"mcpServers":[]}"#,
        ] {
            put(&paths.mcp, source.as_bytes());
            let error = edits(
                &paths,
                "antigravity",
                URL,
                &hook("antigravity"),
                false,
                false,
            )
            .err()
            .unwrap();
            assert!(!error.contains("private-fixture"));
            assert!(!paths.hooks.exists());
            assert!(!script_path(&paths, "antigravity").unwrap().exists());
            assert_eq!(fs::read(&paths.mcp).unwrap(), source.as_bytes());
        }
        fs::remove_file(&paths.mcp).unwrap();
        put(
            &paths.hooks,
            br#"{"prism-observer":{"PostToolUse":[{"hooks":[{"command":"user-owned"}]}]}}"#,
        );
        assert!(edits(
            &paths,
            "antigravity",
            URL,
            &hook("antigravity"),
            false,
            false
        )
        .is_err());
        assert!(edits(
            &paths,
            "antigravity",
            URL,
            &hook("antigravity"),
            true,
            false
        )
        .is_err());
    }

    #[test]
    fn hooks_only_does_not_touch_mcp_and_repairs_missing_owned_files() {
        for host in ["goose", "antigravity"] {
            let (_home, paths) = fixture(host);
            install(&paths, host);
            let before = fs::read(&paths.mcp).unwrap();
            fs::remove_file(script_path(&paths, host).unwrap()).unwrap();
            let plan = edits(
                &paths,
                host,
                "http://localhost:4001/mcp",
                &hook(host),
                false,
                true,
            )
            .unwrap();
            assert!(plan.iter().all(|e| e.path != paths.mcp));
            apply(plan);
            assert_eq!(fs::read(&paths.mcp).unwrap(), before);
            assert!(inspect(&paths, host, URL, &hook(host), None).hook_installed);
            let plan = edits(&paths, host, URL, &hook(host), true, true).unwrap();
            assert!(plan.iter().all(|e| e.path != paths.mcp));
            apply(plan);
            assert_eq!(fs::read(&paths.mcp).unwrap(), before);
        }
    }

    #[test]
    fn artifact_collisions_and_modified_scripts_are_never_overwritten_or_removed() {
        for host in ["goose", "antigravity"] {
            let (_home, paths) = fixture(host);
            let script = script_path(&paths, host).unwrap();
            put(&script, b"#!/bin/sh\necho user-owned\n");
            for remove in [true, false] {
                assert!(edits(&paths, host, URL, &hook(host), remove, false).is_err());
                assert_eq!(fs::read(&script).unwrap(), b"#!/bin/sh\necho user-owned\n");
            }
        }
        let (_home, paths) = fixture("goose");
        put(
            &plugin_root(&paths).unwrap().join("README"),
            b"existing unrelated plugin",
        );
        assert!(edits(&paths, "goose", URL, &hook("goose"), false, false).is_err());
    }

    #[test]
    fn previous_endpoint_is_repairable_and_removable_without_old_token() {
        for host in ["goose", "antigravity"] {
            let (_home, paths) = fixture(host);
            install(&paths, host);
            let new_hook = format!("http://localhost:1111/hooks/{host}/rotated_fixture");
            let setup = inspect(
                &paths,
                host,
                "http://localhost:1111/mcp",
                &new_hook,
                Some(Utc::now() + chrono::Duration::seconds(1)),
            );
            assert!(setup.setup_present && !setup.hook_installed && !setup.events_received);
            apply(
                edits(
                    &paths,
                    host,
                    "http://localhost:1111/mcp",
                    &new_hook,
                    false,
                    false,
                )
                .unwrap(),
            );
            assert!(
                inspect(&paths, host, "http://localhost:1111/mcp", &new_hook, None).hook_installed
            );
            apply(edits(&paths, host, URL, &hook(host), true, false).unwrap());
            assert!(!inspect(&paths, host, URL, &hook(host), None).setup_present);
        }
    }

    #[test]
    fn latest_config_manifest_script_and_plugin_settings_invalidate_evidence() {
        for host in ["goose", "antigravity"] {
            let (_home, paths) = fixture(host);
            install(&paths, host);
            let mut evidence = vec![
                paths.mcp.clone(),
                paths.hooks.clone(),
                script_path(&paths, host).unwrap(),
            ];
            if host == "goose" {
                let settings = goose_settings(&paths).unwrap();
                put(&settings, b"{}");
                evidence.extend([plugin_root(&paths).unwrap().join("plugin.json"), settings]);
            }
            let at = Utc::now() + chrono::Duration::seconds(2);
            assert!(inspect(&paths, host, URL, &hook(host), Some(at)).events_received);
            for path in evidence {
                let file = fs::File::options().write(true).open(&path).unwrap();
                file.set_modified(SystemTime::from(at + chrono::Duration::seconds(2)))
                    .unwrap();
                assert!(!inspect(&paths, host, URL, &hook(host), Some(at)).events_received);
                file.set_modified(SystemTime::from(at - chrono::Duration::seconds(1)))
                    .unwrap();
            }
        }
    }

    #[cfg(unix)]
    #[test]
    fn refuses_symlinked_owned_files_and_plugin_directories() {
        use std::os::unix::fs::symlink;
        let (home, paths) = fixture("goose");
        let outside = home.path().join("outside");
        fs::create_dir_all(&outside).unwrap();
        fs::create_dir_all(plugin_root(&paths).unwrap().parent().unwrap()).unwrap();
        symlink(&outside, plugin_root(&paths).unwrap()).unwrap();
        assert!(edits(&paths, "goose", URL, &hook("goose"), false, false).is_err());
        assert!(fs::read_dir(&outside).unwrap().next().is_none());
        let (home, paths) = fixture("antigravity");
        let outside = home.path().join("unrelated.sh");
        put(&outside, b"user file");
        fs::create_dir_all(paths.hooks.parent().unwrap()).unwrap();
        symlink(&outside, script_path(&paths, "antigravity").unwrap()).unwrap();
        assert!(edits(
            &paths,
            "antigravity",
            URL,
            &hook("antigravity"),
            true,
            false
        )
        .is_err());
        assert_eq!(fs::read(outside).unwrap(), b"user file");
    }

    #[test]
    fn rejects_untrusted_observer_urls_before_generating_executable_source() {
        for host in ["goose", "antigravity"] {
            for url in [
                "https://example.com/hooks",
                "http://127.0.0.1:39701/hooks/goose/x';touch /tmp/bad",
                "http://127.0.0.1:39701/hooks/antigravity/x?redirect=https://example.com",
            ] {
                assert!(script(host, url).is_err());
            }
        }
    }

    #[cfg(unix)]
    #[test]
    fn actual_observer_commands_with_spaces_are_neutral_when_service_is_stopped() {
        for host in ["goose", "antigravity"] {
            let (home, paths) = fixture(host);
            let listener = TcpListener::bind("127.0.0.1:0").unwrap();
            let port = listener.local_addr().unwrap().port();
            drop(listener);
            let url = format!("http://127.0.0.1:{port}/hooks/{host}/fixture");
            put(
                &script_path(&paths, host).unwrap(),
                &script(host, &url).unwrap(),
            );
            let start = Instant::now();
            let mut child = Command::new("sh")
                .arg("-c")
                .arg(command(&paths, host).unwrap())
                .current_dir(home.path())
                .env("HOME", home.path())
                .stdin(Stdio::piped())
                .stdout(Stdio::piped())
                .stderr(Stdio::piped())
                .spawn()
                .unwrap();
            child
                .stdin
                .take()
                .unwrap()
                .write_all(b"{\"fixture\":true}")
                .unwrap();
            let output = child.wait_with_output().unwrap();
            assert!(output.status.success());
            assert_eq!(output.stdout, b"{}\n");
            assert!(output.stderr.is_empty());
            assert!(start.elapsed() < Duration::from_secs(5));
        }
    }

    #[cfg(unix)]
    #[test]
    fn missing_curl_and_nonempty_command_output_remain_neutral() {
        use std::os::unix::fs::{symlink, PermissionsExt};
        for host in ["goose", "antigravity"] {
            let (home, paths) = fixture(host);
            let bin = home.path().join("isolated-bin");
            fs::create_dir_all(&bin).unwrap();
            symlink("/bin/sh", bin.join("sh")).unwrap();
            symlink("/bin/cat", bin.join("cat")).unwrap();
            put(
                &script_path(&paths, host).unwrap(),
                &script(host, &hook(host)).unwrap(),
            );
            for missing in [true, false] {
                if !missing {
                    put(&bin.join("curl"), b"#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"decision\":\"allow\",\"tool_output\":\"unexpected output\"}'\necho noisy-error >&2\nexit 23\n");
                    fs::set_permissions(bin.join("curl"), fs::Permissions::from_mode(0o700))
                        .unwrap();
                }
                let mut child = Command::new("/bin/sh")
                    .arg("-c")
                    .arg(command(&paths, host).unwrap())
                    .current_dir(home.path())
                    .env("HOME", home.path())
                    .env("PATH", &bin)
                    .env_remove("OS")
                    .stdin(Stdio::piped())
                    .stdout(Stdio::piped())
                    .stderr(Stdio::piped())
                    .spawn()
                    .unwrap();
                child
                    .stdin
                    .take()
                    .unwrap()
                    .write_all(br#"{"tool_output":"nonempty\n$(never execute)","decision":"deny"}"#)
                    .unwrap();
                let output = child.wait_with_output().unwrap();
                assert!(output.status.success());
                assert_eq!(output.stdout, b"{}\n");
                assert!(output.stderr.is_empty());
            }
        }
    }

    #[cfg(unix)]
    #[test]
    fn stalled_gateway_is_bounded_by_one_second() {
        let (home, paths) = fixture("antigravity");
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!(
            "http://{}/hooks/antigravity/fixture",
            listener.local_addr().unwrap()
        );
        let receiver = std::thread::spawn(move || {
            let (_stream, _) = listener.accept().unwrap();
            std::thread::sleep(Duration::from_millis(1800));
        });
        put(
            &script_path(&paths, "antigravity").unwrap(),
            &script("antigravity", &url).unwrap(),
        );
        let started = Instant::now();
        let mut child = Command::new("sh")
            .arg("-c")
            .arg(command(&paths, "antigravity").unwrap())
            .current_dir(home.path())
            .env("HOME", home.path())
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        child.stdin.take().unwrap().write_all(b"{}").unwrap();
        let output = child.wait_with_output().unwrap();
        assert!(started.elapsed() < Duration::from_millis(1700));
        assert!(output.status.success());
        assert_eq!(output.stdout, b"{}\n");
        assert!(output.stderr.is_empty());
        receiver.join().unwrap();
    }

    #[test]
    #[ignore = "requires installed agy CLI; uses only an isolated fixture home and plugin validation"]
    fn installed_antigravity_cli_validates_generated_native_configs() {
        let (home, paths) = fixture("antigravity");
        install(&paths, "antigravity");
        let fixture_plugin = home.path().join("validation plugin");
        put(
            &fixture_plugin.join("plugin.json"),
            &encoded(&json!({"name":"prism-fixture", "description":"Isolated validation fixture"})),
        );
        put(
            &fixture_plugin.join("mcp_config.json"),
            &fs::read(&paths.mcp).unwrap(),
        );
        put(
            &fixture_plugin.join("hooks.json"),
            &fs::read(&paths.hooks).unwrap(),
        );
        let output = Command::new("agy")
            .args(["plugin", "validate"])
            .arg(&fixture_plugin)
            .current_dir(home.path())
            .env_clear()
            .env("PATH", std::env::var_os("PATH").unwrap_or_default())
            .env("HOME", home.path())
            .env("USERPROFILE", home.path())
            .env("XDG_CONFIG_HOME", home.path().join("xdg-config"))
            .env("XDG_DATA_HOME", home.path().join("xdg-data"))
            .env("XDG_CACHE_HOME", home.path().join("xdg-cache"))
            .output()
            .expect("requires the installed agy CLI");
        assert!(
            output.status.success(),
            "Isolated Antigravity CLI plugin validation failed: {}",
            String::from_utf8_lossy(&output.stderr)
        );
    }

    #[cfg(unix)]
    #[test]
    #[ignore = "requires PRISM_GOOSE_149_BIN pointing to a separately downloaded Goose 1.49.0; no paid model"]
    fn installed_goose_149_emits_native_tool_observation() {
        let binary = std::env::var_os("PRISM_GOOSE_149_BIN")
            .map(PathBuf::from)
            .expect("Set PRISM_GOOSE_149_BIN to the isolated Goose 1.49.0 executable");
        assert!(binary.is_absolute());
        let (home, _) = fixture("goose");
        let root = home.path().canonicalize().unwrap();
        let paths = resolve_paths("goose", &root, false, Some(root.clone()), None, None).unwrap();
        put(&paths.mcp, b"GOOSE_MODE: auto\nextensions:\n  developer:\n    enabled: true\n    name: developer\n    type: builtin\n");
        // Exercise the real installer: Python receives generated manifest/hooks/
        // script files, and only substitutes its ephemeral loopback port.
        apply(edits(&paths, "goose", URL, &hook("goose"), false, true).unwrap());
        let output = Command::new("python3")
            .arg(
                Path::new(env!("CARGO_MANIFEST_DIR"))
                    .join("src/fixtures/harness_extra/goose-live.py"),
            )
            .arg(binary)
            .arg(&root)
            .arg(hook("goose"))
            .current_dir(&root)
            .env_clear()
            .env("PATH", std::env::var_os("PATH").unwrap_or_default())
            .output()
            .expect("Python 3 is required for the optional local model fixture");
        assert!(
            output.status.success(),
            "Goose fixture failed: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        let summary: Value =
            serde_json::from_slice(&output.stdout).expect("fixture evidence summary");
        assert_eq!(summary["exit"], 0);
        assert_eq!(summary["observed"].as_array().unwrap().len(), 1);
        assert_eq!(summary["marker_in_result"], true);
        println!("{}", String::from_utf8_lossy(&output.stdout).trim());
    }

    #[cfg(unix)]
    #[test]
    fn native_observer_transport_keeps_payload_and_discards_permission_response() {
        for host in ["goose", "antigravity"] {
            let (home, paths) = fixture(host);
            let listener = TcpListener::bind("127.0.0.1:0").unwrap();
            let url = format!(
                "http://{}/hooks/{host}/fixture",
                listener.local_addr().unwrap()
            );
            let receiver = std::thread::spawn(move || {
                let (mut stream, _) = listener.accept().unwrap();
                stream
                    .set_read_timeout(Some(Duration::from_secs(4)))
                    .unwrap();
                let mut bytes = vec![];
                let (header_end, length) = loop {
                    let mut buf = [0u8; 4096];
                    let count = stream.read(&mut buf).unwrap();
                    assert!(count > 0);
                    bytes.extend_from_slice(&buf[..count]);
                    if let Some(pos) = bytes.windows(4).position(|w| w == b"\r\n\r\n") {
                        let headers = std::str::from_utf8(&bytes[..pos]).unwrap();
                        let length: usize = headers
                            .lines()
                            .find_map(|l| {
                                l.to_ascii_lowercase()
                                    .strip_prefix("content-length: ")
                                    .and_then(|v| v.parse().ok())
                            })
                            .unwrap();
                        break (pos + 4, length);
                    }
                };
                while bytes.len() < header_end + length {
                    let mut buf = [0u8; 4096];
                    let count = stream.read(&mut buf).unwrap();
                    assert!(count > 0);
                    bytes.extend_from_slice(&buf[..count]);
                }
                let body: Value =
                    serde_json::from_slice(&bytes[header_end..header_end + length]).unwrap();
                stream.write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 20\r\nConnection: close\r\n\r\n{\"decision\":\"allow\"}").unwrap();
                body
            });
            put(
                &script_path(&paths, host).unwrap(),
                &script(host, &url).unwrap(),
            );
            let native = if host == "goose" {
                json!({"event":"PreToolUseResult","tool_name":"developer__shell","tool_input":{"command":"printf '$HOME'"},"session_id":"fixture","tool_call_id":"call1","decision":"deny","working_dir":"/fixture path"})
            } else {
                json!({"toolCall":{"name":"run_command","args":{"CommandLine":"printf '$HOME'","Cwd":"/fixture path"}},"stepIdx":2,"conversationId":"fixture","workspacePaths":["/fixture path"]})
            };
            let mut child = Command::new("sh")
                .arg("-c")
                .arg(command(&paths, host).unwrap())
                .current_dir(home.path())
                .env("HOME", home.path())
                .stdin(Stdio::piped())
                .stdout(Stdio::piped())
                .stderr(Stdio::piped())
                .spawn()
                .unwrap();
            child
                .stdin
                .take()
                .unwrap()
                .write_all(&serde_json::to_vec(&native).unwrap())
                .unwrap();
            let output = child.wait_with_output().unwrap();
            assert!(output.status.success());
            assert_eq!(output.stdout, b"{}\n");
            assert!(output.stderr.is_empty());
            let body = receiver.join().unwrap();
            if host == "goose" {
                assert_eq!(body, native);
            } else {
                assert_eq!(
                    body,
                    json!({"hook_event_name":"PostToolUse","payload":native})
                );
            }
        }
    }
}
