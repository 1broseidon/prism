use std::net::IpAddr;
use std::path::{Path, PathBuf};

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value;

use super::ToolAnnotations;
use crate::approval::{Offer, OfferKind};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Channel {
    Mcp,
    Native,
}

#[derive(Debug, Clone)]
pub struct Context {
    pub cwd: Option<PathBuf>,
    pub requested_at: DateTime<Utc>,
}

#[derive(Debug, Clone)]
pub struct Action {
    pub agent_id: String,
    pub channel: Channel,
    pub server_id: String,
    pub tool: String,
    pub arguments: Value,
    pub annotations: Option<ToolAnnotations>,
    pub facets: Facets,
    pub context: Context,
}

#[derive(Debug, Clone, Default)]
pub struct Facets {
    pub paths: Vec<PathFacet>,
    pub hosts: Vec<HostFacet>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PathFacet {
    pub path: String,
    pub argument: String,
    pub access: PathAccess,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum PathAccess {
    Read,
    Write,
    Delete,
    Unknown,
}

impl PathAccess {
    pub fn label(self) -> &'static str {
        match self {
            Self::Read => "read",
            Self::Write => "write",
            Self::Delete => "delete",
            Self::Unknown => "unknown",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HostFacet {
    pub host: String,
    pub port: Option<u16>,
    pub scheme: Option<String>,
    pub argument: String,
    pub scope: HostScope,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum HostScope {
    Loopback,
    Private,
    Public,
    Unknown,
}

impl HostScope {
    pub fn label(self) -> &'static str {
        match self {
            Self::Loopback => "loopback",
            Self::Private => "private",
            Self::Public => "public",
            Self::Unknown => "unknown",
        }
    }
}

pub(super) fn home() -> Option<PathBuf> {
    std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .map(PathBuf::from)
}

fn windows_drive(value: &str) -> bool {
    let b = value.as_bytes();
    b.len() >= 3 && b[0].is_ascii_alphabetic() && b[1] == b':' && matches!(b[2], b'\\' | b'/')
}

/// The home directory with forward slashes, resolved like any other path.
pub(super) fn home_str() -> Option<String> {
    home().map(|h| resolve_path(&h.to_string_lossy(), None))
}

/// A `/`-rooted or drive-rooted path, whatever the host OS thinks.
fn rooted(value: &str) -> bool {
    value.starts_with('/') || windows_drive(value)
}

/// Lexical only: neither symlinks nor file existence are consulted. Pure string work, so a
/// facet reads the same on every host: `/`-rooted paths stay `/`-rooted, drive paths keep
/// their drive with forward slashes, `.` and `..` are collapsed.
pub fn resolve_path(value: &str, cwd: Option<&Path>) -> String {
    let mut path = if value == "~" || value.starts_with("~/") {
        match home() {
            Some(home) => {
                let rest = value.strip_prefix('~').unwrap_or("");
                format!("{}{rest}", home.to_string_lossy())
            }
            None => value.to_string(),
        }
    } else {
        value.to_string()
    };
    if windows_drive(&path) {
        path = path.replace('\\', "/");
    }
    if !rooted(&path) {
        if let Some(cwd) = cwd {
            let cwd = resolve_path(&cwd.to_string_lossy(), None);
            path = format!("{cwd}/{path}");
            if windows_drive(&path) {
                path = path.replace('\\', "/");
            }
        }
    }
    let (root, rest) = if windows_drive(&path) {
        (format!("{}/", path[..2].to_ascii_uppercase()), &path[3..])
    } else if let Some(rest) = path.strip_prefix('/') {
        ("/".to_string(), rest)
    } else {
        (String::new(), path.as_str())
    };
    let mut parts: Vec<&str> = Vec::new();
    for segment in rest.split('/') {
        match segment {
            "" | "." => {}
            ".." => {
                if matches!(parts.last(), Some(last) if *last != "..") {
                    parts.pop();
                } else if root.is_empty() {
                    parts.push("..");
                }
            }
            other => parts.push(other),
        }
    }
    let result = format!("{root}{}", parts.join("/"));
    if result.is_empty() {
        ".".into()
    } else {
        result
    }
}

pub(super) fn resolved_path<'a>(value: &'a str, cwd: Option<&Path>) -> std::borrow::Cow<'a, str> {
    if rooted(value)
        && !value.contains('\\')
        && !value
            .split('/')
            .any(|part| part.is_empty() || part == "." || part == "..")
    {
        std::borrow::Cow::Borrowed(value)
    } else {
        std::borrow::Cow::Owned(resolve_path(value, cwd))
    }
}

pub(super) fn under(path: &str, prefix: &str) -> bool {
    let prefix = prefix
        .strip_suffix('/')
        .filter(|p| !p.is_empty())
        .unwrap_or(prefix);
    path == prefix
        || prefix.ends_with('/') && path.starts_with(prefix)
        || path
            .strip_prefix(prefix)
            .is_some_and(|rest| rest.starts_with('/'))
}

/// The parent directory, or `None` at a root.
pub(super) fn parent(path: &str) -> Option<String> {
    let (root, rest) = if windows_drive(path) {
        (&path[..3], &path[3..])
    } else if let Some(rest) = path.strip_prefix('/') {
        ("/", rest)
    } else {
        ("", path)
    };
    let rest = rest.strip_suffix('/').unwrap_or(rest);
    if rest.is_empty() {
        return None;
    }
    let parent = match rest.rfind('/') {
        Some(i) => format!("{root}{}", &rest[..i]),
        None if root.is_empty() => return None,
        None => root.to_string(),
    };
    Some(parent)
}

pub fn short_path(path: &str) -> String {
    if let Some(home) = home_str() {
        if path == home {
            return "~".into();
        }
        if let Some(rest) = path.strip_prefix(&home).and_then(|r| r.strip_prefix('/')) {
            return format!("~/{rest}");
        }
    }
    path.into()
}

impl Action {
    pub fn from_mcp(
        agent_id: impl Into<String>,
        server_id: impl Into<String>,
        tool: impl Into<String>,
        arguments: Value,
        annotations: Option<ToolAnnotations>,
        now: DateTime<Utc>,
    ) -> Self {
        let mut action = Self {
            agent_id: agent_id.into(),
            channel: Channel::Mcp,
            server_id: server_id.into(),
            tool: tool.into(),
            arguments,
            annotations,
            facets: Facets::default(),
            context: Context {
                cwd: None,
                requested_at: now,
            },
        };
        action.extract_facets();
        action
    }

    /// A transport may supply a known working directory; argument hints do not set context.
    pub fn with_cwd(mut self, cwd: PathBuf) -> Self {
        self.context.cwd = Some(cwd);
        self.extract_facets();
        self
    }

    pub fn extract_facets(&mut self) {
        let mut facets = Facets::default();
        walk(&self.arguments, "", 0, &mut |value, pointer| {
            if facets.paths.len() + facets.hosts.len() >= 64 {
                return false;
            }
            let name = pointer
                .rsplit('/')
                .next()
                .unwrap_or("")
                .to_ascii_lowercase();
            // URLs take priority over path name hints, avoiding userinfo/query leakage.
            let host = host_facet(value, &name, pointer);
            if let Some(host) = host {
                facets.hosts.push(host);
            }
            if facets.paths.len() + facets.hosts.len() < 64
                && !value.contains("://")
                && is_path(value, &name)
            {
                let tool = self.tool.to_ascii_lowercase();
                let access = if ["delete", "remove", "rm", "unlink", "trash"]
                    .iter()
                    .any(|s| tool.contains(s))
                {
                    PathAccess::Delete
                } else if [
                    "write", "create", "edit", "update", "move", "rename", "copy", "mkdir", "save",
                    "put", "set",
                ]
                .iter()
                .any(|s| tool.contains(s))
                    || matches!(name.as_str(), "destination" | "dest" | "output")
                {
                    PathAccess::Write
                } else if self
                    .annotations
                    .as_ref()
                    .is_some_and(ToolAnnotations::is_read_only)
                {
                    PathAccess::Read
                } else {
                    PathAccess::Unknown
                };
                facets.paths.push(PathFacet {
                    path: resolve_path(value, self.context.cwd.as_deref()),
                    argument: pointer.into(),
                    access,
                });
            }
            facets.paths.len() + facets.hosts.len() < 64
        });
        self.facets = facets;
    }

    pub fn facets_summary(&self) -> Vec<String> {
        self.facets
            .paths
            .iter()
            .map(|p| format!("path: {} ({})", short_path(&p.path), p.access.label()))
            .chain(self.facets.hosts.iter().map(|h| {
                format!(
                    "host: {} ({})",
                    if h.host.is_empty() { "?" } else { &h.host },
                    h.scope.label()
                )
            }))
            .take(4)
            .collect()
    }

    pub fn offers(&self) -> Vec<Offer> {
        let mut offers = Vec::new();
        if let Some(path) = self
            .facets
            .paths
            .iter()
            .find(|p| matches!(p.access, PathAccess::Write | PathAccess::Delete))
        {
            let cwd = self
                .context
                .cwd
                .as_deref()
                .map(|p| resolve_path(&p.to_string_lossy(), None));
            let parent = cwd
                .filter(|cwd| under(&path.path, cwd))
                .or_else(|| parent(&path.path));
            if let Some(parent) = parent.filter(|p| {
                p != "/" && p != "." && p != ".." && !(windows_drive(p) && p.len() == 3)
            }) {
                offers.push(Offer {
                    kind: OfferKind::PathUnder,
                    label: format!("Under {}", short_path(&parent)),
                    value: parent,
                });
            }
        }
        if let Some(host) = self
            .facets
            .hosts
            .first()
            .filter(|h| !h.host.is_empty() && h.scope != HostScope::Unknown)
        {
            offers.push(Offer {
                kind: OfferKind::Host,
                value: host.host.clone(),
                label: format!("For {}", host.host),
            });
        }
        offers
    }
}

/// Root is depth zero. JSON pointers always escape object keys, including array indices.
pub(super) fn walk(
    value: &Value,
    pointer: &str,
    depth: usize,
    visit: &mut impl FnMut(&str, &str) -> bool,
) -> bool {
    if depth > 8 {
        return true;
    }
    match value {
        Value::String(s) => visit(s, pointer),
        Value::Object(map) => map.iter().all(|(k, v)| {
            walk(
                v,
                &format!("{pointer}/{}", k.replace('~', "~0").replace('/', "~1")),
                depth + 1,
                visit,
            )
        }),
        Value::Array(items) => items
            .iter()
            .enumerate()
            .all(|(i, v)| walk(v, &format!("{pointer}/{i}"), depth + 1, visit)),
        _ => true,
    }
}

fn is_path(value: &str, name: &str) -> bool {
    value.starts_with('/')
        || value.starts_with("~/")
        || value.starts_with("./")
        || value.starts_with("../")
        || windows_drive(value)
        || (!value.is_empty()
            && matches!(
                name,
                "path"
                    | "file"
                    | "filepath"
                    | "file_path"
                    | "dir"
                    | "directory"
                    | "cwd"
                    | "target"
                    | "source"
                    | "destination"
                    | "dest"
                    | "src"
                    | "output"
                    | "input"
            ))
        || (name == "pattern" && value.contains('/'))
}

fn host_facet(value: &str, name: &str, pointer: &str) -> Option<HostFacet> {
    let named = matches!(
        name,
        "url" | "uri" | "host" | "hostname" | "endpoint" | "server" | "base_url"
    );
    let url = reqwest::Url::parse(value).ok().filter(|u| {
        matches!(
            u.scheme(),
            "http"
                | "https"
                | "ws"
                | "wss"
                | "ftp"
                | "ssh"
                | "git"
                | "postgres"
                | "mysql"
                | "redis"
        ) && u.host_str().is_some()
    });
    let explicit = url.is_some();
    let url = url.or_else(|| {
        if !named
            || value.starts_with('/')
            || value.contains("://")
            || value.chars().any(char::is_whitespace)
        {
            return None;
        }
        let authority = if value.parse::<IpAddr>().is_ok_and(|ip| ip.is_ipv6()) {
            format!("[{value}]")
        } else {
            value.to_string()
        };
        reqwest::Url::parse(&format!("http://{authority}")).ok()
    });
    if !named && !explicit {
        return None;
    }
    let mut facet = HostFacet {
        host: String::new(),
        port: None,
        scheme: None,
        argument: pointer.into(),
        scope: HostScope::Unknown,
    };
    if let Some(url) = url {
        if let Some(host) = url.host_str() {
            let host = host
                .trim_matches(['[', ']'])
                .trim_end_matches('.')
                .to_ascii_lowercase();
            if host.parse::<IpAddr>().is_ok()
                || (!host.is_empty()
                    && host.split('.').all(|s| {
                        !s.is_empty()
                            && s.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-')
                            && !s.starts_with('-')
                            && !s.ends_with('-')
                    }))
            {
                facet.scope = host_scope(&host);
                facet.host = host;
                facet.port = if explicit {
                    url.port_or_known_default()
                } else {
                    url.port()
                };
                facet.scheme = explicit.then(|| url.scheme().into());
            }
        }
    }
    Some(facet)
}

fn host_scope(host: &str) -> HostScope {
    match host.parse::<IpAddr>() {
        Ok(IpAddr::V4(ip)) => {
            if ip.is_loopback() {
                HostScope::Loopback
            } else if ip.is_private() || ip.is_link_local() {
                HostScope::Private
            } else if ip.is_unspecified() {
                HostScope::Unknown
            } else {
                HostScope::Public
            }
        }
        Ok(IpAddr::V6(ip)) => {
            if let Some(ip) = ip.to_ipv4_mapped() {
                return host_scope(&ip.to_string());
            }
            if ip.is_loopback() {
                HostScope::Loopback
            } else if ip.is_unique_local() || ip.is_unicast_link_local() {
                HostScope::Private
            } else if ip.is_unspecified() {
                HostScope::Unknown
            } else {
                HostScope::Public
            }
        }
        Err(_) if host == "localhost" => HostScope::Loopback,
        Err(_) if host.ends_with(".local") => HostScope::Private,
        Err(_) => HostScope::Public,
    }
}
