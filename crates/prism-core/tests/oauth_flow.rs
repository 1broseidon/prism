//! The full OAuth 2.1 dance against a live gateway on a loopback port.

use std::collections::HashMap;
use std::time::Duration;

use base64::Engine;
use prism_core::{AgentStatus, Gateway, PrismConfig};
use sha2::{Digest, Sha256};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

struct Reply {
    status: u16,
    headers: HashMap<String, String>,
    body: String,
}

/// A deliberately tiny HTTP/1.1 client so the test has no client-side dependency.
async fn http(port: u16, method: &str, path: &str, headers: &[(&str, &str)], body: &str) -> Reply {
    let mut stream = tokio::net::TcpStream::connect(("127.0.0.1", port))
        .await
        .expect("connect");
    let mut req = format!("{method} {path} HTTP/1.1\r\nConnection: close\r\n");
    if !headers
        .iter()
        .any(|(name, _)| name.eq_ignore_ascii_case("host"))
    {
        req.push_str(&format!("Host: 127.0.0.1:{port}\r\n"));
    }
    for (k, v) in headers {
        req.push_str(&format!("{k}: {v}\r\n"));
    }
    req.push_str(&format!("Content-Length: {}\r\n\r\n{body}", body.len()));
    stream.write_all(req.as_bytes()).await.expect("write");
    let mut raw = Vec::new();
    stream.read_to_end(&mut raw).await.expect("read");
    let text = String::from_utf8_lossy(&raw).to_string();
    let (head, body) = text.split_once("\r\n\r\n").unwrap_or((&text, ""));
    let mut lines = head.lines();
    let status = lines
        .next()
        .and_then(|l| l.split_whitespace().nth(1))
        .and_then(|s| s.parse().ok())
        .expect("status line");
    let headers = lines
        .filter_map(|l| l.split_once(':'))
        .map(|(k, v)| (k.to_ascii_lowercase(), v.trim().to_string()))
        .collect();
    Reply {
        status,
        headers,
        body: body.to_string(),
    }
}

fn form(pairs: &[(&str, &str)]) -> String {
    pairs
        .iter()
        .map(|(k, v)| format!("{k}={}", urlencoding(v)))
        .collect::<Vec<_>>()
        .join("&")
}

fn urlencoding(v: &str) -> String {
    v.bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                (b as char).to_string()
            }
            _ => format!("%{b:02X}"),
        })
        .collect()
}

fn query_param(url: &str, key: &str) -> Option<String> {
    url.split_once('?')?
        .1
        .split('&')
        .filter_map(|kv| kv.split_once('='))
        .find(|(k, _)| *k == key)
        .map(|(_, v)| v.to_string())
}

async fn wait_for_signin(gateway: &Gateway) -> prism_core::PendingSignIn {
    for _ in 0..100 {
        if let Some(s) = gateway.pending_signins().into_iter().next() {
            return s;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    panic!("no sign-in request appeared");
}

/// Register, authorize (approving whatever the panel would show), and exchange: one agent
/// with tokens, ready to talk MCP.
async fn signed_in_agent(
    gateway: &std::sync::Arc<Gateway>,
    port: u16,
    name: &str,
) -> (String, String) {
    let reg = http(
        port,
        "POST",
        "/register",
        &[("Content-Type", "application/json")],
        &format!(r#"{{"client_name":"{name}","redirect_uris":["http://localhost:4444/cb"]}}"#),
    )
    .await;
    let reg: serde_json::Value = serde_json::from_str(&reg.body).unwrap();
    let client_id = reg["client_id"].as_str().unwrap().to_string();
    let verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk";
    let challenge = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .encode(Sha256::digest(verifier.as_bytes()));
    let path = format!(
        "/authorize?response_type=code&client_id={client_id}&redirect_uri={}&code_challenge={challenge}&code_challenge_method=S256",
        urlencoding("http://localhost:4444/cb")
    );
    let parked = tokio::spawn(async move { http(port, "GET", &path, &[], "").await });
    let signin = wait_for_signin(gateway).await;
    if signin.needs_consent {
        gateway.decide_signin(&signin.id, true).unwrap();
    } else {
        gateway.decide_agent(&signin.agent_id, true).await.unwrap();
    }
    let redirect = tokio::time::timeout(Duration::from_secs(5), parked)
        .await
        .unwrap()
        .unwrap();
    let code = query_param(&redirect.headers["location"], "code").unwrap();
    let tokens = http(
        port,
        "POST",
        "/token",
        &[("Content-Type", "application/x-www-form-urlencoded")],
        &form(&[
            ("grant_type", "authorization_code"),
            ("code", &code),
            ("code_verifier", verifier),
            ("client_id", &client_id),
        ]),
    )
    .await;
    assert_eq!(tokens.status, 200, "{}", tokens.body);
    let tokens: serde_json::Value = serde_json::from_str(&tokens.body).unwrap();
    (
        signin.agent_id,
        tokens["access_token"].as_str().unwrap().to_string(),
    )
}

const INIT: &str = r#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}"#;
const LIST_TOOLS: &str = r#"{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}"#;

async fn start() -> (std::sync::Arc<Gateway>, u16, tempfile::TempDir) {
    let dir = tempfile::tempdir().expect("tempdir");
    let port = {
        let probe = std::net::TcpListener::bind("127.0.0.1:0").expect("probe");
        probe.local_addr().expect("addr").port()
    };
    let config = PrismConfig {
        listen_port: port,
        ..PrismConfig::default()
    };
    let config_path = dir.path().join("prism.json");
    config.save(&config_path).expect("save");
    let gateway = Gateway::start(&config_path, dir.path().join("audit.jsonl"))
        .await
        .expect("start");
    for _ in 0..50 {
        if tokio::net::TcpStream::connect(("127.0.0.1", port))
            .await
            .is_ok()
        {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    (gateway, port, dir)
}

#[tokio::test]
async fn loopback_host_and_browser_origin_checks_preserve_native_clients() {
    let (gateway, port, _dir) = start().await;
    for (method, path) in [
        ("GET", "/.well-known/oauth-authorization-server"),
        ("GET", "/.well-known/oauth-protected-resource"),
        ("GET", "/authorize"),
        ("POST", "/register"),
        ("POST", "/token"),
        ("POST", "/revoke"),
        ("POST", "/mcp"),
        ("GET", "/mcp"),
        ("DELETE", "/mcp"),
    ] {
        let response = http(port, method, path, &[("Host", "evil.example")], "").await;
        assert_eq!(response.status, 403, "{path}: {}", response.body);
    }
    for host in ["localhost:1", "localhost", "localhost.evil.example"] {
        let response = http(port, "POST", "/mcp", &[("Host", host)], "{}").await;
        assert_eq!(response.status, 403, "MCP must pass the shared Host guard");
    }
    let meta = http(
        port,
        "GET",
        "/.well-known/oauth-authorization-server",
        &[],
        "",
    )
    .await;
    assert_eq!(meta.status, 200);
    for origin in ["https://evil.example", "null", "http://localhost:1"] {
        let response = http(port, "POST", "/mcp", &[("Origin", origin)], "{}").await;
        assert_eq!(response.status, 403);
    }
    let own_origin = format!("http://localhost:{port}");
    let own = http(port, "POST", "/mcp", &[("Origin", &own_origin)], "{}").await;
    assert_eq!(own.status, 401, "same-origin reaches bearer authentication");
    let native = http(port, "POST", "/mcp", &[], "{}").await;
    assert_eq!(native.status, 401, "native requests need no Origin");
    for path in ["/register", "/token", "/revoke"] {
        let response = http(
            port,
            "POST",
            path,
            &[
                ("Origin", "https://evil.example"),
                ("Content-Type", "application/x-www-form-urlencoded"),
            ],
            "token=x",
        )
        .await;
        assert_eq!(
            response.status, 403,
            "form POST {path} is checked without relying on CORS"
        );
    }
    let native_token = http(
        port,
        "POST",
        "/token",
        &[("Content-Type", "application/x-www-form-urlencoded")],
        "grant_type=invalid",
    )
    .await;
    assert_eq!(
        native_token.status, 400,
        "native form POST reaches OAuth validation"
    );
    for origin in ["https://client.example", "null"] {
        let browser = http(port, "GET", "/authorize", &[("Origin", origin)], "").await;
        assert_eq!(
            browser.status, 400,
            "authorization navigation reaches parameter validation"
        );
        assert!(browser.body.contains("client_id is required"));
    }
    gateway.shutdown().await;
}

#[tokio::test]
async fn register_authorize_token_and_call() {
    let (gateway, port, _dir) = start().await;

    // Discovery: the resource says who its authorization server is.
    let meta = http(
        port,
        "GET",
        "/.well-known/oauth-authorization-server",
        &[],
        "",
    )
    .await;
    assert_eq!(meta.status, 200);
    let meta: serde_json::Value = serde_json::from_str(&meta.body).unwrap();
    assert_eq!(
        meta["registration_endpoint"],
        format!("http://127.0.0.1:{port}/register")
    );
    assert_eq!(meta["code_challenge_methods_supported"][0], "S256");

    // No token: 401 that points at the resource metadata.
    let anon = http(
        port,
        "POST",
        "/mcp",
        &[("Content-Type", "application/json")],
        "{}",
    )
    .await;
    assert_eq!(anon.status, 401);
    let www = &anon.headers["www-authenticate"];
    assert!(
        www.contains("resource_metadata=\"http://127.0.0.1:"),
        "{www}"
    );

    // Dynamic registration is open.
    let reg = http(
        port,
        "POST",
        "/register",
        &[("Content-Type", "application/json")],
        r#"{"client_name":"claude-code","redirect_uris":["http://localhost:4444/cb"],"token_endpoint_auth_method":"none"}"#,
    )
    .await;
    assert_eq!(reg.status, 201, "{}", reg.body);
    let reg: serde_json::Value = serde_json::from_str(&reg.body).unwrap();
    let client_id = reg["client_id"].as_str().unwrap().to_string();

    // A non-loopback http redirect is refused.
    let bad = http(
        port,
        "POST",
        "/register",
        &[("Content-Type", "application/json")],
        r#"{"client_name":"x","redirect_uris":["http://evil.example/cb"]}"#,
    )
    .await;
    assert_eq!(bad.status, 400);
    assert!(bad.body.contains("invalid_redirect_uri"));

    // PKCE material.
    let verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk";
    let challenge = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .encode(Sha256::digest(verifier.as_bytes()));

    // The browser parks on /authorize until the operator decides in the panel.
    let path = format!(
        "/authorize?response_type=code&client_id={client_id}&redirect_uri={}&code_challenge={challenge}&code_challenge_method=S256&state=xyz&resource={}",
        urlencoding("http://localhost:4444/cb"),
        urlencoding(&format!("http://127.0.0.1:{port}/mcp"))
    );
    let parked = tokio::spawn(async move { http(port, "GET", &path, &[], "").await });

    let mut agent_id = None;
    for _ in 0..100 {
        if let Some(a) = gateway
            .agents()
            .await
            .into_iter()
            .find(|a| a.agent.status == AgentStatus::Pending)
        {
            agent_id = Some(a.agent.id);
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    let agent_id = agent_id.expect("pending agent appeared");
    let agent = gateway
        .agents()
        .await
        .into_iter()
        .find(|a| a.agent.id == agent_id)
        .unwrap();
    assert_eq!(agent.agent.name, "claude-code");
    assert_eq!(agent.agent.client_id.as_deref(), Some(client_id.as_str()));

    gateway.decide_agent(&agent_id, true).await.unwrap();
    let redirect = tokio::time::timeout(Duration::from_secs(5), parked)
        .await
        .expect("authorize returned")
        .unwrap();
    assert_eq!(redirect.status, 303, "{}", redirect.body);
    let location = redirect.headers["location"].clone();
    assert!(
        location.starts_with("http://localhost:4444/cb?"),
        "{location}"
    );
    assert_eq!(query_param(&location, "state").as_deref(), Some("xyz"));
    let code = query_param(&location, "code").expect("code");

    // Wrong verifier: no token, and the code is burnt.
    let bad = http(
        port,
        "POST",
        "/token",
        &[("Content-Type", "application/x-www-form-urlencoded")],
        &form(&[
            ("grant_type", "authorization_code"),
            ("code", &code),
            (
                "code_verifier",
                "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXX",
            ),
            ("client_id", &client_id),
        ]),
    )
    .await;
    assert_eq!(bad.status, 400);
    assert!(bad.body.contains("invalid_grant"));
    let again = http(
        port,
        "POST",
        "/token",
        &[("Content-Type", "application/x-www-form-urlencoded")],
        &form(&[
            ("grant_type", "authorization_code"),
            ("code", &code),
            ("code_verifier", verifier),
            ("client_id", &client_id),
        ]),
    )
    .await;
    assert_eq!(again.status, 400, "a code is single use");

    // An approved agent signing in again still needs a yes: a public client id is not proof.
    let path = format!(
        "/authorize?response_type=code&client_id={client_id}&redirect_uri={}&code_challenge={challenge}&code_challenge_method=S256",
        urlencoding("http://localhost:4444/cb")
    );
    let parked = tokio::spawn({
        let path = path.clone();
        async move { http(port, "GET", &path, &[], "").await }
    });
    let signin = wait_for_signin(&gateway).await;
    assert_eq!(signin.agent_id, agent_id);
    assert!(signin.needs_consent);
    assert_eq!(gateway.status().await.pending_signins, 1);
    gateway.decide_signin(&signin.id, false).unwrap();
    let denied = tokio::time::timeout(Duration::from_secs(5), parked)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(denied.status, 303);
    assert_eq!(
        query_param(&denied.headers["location"], "error").as_deref(),
        Some("access_denied")
    );
    assert!(
        gateway.agents().await[0].tokens.is_empty(),
        "no token after a refused sign-in"
    );

    let parked = tokio::spawn({
        let path = path.clone();
        async move { http(port, "GET", &path, &[], "").await }
    });
    let signin = wait_for_signin(&gateway).await;
    gateway.decide_signin(&signin.id, true).unwrap();
    let redirect = tokio::time::timeout(Duration::from_secs(5), parked)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(redirect.status, 303);
    let code = query_param(&redirect.headers["location"], "code").unwrap();
    assert_eq!(gateway.status().await.pending_signins, 0);

    let tokens = http(
        port,
        "POST",
        "/token",
        &[("Content-Type", "application/x-www-form-urlencoded")],
        &form(&[
            ("grant_type", "authorization_code"),
            ("code", &code),
            ("code_verifier", verifier),
            ("client_id", &client_id),
            ("redirect_uri", "http://localhost:4444/cb"),
        ]),
    )
    .await;
    assert_eq!(tokens.status, 200, "{}", tokens.body);
    let tokens: serde_json::Value = serde_json::from_str(&tokens.body).unwrap();
    let access = tokens["access_token"].as_str().unwrap().to_string();
    let refresh = tokens["refresh_token"].as_str().unwrap().to_string();
    assert_eq!(tokens["token_type"], "Bearer");

    // The token is the identity: initialize succeeds with a session.
    let init = http(
        port,
        "POST",
        "/mcp",
        &[
            ("Content-Type", "application/json"),
            ("Accept", "application/json, text/event-stream"),
            ("Authorization", &format!("Bearer {access}")),
        ],
        r#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"spoofed-name","version":"1"}}}"#,
    )
    .await;
    assert_eq!(init.status, 200, "{}", init.body);
    assert!(init.headers.contains_key("mcp-session-id"));
    let views = gateway.agents().await;
    assert_eq!(
        views.len(),
        1,
        "the announced name did not create a second agent"
    );
    assert_eq!(views[0].tokens.len(), 2);

    // Refresh rotates: the old refresh token dies with the exchange.
    let rotated = http(
        port,
        "POST",
        "/token",
        &[("Content-Type", "application/x-www-form-urlencoded")],
        &form(&[
            ("grant_type", "refresh_token"),
            ("refresh_token", &refresh),
            ("client_id", &client_id),
        ]),
    )
    .await;
    assert_eq!(rotated.status, 200, "{}", rotated.body);
    let replay = http(
        port,
        "POST",
        "/token",
        &[("Content-Type", "application/x-www-form-urlencoded")],
        &form(&[("grant_type", "refresh_token"), ("refresh_token", &refresh)]),
    )
    .await;
    assert_eq!(replay.status, 400);

    // Deny in the panel: every token is gone and the next call is a 401.
    gateway.decide_agent(&agent_id, false).await.unwrap();
    let after = http(
        port,
        "POST",
        "/mcp",
        &[
            ("Content-Type", "application/json"),
            ("Accept", "application/json, text/event-stream"),
            ("Authorization", &format!("Bearer {access}")),
        ],
        "{}",
    )
    .await;
    assert_eq!(after.status, 401);
    assert!(after.headers["www-authenticate"].contains("invalid_token"));
    assert!(gateway.agents().await[0].tokens.is_empty());

    // A denied agent gets access_denied and nothing else.
    let path = format!(
        "/authorize?response_type=code&client_id={client_id}&redirect_uri={}&code_challenge={challenge}&code_challenge_method=S256",
        urlencoding("http://localhost:4444/cb")
    );
    let denied = http(port, "GET", &path, &[], "").await;
    assert_eq!(denied.status, 303);
    assert_eq!(
        query_param(&denied.headers["location"], "error").as_deref(),
        Some("access_denied")
    );

    gateway.shutdown().await;
}

#[tokio::test]
async fn sessions_are_bound_to_the_identity_that_opened_them() {
    let (gateway, port, _dir) = start().await;
    let (agent_a, token_a) = signed_in_agent(&gateway, port, "alpha").await;
    let (agent_b, token_b) = signed_in_agent(&gateway, port, "beta").await;
    assert_ne!(agent_a, agent_b);

    let init = http(
        port,
        "POST",
        "/mcp",
        &[
            ("Content-Type", "application/json"),
            ("Accept", "application/json, text/event-stream"),
            ("Authorization", &format!("Bearer {token_a}")),
        ],
        INIT,
    )
    .await;
    assert_eq!(init.status, 200, "{}", init.body);
    let session = init.headers["mcp-session-id"].clone();

    // Beta holds a perfectly valid token, but this is alpha's session.
    let hijack = http(
        port,
        "POST",
        "/mcp",
        &[
            ("Content-Type", "application/json"),
            ("Accept", "application/json, text/event-stream"),
            ("Authorization", &format!("Bearer {token_b}")),
            ("Mcp-Session-Id", &session),
        ],
        LIST_TOOLS,
    )
    .await;
    assert_eq!(hijack.status, 403, "{}", hijack.body);
    let stream = http(
        port,
        "GET",
        "/mcp",
        &[
            ("Accept", "text/event-stream"),
            ("Authorization", &format!("Bearer {token_b}")),
            ("Mcp-Session-Id", &session),
        ],
        "",
    )
    .await;
    assert_eq!(stream.status, 403);
    let close = http(
        port,
        "DELETE",
        "/mcp",
        &[
            ("Authorization", &format!("Bearer {token_b}")),
            ("Mcp-Session-Id", &session),
        ],
        "",
    )
    .await;
    assert_eq!(close.status, 403);

    // The owner carries on.
    let own = http(
        port,
        "POST",
        "/mcp",
        &[
            ("Content-Type", "application/json"),
            ("Accept", "application/json, text/event-stream"),
            ("Authorization", &format!("Bearer {token_a}")),
            ("Mcp-Session-Id", &session),
        ],
        LIST_TOOLS,
    )
    .await;
    assert_eq!(own.status, 200, "{}", own.body);
    gateway.shutdown().await;
}

// Manual and OAuth clients use the same HTTP authentication and session ownership checks.
#[tokio::test]
async fn manual_tokens_replace_anonymous_access_and_revoke_live_sessions() {
    let (gateway, port, dir) = start().await;
    let (oauth_id, oauth_token) = signed_in_agent(&gateway, port, "same-name").await;
    let issued = gateway.create_manual_agent("same-name").await.unwrap();
    let other = gateway.create_manual_agent("same-name").await.unwrap();
    assert_ne!(issued.agent_id, oauth_id);
    assert_ne!(issued.agent_id, other.agent_id);
    assert_eq!(
        gateway.authenticate(&issued.token).await.as_deref(),
        Some(issued.agent_id.as_str())
    );
    let persisted = std::fs::read_to_string(dir.path().join("prism.json")).unwrap();
    assert!(!persisted.contains(&issued.token));
    assert!(!serde_json::to_string(&gateway.agents().await)
        .unwrap()
        .contains(&issued.token));
    let config: PrismConfig = serde_json::from_str(&persisted).unwrap();
    let record = config
        .tokens
        .iter()
        .find(|t| t.agent_id == issued.agent_id)
        .unwrap();
    assert_eq!(record.kind, prism_core::TokenKind::Manual);
    assert_eq!(record.hash, prism_core::hash_token(&issued.token));
    assert_eq!(record.expires_at, None);
    assert_eq!(record.client_id, None);
    assert_eq!(
        gateway
            .agents()
            .await
            .iter()
            .find(|a| a.agent.id == issued.agent_id)
            .unwrap()
            .agent
            .posture,
        prism_core::Posture::FirstUse
    );

    let base = [
        ("Content-Type", "application/json"),
        ("Accept", "application/json, text/event-stream"),
    ];
    let bearer = format!("Bearer {}", issued.token);
    let init = INIT.replace("\"x\"", "\"same-name\"");
    assert_eq!(http(port, "POST", "/mcp", &base, &init).await.status, 401);
    let initialized = http(
        port,
        "POST",
        "/mcp",
        &[base[0], base[1], ("Authorization", &bearer)],
        &init,
    )
    .await;
    assert_eq!(initialized.status, 200);
    let session = initialized.headers["mcp-session-id"].clone();
    let headers = [
        base[0],
        base[1],
        ("Authorization", bearer.as_str()),
        ("Mcp-Session-Id", session.as_str()),
    ];
    assert_eq!(
        http(port, "POST", "/mcp", &headers, LIST_TOOLS)
            .await
            .status,
        200
    );
    for wrong in [&oauth_token, &other.token] {
        assert_eq!(
            http(
                port,
                "POST",
                "/mcp",
                &[
                    base[0],
                    base[1],
                    ("Authorization", &format!("Bearer {wrong}")),
                    ("Mcp-Session-Id", &session)
                ],
                LIST_TOOLS
            )
            .await
            .status,
            403
        );
    }
    let refresh = http(
        port,
        "POST",
        "/token",
        &[("Content-Type", "application/x-www-form-urlencoded")],
        &form(&[
            ("grant_type", "refresh_token"),
            ("refresh_token", &issued.token),
        ]),
    )
    .await;
    assert_eq!(
        refresh.status, 400,
        "manual tokens cannot be exchanged for OAuth tokens"
    );
    assert!(gateway.replace_manual_token(&oauth_id).await.is_err());
    assert!(gateway.create_manual_agent("  ").await.is_err());

    let replacement = gateway
        .replace_manual_token(&issued.agent_id)
        .await
        .unwrap();
    assert_ne!(replacement.token, issued.token);
    assert_eq!(gateway.authenticate(&issued.token).await, None);
    for method in ["POST", "GET", "DELETE"] {
        assert_eq!(
            http(
                port,
                method,
                "/mcp",
                &headers,
                if method == "POST" { LIST_TOOLS } else { "" }
            )
            .await
            .status,
            401
        );
    }
    assert_eq!(
        gateway.authenticate(&replacement.token).await.as_deref(),
        Some(issued.agent_id.as_str())
    );
    assert_eq!(
        gateway
            .agents()
            .await
            .iter()
            .find(|a| a.agent.id == issued.agent_id)
            .unwrap()
            .tokens
            .len(),
        1
    );
    let fresh_bearer = format!("Bearer {}", replacement.token);
    // Same agent can continue its session with its replacement token.
    assert_eq!(
        http(
            port,
            "POST",
            "/mcp",
            &[
                base[0],
                base[1],
                ("Authorization", fresh_bearer.as_str()),
                ("Mcp-Session-Id", &session)
            ],
            LIST_TOOLS
        )
        .await
        .status,
        200
    );
    gateway.revoke_agent_tokens(&issued.agent_id).await.unwrap();
    assert_eq!(gateway.authenticate(&replacement.token).await, None);
    assert_eq!(
        gateway.authenticate(&oauth_token).await.as_deref(),
        Some(oauth_id.as_str())
    );
    let final_token = gateway
        .replace_manual_token(&issued.agent_id)
        .await
        .unwrap();
    gateway.decide_agent(&issued.agent_id, false).await.unwrap();
    assert_eq!(gateway.authenticate(&final_token.token).await, None);
    assert!(gateway
        .replace_manual_token(&issued.agent_id)
        .await
        .is_err());
    gateway.shutdown().await;
}

#[tokio::test]
async fn old_anonymous_settings_are_ignored_and_existing_grants_survive_provisioning() {
    let dir = tempfile::tempdir().unwrap();
    let port = std::net::TcpListener::bind("127.0.0.1:0")
        .unwrap()
        .local_addr()
        .unwrap()
        .port();
    let path = dir.path().join("prism.json");
    let legacy = serde_json::json!({
        "listen_port": port, "allow_unauthenticated": true,
        "agents": [{"id":"old", "name":"legacy", "token":"obsolete-key", "status":"approved", "created_at":"2026-09-01T00:00:00Z", "posture":"supervised"}],
        "rules": [{"id":"grant", "agent_id":"old", "server_id":null, "tool":"read_file", "decision":"deny", "scope":"always", "created_at":"2026-09-01T00:00:00Z"}]
    });
    std::fs::write(&path, legacy.to_string()).unwrap();
    let gateway = Gateway::start(&path, dir.path().join("audit.jsonl"))
        .await
        .unwrap();
    for _ in 0..50 {
        if tokio::net::TcpStream::connect(("127.0.0.1", port))
            .await
            .is_ok()
        {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    let headers = [
        ("Content-Type", "application/json"),
        ("Accept", "application/json, text/event-stream"),
    ];
    assert_eq!(http(port, "POST", "/mcp", &headers, INIT).await.status, 401);
    assert_eq!(gateway.authenticate("obsolete-key").await, None);
    assert_eq!(
        gateway.agents().await.len(),
        1,
        "bare requests cannot create agents"
    );
    let token = gateway.replace_manual_token("old").await.unwrap();
    let saved = PrismConfig::load(&path).unwrap();
    assert_eq!(saved.rules[0].id, "grant");
    assert_eq!(saved.agents[0].posture, prism_core::Posture::Supervised);
    let json = std::fs::read_to_string(&path).unwrap();
    assert!(!json.contains("allow_unauthenticated"));
    assert!(!json.contains("obsolete-key"));
    assert!(!json.contains(&token.token));
    gateway.shutdown().await;
    // Reopen the actual persisted file on another listener; tokens survive app restarts.
    let mut saved = saved;
    saved.listen_port = 0;
    saved.save(&path).unwrap();
    let reopened = Gateway::start(&path, dir.path().join("audit.jsonl"))
        .await
        .unwrap();
    assert_eq!(
        reopened.authenticate(&token.token).await.as_deref(),
        Some("old")
    );
    reopened.remove_agent("old").await.unwrap();
    assert_eq!(reopened.authenticate(&token.token).await, None);
    reopened.shutdown().await;
}

#[tokio::test]
async fn signin_caps_do_not_share_consent_and_release_cancelled_waiters() {
    let (gateway, _port, _dir) = start().await;
    let request = |client_id: &str| {
        serde_json::from_value::<prism_core::AuthorizeParams>(serde_json::json!({
        "client_id": client_id, "response_type": "code", "code_challenge": "test-challenge", "code_challenge_method": "S256"
    })).unwrap()
    };
    let mut clients = Vec::new();
    for i in 0..17 {
        clients.push(gateway.register_client(serde_json::from_value(serde_json::json!({
            "client_name": format!("cap-test-{i}"), "redirect_uris": ["http://localhost/cb"]
        })).unwrap()).await.unwrap());
    }
    let mut parked = Vec::new();
    for client in &clients[..16] {
        let g = gateway.clone();
        let params = request(&client.client_id);
        parked.push(tokio::spawn(async move { g.authorize(params).await }));
        if parked.len() == 1 {
            wait_for_signin(&gateway).await;
            let duplicate = tokio::time::timeout(
                Duration::from_secs(2),
                gateway.authorize(request(&client.client_id)),
            )
            .await
            .unwrap();
            let prism_core::AuthorizeOutcome::Redirect(uri) = duplicate else {
                panic!("expected OAuth error")
            };
            assert_eq!(
                query_param(&uri, "error").as_deref(),
                Some("temporarily_unavailable")
            );
            assert_eq!(gateway.pending_signins().len(), 1);
        }
    }
    for _ in 0..100 {
        if gateway.pending_signins().len() == 16 {
            break;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert_eq!(gateway.pending_signins().len(), 16);
    for client in [&clients[0], &clients[16]] {
        let outcome = tokio::time::timeout(
            Duration::from_secs(2),
            gateway.authorize(request(&client.client_id)),
        )
        .await
        .unwrap();
        let prism_core::AuthorizeOutcome::Redirect(uri) = outcome else {
            panic!("expected OAuth error")
        };
        assert_eq!(
            query_param(&uri, "error").as_deref(),
            Some("temporarily_unavailable")
        );
        assert!(query_param(&uri, "code").is_none());
    }
    assert_eq!(gateway.agents().await.len(), 16);
    parked[0].abort();
    let _ = (&mut parked[0]).await;
    assert_eq!(gateway.pending_signins().len(), 15);
    // The cancelled request's slot can be reused, with fresh consent.
    let g = gateway.clone();
    let params = request(&clients[0].client_id);
    let retry = tokio::spawn(async move { g.authorize(params).await });
    for _ in 0..100 {
        if gateway.pending_signins().len() == 16 {
            break;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    let signin = gateway
        .pending_signins()
        .into_iter()
        .find(|s| s.client_name == "cap-test-0")
        .unwrap();
    gateway.decide_agent(&signin.agent_id, true).await.unwrap();
    let prism_core::AuthorizeOutcome::Redirect(uri) = retry.await.unwrap() else {
        panic!("expected code")
    };
    assert!(query_param(&uri, "code").is_some());
    assert_eq!(gateway.pending_signins().len(), 15);
    for job in parked {
        job.abort();
    }
    gateway.shutdown().await;
}

#[tokio::test]
async fn oauth_http_limits_reject_floods_and_large_bodies() {
    let (gateway, port, _dir) = start().await;
    let oversized = serde_json::json!({"client_name": "x".repeat(33 * 1024), "redirect_uris": ["http://localhost/cb"]}).to_string();
    assert_eq!(
        http(
            port,
            "POST",
            "/register",
            &[("Content-Type", "application/json")],
            &oversized
        )
        .await
        .status,
        413
    );
    for (method, path, content_type, limit) in [
        ("POST", "/register", "application/json", 29),
        ("GET", "/authorize", "application/json", 30),
        ("POST", "/token", "application/x-www-form-urlencoded", 120),
        ("POST", "/revoke", "application/x-www-form-urlencoded", 60),
    ] {
        for _ in 0..limit {
            let reply = http(port, method, path, &[("Content-Type", content_type)], "").await;
            assert_ne!(reply.status, 429, "{path}");
        }
        let reply = http(port, method, path, &[("Content-Type", content_type)], "").await;
        assert_eq!(reply.status, 429, "{path}: {}", reply.body);
        assert!(reply.headers["retry-after"].parse::<u64>().unwrap() > 0);
        assert_eq!(reply.headers["cache-control"], "no-store");
    }
    assert_eq!(
        http(
            port,
            "GET",
            "/.well-known/oauth-authorization-server",
            &[],
            ""
        )
        .await
        .status,
        200
    );
    assert!(PrismConfig::load(&_dir.path().join("prism.json"))
        .unwrap()
        .clients
        .is_empty());
    gateway.shutdown().await;
}
