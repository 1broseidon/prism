//! Versioned, offline server provisioning. Secrets resolve once, into the OS store.
use std::{
    collections::{BTreeMap, HashSet},
    io::Read,
    path::Path,
};

use crate::{
    credentials::{self, CredentialStore, LaunchSettings, NativeStore},
    profile::ProfileLock,
    Error, HttpAuth, PrismConfig, Result, ServerConfig,
};
use serde::{Deserialize, Serialize};

const MAX_MANIFEST_BYTES: u64 = 1024 * 1024;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProvisionManifest {
    version: u32,
    servers: Vec<ProvisionServer>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ProvisionServer {
    id: String,
    name: String,
    #[serde(default)]
    command: String,
    #[serde(default)]
    args: Vec<String>,
    #[serde(default)]
    env: BTreeMap<String, SecretSource>,
    #[serde(default = "enabled")]
    enabled: bool,
    url: Option<String>,
    auth: Option<HttpAuth>,
    #[serde(default)]
    headers: BTreeMap<String, SecretSource>,
}

fn enabled() -> bool {
    true
}

// No Debug: the prefix or a mistakenly supplied variable name may contain a secret.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SecretSource {
    env: String,
    #[serde(default)]
    prefix: String,
}

impl SecretSource {
    fn resolve(&self, environment: &dyn Fn(&str) -> Option<String>) -> Result<String> {
        if self.env.is_empty()
            || !self
                .env
                .bytes()
                .all(|c| c.is_ascii_alphanumeric() || c == b'_')
        {
            return Err(Error::Invalid(
                "credential references need an environment variable name".into(),
            ));
        }
        let value = environment(&self.env)
            .filter(|v| !v.is_empty())
            .ok_or_else(|| {
                Error::Invalid(
                    "a referenced credential environment variable is missing or empty".into(),
                )
            })?;
        if value.len() + self.prefix.len() > MAX_MANIFEST_BYTES as usize {
            return Err(Error::Invalid("a credential value exceeds 1 MiB".into()));
        }
        Ok(format!("{}{value}", self.prefix))
    }
}

#[derive(Debug, Default, Serialize, PartialEq, Eq)]
pub struct ProvisionReport {
    pub added: usize,
    pub updated: usize,
    pub unchanged: usize,
    /// The apply committed; old credentials may require cleanup. Never retry as a rollback.
    pub cleanup_pending: bool,
}

impl ProvisionManifest {
    pub fn read(path: &Path) -> Result<Self> {
        let mut bytes = Vec::new();
        std::fs::File::open(path)?
            .take(MAX_MANIFEST_BYTES + 1)
            .read_to_end(&mut bytes)?;
        if bytes.len() as u64 > MAX_MANIFEST_BYTES {
            return Err(Error::Invalid("provisioning manifest exceeds 1 MiB".into()));
        }
        Self::parse(&bytes)
    }

    pub fn parse(bytes: &[u8]) -> Result<Self> {
        // Serde errors can include unknown fields or enum values supplied by the caller.
        serde_json::from_slice(bytes).map_err(|error| {
            Error::Invalid(format!(
                "invalid provisioning manifest at line {}, column {}; check the version 1 schema",
                error.line(),
                error.column()
            ))
        })
    }
}

/// Apply to a stopped profile. The parent directory must belong exclusively to Prism.
pub fn apply_manifest(config_path: &Path, manifest: ProvisionManifest) -> Result<ProvisionReport> {
    apply_with_store(config_path, manifest, &NativeStore::default(), &|name| {
        std::env::var(name).ok()
    })
}

pub(crate) fn apply_with_store(
    path: &Path,
    manifest: ProvisionManifest,
    store: &dyn CredentialStore,
    environment: &dyn Fn(&str) -> Option<String>,
) -> Result<ProvisionReport> {
    if manifest.version != 1 || manifest.servers.len() > 256 {
        return Err(Error::Invalid(
            "expected manifest version 1 with at most 256 servers".into(),
        ));
    }
    let _profile = ProfileLock::acquire(path)?;
    let previous = if path.exists() {
        PrismConfig::load(path)?
    } else {
        PrismConfig::default()
    };
    let mut updated = previous.clone();
    let mut ids = HashSet::new();
    let mut report = ProvisionReport::default();
    let mut resolved = Vec::new();
    for input in manifest.servers {
        if input.id.is_empty()
            || input.id.len() > 80
            || !input
                .id
                .bytes()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'-' | b'_'))
            || !ids.insert(input.id.clone())
        {
            return Err(Error::Invalid(
                "server IDs must be unique, 1..80 ASCII letters, digits, hyphens or underscores"
                    .into(),
            ));
        }
        let old = previous.servers.iter().find(|s| s.id == input.id);
        if input.url.is_some() && input.auth.is_none() {
            return Err(Error::Invalid(
                "every remote server needs an explicit auth mode".into(),
            ));
        }
        if old.is_some_and(|s| s.is_remote() != input.url.is_some()) {
            return Err(Error::Invalid(
                "a provisioned server cannot change transport; use a new ID".into(),
            ));
        }
        let auth = input.auth.unwrap_or_default();
        if auth != HttpAuth::Header && !input.headers.is_empty() {
            return Err(Error::Invalid(
                "provisioned headers require explicit header auth".into(),
            ));
        }
        let resolve = |values: BTreeMap<String, SecretSource>| -> Result<BTreeMap<String, String>> {
            values
                .into_iter()
                .map(|(name, source)| Ok((name, source.resolve(environment)?)))
                .collect()
        };
        let mut server = ServerConfig {
            id: input.id,
            name: input.name,
            command: input.command,
            args: input.args,
            env: resolve(input.env)?,
            headers: resolve(input.headers)?,
            credential_ref: None,
            enabled: input.enabled,
            url: input.url,
            auth,
            oauth_ref: None,
            hidden_tools: old.map(|s| s.hidden_tools.clone()).unwrap_or_default(),
        };
        crate::gateway::validate_server(&mut server, &[], None)?;
        if auth == HttpAuth::Oauth {
            server.oauth_ref = old
                .filter(|s| s.auth == auth && s.url == server.url)
                .and_then(|s| s.oauth_ref.clone())
                .or_else(|| Some(uuid::Uuid::new_v4().to_string()));
        }
        let launch = LaunchSettings {
            args: server.args.clone(),
            env: server.env.clone(),
            headers: server.headers.clone(),
        };
        if let Some(old) = old {
            // Resolve existing secrets too: a locked/missing store cannot silently replace them.
            if credentials::resolve(store, old)? == launch && old.credential_ref.is_some() {
                server.credential_ref = old.credential_ref.clone();
                server.args.clear();
                server.env.clear();
                server.headers.clear();
            }
            if &server == old {
                report.unchanged += 1;
            } else {
                report.updated += 1;
            }
        } else {
            report.added += 1;
        }
        resolved.push(server);
    }
    // Validate the complete desired catalog before touching the store; permit name swaps.
    updated.servers.retain(|s| !ids.contains(&s.id));
    for server in &mut resolved {
        let mut checked = server.clone();
        if checked.credential_ref.is_some() {
            let launch = credentials::resolve(store, server)?;
            checked.args = launch.args;
            checked.env = launch.env;
            checked.headers = launch.headers;
        }
        crate::gateway::validate_server(&mut checked, &updated.servers, None)?;
        updated.servers.push(server.clone());
    }
    let mut created: Vec<String> = Vec::new();
    for server in &mut updated.servers {
        let old_ref = server.credential_ref.clone();
        if let Err(error) = credentials::protect_server(store, server) {
            for id in &created {
                let _ = credentials::delete(store, id);
            }
            return Err(error);
        }
        if old_ref.is_none() {
            if let Some(id) = &server.credential_ref {
                created.push(id.clone());
            }
        }
    }
    if previous.servers == updated.servers && path.exists() {
        return Ok(report);
    }
    // Keep both verified generations if atomic replacement has an uncertain disk outcome.
    updated.save(path)?;
    for old in &previous.servers {
        if let Some(new) = updated.servers.iter().find(|s| s.id == old.id) {
            for reference in [
                old.credential_ref
                    .as_ref()
                    .filter(|r| Some(*r) != new.credential_ref.as_ref()),
                old.oauth_ref
                    .as_ref()
                    .filter(|r| Some(*r) != new.oauth_ref.as_ref()),
            ]
            .into_iter()
            .flatten()
            {
                // Older hand-edited profiles can share a reference across servers.
                if updated.servers.iter().any(|server| {
                    server.credential_ref.as_ref() == Some(reference)
                        || server.oauth_ref.as_ref() == Some(reference)
                }) {
                    continue;
                }
                if credentials::delete_if_present(store, reference).is_err() {
                    report.cleanup_pending = true;
                }
            }
        }
    }
    Ok(report)
}

#[cfg(test)]
#[path = "provision_tests.rs"]
mod tests;
