use super::*;
use crate::{credentials::tests::MemoryStore, Gateway};
use serde_json::{json, Value};
use std::sync::{
    atomic::{AtomicBool, AtomicUsize, Ordering},
    Arc,
};

fn manifest(url: &str) -> Value {
    json!({"version":1,"servers":[{"id":"github","name":"GitHub","url":url,"auth":"header",
        "headers":{"Authorization":{"env":"TEST_TOKEN","prefix":"Bearer "}}}]})
}

fn apply(
    path: &Path,
    input: Value,
    store: &dyn CredentialStore,
    value: Option<&str>,
) -> Result<ProvisionReport> {
    apply_with_store(
        path,
        ProvisionManifest::parse(&serde_json::to_vec(&input).unwrap())?,
        store,
        &|_| value.map(str::to_owned),
    )
}

#[test]
fn apply_is_idempotent_updates_in_place_and_preserves_operator_state() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prism.json");
    let store = MemoryStore::default();
    let input = manifest("http://localhost:1234/mcp");
    assert_eq!(
        apply(&path, input.clone(), &store, Some("private-one"))
            .unwrap()
            .added,
        1
    );
    let mut config = PrismConfig::load(&path).unwrap();
    config.servers[0]
        .hidden_tools
        .insert("delete_repository".into());
    config.do_not_disturb = true;
    config.rules.push(serde_json::from_value(json!({"id":"rule","server_id":"github","tool":"ping","decision":"deny","scope":"always","created_at":"2026-09-13T00:00:00Z"})).unwrap());
    let mut extra = config.servers[0].clone();
    extra.id = "unrelated".into();
    extra.name = "Other".into();
    extra.enabled = false;
    // A hand-edited profile can share a reference: updating one must preserve the other.
    config.servers.push(extra.clone());
    config.save(&path).unwrap();
    let original_ref = config.servers[0].credential_ref.clone();
    assert_eq!(
        apply(&path, input.clone(), &store, Some("private-one"))
            .unwrap()
            .unchanged,
        1
    );
    let stable = std::fs::read(&path).unwrap();
    assert_eq!(
        apply(&path, input.clone(), &store, Some("private-one"))
            .unwrap()
            .unchanged,
        1
    );
    assert_eq!(std::fs::read(&path).unwrap(), stable);
    let mut changed = input;
    changed["servers"][0]["name"] = json!("Renamed");
    changed["servers"][0]["url"] = json!("http://127.0.0.1:4321/mcp");
    assert_eq!(
        apply(&path, changed, &store, Some("private-two"))
            .unwrap()
            .updated,
        1
    );
    let updated = PrismConfig::load(&path).unwrap();
    let server = updated.servers.iter().find(|s| s.id == "github").unwrap();
    assert_eq!(server.hidden_tools, config.servers[0].hidden_tools);
    assert_eq!(updated.rules, config.rules);
    assert!(updated.do_not_disturb);
    assert!(updated.servers.contains(&extra));
    assert_eq!(
        credentials::resolve(&store, server).unwrap().headers["Authorization"],
        "Bearer private-two"
    );
    assert_ne!(server.credential_ref, original_ref);
    assert_eq!(
        credentials::resolve(&store, &extra).unwrap().headers["Authorization"],
        "Bearer private-one"
    );
    let bytes = std::fs::read_to_string(&path).unwrap();
    assert!(
        !bytes.contains("private-one")
            && !bytes.contains("private-two")
            && !bytes.contains("TEST_TOKEN")
    );
}

#[test]
fn invalid_or_missing_credentials_change_nothing_and_errors_do_not_echo_input() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prism.json");
    let store = MemoryStore::default();
    let input = manifest("http://localhost/mcp");
    apply(&path, input.clone(), &store, Some("original")).unwrap();
    let before = std::fs::read(&path).unwrap();
    assert!(apply(&path, input.clone(), &store, None).is_err());
    for field in ["auth", "credential_ref", "headers"] {
        let mut bad = input.clone();
        bad["servers"][0][field] = json!("never-print-this-secret");
        let error = apply(&path, bad, &store, Some("never-print-this-secret"))
            .unwrap_err()
            .to_string();
        assert!(!error.contains("never-print-this-secret"));
    }
    let mut missing_auth = input.clone();
    missing_auth["servers"][0]
        .as_object_mut()
        .unwrap()
        .remove("auth");
    assert!(apply(&path, missing_auth, &store, Some("secret")).is_err());
    let mut duplicate = input.clone();
    duplicate["servers"]
        .as_array_mut()
        .unwrap()
        .push(input["servers"][0].clone());
    assert!(apply(&path, duplicate, &store, Some("secret")).is_err());
    let mut wrong_transport = input;
    wrong_transport["servers"][0] = json!({"id":"github","name":"GitHub","command":"fixture"});
    assert!(apply(&path, wrong_transport, &store, Some("secret")).is_err());
    assert_eq!(std::fs::read(&path).unwrap(), before);
}

struct FailingStore {
    inner: MemoryStore,
    fail: AtomicBool,
    fail_after: AtomicUsize,
}
impl CredentialStore for FailingStore {
    fn get(&self, key: &str) -> Result<Vec<u8>> {
        if self.fail.load(Ordering::SeqCst) {
            Err(Error::Invalid("credential store unavailable".into()))
        } else {
            self.inner.get(key)
        }
    }
    fn set(&self, key: &str, value: &[u8]) -> Result<()> {
        if self.fail_after.fetch_sub(1, Ordering::SeqCst) == 1 {
            return Err(Error::Invalid("credential store unavailable".into()));
        }
        self.inner.set(key, value)
    }
    fn delete(&self, key: &str) -> Result<()> {
        self.inner.delete(key)
    }
}

#[test]
fn locked_store_and_partial_writes_preserve_the_original_config_and_credentials() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prism.json");
    let store = FailingStore {
        inner: MemoryStore::default(),
        fail: AtomicBool::new(false),
        fail_after: AtomicUsize::new(100),
    };
    let input = manifest("http://localhost/mcp");
    apply(&path, input.clone(), &store, Some("original")).unwrap();
    let config = PrismConfig::load(&path).unwrap();
    let before = std::fs::read(&path).unwrap();
    store.fail.store(true, Ordering::SeqCst);
    assert!(apply(&path, input.clone(), &store, Some("changed")).is_err());
    store.fail.store(false, Ordering::SeqCst);
    store.fail_after.store(2, Ordering::SeqCst);
    assert!(apply(&path, input, &store, Some("changed")).is_err());
    assert_eq!(std::fs::read(&path).unwrap(), before);
    assert_eq!(
        credentials::resolve(&store, &config.servers[0])
            .unwrap()
            .headers["Authorization"],
        "Bearer original"
    );
}

#[tokio::test]
async fn profile_lock_excludes_imports_and_duplicate_gateways() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prism.json");
    PrismConfig {
        listen_port: 0,
        ..Default::default()
    }
    .save(&path)
    .unwrap();
    let store = Arc::new(MemoryStore::default());
    let gateway = Gateway::start_with_credentials(
        path.clone(),
        dir.path().join("audit.jsonl"),
        store.clone(),
    )
    .await
    .unwrap();
    assert!(apply(
        &path,
        manifest("http://localhost/mcp"),
        store.as_ref(),
        Some("value")
    )
    .unwrap_err()
    .to_string()
    .contains("profile is in use"));
    assert!(Gateway::start_with_credentials(
        path.clone(),
        dir.path().join("other-audit.jsonl"),
        store.clone()
    )
    .await
    .is_err());
    gateway.shutdown().await;
    let weak = Arc::downgrade(&gateway);
    drop(gateway);
    tokio::time::timeout(std::time::Duration::from_secs(2), async {
        while weak.upgrade().is_some() {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    assert!(apply(
        &path,
        json!({"version":1,"servers":[]}),
        store.as_ref(),
        None
    )
    .is_ok());
}

#[derive(Clone)]
struct Pinger;
impl rmcp::ServerHandler for Pinger {
    fn get_info(&self) -> rmcp::model::ServerInfo {
        rmcp::model::ServerInfo::new(
            rmcp::model::ServerCapabilities::builder()
                .enable_tools()
                .build(),
        )
    }
    async fn list_tools(
        &self,
        _: Option<rmcp::model::PaginatedRequestParams>,
        _: rmcp::service::RequestContext<rmcp::RoleServer>,
    ) -> std::result::Result<rmcp::model::ListToolsResult, rmcp::ErrorData> {
        Ok(rmcp::model::ListToolsResult {
            tools: vec![rmcp::model::Tool::new(
                "ping",
                "pong",
                serde_json::Map::new(),
            )],
            ..Default::default()
        })
    }
}

#[tokio::test]
async fn provisioned_header_server_connects_on_startup_despite_oauth_metadata() {
    use axum::{response::IntoResponse, routing::get, Router};
    use rmcp::transport::{
        streamable_http_server::{
            session::local::LocalSessionManager, tower::StreamableHttpService,
        },
        StreamableHttpServerConfig,
    };
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let url = format!("http://{address}/mcp");
    let service: StreamableHttpService<Pinger, LocalSessionManager> = StreamableHttpService::new(
        || Ok(Pinger),
        LocalSessionManager::default().into(),
        StreamableHttpServerConfig::default().disable_allowed_hosts(),
    );
    let metadata = json!({"resource":url,"authorization_servers":[format!("http://{address}")]});
    let app = Router::new()
        .route(
            "/.well-known/oauth-protected-resource",
            get(move || {
                let metadata = metadata.clone();
                async move { axum::Json(metadata) }
            }),
        )
        .nest_service("/mcp", service)
        .layer(axum::middleware::from_fn(
            |request: axum::extract::Request, next: axum::middleware::Next| async move {
                if request.uri().path().starts_with("/.well-known")
                    || request
                        .headers()
                        .get("Authorization")
                        .and_then(|v| v.to_str().ok())
                        == Some("Bearer provision-test-secret")
                {
                    next.run(request).await
                } else {
                    (
                        http::StatusCode::UNAUTHORIZED,
                        [("www-authenticate", "Bearer")],
                    )
                        .into_response()
                }
            },
        ));
    let serving = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prism.json");
    let store = Arc::new(MemoryStore::default());
    PrismConfig {
        listen_port: 0,
        ..Default::default()
    }
    .save(&path)
    .unwrap();
    apply(
        &path,
        manifest(&url),
        store.as_ref(),
        Some("provision-test-secret"),
    )
    .unwrap();
    for _ in 0..2 {
        let gateway = Gateway::start_with_credentials(
            path.clone(),
            dir.path().join("audit.jsonl"),
            store.clone(),
        )
        .await
        .unwrap();
        let servers = gateway.servers().await;
        assert!(matches!(
            servers[0].status,
            crate::BackendStatus::Running { tool_count: 1 }
        ));
        assert_eq!(gateway.server_tools("github").await[0].name, "ping");
        gateway.shutdown().await;
        let weak = Arc::downgrade(&gateway);
        drop(gateway);
        tokio::time::timeout(std::time::Duration::from_secs(2), async {
            while weak.upgrade().is_some() {
                tokio::task::yield_now().await;
            }
        })
        .await
        .unwrap();
    }
    assert!(!std::fs::read_to_string(&path)
        .unwrap()
        .contains("provision-test-secret"));
    serving.abort();
}
