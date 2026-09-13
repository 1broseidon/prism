use super::*;
use crate::backend::{BackendManager, BackendStatus};
use crate::credentials::tests::MemoryStore;
use axum::response::IntoResponse;
use rmcp::transport::streamable_http_client::StreamableHttpClientTransport;
use std::collections::BTreeMap;

const SECRET: &str = "Bearer fixture-secret https://user:password@example.invalid/private?token=fixture-secret auth 401";

fn server(url: String) -> ServerConfig {
    ServerConfig {
        id: "fixture".into(),
        name: "fixture".into(),
        command: String::new(),
        args: vec![],
        env: BTreeMap::new(),
        credential_ref: None,
        enabled: true,
        url: Some(url),
        auth: HttpAuth::None,
        headers: BTreeMap::new(),
        oauth_ref: None,
        hidden_tools: Default::default(),
    }
}

async fn fixture(
    status: StatusCode,
    rpc: Option<i32>,
    challenge: bool,
) -> (String, tokio::task::JoinHandle<()>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!(
        "http://user:password@127.0.0.1:{}/mcp?token=fixture-secret",
        listener.local_addr().unwrap().port()
    );
    let app = Router::new().route("/mcp", axum::routing::post(move |axum::Json(request): axum::Json<serde_json::Value>| async move {
        let mut response = match rpc {
            Some(code) => (status, axum::Json(serde_json::json!({ "jsonrpc":"2.0", "id":request["id"], "error":{"code":code,"message":SECRET,"data":{"token":SECRET}} }))).into_response(),
            None => (status, SECRET).into_response(),
        };
        if challenge { response.headers_mut().insert(http::header::WWW_AUTHENTICATE, http::HeaderValue::from_static("Bearer error=\"insufficient_scope\", scope=\"fixture-secret\"")); }
        response
    }));
    let task = tokio::spawn(async move {
        axum::serve(listener, app).await.unwrap();
    });
    (url, task)
}

#[tokio::test]
async fn numeric_handshake_causes_reach_status_and_events_without_provider_text() {
    for (status, rpc, challenge, category, expected_http) in [
        (
            StatusCode::BAD_REQUEST,
            Some(-32600),
            false,
            ConnectionFailureKind::Protocol,
            Some(400),
        ),
        (
            StatusCode::MISDIRECTED_REQUEST,
            None,
            false,
            ConnectionFailureKind::HostRejected,
            Some(421),
        ),
        (
            StatusCode::FORBIDDEN,
            None,
            false,
            ConnectionFailureKind::Forbidden,
            Some(403),
        ),
        (
            StatusCode::FORBIDDEN,
            None,
            true,
            ConnectionFailureKind::Forbidden,
            Some(403),
        ),
        (
            StatusCode::OK,
            Some(-32603),
            false,
            ConnectionFailureKind::Rpc,
            None,
        ),
        (
            StatusCode::OK,
            None,
            false,
            ConnectionFailureKind::InvalidResponse,
            None,
        ),
    ] {
        let (url, task) = fixture(status, rpc, challenge).await;
        let (events, mut receiver) = crate::events::channel();
        let manager = BackendManager::new(events, Arc::new(MemoryStore::default()));
        manager.start(server(url)).await;
        let (_, result) = manager.snapshot().await.pop().unwrap();
        let BackendStatus::Failed {
            error,
            diagnostic: Some(diagnostic),
        } = &result
        else {
            panic!("expected a structured failure: {result:?}");
        };
        assert_eq!(diagnostic.category, category);
        assert_eq!(diagnostic.http_status, expected_http);
        assert_eq!(diagnostic.rpc_code, rpc);
        if let Some(code) = rpc {
            assert!(error.contains(&code.to_string()));
        }
        let mut serialized = serde_json::to_string(&result).unwrap();
        while let Ok(event) = receiver.try_recv() {
            serialized.push_str(&serde_json::to_string(&event).unwrap());
        }
        for secret in [
            "fixture-secret",
            "password",
            "example.invalid",
            "insufficient_scope",
        ] {
            assert!(!serialized.contains(secret), "leaked {secret}");
        }
        task.abort();
    }
}

#[tokio::test]
async fn forbidden_with_challenge_is_not_an_oauth_signin_request() {
    let (url, task) = fixture(StatusCode::FORBIDDEN, None, true).await;
    let transport = StreamableHttpClientTransport::with_client(
        http_client().unwrap(),
        transport_config(&url, &BTreeMap::new()).unwrap(),
    );
    let result = handshake(
        Upstream::default().serve_with_lifecycle(transport, remote_lifecycle()),
        HttpAuth::Oauth,
        &url,
        HANDSHAKE_TIMEOUT,
    )
    .await;
    assert!(matches!(
        result,
        Err(Error::Connection(ConnectionFailure {
            category: ConnectionFailureKind::Forbidden,
            http_status: Some(403),
            ..
        }))
    ));
    task.abort();
}

#[tokio::test]
async fn handshake_timeout_has_a_closed_diagnostic() {
    let result = handshake(
        std::future::pending(),
        HttpAuth::None,
        "http://127.0.0.1",
        Duration::from_millis(5),
    )
    .await;
    assert!(matches!(
        result,
        Err(Error::Connection(ConnectionFailure {
            category: ConnectionFailureKind::Timeout,
            ..
        }))
    ));
}

#[tokio::test]
async fn command_protocol_errors_keep_the_code_and_discard_stderr() {
    let script = r#"import json,sys
print('stderr-secret', file=sys.stderr, flush=True)
for line in sys.stdin:
    request=json.loads(line)
    if 'id' in request:
        print(json.dumps({'jsonrpc':'2.0','id':request['id'],'error':{'code':-32600,'message':'auth 401 stdout-secret'}}),flush=True)
"#;
    let mut config = server(String::new());
    config.url = None;
    config.command = "python3".into();
    config.args = vec!["-u".into(), "-c".into(), script.into()];
    let (events, _) = crate::events::channel();
    let manager = BackendManager::new(events, Arc::new(MemoryStore::default()));
    manager.start(config).await;
    let (_, status) = manager.snapshot().await.pop().unwrap();
    assert!(matches!(
        &status,
        BackendStatus::Failed {
            diagnostic: Some(ConnectionFailure {
                category: ConnectionFailureKind::Protocol,
                rpc_code: Some(-32600),
                ..
            }),
            ..
        }
    ));
    let serialized = serde_json::to_string(&status).unwrap();
    assert!(!serialized.contains("stdout-secret") && !serialized.contains("stderr-secret"));
}
