//! Exercise the actual Axum extraction, host/token checks, gateway recording and audit export.
use super::*;
use crate::native::{self, MAX_BODY_BYTES};
use serde_json::{json, Value};

struct Fixture {
    gateway: Arc<Gateway>,
    client: reqwest::Client,
    base: String,
    server: tokio::task::JoinHandle<()>,
    _dir: tempfile::TempDir,
}

impl Drop for Fixture {
    fn drop(&mut self) {
        self.server.abort();
    }
}

impl Fixture {
    async fn new() -> Self {
        let dir = tempfile::tempdir().unwrap();
        let gateway = Arc::new(retained_history_tests::gateway(
            &dir.path().join("audit.jsonl"),
        ));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let router = native::router(gateway.clone());
        let server = tokio::spawn(async move {
            axum::serve(listener, router).await.unwrap();
        });
        let client = reqwest::Client::builder()
            .no_proxy()
            .timeout(std::time::Duration::from_secs(5))
            .build()
            .unwrap();
        Self {
            gateway,
            client,
            base,
            server,
            _dir: dir,
        }
    }

    async fn post(&self, host: &str, body: Value) -> reqwest::Response {
        self.client
            .post(format!("{}/hooks/{host}/test-token", self.base))
            .json(&body)
            .send()
            .await
            .unwrap()
    }

    async fn neutral(&self, host: &str, body: Value) {
        let response = self.post(host, body).await;
        assert_eq!(response.status(), 200, "{host}");
        assert_eq!(response.json::<Value>().await.unwrap(), json!({}));
    }
}

fn cursor(id: &str) -> Value {
    json!({"hook_event_name":"preToolUse", "conversation_id":"cursor-session", "cwd":"/home/u/proj",
        "workspace_roots":["/home/u/proj"], "tool_use_id":id, "tool_name":"Shell",
        "tool_input":{"command":"sudo ls", "content":"RAW PRIVATE CONTENT"}})
}

fn goose(id: &str, decision: &str) -> Value {
    json!({"event":"PreToolUseResult", "session_id":"goose-session", "working_dir":"/home/u/proj",
        "tool_call_id":id, "tool_name":"shell", "tool_input":{"command":"ls"},
        "decision":decision, "reason":"RAW PRIVATE DENIAL", "policy_evaluated":true})
}

fn antigravity(step: u64, error: &str) -> Value {
    json!({"hook_event_name":"PostToolUse", "payload":{
        "conversationId":"agy-session", "stepIdx":step, "workspacePaths":["/home/u/proj"],
        "toolCall":{"name":"write_to_file", "args":{"TargetFile":"/home/u/proj/a.rs", "CodeContent":"RAW PRIVATE CONTENT"}},
        "error":error, "transcriptPath":"RAW PRIVATE PATH", "artifactDirectoryPath":"RAW PRIVATE PATH"
    }})
}

#[tokio::test]
async fn all_six_hosts_record_neutral_redacted_observations_without_enforcement() {
    let fixture = Fixture::new().await;
    for host in [native::HOST_CLAUDE_CODE, native::HOST_CODEX] {
        fixture.neutral(host, json!({"hook_event_name":"PreToolUse", "session_id":"session",
            "cwd":"/home/u/proj", "tool_name":"Write", "tool_input":{"file_path":"a.rs", "content":"RAW PRIVATE CONTENT"}})).await;
    }
    fixture.neutral(native::HOST_CURSOR, cursor("1")).await;
    fixture.neutral(native::HOST_OPENCODE, json!({"hook_event_name":"PreToolUse", "session_id":"oc-session",
        "tool_use_id":"1", "cwd":"/home/u/proj", "tool_name":"apply_patch",
        "tool_input":{"patchText":"*** Update File: a.rs\n*** Move to: ../sibling/a.rs\n+RAW PRIVATE CONTENT"}})).await;
    fixture
        .neutral(native::HOST_GOOSE, goose("1", "deny"))
        .await;
    fixture
        .neutral(
            native::HOST_ANTIGRAVITY,
            antigravity(1, "RAW PRIVATE ERROR"),
        )
        .await;

    let entries = fixture.gateway.audit(100).await;
    assert_eq!(entries.len(), 6);
    for host in native::HOSTS {
        let entry = entries
            .iter()
            .find(|entry| entry.server_id == *host)
            .unwrap();
        assert_eq!(entry.agent_id, format!("host:{host}"));
        assert_eq!(entry.agent_name, native::harness_display_name(host));
        assert!(matches!(entry.source, AuditSource::Observed));
        assert!(matches!(entry.attention, Attention::Silent));
        assert!(!entry.native.as_ref().unwrap().via_prism);
        assert!(entry.error.is_none());
    }
    assert!(matches!(
        entries
            .iter()
            .find(|e| e.server_id == "goose")
            .unwrap()
            .verdict,
        AuditVerdict::Denied
    ));
    assert!(matches!(
        entries
            .iter()
            .find(|e| e.server_id == "antigravity")
            .unwrap()
            .verdict,
        AuditVerdict::Error
    ));
    assert_eq!(
        entries
            .iter()
            .find(|e| e.server_id == "cursor")
            .unwrap()
            .native
            .as_ref()
            .unwrap()
            .would_hold
            .as_deref(),
        Some("sudo")
    );
    assert_eq!(
        entries
            .iter()
            .find(|e| e.server_id == "opencode")
            .unwrap()
            .native
            .as_ref()
            .unwrap()
            .would_hold
            .as_deref(),
        Some("write_outside_cwd")
    );
    assert!(fixture.gateway.pending().await.is_empty());
    assert!(!serde_json::to_string(&entries)
        .unwrap()
        .contains("RAW PRIVATE"));
    let status = fixture.gateway.native_status().await.unwrap();
    assert_eq!(status.hosts.len(), 6);
    assert!(status.hosts.iter().all(|h| h.last_event_at.is_some()));
    assert_eq!(status.actions_7d, 6);
    let exported = fixture.gateway.native_export(7).await.unwrap();
    assert!(!exported.contains("RAW PRIVATE"));
}

#[tokio::test]
async fn concurrent_retries_record_one_event_and_identical_new_calls_record_separately() {
    let fixture = Fixture::new().await;
    let (a, b) = tokio::join!(
        fixture.post("cursor", cursor("1")),
        fixture.post("cursor", cursor("1"))
    );
    assert_eq!(a.status(), 200);
    assert_eq!(b.status(), 200);
    assert_eq!(fixture.gateway.audit(100).await.len(), 1);
    fixture.neutral("cursor", cursor("2")).await;
    fixture.neutral("goose", goose("1", "allow")).await;
    fixture.neutral("goose", goose("1", "allow")).await;
    fixture.neutral("goose", goose("2", "allow")).await;
    fixture.neutral("antigravity", antigravity(1, "")).await;
    fixture.neutral("antigravity", antigravity(1, "")).await;
    fixture.neutral("antigravity", antigravity(2, "")).await;
    assert_eq!(fixture.gateway.audit(100).await.len(), 6);
}

#[tokio::test]
async fn invalid_and_irrelevant_payloads_never_create_agents_or_coverage() {
    let fixture = Fixture::new().await;
    assert_eq!(fixture.post("unsupported", cursor("1")).await.status(), 404);
    let response = fixture
        .client
        .post(format!("{}/hooks/cursor/bad-token", fixture.base))
        .json(&cursor("1"))
        .send()
        .await
        .unwrap();
    assert_eq!(response.status(), 404);
    for (host, body) in [
        ("cursor", json!({})),
        ("cursor", json!([])),
        (
            "cursor",
            json!({"tool_name":"Shell", "tool_input":{"command":"ls"}}),
        ),
        ("goose", cursor("1")),
        ("antigravity", antigravity(1, "")["payload"].clone()),
    ] {
        assert_eq!(fixture.post(host, body).await.status(), 400);
    }
    for (host, body) in [
        ("cursor", json!({"hook_event_name":"sessionStart"})),
        ("opencode", json!({"hook_event_name":"PostToolUse"})),
        ("goose", json!({"event":"PreToolUse"})),
        ("goose", json!({"event":"PostToolUse"})),
        ("antigravity", json!({"hook_event_name":"PreToolUse"})),
        ("claude-code", json!({"hook_event_name":"Stop"})),
        ("codex", json!({"hook_event_name":"PostToolUse"})),
    ] {
        fixture.neutral(host, body).await;
    }
    let malformed = fixture
        .client
        .post(format!("{}/hooks/cursor/test-token", fixture.base))
        .header("content-type", "application/json")
        .body("{bad json with PRIVATE")
        .send()
        .await
        .unwrap();
    assert_eq!(malformed.status(), 400);
    assert!(!malformed.text().await.unwrap().contains("PRIVATE"));
    assert!(fixture.gateway.audit(100).await.is_empty());
    assert!(fixture.gateway.config.read().await.agents.is_empty());
    assert!(fixture
        .gateway
        .native_status()
        .await
        .unwrap()
        .last_event_at
        .is_none());
}

#[tokio::test]
async fn all_host_routes_enforce_the_body_limit_without_leaking_input() {
    let fixture = Fixture::new().await;
    for host in native::HOSTS {
        let body = json!({"private":"x".repeat(MAX_BODY_BYTES)});
        let response = fixture.post(host, body).await;
        assert_eq!(response.status(), 413, "{host}");
        assert!(response.text().await.unwrap().is_empty());
    }
    let mut accepted = cursor("1");
    let length = accepted.to_string().len();
    // The exact boundary is accepted; the unused payload field is never recorded.
    accepted["padding"] = json!("x".repeat(MAX_BODY_BYTES - length - 13));
    assert_eq!(accepted.to_string().len(), MAX_BODY_BYTES);
    fixture.neutral("cursor", accepted.clone()).await;
    accepted["padding"] = json!(format!("{}x", accepted["padding"].as_str().unwrap()));
    assert_eq!(fixture.post("cursor", accepted).await.status(), 413);
    assert_eq!(fixture.gateway.audit(100).await.len(), 1);
}

#[tokio::test]
async fn disabled_and_revoked_native_observers_preserve_existing_policy() {
    let fixture = Fixture::new().await;
    fixture.gateway.set_observe_native(false).await.unwrap();
    fixture.neutral("cursor", cursor("1")).await;
    assert!(fixture.gateway.audit(100).await.is_empty());
    assert!(fixture
        .gateway
        .native_status()
        .await
        .unwrap()
        .last_event_at
        .is_none());
    fixture.gateway.set_observe_native(true).await.unwrap();
    fixture.neutral("cursor", cursor("2")).await;
    assert_eq!(fixture.gateway.audit(100).await.len(), 1);
    fixture
        .gateway
        .config
        .write()
        .await
        .agents
        .iter_mut()
        .find(|a| a.id == "host:cursor")
        .unwrap()
        .status = AgentStatus::Denied;
    assert_eq!(fixture.post("cursor", cursor("3")).await.status(), 403);
    assert_eq!(fixture.gateway.audit(100).await.len(), 1);
    assert!(fixture.gateway.pending().await.is_empty());
}
