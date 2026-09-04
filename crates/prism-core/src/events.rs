use serde::{Deserialize, Serialize};
use tokio::sync::broadcast;

use crate::approval::{Decision, PendingCall};
use crate::audit::AuditEntry;
use crate::backend::BackendStatus;
use crate::config::{AgentConfig, AgentStatus};

/// Events broadcast to desktop (and any other subscriber).
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", content = "data", rename_all = "snake_case")]
pub enum GatewayEvent {
    PendingCall(PendingCall),
    CallDecided {
        id: String,
        decision: Decision,
    },
    /// An MCP client with an unknown `clientInfo.name` connected and is waiting for approval.
    AgentRequested(AgentConfig),
    AgentDecided {
        agent_id: String,
        status: AgentStatus,
    },
    AgentConnected {
        agent_id: String,
    },
    AgentDisconnected {
        agent_id: String,
    },
    ServerStatus {
        server_id: String,
        status: BackendStatus,
    },
    Audit(AuditEntry),
    RulesChanged,
}

pub type EventSender = broadcast::Sender<GatewayEvent>;
pub type EventReceiver = broadcast::Receiver<GatewayEvent>;

pub fn channel() -> (EventSender, EventReceiver) {
    broadcast::channel(512)
}
