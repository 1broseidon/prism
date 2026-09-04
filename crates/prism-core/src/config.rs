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

/// An agent that has connected to the gateway. Identified by the `clientInfo.name` it sends in
/// MCP `initialize`; process identity arrives with the stdio shim in a later phase.
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

/// Allow or deny a matching tool call.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum RuleDecision {
    Allow,
    Deny,
}

/// How long a rule lasts. Session-scoped rules are never written to disk.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum RuleScope {
    Session,
    Always,
}

/// A policy rule matching some combination of agent, server, and tool.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Rule {
    pub id: String,
    pub agent_id: Option<String>,
    pub server_id: Option<String>,
    pub tool: Option<String>,
    pub decision: RuleDecision,
    pub scope: RuleScope,
    pub created_at: DateTime<Utc>,
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
        }
    }
}

impl PrismConfig {
    /// Load pretty JSON from `path`.
    pub fn load(path: impl AsRef<Path>) -> Result<Self> {
        let data = std::fs::read_to_string(path)?;
        let mut config: Self = serde_json::from_str(&data)?;
        for agent in &mut config.agents {
            if agent.client_name.is_empty() {
                agent.client_name = agent.name.clone();
            }
        }
        Ok(config)
    }

    /// Write pretty JSON to `path`. Session-scoped rules are never persisted.
    pub fn save(&self, path: impl AsRef<Path>) -> Result<()> {
        let mut to_save = self.clone();
        to_save.rules.retain(|rule| rule.scope == RuleScope::Always);
        if let Some(parent) = path.as_ref().parent() {
            if !parent.as_os_str().is_empty() {
                std::fs::create_dir_all(parent)?;
            }
        }
        let data = serde_json::to_string_pretty(&to_save)?;
        std::fs::write(path, data)?;
        Ok(())
    }
}

impl PrismConfig {
    /// Find the agent for an MCP client by its `clientInfo.name`, or register it as pending.
    /// Returns the agent and whether it was newly created.
    pub fn find_or_request_agent(
        &mut self,
        client_name: &str,
        client_version: Option<&str>,
    ) -> (AgentConfig, bool) {
        let client_name = client_name.trim();
        let client_name = if client_name.is_empty() {
            "unknown"
        } else {
            client_name
        };
        if let Some(agent) = self
            .agents
            .iter_mut()
            .find(|a| a.client_name.eq_ignore_ascii_case(client_name))
        {
            if let Some(v) = client_version {
                agent.client_version = Some(v.to_string());
            }
            return (agent.clone(), false);
        }
        let agent = AgentConfig {
            id: uuid::Uuid::new_v4().to_string(),
            name: client_name.to_string(),
            client_name: client_name.to_string(),
            client_version: client_version.map(str::to_string),
            status: AgentStatus::Pending,
            created_at: Utc::now(),
            decided_at: None,
        };
        self.agents.push(agent.clone());
        (agent, true)
    }
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
            panel_anchor: PanelAnchor::BottomLeft,
            servers: vec![ServerConfig {
                id: "srv-1".into(),
                name: "files".into(),
                command: "npx".into(),
                args: vec![
                    "-y".into(),
                    "@modelcontextprotocol/server-filesystem".into(),
                ],
                env: BTreeMap::from([("FOO".into(), "bar".into())]),
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
            }],
            rules: vec![
                Rule {
                    id: "rule-always".into(),
                    agent_id: Some("agt-1".into()),
                    server_id: Some("srv-1".into()),
                    tool: Some("read".into()),
                    decision: RuleDecision::Allow,
                    scope: RuleScope::Always,
                    created_at: created,
                },
                Rule {
                    id: "rule-session".into(),
                    agent_id: Some("agt-1".into()),
                    server_id: None,
                    tool: None,
                    decision: RuleDecision::Deny,
                    scope: RuleScope::Session,
                    created_at: created,
                },
            ],
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
    fn unknown_client_becomes_pending_and_is_reused() {
        let mut config = PrismConfig::default();
        let (first, new) = config.find_or_request_agent("Claude Code", Some("2.1"));
        assert!(new);
        assert_eq!(first.status, AgentStatus::Pending);
        assert_eq!(first.client_name, "Claude Code");

        let (again, new) = config.find_or_request_agent("claude code", Some("2.2"));
        assert!(!new);
        assert_eq!(again.id, first.id);
        assert_eq!(again.client_version.as_deref(), Some("2.2"));
        assert_eq!(config.agents.len(), 1);
    }

    #[test]
    fn empty_client_name_falls_back_to_unknown() {
        let mut config = PrismConfig::default();
        let (agent, _) = config.find_or_request_agent("   ", None);
        assert_eq!(agent.client_name, "unknown");
    }

    #[test]
    fn legacy_agent_without_status_loads_as_approved() {
        let json = r#"{"agents":[{"id":"a1","name":"Old","token":"tok","created_at":"2026-09-01T00:00:00Z"}]}"#;
        let config: PrismConfig = serde_json::from_str(json).expect("legacy config parses");
        assert_eq!(config.agents[0].status, AgentStatus::Approved);
    }
}
