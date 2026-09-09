//! The loopback listener as the operator sees it: a clash is state, retry and move are
//! explicit, and a refused move changes nothing.
use std::sync::Arc;
use std::time::Duration;

use super::*;
use crate::listener::{ListenerState, PortHolder};

async fn start(dir: &tempfile::TempDir, port: u16) -> Arc<Gateway> {
    let config = PrismConfig {
        listen_port: port,
        ..Default::default()
    };
    let path = dir.path().join("prism.json");
    config.save(&path).unwrap();
    Gateway::start_with_credentials(
        path,
        dir.path().join("audit.jsonl"),
        Arc::new(crate::credentials::tests::MemoryStore::default()),
    )
    .await
    .unwrap()
}

fn take_port() -> (std::net::TcpListener, u16) {
    let taken = std::net::TcpListener::bind(("127.0.0.1", 0)).unwrap();
    let port = taken.local_addr().unwrap().port();
    (taken, port)
}

async fn answers(port: u16) -> bool {
    tokio::time::timeout(
        Duration::from_secs(2),
        tokio::net::TcpStream::connect(("127.0.0.1", port)),
    )
    .await
    .is_ok_and(|r| r.is_ok())
}

async fn issuer_at(port: u16) -> Option<String> {
    let url = format!("http://127.0.0.1:{port}/.well-known/oauth-authorization-server");
    let body: serde_json::Value = reqwest::get(url).await.ok()?.json().await.ok()?;
    body.get("issuer")?.as_str().map(str::to_string)
}

#[tokio::test]
async fn a_taken_port_is_reported_and_retry_binds_once_it_is_free() {
    let dir = tempfile::tempdir().unwrap();
    let (taken, port) = take_port();
    let gateway = start(&dir, port).await;

    let status = gateway.status().await;
    assert!(!status.listening);
    assert_eq!(status.listen_port, port);
    assert_eq!(
        status.listener,
        ListenerState::PortInUse { port, holder: None }
    );

    let err = gateway.retry_listener().await.unwrap_err();
    assert_eq!(
        err.to_string(),
        format!("invalid argument: Port {port} is in use")
    );

    drop(taken);
    let mut events = gateway.subscribe();
    gateway.retry_listener().await.unwrap();
    assert!(matches!(
        events.recv().await,
        Ok(GatewayEvent::ListenerChanged)
    ));
    let status = gateway.status().await;
    assert!(status.listening);
    assert_eq!(status.listener, ListenerState::Listening);
    assert_eq!(status.listen_port, port);
    assert_eq!(
        issuer_at(port).await.as_deref(),
        Some(&*format!("http://127.0.0.1:{port}"))
    );
    // Listening already: a second retry is a no-op, not a second server.
    gateway.retry_listener().await.unwrap();
    gateway.shutdown().await;
}

#[tokio::test]
async fn another_prism_on_the_port_is_named() {
    let first_dir = tempfile::tempdir().unwrap();
    let first = start(&first_dir, 0).await;
    let port = first.listen_port();
    assert_ne!(port, 0, "port 0 reports the port the OS chose");

    let second_dir = tempfile::tempdir().unwrap();
    let second = start(&second_dir, port).await;
    assert_eq!(
        second.status().await.listener,
        ListenerState::PortInUse {
            port,
            holder: Some(PortHolder::Prism)
        }
    );
    assert_eq!(
        second.retry_listener().await.unwrap_err().to_string(),
        format!("invalid argument: Port {port} is in use by another copy of Prism")
    );
    second.shutdown().await;
    first.shutdown().await;
}

#[tokio::test]
async fn a_refused_move_changes_nothing_and_an_accepted_one_moves_everything() {
    let dir = tempfile::tempdir().unwrap();
    let gateway = start(&dir, 0).await;
    let old = gateway.listen_port();
    let (taken, target) = take_port();

    let err = gateway.set_listen_port(target).await.unwrap_err();
    assert_eq!(
        err.to_string(),
        format!("invalid argument: Port {target} is in use")
    );
    let status = gateway.status().await;
    assert!(status.listening);
    assert_eq!(status.listen_port, old);
    assert_eq!(
        PrismConfig::load(dir.path().join("prism.json"))
            .unwrap()
            .listen_port,
        0
    );

    drop(taken);
    let mut events = gateway.subscribe();
    gateway.set_listen_port(target).await.unwrap();
    assert!(matches!(
        events.recv().await,
        Ok(GatewayEvent::ListenerChanged)
    ));
    assert_eq!(gateway.listen_port(), target);
    assert_eq!(
        PrismConfig::load(dir.path().join("prism.json"))
            .unwrap()
            .listen_port,
        target
    );
    assert!(answers(target).await);
    assert_eq!(
        issuer_at(target).await.as_deref(),
        Some(&*format!("http://127.0.0.1:{target}"))
    );
    assert_eq!(
        gateway.connect_snippet().unwrap().url,
        format!("http://127.0.0.1:{target}/mcp")
    );
    assert!(gateway
        .hook_url("codex")
        .starts_with(&format!("http://127.0.0.1:{target}/hooks/")));
    // The old server stops accepting once its connections drain.
    let mut tries = 0;
    while answers(old).await && tries < 50 {
        tokio::time::sleep(Duration::from_millis(50)).await;
        tries += 1;
    }
    assert!(!answers(old).await, "old port still answers");

    // Same port again is a no-op; port 0 is refused.
    gateway.set_listen_port(target).await.unwrap();
    assert!(gateway.set_listen_port(0).await.is_err());
    gateway.shutdown().await;
}

#[tokio::test]
async fn a_suggested_port_is_free_and_not_the_configured_one() {
    let dir = tempfile::tempdir().unwrap();
    let (_taken, port) = take_port();
    let gateway = start(&dir, port).await;
    let suggested = gateway.suggest_port().await.unwrap();
    assert_ne!(suggested, port);
    assert!(std::net::TcpListener::bind(("127.0.0.1", suggested)).is_ok());
    gateway.shutdown().await;
}

#[tokio::test]
async fn shutdown_reports_stopped() {
    let dir = tempfile::tempdir().unwrap();
    let gateway = start(&dir, 0).await;
    gateway.shutdown().await;
    let status = gateway.status().await;
    assert!(!status.listening);
    assert_eq!(status.listener, ListenerState::Stopped);
}

/// One GET over a raw socket, so the Host header is exactly what the test says.
async fn get_with_host(port: u16, host: &str, path: &str) -> (u16, String) {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let mut stream = tokio::net::TcpStream::connect(("127.0.0.1", port))
        .await
        .unwrap();
    stream
        .write_all(
            format!("GET {path} HTTP/1.1\r\nHost: {host}\r\nConnection: close\r\n\r\n").as_bytes(),
        )
        .await
        .unwrap();
    let mut raw = Vec::new();
    stream.read_to_end(&mut raw).await.unwrap();
    let text = String::from_utf8_lossy(&raw).into_owned();
    let status = text
        .split_whitespace()
        .nth(1)
        .and_then(|s| s.parse().ok())
        .unwrap_or(0);
    let body = text
        .split_once("\r\n\r\n")
        .map(|(_, b)| b.to_string())
        .unwrap_or_default();
    (status, body)
}

fn issuer_in(body: &str) -> Option<String> {
    serde_json::from_str::<serde_json::Value>(body)
        .ok()?
        .get("issuer")?
        .as_str()
        .map(str::to_string)
}

#[tokio::test]
async fn on_the_network_the_issuer_is_the_origin_the_client_dialed() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prism.json");
    PrismConfig {
        listen_port: 0,
        listen_address: crate::ListenAddress::Network,
        ..Default::default()
    }
    .save(&path)
    .unwrap();
    let gateway = Gateway::start_with_credentials(
        path.clone(),
        dir.path().join("audit.jsonl"),
        Arc::new(crate::credentials::tests::MemoryStore::default()),
    )
    .await
    .unwrap();
    let port = gateway.listen_port();
    let status = gateway.status().await;
    assert!(status.listening);
    assert_eq!(status.listen_address, crate::ListenAddress::Network);
    if let Some(url) = &status.network_url {
        assert!(url.starts_with("http://") && url.ends_with(&format!(":{port}/mcp")));
        assert!(!url.contains("127.0.0.1"));
    }

    let meta = "/.well-known/oauth-authorization-server";
    // Loopback clients still see the loopback issuer.
    let (code, body) = get_with_host(port, &format!("127.0.0.1:{port}"), meta).await;
    assert_eq!(code, 200);
    assert_eq!(
        issuer_in(&body).as_deref(),
        Some(&*format!("http://127.0.0.1:{port}"))
    );
    // A client that dialed the machine's address sees that address, so its resource check holds.
    let (code, body) = get_with_host(port, &format!("192.0.2.10:{port}"), meta).await;
    assert_eq!(code, 200);
    assert_eq!(
        issuer_in(&body).as_deref(),
        Some(&*format!("http://192.0.2.10:{port}"))
    );
    let (code, body) = get_with_host(
        port,
        &format!("192.0.2.10:{port}"),
        "/.well-known/oauth-protected-resource",
    )
    .await;
    assert_eq!(code, 200);
    assert!(body.contains(&format!("\"resource\":\"http://192.0.2.10:{port}/\"")));
    // The bearer challenge points at the same origin.
    let (code, _) = get_with_host(port, &format!("192.0.2.10:{port}"), "/mcp").await;
    assert_eq!(code, 401);
    // A name is still a rebinding attempt.
    let (code, _) = get_with_host(port, &format!("prism.example:{port}"), meta).await;
    assert_eq!(code, 403);

    // Back to loopback only: addresses stop passing, the port and tokens stay.
    let mut events = gateway.subscribe();
    gateway
        .set_listen_address(crate::ListenAddress::Loopback)
        .await
        .unwrap();
    assert!(matches!(
        events.recv().await,
        Ok(GatewayEvent::ListenerChanged)
    ));
    assert_eq!(gateway.listen_port(), port);
    assert_eq!(
        PrismConfig::load(&path).unwrap().listen_address,
        crate::ListenAddress::Loopback
    );
    assert_eq!(gateway.status().await.network_url, None);
    let mut tries = 0;
    loop {
        let (code, _) = get_with_host(port, &format!("192.0.2.10:{port}"), meta).await;
        if code == 403 || tries == 50 {
            assert_eq!(code, 403);
            break;
        }
        tries += 1;
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    let (code, _) = get_with_host(port, &format!("127.0.0.1:{port}"), meta).await;
    assert_eq!(code, 200);
    // Same address again is a no-op.
    gateway
        .set_listen_address(crate::ListenAddress::Loopback)
        .await
        .unwrap();
    gateway.shutdown().await;
}

#[tokio::test]
async fn the_default_stays_loopback_only() {
    let dir = tempfile::tempdir().unwrap();
    let gateway = start(&dir, 0).await;
    let port = gateway.listen_port();
    let status = gateway.status().await;
    assert_eq!(status.listen_address, crate::ListenAddress::Loopback);
    assert_eq!(status.network_url, None);
    assert_eq!(gateway.connect_snippet().unwrap().network_url, None);
    let (code, _) = get_with_host(
        port,
        &format!("192.0.2.10:{port}"),
        "/.well-known/oauth-authorization-server",
    )
    .await;
    assert_eq!(code, 403);
    gateway.shutdown().await;
}
