//! Headless Prism MCP gateway: policy, stdio backends, approvals, and audit.

pub mod activity;
mod approval;
mod audit;
mod backend;
mod config;
mod credentials;
mod error;
mod events;
mod gateway;
mod http_security;
pub mod native;
mod oauth;
mod policy;
mod remote;
mod shell_path;
mod storage;

pub use approval::{
    ApprovalRegistry, Decision, DecisionScope, DecisionTarget, DecisionVerdict, HoldOutcome,
    HoldReason, PendingCall, DEFAULT_HOLD_TIMEOUT, TIMEOUT_MESSAGE,
};
pub use audit::{AuditEntry, AuditLog, AuditSource, AuditVerdict, NativeDetail};
pub use backend::{BackendStatus, ServerView};
pub use config::{
    AgentConfig, AgentStatus, Attention, HttpAuth, PanelAnchor, Posture, PrismConfig, Rule,
    RuleDecision, RuleScope, ServerConfig, TimeoutBehavior,
};
pub use config::{OAuthClient, TokenKind, TokenRecord};
pub use error::{Error, Result};
pub use events::{EventReceiver, GatewayEvent};
pub use gateway::{AgentView, ConnectSnippet, Gateway, GatewayStatus, NewRule, Settings, ToolInfo};
pub use native::{NativeStatus, ReasonCount, ShadowRule};
pub use oauth::{
    hash_token, pkce_matches, redirect_uri_allowed, AuthenticatedAgent, AuthorizeOutcome,
    AuthorizeParams, ManualToken, OAuthError, PendingSignIn, RegisterRequest, TokenRequest,
    TokenResponse, TokenView,
};
pub use policy::{evaluate, glob_match, Decider, Evaluation, ToolAnnotations, Verdict};
pub use shell_path::adopt_login_shell_path;
