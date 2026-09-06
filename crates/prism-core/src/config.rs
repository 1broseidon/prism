use std::collections::BTreeMap;
use std::path::Path;

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

use crate::error::Result;

/// A user-configured stdio MCP server that Prism will spawn.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ServerConfig {
    pub id: String,
    pub name: String,
    pub command: String,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub env: BTreeMap<String, String>,
    /// OS credential-store reference for argument and environment values.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub credential_ref: Option<String>,
    #[serde(default = "default_true")]
    pub enabled: bool,
}

/// Whether an agent may see and call tools. New agents start pending until a human decides.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum AgentStatus {
    Pending,
    Approved,
    Denied,
}

/// The default an agent starts from when no rule matches one of its calls.
#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Posture {
    /// Every call asks.
    Supervised,
    /// Every call asks until a rule exists for that tool; the panel offers to remember the answer.
    #[default]
    FirstUse,
    /// Tools the server marks read-only pass silently; everything else asks. Trusts the server's
    /// own annotations, which is fine for servers the operator installed.
    Guided,
    /// Everything passes and is logged.
    Trusted,
}

/// How loudly Prism tells the operator about a call it resolved without asking.
#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(rename_all = "snake_case")]
pub enum Attention {
    #[default]
    Silent,
    /// Flip the tray icon until the panel is next opened.
    Badge,
    /// Badge plus an OS notification.
    Notify,
    /// Notify plus open the panel.
    Open,
}

/// An agent identified by an OAuth or manually issued bearer token.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct AgentConfig {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub client_name: String,
    #[serde(default)]
    pub client_version: Option<String>,
    #[serde(default = "AgentStatus::approved")]
    pub status: AgentStatus,
    pub created_at: DateTime<Utc>,
    #[serde(default)]
    pub decided_at: Option<DateTime<Utc>>,
    /// What happens to a call no rule covers.
    #[serde(default)]
    pub posture: Posture,
    /// How calls resolved without asking are surfaced. Rules may override it.
    #[serde(default)]
    pub attention: Attention,
    /// The OAuth client this agent signs in as; absent for manually configured agents.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub client_id: Option<String>,
    /// Set for an agent host observed through its hooks (`claude-code`). Such agents never hold
    /// a token or an MCP session; their record exists for the coverage label and the feed.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub host: Option<String>,
}

/// An OAuth client registered dynamically (RFC 7591). Registration is open; it grants
/// nothing until the operator approves the agent that signs in with it.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct OAuthClient {
    pub client_id: String,
    pub client_name: String,
    pub redirect_uris: Vec<String>,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum TokenKind {
    Access,
    Refresh,
    Manual,
}

/// One issued token, stored only as a SHA-256 hash. The clear text is seen once, by the client.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct TokenRecord {
    pub hash: String,
    pub kind: TokenKind,
    pub agent_id: String,
    pub client_id: Option<String>,
    pub created_at: DateTime<Utc>,
    pub expires_at: Option<DateTime<Utc>>,
}

impl TokenRecord {
    pub fn is_expired(&self, now: DateTime<Utc>) -> bool {
        match self.expires_at {
            Some(expiry) => expiry <= now,
            None => self.kind != TokenKind::Manual,
        }
    }
}

impl AgentStatus {
    /// Agents written by earlier versions were created by hand, so treat them as approved.
    fn approved() -> Self {
        AgentStatus::Approved
    }
}

impl AgentConfig {
    pub fn is_approved(&self) -> bool {
        self.status == AgentStatus::Approved
    }
}

/// What a rule does with a matching tool call. `Ask` is an explicit override, for example one
/// tool that should always be confirmed under an otherwise trusted agent.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum RuleDecision {
    Allow,
    Deny,
    Ask,
}

/// How long a rule lasts. Session-scoped rules are never written to disk.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum RuleScope {
    Session,
    Always,
}

/// A policy rule matching some combination of agent, server, and tool.
///
/// Decision, attention, and duration are independent: a rule can allow silently, allow and
/// notify, deny and open the panel, and any of those for thirty minutes or forever.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Rule {
    pub id: String,
    pub agent_id: Option<String>,
    pub server_id: Option<String>,
    /// Exact tool name, or a glob with `*` such as `create_*`. `None` matches every tool.
    pub tool: Option<String>,
    pub decision: RuleDecision,
    /// `None` inherits the agent's attention.
    #[serde(default)]
    pub attention: Option<Attention>,
    pub scope: RuleScope,
    /// Time-boxed grants expire on their own and are pruned when seen.
    #[serde(default)]
    pub expires_at: Option<DateTime<Utc>>,
    /// Reserved for argument-level conditions (path prefixes, host allowlists). Unused today.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub condition: Option<serde_json::Value>,
    pub created_at: DateTime<Utc>,
}

impl Rule {
    pub fn is_expired(&self, now: DateTime<Utc>) -> bool {
        self.expires_at.is_some_and(|t| t <= now)
    }

    pub fn tool_is_glob(&self) -> bool {
        self.tool.as_deref().is_some_and(|t| t.contains('*'))
    }
}

/// What a held call becomes when nobody answers, or when do-not-disturb is on.
#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum TimeoutBehavior {
    #[default]
    Deny,
    /// Let it through if the server marks the tool read-only, otherwise deny.
    AllowReadOnly,
}

/// On-disk (and in-memory) Prism configuration.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct PrismConfig {
    #[serde(default)]
    pub servers: Vec<ServerConfig>,
    #[serde(default)]
    pub agents: Vec<AgentConfig>,
    #[serde(default)]
    pub rules: Vec<Rule>,
    #[serde(default = "default_listen_port")]
    pub listen_port: u16,
    #[serde(default = "default_true")]
    pub auto_open_on_pending: bool,
    /// Where the panel opens. `auto` infers the tray edge from the monitor's work area.
    #[serde(default)]
    pub panel_anchor: PanelAnchor,
    /// What a held call becomes when nobody answers in time.
    #[serde(default)]
    pub on_timeout: TimeoutBehavior,
    /// While on, asks resolve by `on_timeout` immediately and attention is capped at a badge.
    /// Agent connection requests still come through.
    #[serde(default)]
    pub do_not_disturb: bool,
    /// Tripwire: above this many calls per minute from one agent, allows become asks.
    #[serde(default)]
    pub rate_limit_per_minute: Option<u32>,
    /// How long a held call waits for a human.
    #[serde(default = "default_hold_timeout_secs")]
    pub hold_timeout_secs: u64,
    /// Record native actions reported by agent hosts' hooks. Off keeps the hook route answering
    /// but writes nothing.
    #[serde(default = "default_true")]
    pub observe_native: bool,
    #[serde(default)]
    pub clients: Vec<OAuthClient>,
    #[serde(default)]
    pub tokens: Vec<TokenRecord>,
}

/// Screen corner the panel anchors to. Tray icons sit at the right end of a top or bottom
/// panel on most desktops; `auto` reads the panel's reserved strut and picks.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum PanelAnchor {
    #[default]
    Auto,
    TopRight,
    TopLeft,
    BottomRight,
    BottomLeft,
}

impl Default for PrismConfig {
    fn default() -> Self {
        Self {
            servers: Vec::new(),
            agents: Vec::new(),
            rules: Vec::new(),
            listen_port: default_listen_port(),
            auto_open_on_pending: true,
            panel_anchor: PanelAnchor::Auto,
            on_timeout: TimeoutBehavior::Deny,
            do_not_disturb: false,
            rate_limit_per_minute: None,
            hold_timeout_secs: default_hold_timeout_secs(),
            observe_native: true,
            clients: Vec::new(),
            tokens: Vec::new(),
        }
    }
}

impl PrismConfig {
    /// Load pretty JSON from `path`.
    pub fn load(path: impl AsRef<Path>) -> Result<Self> {
        let mut config: Self = serde_json::from_reader(crate::storage::read(path.as_ref())?)
            .map_err(|err: serde_json::Error| {
                crate::error::Error::Invalid(format!(
                    "invalid configuration JSON at line {}, column {}",
                    err.line(),
                    err.column()
                ))
            })?;
        for agent in &mut config.agents {
            if agent.client_name.is_empty() {
                agent.client_name = agent.name.clone();
            }
        }
        Ok(config)
    }

    /// Write pretty JSON to `path`. Session-scoped rules are never persisted.
    pub fn save(&self, path: impl AsRef<Path>) -> Result<()> {
        if self
            .servers
            .iter()
            .any(|s| !s.args.is_empty() || !s.env.is_empty())
        {
            return Err(crate::error::Error::Invalid(
                "server launch values must be moved to OS credential storage before saving".into(),
            ));
        }
        let mut to_save = self.clone();
        let now = Utc::now();
        to_save
            .rules
            .retain(|rule| rule.scope == RuleScope::Always && !rule.is_expired(now));
        to_save.tokens.retain(|token| !token.is_expired(now));
        let data = serde_json::to_string_pretty(&to_save)?;
        crate::storage::atomic_write(path.as_ref(), data.as_bytes())?;
        Ok(())
    }
}

impl PrismConfig {
    /// The agent bound to an OAuth client, or a new pending agent named after the client.
    /// A client id is bound to exactly one agent, so a re-registered client shows up as a new
    /// agent and asks for approval again rather than borrowing an old grant by name.
    pub fn find_or_request_agent_for_client(
        &mut self,
        client: &OAuthClient,
    ) -> (AgentConfig, bool) {
        if let Some(agent) = self
            .agents
            .iter()
            .find(|a| a.client_id.as_deref() == Some(client.client_id.as_str()))
        {
            return (agent.clone(), false);
        }
        let base = client.client_name.trim();
        let base = if base.is_empty() { "unknown" } else { base };
        let mut name = base.to_string();
        let mut n = 2;
        while self
            .agents
            .iter()
            .any(|a| a.name.eq_ignore_ascii_case(&name))
        {
            name = format!("{base} ({n})");
            n += 1;
        }
        let agent = AgentConfig {
            id: uuid::Uuid::new_v4().to_string(),
            name,
            client_name: base.to_string(),
            client_version: None,
            status: AgentStatus::Pending,
            created_at: Utc::now(),
            decided_at: None,
            posture: Posture::default(),
            attention: Attention::default(),
            client_id: Some(client.client_id.clone()),
            host: None,
        };
        self.agents.push(agent.clone());
        (agent, true)
    }
}

fn default_hold_timeout_secs() -> u64 {
    120
}

fn default_listen_port() -> u16 {
    9086
}

fn default_true() -> bool {
    true
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::TimeZone;

    #[test]
    fn config_round_trip_drops_session_rules() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("prism.json");
        let created = Utc.with_ymd_and_hms(2026, 1, 2, 3, 4, 5).unwrap();

        let original = PrismConfig {
            listen_port: 9099,
            auto_open_on_pending: false,
            observe_native: true,
            panel_anchor: PanelAnchor::BottomLeft,
            servers: vec![ServerConfig {
                id: "srv-1".into(),
                name: "files".into(),
                command: "npx".into(),
                args: Vec::new(),
                env: BTreeMap::new(),
                credential_ref: Some(uuid::Uuid::new_v4().to_string()),
                enabled: true,
            }],
            agents: vec![AgentConfig {
                id: "agt-1".into(),
                name: "claude".into(),
                client_name: "claude-code".into(),
                client_version: Some("2.0".into()),
                status: AgentStatus::Approved,
                decided_at: None,
                created_at: created,
                posture: Posture::Guided,
                attention: Attention::Notify,
                client_id: None,
                host: None,
            }],
            rules: vec![
                Rule {
                    id: "rule-always".into(),
                    agent_id: Some("agt-1".into()),
                    server_id: Some("srv-1".into()),
                    tool: Some("read".into()),
                    decision: RuleDecision::Allow,
                    attention: Some(Attention::Badge),
                    scope: RuleScope::Always,
                    expires_at: None,
                    condition: None,
                    created_at: created,
                },
                Rule {
                    id: "rule-session".into(),
                    agent_id: Some("agt-1".into()),
                    server_id: None,
                    tool: None,
                    decision: RuleDecision::Deny,
                    attention: None,
                    scope: RuleScope::Session,
                    expires_at: None,
                    condition: None,
                    created_at: created,
                },
                Rule {
                    id: "rule-expired".into(),
                    agent_id: None,
                    server_id: Some("srv-1".into()),
                    tool: Some("write_*".into()),
                    decision: RuleDecision::Ask,
                    attention: Some(Attention::Open),
                    scope: RuleScope::Always,
                    expires_at: Some(created - chrono::Duration::minutes(1)),
                    condition: None,
                    created_at: created,
                },
            ],
            on_timeout: TimeoutBehavior::AllowReadOnly,
            do_not_disturb: true,
            rate_limit_per_minute: Some(60),
            hold_timeout_secs: 45,
            clients: Vec::new(),
            tokens: Vec::new(),
        };

        original.save(&path).expect("save");
        let loaded = PrismConfig::load(&path).expect("load");

        assert_eq!(loaded.listen_port, 9099);
        assert!(!loaded.auto_open_on_pending);
        assert_eq!(loaded.panel_anchor, PanelAnchor::BottomLeft);
        assert_eq!(loaded.servers, original.servers);
        assert_eq!(loaded.agents, original.agents);
        assert_eq!(loaded.rules.len(), 1);
        assert_eq!(loaded.rules[0].id, "rule-always");
        assert_eq!(loaded.rules[0].scope, RuleScope::Always);
        assert_eq!(loaded.rules[0].attention, Some(Attention::Badge));
        assert_eq!(loaded.on_timeout, TimeoutBehavior::AllowReadOnly);
        assert!(loaded.do_not_disturb);
        assert_eq!(loaded.rate_limit_per_minute, Some(60));
        assert_eq!(loaded.hold_timeout_secs, 45);
    }

    #[test]
    fn legacy_config_gets_defaults() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("prism.json");
        std::fs::write(
            &path,
            r#"{"agents":[{"id":"a","name":"old","created_at":"2026-01-01T00:00:00Z"}],
                "rules":[{"id":"r","agent_id":"a","server_id":null,"tool":null,"decision":"allow",
                "scope":"always","created_at":"2026-01-01T00:00:00Z"}]}"#,
        )
        .expect("write");
        let loaded = PrismConfig::load(&path).expect("load");
        assert_eq!(loaded.agents[0].posture, Posture::FirstUse);
        assert_eq!(loaded.agents[0].attention, Attention::Silent);
        assert_eq!(loaded.rules[0].attention, None);
        assert_eq!(loaded.rules[0].expires_at, None);
        assert_eq!(loaded.on_timeout, TimeoutBehavior::Deny);
        assert_eq!(loaded.hold_timeout_secs, 120);
    }

    #[test]
    fn default_listen_port_is_9086() {
        assert_eq!(PrismConfig::default().listen_port, 9086);
    }
}

#[cfg(test)]
mod agent_tests {
    use super::*;

    #[test]
    fn legacy_agent_without_status_loads_as_approved() {
        let json = r#"{"agents":[{"id":"a1","name":"Old","token":"tok","created_at":"2026-09-01T00:00:00Z"}]}"#;
        let config: PrismConfig = serde_json::from_str(json).expect("legacy config parses");
        assert_eq!(config.agents[0].status, AgentStatus::Approved);
    }
}
