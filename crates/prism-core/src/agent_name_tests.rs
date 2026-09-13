use super::*;
use crate::credentials::tests::MemoryStore;
use std::time::Duration;

async fn gateway() -> (tempfile::TempDir, Arc<Gateway>, Arc<MemoryStore>) {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prism.json");
    PrismConfig {
        listen_port: 0,
        ..Default::default()
    }
    .save(&path)
    .unwrap();
    let store = Arc::new(MemoryStore::default());
    let gateway =
        Gateway::start_with_credentials(path, dir.path().join("audit.jsonl"), store.clone())
            .await
            .unwrap();
    (dir, gateway, store)
}

#[tokio::test]
async fn rename_changes_only_the_display_name_and_survives_a_real_restart() {
    let (dir, gateway, store) = gateway().await;
    let token = gateway
        .create_manual_agent("Original client")
        .await
        .unwrap();
    gateway
        .set_agent_policy(
            &token.agent_id,
            Some(Posture::Guided),
            Some(Attention::Notify),
        )
        .await
        .unwrap();
    gateway.ensure_host_agent("codex").await.unwrap();
    let server: ServerConfig = serde_json::from_value(serde_json::json!({"id":"remote", "name":"Remote", "url":"http://127.0.0.1:1/mcp", "auth":"header", "headers":{"Authorization":"Bearer fixture-secret"}, "enabled":false, "hidden_tools":["hidden"]})).unwrap();
    gateway.add_server(server).await.unwrap();
    gateway
        .add_rule(NewRule {
            agent_id: Some(token.agent_id.clone()),
            server_id: Some("remote".into()),
            tool: Some("read".into()),
            decision: RuleDecision::Allow,
            attention: None,
            scope: RuleScope::Always,
            minutes: None,
        })
        .await
        .unwrap();
    let before = gateway.config.read().await.clone();
    let mut events = gateway.subscribe();
    let renamed = gateway
        .rename_agent(&token.agent_id, "  Personal work  ")
        .await
        .unwrap();
    let mut expected = before.clone();
    expected
        .agents
        .iter_mut()
        .find(|a| a.id == token.agent_id)
        .unwrap()
        .name = "Personal work".into();
    assert_eq!(renamed.client_name, "Original client");
    assert_eq!(
        serde_json::to_value(&*gateway.config.read().await).unwrap(),
        serde_json::to_value(&expected).unwrap()
    );
    assert!(
        matches!(events.recv().await.unwrap(), GatewayEvent::AgentUpdated { agent_id } if agent_id == token.agent_id)
    );
    assert_eq!(
        gateway.authenticate(&token.token).await.as_deref(),
        Some(token.agent_id.as_str())
    );
    gateway
        .rename_agent("host:codex", "Terminal assistant")
        .await
        .unwrap();
    gateway.ensure_host_agent("codex").await.unwrap();
    let snapshot = serde_json::to_value(&*gateway.config.read().await).unwrap();
    gateway.shutdown().await;
    let weak = Arc::downgrade(&gateway);
    drop(gateway);
    tokio::time::timeout(Duration::from_secs(2), async {
        while weak.upgrade().is_some() {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    let restarted = Gateway::start_with_credentials(
        dir.path().join("prism.json"),
        dir.path().join("audit.jsonl"),
        store,
    )
    .await
    .unwrap();
    assert_eq!(
        serde_json::to_value(&*restarted.config.read().await).unwrap(),
        snapshot
    );
    assert_eq!(
        restarted.authenticate(&token.token).await.as_deref(),
        Some(token.agent_id.as_str())
    );
    restarted.ensure_host_agent("codex").await.unwrap();
    assert_eq!(
        restarted
            .config
            .read()
            .await
            .agents
            .iter()
            .find(|a| a.id == "host:codex")
            .unwrap()
            .name,
        "Terminal assistant"
    );
    restarted.shutdown().await;
}

#[tokio::test]
async fn colliding_renames_serialize_and_failed_save_does_not_change_memory_or_emit_success() {
    let (dir, gateway, _store) = gateway().await;
    let one = gateway.create_manual_agent("One").await.unwrap();
    let two = gateway.create_manual_agent("Two").await.unwrap();
    let (first, second) = tokio::join!(
        gateway.rename_agent(&one.agent_id, "Maße"),
        gateway.rename_agent(&two.agent_id, "MASSE")
    );
    assert_ne!(first.is_ok(), second.is_ok());
    let before = serde_json::to_value(&*gateway.config.read().await).unwrap();
    let path = dir.path().join("prism.json");
    let bytes = std::fs::read(&path).unwrap();
    let mut events = gateway.subscribe();
    assert!(gateway
        .rename_agent("removed-agent", "Missing")
        .await
        .is_err());
    std::fs::remove_file(&path).unwrap();
    std::fs::create_dir(&path).unwrap();
    assert!(gateway
        .rename_agent(&one.agent_id, "Unsaved")
        .await
        .is_err());
    assert_eq!(
        serde_json::to_value(&*gateway.config.read().await).unwrap(),
        before
    );
    assert!(events.try_recv().is_err());
    assert_eq!(
        gateway.authenticate(&one.token).await.as_deref(),
        Some(one.agent_id.as_str())
    );
    std::fs::remove_dir(&path).unwrap();
    std::fs::write(&path, bytes).unwrap();
    gateway
        .rename_agent(&one.agent_id, "Saved after retry")
        .await
        .unwrap();
    assert_eq!(
        PrismConfig::load(&path)
            .unwrap()
            .agents
            .iter()
            .find(|a| a.id == one.agent_id)
            .unwrap()
            .name,
        "Saved after retry"
    );
    gateway.shutdown().await;
}

#[tokio::test]
async fn manual_names_cannot_claim_harness_identity_or_prevent_automatic_registration() {
    let (_dir, gateway, _store) = gateway().await;
    let manual = gateway.create_manual_agent("Codex").await.unwrap();
    assert!(gateway.create_manual_agent("  CODEX ").await.is_err());
    gateway.ensure_host_agent("codex").await.unwrap();
    let config = gateway.config.read().await;
    assert_eq!(
        config
            .agents
            .iter()
            .find(|a| a.id == "host:codex")
            .unwrap()
            .name,
        "Codex (2)"
    );
    assert!(config
        .agents
        .iter()
        .find(|a| a.id == manual.agent_id)
        .unwrap()
        .host
        .is_none());
    drop(config);
    gateway.shutdown().await;
}
