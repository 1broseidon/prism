//! Server launch values live in the OS credential store, never in saved JSON.
use std::collections::BTreeMap;
use std::sync::Mutex;

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::config::{PrismConfig, ServerConfig};
use crate::error::{Error, Result};

const SERVICE: &str = "dev.prism.gateway.servers";
// Windows generic credentials have a 2560-byte limit. Chunking also handles long env values.
const CHUNK_BYTES: usize = 2000;
const MAX_BYTES: usize = 1024 * 1024;

pub(crate) trait CredentialStore: Send + Sync {
    fn set(&self, key: &str, value: &[u8]) -> Result<()>;
    fn get(&self, key: &str) -> Result<Vec<u8>>;
    fn delete(&self, key: &str) -> Result<()>;
}

#[derive(Default)]
pub(crate) struct NativeStore(Mutex<()>);

fn unavailable() -> Error {
    Error::Gateway("OS credential storage is locked, unavailable, or missing an entry. Unlock your keychain/credential store and retry; Prism does not fall back to plaintext.".into())
}

impl CredentialStore for NativeStore {
    fn set(&self, key: &str, value: &[u8]) -> Result<()> {
        let _guard = self.0.lock().map_err(|_| unavailable())?;
        keyring::Entry::new(SERVICE, key)
            .and_then(|entry| entry.set_secret(value))
            .map_err(|_| unavailable())
    }

    fn get(&self, key: &str) -> Result<Vec<u8>> {
        let _guard = self.0.lock().map_err(|_| unavailable())?;
        keyring::Entry::new(SERVICE, key)
            .and_then(|entry| entry.get_secret())
            .map_err(|_| unavailable())
    }

    fn delete(&self, key: &str) -> Result<()> {
        let _guard = self.0.lock().map_err(|_| unavailable())?;
        match keyring::Entry::new(SERVICE, key).and_then(|entry| entry.delete_credential()) {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(_) => Err(unavailable()),
        }
    }
}

// Deliberately no Debug: launch settings may contain credentials.
#[derive(Default, Serialize, Deserialize, PartialEq, Eq)]
pub(crate) struct LaunchSettings {
    pub args: Vec<String>,
    pub env: BTreeMap<String, String>,
}

#[derive(Serialize, Deserialize)]
struct Manifest {
    chunks: usize,
    digest: Vec<u8>,
}

fn manifest(store: &dyn CredentialStore, id: &str) -> Result<Manifest> {
    uuid::Uuid::parse_str(id)
        .map_err(|_| Error::Invalid("invalid server credential reference".into()))?;
    let manifest: Manifest = serde_json::from_slice(&store.get(id)?).map_err(|_| unavailable())?;
    if manifest.chunks == 0 || manifest.chunks > MAX_BYTES.div_ceil(CHUNK_BYTES) {
        return Err(unavailable());
    }
    Ok(manifest)
}

pub(crate) fn resolve(
    store: &dyn CredentialStore,
    config: &ServerConfig,
) -> Result<LaunchSettings> {
    let Some(id) = &config.credential_ref else {
        return Ok(LaunchSettings {
            args: config.args.clone(),
            env: config.env.clone(),
        });
    };
    if !config.args.is_empty() || !config.env.is_empty() {
        return Err(Error::Invalid(
            "server contains both credential references and plaintext launch values".into(),
        ));
    }
    let manifest = manifest(store, id)?;
    let mut bytes = Vec::new();
    for chunk in 0..manifest.chunks {
        let value = store.get(&format!("{id}/{chunk}"))?;
        if value.len() > CHUNK_BYTES {
            return Err(unavailable());
        }
        bytes.extend(value);
    }
    if Sha256::digest(&bytes).as_slice() != manifest.digest {
        return Err(unavailable());
    }
    serde_json::from_slice(&bytes).map_err(|_| unavailable())
}

/// Verify every write before removing any plaintext from the in-memory config.
pub(crate) fn protect_server(store: &dyn CredentialStore, server: &mut ServerConfig) -> Result<()> {
    if let Some(id) = &server.credential_ref {
        uuid::Uuid::parse_str(id)
            .map_err(|_| Error::Invalid("invalid server credential reference".into()))?;
        if !server.args.is_empty() || !server.env.is_empty() {
            return Err(Error::Invalid(
                "server contains both credential references and plaintext launch values".into(),
            ));
        }
        // Already migrated: resolve at server startup. A missing credential should mark
        // that server failed, while leaving the panel available to remove/re-add it.
        return Ok(());
    }
    if server.args.is_empty() && server.env.is_empty() {
        return Ok(());
    }
    let settings = LaunchSettings {
        args: server.args.clone(),
        env: server.env.clone(),
    };
    let bytes = serde_json::to_vec(&settings)?;
    if bytes.len() > MAX_BYTES {
        return Err(Error::Invalid("server launch settings exceed 1 MiB".into()));
    }
    let id = uuid::Uuid::new_v4().to_string();
    let chunks = bytes.len().div_ceil(CHUNK_BYTES);
    let result = (|| {
        for (index, chunk) in bytes.chunks(CHUNK_BYTES).enumerate() {
            store.set(&format!("{id}/{index}"), chunk)?;
        }
        store.set(
            &id,
            &serde_json::to_vec(&Manifest {
                chunks,
                digest: Sha256::digest(&bytes).to_vec(),
            })?,
        )?;
        let mut secured = server.clone();
        secured.credential_ref = Some(id.clone());
        secured.args.clear();
        secured.env.clear();
        if resolve(store, &secured)? != settings {
            return Err(unavailable());
        }
        *server = secured;
        Ok(())
    })();
    if result.is_err() {
        for index in 0..chunks {
            let _ = store.delete(&format!("{id}/{index}"));
        }
        let _ = store.delete(&id);
    }
    result
}

pub(crate) fn delete(store: &dyn CredentialStore, id: &str) -> Result<()> {
    let manifest = manifest(store, id)?;
    for chunk in 0..manifest.chunks {
        store.delete(&format!("{id}/{chunk}"))?;
    }
    store.delete(id)
}

pub(crate) fn migrate(
    config: &PrismConfig,
    path: &std::path::Path,
    store: &dyn CredentialStore,
) -> Result<PrismConfig> {
    let mut secured = config.clone();
    let result = (|| {
        for server in &mut secured.servers {
            protect_server(store, server)?;
        }
        Ok::<_, Error>(())
    })();
    if let Err(err) = result {
        // New records only; an existing config's references must remain usable.
        for (old, new) in config.servers.iter().zip(&secured.servers) {
            if old.credential_ref.is_none() {
                if let Some(id) = &new.credential_ref {
                    let _ = delete(store, id);
                }
            }
        }
        return Err(err);
    }
    // Once a disk replacement has been attempted its outcome may be uncertain (for
    // example, directory fsync failed). Keep verified entries for recovery in that case.
    secured.save(path)?;
    Ok(secured)
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use std::collections::HashMap;

    #[derive(Default)]
    pub(crate) struct MemoryStore(pub Mutex<HashMap<String, Vec<u8>>>);
    impl CredentialStore for MemoryStore {
        fn set(&self, key: &str, value: &[u8]) -> Result<()> {
            self.0.lock().unwrap().insert(key.into(), value.into());
            Ok(())
        }
        fn get(&self, key: &str) -> Result<Vec<u8>> {
            self.0
                .lock()
                .unwrap()
                .get(key)
                .cloned()
                .ok_or_else(unavailable)
        }
        fn delete(&self, key: &str) -> Result<()> {
            self.0.lock().unwrap().remove(key);
            Ok(())
        }
    }

    fn server() -> ServerConfig {
        ServerConfig {
            id: "server".into(),
            name: "test".into(),
            command: "echo".into(),
            args: vec!["--token=argument-secret".into()],
            env: BTreeMap::from([("CUSTOM_VALUE".into(), "env-secret".repeat(1000))]),
            enabled: false,
            credential_ref: None,
        }
    }

    #[test]
    fn migrates_plaintext_and_restores_chunked_launch_values() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("prism.json");
        let original = PrismConfig {
            servers: vec![server()],
            ..Default::default()
        };
        std::fs::write(&path, serde_json::to_vec(&original).unwrap()).unwrap();
        let store = MemoryStore::default();
        migrate(&PrismConfig::load(&path).unwrap(), &path, &store).unwrap();
        let disk = std::fs::read_to_string(&path).unwrap();
        assert!(!disk.contains("argument-secret"));
        assert!(!disk.contains("env-secret"));
        let loaded = PrismConfig::load(&path).unwrap();
        let launch = resolve(&store, &loaded.servers[0]).unwrap();
        assert_eq!(launch.args, original.servers[0].args);
        assert_eq!(launch.env, original.servers[0].env);
        assert!(store
            .0
            .lock()
            .unwrap()
            .values()
            .all(|bytes| bytes.len() <= CHUNK_BYTES));
        let count = store.0.lock().unwrap().len();
        migrate(&loaded, &path, &store).unwrap();
        assert_eq!(
            store.0.lock().unwrap().len(),
            count,
            "migration is idempotent"
        );
        delete(&store, loaded.servers[0].credential_ref.as_ref().unwrap()).unwrap();
        assert!(store.0.lock().unwrap().is_empty());
    }

    #[test]
    fn unavailable_store_preserves_original_file_and_never_saves_plaintext() {
        struct Locked;
        impl CredentialStore for Locked {
            fn set(&self, _: &str, _: &[u8]) -> Result<()> {
                Err(unavailable())
            }
            fn get(&self, _: &str) -> Result<Vec<u8>> {
                Err(unavailable())
            }
            fn delete(&self, _: &str) -> Result<()> {
                Ok(())
            }
        }
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("prism.json");
        let config = PrismConfig {
            servers: vec![server()],
            ..Default::default()
        };
        let original = serde_json::to_vec(&config).unwrap();
        std::fs::write(&path, &original).unwrap();
        assert!(migrate(&config, &path, &Locked).is_err());
        assert_eq!(std::fs::read(&path).unwrap(), original);
        assert!(config.save(&path).is_err());
    }

    #[test]
    fn failed_readback_preserves_plaintext_and_cleans_partial_credentials() {
        struct Corrupted(MemoryStore);
        impl CredentialStore for Corrupted {
            fn set(&self, key: &str, value: &[u8]) -> Result<()> {
                self.0.set(key, value)
            }
            fn get(&self, key: &str) -> Result<Vec<u8>> {
                if key.contains('/') {
                    Ok(b"corrupt".to_vec())
                } else {
                    self.0.get(key)
                }
            }
            fn delete(&self, key: &str) -> Result<()> {
                self.0.delete(key)
            }
        }
        let store = Corrupted(MemoryStore::default());
        let original = server();
        let mut candidate = original.clone();
        assert!(protect_server(&store, &mut candidate).is_err());
        assert_eq!(candidate, original);
        assert!(store.0 .0.lock().unwrap().is_empty());
    }

    #[tokio::test]
    async fn gateway_add_and_remove_protects_config_and_cleans_credentials() {
        use std::sync::Arc;
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("prism.json");
        PrismConfig {
            listen_port: 0,
            ..Default::default()
        }
        .save(&path)
        .unwrap();
        let store = Arc::new(MemoryStore::default());
        let gateway = crate::Gateway::start_with_credentials(
            path.clone(),
            dir.path().join("audit.jsonl"),
            store.clone(),
        )
        .await
        .unwrap();
        let added = gateway.add_server(server()).await.unwrap();
        assert!(added.args.is_empty());
        assert!(added.env.is_empty());
        assert!(added.credential_ref.is_some());
        assert_eq!(resolve(store.as_ref(), &added).unwrap().args, server().args);
        let disk = std::fs::read_to_string(&path).unwrap();
        assert!(!disk.contains("argument-secret"));
        assert!(!disk.contains("env-secret"));
        gateway.remove_server(&added.id).await.unwrap();
        assert!(store.0.lock().unwrap().is_empty());
        assert!(PrismConfig::load(&path).unwrap().servers.is_empty());
        gateway.shutdown().await;
    }

    #[test]
    #[ignore = "requires an unlocked native OS credential store"]
    fn native_store_round_trip() {
        let store = NativeStore::default();
        let mut server = server();
        protect_server(&store, &mut server).unwrap();
        let id = server.credential_ref.as_ref().unwrap();
        let resolved = resolve(&store, &server);
        let removed = delete(&store, id);
        assert!(resolved.is_ok());
        removed.unwrap();
        assert!(resolve(&store, &server).is_err());
    }
}
