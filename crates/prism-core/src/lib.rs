//! Headless Prism MCP gateway: policy, stdio backends, approvals, and audit.

mod approval;
mod audit;
mod backend;
mod config;
mod error;
mod events;
mod gateway;
mod policy;

pub use approval::{
    ApprovalRegistry, Decision, DecisionScope, DecisionVerdict, HoldOutcome, PendingCall,
    DEFAULT_HOLD_TIMEOUT, TIMEOUT_MESSAGE,
};
pub use audit::{AuditEntry, AuditLog, AuditSource, AuditVerdict};
pub use backend::{BackendStatus, ServerView};
pub use config::{
    AgentConfig, AgentStatus, PanelAnchor, PrismConfig, Rule, RuleDecision, RuleScope, ServerConfig,
};
pub use error::{Error, Result};
pub use events::{EventReceiver, GatewayEvent};
pub use gateway::{AgentView, ConnectSnippet, Gateway, GatewayStatus};
pub use policy::{evaluate, ToolAnnotations, Verdict};
