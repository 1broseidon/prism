use super::*;
use crate::credentials::{self, tests::MemoryStore, CredentialStore};
use std::collections::BTreeMap;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Mutex,
};
use std::time::Duration;

struct DeleteGate {
    id: String,
    entered: tokio::sync::oneshot::Sender<()>,
    release: std::sync::mpsc::Receiver<()>,
}

#[derive(Default)]
struct Store {
    inner: MemoryStore,
    gate: Mutex<Option<DeleteGate>>,
    fail_delete: AtomicBool,
}

impl CredentialStore for Store {
    fn set(&self, key: &str, value: &[u8]) -> Result<()> {
        self.inner.set(key, value)
    }
    fn get(&self, key: &str) -> Result<Vec<u8>> {
        self.inner.get(key)
    }
    fn delete(&self, key: &str) -> Result<()> {
        let gate = {
            let mut gate = self.gate.lock().unwrap();
            if gate.as_ref().is_some_and(|gate| gate.id == key) {
                gate.take()
            } else {
                None
            }
        };
        if let Some(gate) = gate {
            let _ = gate.entered.send(());
            gate.release
                .recv_timeout(Duration::from_secs(5))
                .expect("test releases cleanup");
        }
        if self.fail_delete.load(Ordering::SeqCst) {
            return Err(Error::Gateway("fixture keyring cleanup failure".into()));
        }
        self.inner.delete(key)
    }
}

async fn gateway() -> (tempfile::TempDir, Arc<Gateway>, Arc<Store>) {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prism.json");
    PrismConfig {
        listen_port: 0,
        ..Default::default()
    }
    .save(&path)
    .unwrap();
    let store = Arc::new(Store::default());
    let gateway =
        Gateway::start_with_credentials(path, dir.path().join("audit.jsonl"), store.clone())
            .await
            .unwrap();
    (dir, gateway, store)
}

fn server(id: &str) -> ServerConfig {
    ServerConfig {
        id: id.into(),
        name: id.into(),
        command: String::new(),
        args: vec![],
        env: Default::default(),
        enabled: false,
        credential_ref: None,
        url: Some("http://127.0.0.1:1/mcp".into()),
        auth: HttpAuth::Header,
        headers: headers("old-key"),
        oauth_ref: None,
        hidden_tools: Default::default(),
    }
}

fn headers(key: &str) -> BTreeMap<String, String> {
    BTreeMap::from([("Authorization".into(), format!("Bearer {key}"))])
}

#[tokio::test]
async fn shutdown_cancels_a_stalled_restart_without_waiting_for_its_mutation_lock() {
    let (_dir, gateway, _store) = gateway().await;
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!("http://{}/mcp", listener.local_addr().unwrap());
    let reached = Arc::new(tokio::sync::Notify::new());
    let request_reached = reached.clone();
    let app = axum::Router::new().fallback(move || {
        request_reached.notify_one();
        std::future::pending::<http::StatusCode>()
    });
    let serving = tokio::spawn(async move {
        axum::serve(listener, app).await.unwrap();
    });
    let mut config = server("slow");
    config.url = Some(url);
    gateway.add_server(config).await.unwrap();
    // Prepare an enabled restart without connecting in the setup step.
    gateway.config.write().await.servers[0].enabled = true;
    let restarting = gateway.clone();
    let restart = tokio::spawn(async move { restarting.restart_server("slow").await });
    tokio::time::timeout(Duration::from_secs(2), reached.notified())
        .await
        .unwrap();
    let started = std::time::Instant::now();
    tokio::time::timeout(Duration::from_secs(1), gateway.shutdown())
        .await
        .unwrap();
    tokio::time::timeout(Duration::from_secs(1), restart)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(started.elapsed() < Duration::from_secs(2));
    assert!(gateway
        .backends
        .snapshot()
        .await
        .iter()
        .all(|(_, status)| matches!(status, BackendStatus::Stopped)));
    assert!(gateway.backends.list_tools(false).await.is_empty());
    serving.abort();
}

#[tokio::test]
async fn shutdown_does_not_wait_for_credential_cleanup_or_allow_its_late_activation() {
    let (_dir, gateway, store) = gateway().await;
    let added = gateway.add_server(server("one")).await.unwrap();
    let (entered, waiting) = tokio::sync::oneshot::channel();
    let (release, receive) = std::sync::mpsc::channel();
    *store.gate.lock().unwrap() = Some(DeleteGate {
        id: added.credential_ref.unwrap(),
        entered,
        release: receive,
    });
    let updating = gateway.clone();
    let edit = tokio::spawn(async move {
        updating
            .update_server(
                "one",
                ServerUpdate {
                    headers: Some(headers("new-key")),
                    ..Default::default()
                },
            )
            .await
    });
    tokio::time::timeout(Duration::from_secs(2), waiting)
        .await
        .unwrap()
        .unwrap();
    let stopped = tokio::time::timeout(Duration::from_secs(1), gateway.shutdown()).await;
    // Release even on regression so the test never strands a blocking keyring thread.
    release.send(()).unwrap();
    stopped.unwrap();
    edit.await.unwrap().unwrap();
    assert!(gateway
        .backends
        .snapshot()
        .await
        .iter()
        .all(|(_, status)| matches!(status, BackendStatus::Stopped)));
}

#[tokio::test]
async fn edits_serialize_with_remove_newer_edit_and_restart_through_cleanup() {
    for operation in ["remove", "edit", "restart"] {
        let (_dir, gateway, store) = gateway().await;
        let added = gateway.add_server(server("one")).await.unwrap();
        let (entered, waiting) = tokio::sync::oneshot::channel();
        let (release, receive) = std::sync::mpsc::channel();
        *store.gate.lock().unwrap() = Some(DeleteGate {
            id: added.credential_ref.unwrap(),
            entered,
            release: receive,
        });
        let updating = gateway.clone();
        let first = tokio::spawn(async move {
            updating
                .update_server(
                    "one",
                    ServerUpdate {
                        name: Some("first".into()),
                        headers: Some(headers("replacement")),
                        ..Default::default()
                    },
                )
                .await
        });
        tokio::time::timeout(Duration::from_secs(2), waiting)
            .await
            .unwrap()
            .unwrap();
        // Configuration is already durable, but cleanup/activation is deliberately paused.
        assert_eq!(gateway.server_config("one").await.unwrap().name, "first");
        let changing = gateway.clone();
        let mut next = tokio::spawn(async move {
            match operation {
                "remove" => changing.remove_server("one").await,
                "edit" => changing
                    .update_server(
                        "one",
                        ServerUpdate {
                            name: Some("latest".into()),
                            ..Default::default()
                        },
                    )
                    .await
                    .map(|_| ()),
                _ => changing.restart_server("one").await,
            }
        });
        assert!(tokio::time::timeout(Duration::from_millis(50), &mut next)
            .await
            .is_err());
        // A different server is independent of the stalled keyring operation.
        tokio::time::timeout(Duration::from_secs(1), gateway.add_server(server("other")))
            .await
            .unwrap()
            .unwrap();
        release.send(()).unwrap();
        first.await.unwrap().unwrap();
        next.await.unwrap().unwrap();
        let snapshot = gateway.backends.snapshot().await;
        if operation == "remove" {
            assert!(gateway.server_config("one").await.is_err());
            assert!(!snapshot.iter().any(|(server, _)| server.id == "one"));
        } else {
            let expected = if operation == "edit" {
                "latest"
            } else {
                "first"
            };
            assert_eq!(
                snapshot
                    .iter()
                    .find(|(server, _)| server.id == "one")
                    .unwrap()
                    .0
                    .name,
                expected
            );
            let saved = gateway.server_config("one").await.unwrap();
            assert_eq!(saved.name, expected);
            assert_eq!(
                credentials::resolve(store.as_ref(), &saved)
                    .unwrap()
                    .headers,
                headers("replacement")
            );
        }
        gateway.shutdown().await;
    }
}

#[tokio::test]
async fn changing_origin_requires_explicit_headers_and_invalid_edits_never_commit() {
    let (_dir, gateway, store) = gateway().await;
    let added = gateway.add_server(server("one")).await.unwrap();
    for update in [
        ServerUpdate {
            url: Some("http://127.0.0.1:2/mcp".into()),
            ..Default::default()
        },
        ServerUpdate {
            headers: Some(BTreeMap::from([("bad header".into(), "secret".into())])),
            ..Default::default()
        },
        ServerUpdate {
            headers: Some(BTreeMap::new()),
            ..Default::default()
        },
    ] {
        assert!(matches!(
            gateway.update_server("one", update).await,
            Err(Error::Invalid(_))
        ));
        assert_eq!(gateway.server_config("one").await.unwrap(), added);
        assert_eq!(
            PrismConfig::load(&gateway.config_path).unwrap().servers[0],
            added
        );
    }
    let moved = gateway
        .update_server(
            "one",
            ServerUpdate {
                url: Some("http://127.0.0.1:2/mcp".into()),
                headers: Some(headers("new-origin-key")),
                ..Default::default()
            },
        )
        .await
        .unwrap();
    assert_eq!(moved.id, added.id);
    assert_eq!(
        credentials::resolve(store.as_ref(), &moved)
            .unwrap()
            .headers,
        headers("new-origin-key")
    );
    assert!(credentials::get_blob(store.as_ref(), added.credential_ref.as_ref().unwrap()).is_err());
    let none = gateway
        .update_server(
            "one",
            ServerUpdate {
                auth: Some(HttpAuth::None),
                ..Default::default()
            },
        )
        .await
        .unwrap();
    assert!(credentials::resolve(store.as_ref(), &none)
        .unwrap()
        .headers
        .is_empty());
    gateway.shutdown().await;
}

#[tokio::test]
async fn cleanup_failure_reports_a_committed_edit_and_keeps_the_new_credentials() {
    let (_dir, gateway, store) = gateway().await;
    let added = gateway.add_server(server("one")).await.unwrap();
    store.fail_delete.store(true, Ordering::SeqCst);
    assert!(matches!(
        gateway
            .update_server(
                "one",
                ServerUpdate {
                    name: Some("saved".into()),
                    headers: Some(headers("new-key")),
                    ..Default::default()
                }
            )
            .await,
        Err(Error::ServerUpdatedCleanupFailed)
    ));
    let saved = gateway.server_config("one").await.unwrap();
    assert_eq!(
        PrismConfig::load(&gateway.config_path).unwrap().servers[0],
        saved
    );
    assert_eq!(gateway.backends.snapshot().await[0].0.name, "saved");
    assert_eq!(
        credentials::resolve(store.as_ref(), &saved)
            .unwrap()
            .headers,
        headers("new-key")
    );
    assert_ne!(saved.credential_ref, added.credential_ref);
    gateway.shutdown().await;
}

#[tokio::test]
async fn changed_oauth_resource_and_signout_invalidate_late_browser_completions() {
    for operation in ["url", "signout", "remove", "rename"] {
        let (_dir, gateway, store) = gateway().await;
        let mut config = server("one");
        config.auth = HttpAuth::Oauth;
        config.headers.clear();
        let before = gateway.add_server(config).await.unwrap();
        let old_ref = before.oauth_ref.as_ref().unwrap();
        credentials::put_blob(
            store.as_ref(),
            old_ref,
            br#"{"client_id":"old-registration"}"#,
        )
        .unwrap();
        match operation {
            "url" => {
                gateway
                    .update_server(
                        "one",
                        ServerUpdate {
                            url: Some("http://127.0.0.1:1/another-resource".into()),
                            ..Default::default()
                        },
                    )
                    .await
                    .unwrap();
            }
            "signout" => gateway.sign_out_server("one").await.unwrap(),
            "remove" => gateway.remove_server("one").await.unwrap(),
            _ => {
                gateway
                    .update_server(
                        "one",
                        ServerUpdate {
                            name: Some("renamed".into()),
                            ..Default::default()
                        },
                    )
                    .await
                    .unwrap();
            }
        }
        if operation != "rename" {
            assert!(credentials::get_blob(store.as_ref(), old_ref).is_err());
        }
        // Simulate the credential save completing after the operator's mutation.
        credentials::put_blob(
            store.as_ref(),
            old_ref,
            br#"{"client_id":"late-registration"}"#,
        )
        .unwrap();
        gateway.finish_server_sign_in(before.clone(), Ok(())).await;
        gateway
            .finish_server_sign_in(before.clone(), Err(Error::Gateway("old failure".into())))
            .await;
        if operation == "remove" {
            assert!(gateway.backends.snapshot().await.is_empty());
        } else {
            let current = gateway.server_config("one").await.unwrap();
            if operation == "rename" {
                assert_eq!(gateway.backends.snapshot().await[0].0.name, "renamed");
                assert_eq!(current.oauth_ref, before.oauth_ref);
            } else {
                assert_ne!(current.oauth_ref, before.oauth_ref);
                assert_eq!(
                    gateway.backends.snapshot().await[0].1,
                    BackendStatus::Stopped
                );
            }
        }
        if operation != "rename" {
            assert!(credentials::get_blob(store.as_ref(), old_ref).is_err());
        }
        gateway.shutdown().await;
    }
}
