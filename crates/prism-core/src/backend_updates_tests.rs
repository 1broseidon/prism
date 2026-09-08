use super::*;
use crate::credentials::tests::MemoryStore;
use crate::{Gateway, PrismConfig};
use rmcp::model::*;
use rmcp::service::{ClientLifecycleMode, ClientServiceExt, RequestContext, SubscriptionContext};
use rmcp::{ErrorData, RoleServer, ServerHandler};
use std::borrow::Cow;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};

fn tool(name: &str) -> Tool {
    Tool::new(
        name.to_string(),
        "fixture",
        serde_json::json!({"type":"object"})
            .as_object()
            .unwrap()
            .clone(),
    )
}

#[derive(Clone)]
struct DynamicServer {
    version: ProtocolVersion,
    updates: bool,
    tools: Arc<RwLock<Vec<Tool>>>,
    changes: tokio::sync::broadcast::Sender<()>,
    peers: Arc<RwLock<Vec<Peer<RoleServer>>>>,
    fail: Arc<AtomicBool>,
    lists: Arc<AtomicUsize>,
    subscriptions: Arc<AtomicUsize>,
    calls: Arc<AtomicUsize>,
    held_list: Arc<std::sync::Mutex<Option<tokio::sync::oneshot::Receiver<()>>>>,
    listing: Arc<Notify>,
}

impl ServerHandler for DynamicServer {
    fn supported_protocol_versions(&self) -> Cow<'static, [ProtocolVersion]> {
        Cow::Owned(vec![self.version.clone()])
    }
    fn get_info(&self) -> ServerInfo {
        let capabilities = if self.updates {
            ServerCapabilities::builder()
                .enable_tools()
                .enable_tool_list_changed()
                .build()
        } else {
            ServerCapabilities::builder().enable_tools().build()
        };
        ServerInfo::new(capabilities)
    }
    async fn discover(
        &self,
        _: RequestContext<RoleServer>,
    ) -> std::result::Result<DiscoverResult, ErrorData> {
        if self.version < ProtocolVersion::V_2026_07_28 {
            // A pre-discovery server must force the actual initialize fallback,
            // not advertise old versions through a modern stateless lifecycle.
            return Err(ErrorData::method_not_found::<DiscoverRequestMethod>());
        }
        Ok(DiscoverResult::from_server_info(
            vec![self.version.clone()],
            self.get_info(),
        ))
    }
    async fn list_tools(
        &self,
        request: Option<PaginatedRequestParams>,
        context: RequestContext<RoleServer>,
    ) -> std::result::Result<ListToolsResult, ErrorData> {
        self.lists.fetch_add(1, Ordering::SeqCst);
        if self.fail.load(Ordering::SeqCst) {
            return Err(ErrorData::internal_error("fixture failed", None));
        }
        if self.version < ProtocolVersion::V_2026_07_28 {
            self.peers.write().await.push(context.peer);
        }
        let offset = request
            .and_then(|r| r.cursor)
            .and_then(|c| c.parse::<usize>().ok())
            .unwrap_or(0);
        let mut result = ListToolsResult::default();
        {
            let tools = self.tools.read().await;
            // One tool per page exercises the catalog's complete pagination.
            result.tools = tools.iter().skip(offset).take(1).cloned().collect();
            if offset + 1 < tools.len() {
                result.next_cursor = Some((offset + 1).to_string());
            }
        }
        let held = self.held_list.lock().unwrap().take();
        if let Some(held) = held {
            self.listing.notify_one();
            let _ = held.await;
        }
        Ok(result)
    }
    fn accepted_subscription_filter(
        &self,
        requested: &SubscriptionFilter,
    ) -> Option<SubscriptionFilter> {
        Some(requested.intersection(&SubscriptionFilter::builder().tools_list_changed().build()))
    }
    async fn listen(&self, context: SubscriptionContext) -> std::result::Result<(), ErrorData> {
        let mut changes = self.changes.subscribe();
        self.subscriptions.fetch_add(1, Ordering::SeqCst);
        loop {
            tokio::select! {
                _ = context.cancelled() => break,
                changed = changes.recv() => {
                    if changed.is_err() { break; }
                    if context.sink().notify_tool_list_changed().await.is_err() { break; }
                }
            }
        }
        Ok(())
    }
    async fn call_tool(
        &self,
        _: CallToolRequestParams,
        _: RequestContext<RoleServer>,
    ) -> std::result::Result<CallToolResponse, ErrorData> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        Ok(CallToolResult::success(vec![ContentBlock::text("called")]).into())
    }
}

struct Fixture {
    server: DynamicServer,
    config: ServerConfig,
    task: tokio::task::JoinHandle<()>,
}
impl Drop for Fixture {
    fn drop(&mut self) {
        self.task.abort();
    }
}
impl Fixture {
    async fn new(version: ProtocolVersion, updates: bool) -> Self {
        use rmcp::transport::streamable_http_server::{
            session::local::LocalSessionManager, tower::StreamableHttpService,
        };
        let server = DynamicServer {
            version,
            updates,
            tools: Arc::new(RwLock::new(vec![tool("one"), tool("two")])),
            changes: tokio::sync::broadcast::channel(8).0,
            peers: Default::default(),
            fail: Default::default(),
            lists: Default::default(),
            subscriptions: Default::default(),
            calls: Default::default(),
            held_list: Default::default(),
            listing: Default::default(),
        };
        let handler = server.clone();
        let service = StreamableHttpService::new(
            move || Ok(handler.clone()),
            Arc::new(LocalSessionManager::default()),
            rmcp::transport::StreamableHttpServerConfig::default().disable_allowed_hosts(),
        );
        let listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
            .await
            .unwrap();
        let url = format!("http://{}/mcp", listener.local_addr().unwrap());
        let task = tokio::spawn(async move {
            axum::serve(listener, axum::Router::new().nest_service("/mcp", service))
                .await
                .unwrap();
        });
        let config = serde_json::from_value(
            serde_json::json!({"id":"fixture", "name":"fixture", "url":url, "enabled":true}),
        )
        .unwrap();
        Self {
            server,
            config,
            task,
        }
    }
    async fn change(&self, tools: Vec<Tool>) {
        *self.server.tools.write().await = tools;
        let _ = self.server.changes.send(());
        // Only one notification is necessary even if multiple list pages registered the peer.
        if let Some(peer) = self.server.peers.read().await.first() {
            peer.notify_tool_list_changed().await.unwrap();
        }
    }
}

async fn eventually(mut condition: impl AsyncFnMut() -> bool) {
    tokio::time::timeout(Duration::from_secs(5), async {
        while !condition().await {
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
}

async fn gateway() -> (Arc<Gateway>, tempfile::TempDir, u16) {
    let dir = tempfile::tempdir().unwrap();
    let listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
        .await
        .unwrap();
    let port = listener.local_addr().unwrap().port();
    drop(listener);
    let config = PrismConfig {
        listen_port: port,
        ..Default::default()
    };
    let path = dir.path().join("prism.json");
    config.save(&path).unwrap();
    let gateway = Gateway::start_with_credentials(
        path,
        dir.path().join("audit.jsonl"),
        Arc::new(MemoryStore::default()),
    )
    .await
    .unwrap();
    eventually(async || {
        tokio::net::TcpStream::connect(("127.0.0.1", port))
            .await
            .is_ok()
    })
    .await;
    (gateway, dir, port)
}

async fn downstream(port: u16, bearer: &str, modern: bool) -> McpClient {
    use rmcp::transport::streamable_http_client::{
        StreamableHttpClientTransport, StreamableHttpClientTransportConfig,
    };
    let config =
        StreamableHttpClientTransportConfig::with_uri(format!("http://127.0.0.1:{port}/mcp"))
            .custom_headers(
                [(
                    http::header::AUTHORIZATION,
                    format!("Bearer {bearer}").parse().unwrap(),
                )]
                .into(),
            );
    let mode = if modern {
        ClientLifecycleMode::Discover {
            preferred_versions: vec![ProtocolVersion::V_2026_07_28],
        }
    } else {
        ClientLifecycleMode::Initialize
    };
    Upstream::default()
        .serve_with_lifecycle(
            StreamableHttpClientTransport::with_client(reqwest::Client::new(), config),
            mode,
        )
        .await
        .unwrap()
}

#[tokio::test]
async fn upstream_changes_reach_legacy_and_modern_agents_and_exposure_is_enforced() {
    for modern_upstream in [false, true] {
        let version = if modern_upstream {
            ProtocolVersion::V_2026_07_28
        } else {
            ProtocolVersion::V_2025_11_25
        };
        let fixture = Fixture::new(version, true).await;
        let (gateway, _dir, port) = gateway().await;
        gateway.add_server(fixture.config.clone()).await.unwrap();
        let token = gateway.create_manual_agent("fixture-client").await.unwrap();
        let legacy = downstream(port, &token.token, false).await;
        let modern = downstream(port, &token.token, true).await;
        let mut subscription = modern
            .listen(SubscriptionFilter::builder().tools_list_changed().build())
            .await
            .unwrap();
        assert_eq!(modern.list_all_tools().await.unwrap().len(), 2);
        if modern_upstream {
            eventually(async || fixture.server.subscriptions.load(Ordering::SeqCst) > 0).await;
        }
        fixture.change(vec![tool("three")]).await;
        tokio::time::timeout(Duration::from_secs(5), subscription.next())
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        tokio::time::timeout(Duration::from_secs(5), legacy.service().changed.notified())
            .await
            .unwrap();
        assert_eq!(
            modern.list_all_tools().await.unwrap()[0].name,
            "fixture__three"
        );
        assert!(modern
            .call_tool(CallToolRequestParams::new("fixture__one"))
            .await
            .is_err());
        assert!(modern
            .call_tool(CallToolRequestParams::new("fixture__missing"))
            .await
            .is_err());

        gateway
            .set_tool_exposed("fixture", "three", false)
            .await
            .unwrap();
        tokio::time::timeout(Duration::from_secs(5), subscription.next())
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        tokio::time::timeout(Duration::from_secs(5), legacy.service().changed.notified())
            .await
            .unwrap();
        assert!(modern.list_all_tools().await.unwrap().is_empty());
        assert!(modern
            .call_tool(CallToolRequestParams::new("fixture__three"))
            .await
            .is_err());
        assert_eq!(fixture.server.calls.load(Ordering::SeqCst), 0);
        gateway
            .set_tool_exposed("fixture", "three", true)
            .await
            .unwrap();
        tokio::time::timeout(Duration::from_secs(5), subscription.next())
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        assert_eq!(modern.list_all_tools().await.unwrap().len(), 1);
        drop(subscription);
        legacy.cancel().await.unwrap();
        modern.cancel().await.unwrap();
        gateway.shutdown().await;
    }
}

#[tokio::test]
async fn refresh_failures_preserve_catalog_retry_and_stop_cancels_updates() {
    let fixture = Fixture::new(ProtocolVersion::V_2026_07_28, true).await;
    let (events, _) = crate::events::channel();
    let manager = BackendManager::new(events, Arc::new(MemoryStore::default()));
    manager.start(fixture.config.clone()).await;
    eventually(async || fixture.server.subscriptions.load(Ordering::SeqCst) > 0).await;
    fixture.server.fail.store(true, Ordering::SeqCst);
    let before = fixture.server.lists.load(Ordering::SeqCst);
    fixture.change(vec![tool("new")]).await;
    eventually(async || fixture.server.lists.load(Ordering::SeqCst) > before).await;
    assert!(manager.resolve_tool("fixture__one").await.is_some());
    fixture.server.fail.store(false, Ordering::SeqCst);
    eventually(async || manager.resolve_tool("fixture__new").await.is_some()).await;
    let (peer, generation) = {
        let catalog = manager.backends.read().await;
        let backend = &catalog.entries["fixture"];
        (
            backend.client.as_ref().unwrap().peer().clone(),
            backend.generation,
        )
    };
    manager.stop("fixture").await;
    let _ = refresh_tools(
        &Arc::downgrade(&manager.backends),
        &manager.events,
        "fixture",
        generation,
        &peer,
    )
    .await;
    assert!(manager.list_tools(false).await.is_empty());
}

#[tokio::test]
async fn unsupported_upstreams_remain_usable_and_refresh_on_restart() {
    let fixture = Fixture::new(ProtocolVersion::V_2026_07_28, false).await;
    let manager = BackendManager::new(crate::events::channel().0, Arc::new(MemoryStore::default()));
    manager.start(fixture.config.clone()).await;
    assert_eq!(fixture.server.subscriptions.load(Ordering::SeqCst), 0);
    fixture.change(vec![tool("new")]).await;
    assert!(manager.resolve_tool("fixture__one").await.is_some());
    manager.restart("fixture").await.unwrap();
    assert!(manager.resolve_tool("fixture__new").await.is_some());
    assert_eq!(fixture.server.subscriptions.load(Ordering::SeqCst), 0);
    manager.stop("fixture").await;
}

#[tokio::test]
async fn notification_refresh_waits_for_an_older_explicit_refresh() {
    let fixture = Fixture::new(ProtocolVersion::V_2025_11_25, true).await;
    *fixture.server.tools.write().await = vec![tool("old")];
    let manager = Arc::new(BackendManager::new(
        crate::events::channel().0,
        Arc::new(MemoryStore::default()),
    ));
    manager.start(fixture.config.clone()).await;
    let (release, held) = tokio::sync::oneshot::channel();
    *fixture.server.held_list.lock().unwrap() = Some(held);
    let refreshing = manager.clone();
    let refresh = tokio::spawn(async move { refreshing.list_tools(true).await });
    fixture.server.listing.notified().await;
    let before = fixture.server.lists.load(Ordering::SeqCst);
    fixture.change(vec![tool("new")]).await;
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert_eq!(fixture.server.lists.load(Ordering::SeqCst), before);
    release.send(()).unwrap();
    refresh.await.unwrap();
    eventually(async || manager.resolve_tool("fixture__new").await.is_some()).await;
    assert!(manager.resolve_tool("fixture__old").await.is_none());
    manager.stop("fixture").await;
}

#[tokio::test]
async fn replacing_a_backend_during_refresh_cannot_restore_the_old_catalog() {
    let fixture = Fixture::new(ProtocolVersion::V_2025_11_25, true).await;
    *fixture.server.tools.write().await = vec![tool("old")];
    let manager = Arc::new(BackendManager::new(
        crate::events::channel().0,
        Arc::new(MemoryStore::default()),
    ));
    manager.start(fixture.config.clone()).await;
    let (release, held) = tokio::sync::oneshot::channel();
    *fixture.server.held_list.lock().unwrap() = Some(held);
    let refreshing = manager.clone();
    let refresh = tokio::spawn(async move { refreshing.list_tools(true).await });
    fixture.server.listing.notified().await;
    *fixture.server.tools.write().await = vec![tool("new")];
    manager.start(fixture.config.clone()).await;
    let _ = release.send(());
    refresh.await.unwrap();
    assert!(manager.resolve_tool("fixture__new").await.is_some());
    assert!(manager.resolve_tool("fixture__old").await.is_none());
    manager.stop("fixture").await;
}

#[tokio::test]
async fn notifications_refresh_only_the_affected_backend() {
    let first = Fixture::new(ProtocolVersion::V_2025_11_25, true).await;
    let mut other = Fixture::new(ProtocolVersion::V_2025_11_25, true).await;
    other.config.id = "other".into();
    other.config.name = "other".into();
    let manager = BackendManager::new(crate::events::channel().0, Arc::new(MemoryStore::default()));
    manager.start(first.config.clone()).await;
    manager.start(other.config.clone()).await;
    let before = other.server.lists.load(Ordering::SeqCst);
    first.change(vec![tool("new")]).await;
    eventually(async || manager.resolve_tool("fixture__new").await.is_some()).await;
    assert_eq!(other.server.lists.load(Ordering::SeqCst), before);
    assert!(manager.resolve_tool("other__one").await.is_some());
    manager.stop("fixture").await;
    manager.stop("other").await;
}

#[tokio::test]
async fn routing_uses_exact_pairs_and_rejects_ambiguous_names() {
    let manager = BackendManager::new(crate::events::channel().0, Arc::new(MemoryStore::default()));
    {
        let mut catalog = manager.backends.write().await;
        for (id, name, tools) in [
            ("a", "files", vec![tool("read"), tool("extra__read")]),
            ("b", "files__extra", vec![tool("read"), tool("write")]),
        ] {
            catalog.entries.insert(
                id.into(),
                Backend {
                    config: serde_json::from_value(
                        serde_json::json!({"id":id,"name":name,"command":"fixture"}),
                    )
                    .unwrap(),
                    status: BackendStatus::Running {
                        tool_count: tools.len(),
                    },
                    tools,
                    client: None,
                    refresh: Default::default(),
                    generation: uuid::Uuid::new_v4(),
                    stop: CancellationToken::new(),
                },
            );
        }
        catalog.reindex();
    }
    assert_eq!(manager.resolve_tool("files__read").await.unwrap().0.id, "a");
    assert_eq!(
        manager
            .resolve_tool("files__extra__write")
            .await
            .unwrap()
            .0
            .id,
        "b"
    );
    assert!(manager.resolve_tool("files__extra__read").await.is_none());
    assert!(manager.resolve_tool("files__nonexistent").await.is_none());
    assert_eq!(manager.list_tools(false).await.len(), 2);
    manager.remove("b").await;
    assert_eq!(
        manager
            .resolve_tool("files__extra__read")
            .await
            .unwrap()
            .0
            .id,
        "a"
    );
}
