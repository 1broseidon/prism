use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use tokio::sync::{oneshot, Mutex};

use crate::config::Posture;
use crate::error::{Error, Result};

/// How long a held call waits for a human before it is denied, unless the config says otherwise.
pub const DEFAULT_HOLD_TIMEOUT: Duration = Duration::from_secs(120);

pub const TIMEOUT_MESSAGE: &str =
    "Prism held this call for approval and nobody answered in time. Open the Prism panel and retry.";

/// Why a call is being held rather than resolved by policy.
#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum HoldReason {
    /// A rule or the agent's posture said ask.
    #[default]
    Policy,
    /// Policy said allow, but the agent tripped the per-minute rate limit.
    RateLimit,
}

/// A tool call waiting on a human decision.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct PendingCall {
    pub id: String,
    pub agent_id: String,
    pub agent_name: String,
    pub server_id: String,
    pub server_name: String,
    pub tool: String,
    pub arguments: serde_json::Value,
    pub requested_at: DateTime<Utc>,
    /// When the hold times out; the panel counts down to this.
    #[serde(default)]
    pub deadline: Option<DateTime<Utc>>,
    /// The agent's posture, so the panel can pick sensible defaults (first-use remembers).
    #[serde(default)]
    pub posture: Posture,
    #[serde(default)]
    pub reason: HoldReason,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum DecisionVerdict {
    Allow,
    Deny,
}

/// How long the answer lasts. `For` is a time box in minutes.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum DecisionScope {
    Once,
    Session,
    Always,
    For { minutes: u32 },
}

/// How wide the remembered rule reaches: this tool, everything on the server, or everything.
#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum DecisionTarget {
    #[default]
    Tool,
    Server,
    Agent,
}

/// Human decision for a pending call.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
pub struct Decision {
    pub verdict: DecisionVerdict,
    pub scope: DecisionScope,
    #[serde(default)]
    pub target: DecisionTarget,
}

/// Result of waiting on a held call.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum HoldOutcome {
    Decided(Decision),
    Timeout,
}

struct Slot {
    call: PendingCall,
    tx: oneshot::Sender<Decision>,
}

/// Holds a oneshot sender per pending call and times unanswered holds out.
#[derive(Clone)]
pub struct ApprovalRegistry {
    inner: Arc<Mutex<HashMap<String, Slot>>>,
    timeout: Duration,
}

impl ApprovalRegistry {
    pub fn new() -> Self {
        Self::with_timeout(DEFAULT_HOLD_TIMEOUT)
    }

    pub fn with_timeout(timeout: Duration) -> Self {
        Self {
            inner: Arc::new(Mutex::new(HashMap::new())),
            timeout,
        }
    }

    pub async fn register(&self, call: PendingCall) -> HoldOutcome {
        self.register_for(call, self.timeout).await
    }

    /// Hold `call` for at most `timeout`, whatever the registry default is.
    pub async fn register_for(&self, call: PendingCall, timeout: Duration) -> HoldOutcome {
        let id = call.id.clone();
        let (tx, rx) = oneshot::channel();
        {
            let mut map = self.inner.lock().await;
            map.insert(id.clone(), Slot { call, tx });
        }

        match tokio::time::timeout(timeout, rx).await {
            Ok(Ok(decision)) => HoldOutcome::Decided(decision),
            Ok(Err(_)) => {
                self.inner.lock().await.remove(&id);
                HoldOutcome::Timeout
            }
            Err(_) => {
                self.inner.lock().await.remove(&id);
                HoldOutcome::Timeout
            }
        }
    }

    pub async fn decide(&self, id: &str, decision: Decision) -> Result<PendingCall> {
        let mut map = self.inner.lock().await;
        let slot = map
            .remove(id)
            .ok_or_else(|| Error::NotFound(format!("pending call {id}")))?;
        let call = slot.call.clone();
        let _ = slot.tx.send(decision);
        Ok(call)
    }

    pub async fn list(&self) -> Vec<PendingCall> {
        let map = self.inner.lock().await;
        map.values().map(|slot| slot.call.clone()).collect()
    }

    pub async fn is_empty(&self) -> bool {
        self.inner.lock().await.is_empty()
    }
}

impl Default for ApprovalRegistry {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::Utc;

    fn sample(id: &str) -> PendingCall {
        PendingCall {
            id: id.into(),
            agent_id: "agt".into(),
            agent_name: "Claude".into(),
            server_id: "srv".into(),
            server_name: "files".into(),
            tool: "read".into(),
            arguments: serde_json::json!({"path": "/tmp"}),
            requested_at: Utc::now(),
            deadline: None,
            posture: Posture::default(),
            reason: HoldReason::Policy,
        }
    }

    #[tokio::test]
    async fn decide_resolves_holder() {
        let registry = ApprovalRegistry::with_timeout(Duration::from_secs(5));
        let pending = sample("p1");
        let registry_wait = registry.clone();
        let handle = tokio::spawn(async move { registry_wait.register(pending).await });

        tokio::time::sleep(Duration::from_millis(20)).await;
        let decision = Decision {
            verdict: DecisionVerdict::Allow,
            scope: DecisionScope::Once,
            target: DecisionTarget::Tool,
        };
        registry.decide("p1", decision).await.expect("decide");
        let outcome = handle.await.expect("join");
        assert_eq!(outcome, HoldOutcome::Decided(decision));
        assert!(registry.list().await.is_empty());
    }

    #[tokio::test]
    async fn timeout_denies_and_clears() {
        let registry = ApprovalRegistry::with_timeout(Duration::from_millis(40));
        let outcome = registry.register(sample("p2")).await;
        assert_eq!(outcome, HoldOutcome::Timeout);
        assert!(registry.list().await.is_empty());
        let err = registry
            .decide(
                "p2",
                Decision {
                    verdict: DecisionVerdict::Allow,
                    scope: DecisionScope::Once,
                    target: DecisionTarget::Tool,
                },
            )
            .await;
        assert!(err.is_err());
    }

    #[tokio::test]
    async fn decide_unknown_id_is_not_found() {
        let registry = ApprovalRegistry::new();
        let err = registry
            .decide(
                "missing",
                Decision {
                    verdict: DecisionVerdict::Deny,
                    scope: DecisionScope::Once,
                    target: DecisionTarget::Tool,
                },
            )
            .await
            .expect_err("missing");
        match err {
            Error::NotFound(msg) => assert!(msg.contains("missing")),
            other => panic!("unexpected {other:?}"),
        }
    }
}
