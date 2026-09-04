use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Instant;

use axum::Router;
use chrono::Utc;
use rmcp::handler::server::ServerHandler;
use rmcp::model::{
    CallToolRequestParams, CallToolResponse, CallToolResult, ContentBlock, InitializeRequestParams,
    InitializeResult, ListToolsResult, PaginatedRequestParams, ServerCapabilities, ServerInfo,
    Tool,
};
use rmcp::service::{Peer, RequestContext};
use rmcp::transport::streamable_http_server::session::local::LocalSessionManager;
use rmcp::transport::streamable_http_server::tower::StreamableHttpService;
use rmcp::transport::StreamableHttpServerConfig;
use rmcp::{ErrorData as McpError, RoleServer};
use serde::Serialize;
use std::collections::HashMap;
use tokio::sync::RwLock;
use tokio_util::sync::CancellationToken;
use tracing::{info, warn};

use crate::approval::{
    ApprovalRegistry, Decision, DecisionScope, DecisionVerdict, HoldOutcome, PendingCall,
    TIMEOUT_MESSAGE,
};
use crate::audit::{AuditEntry, AuditLog, AuditSource, AuditVerdict};
use crate::backend::{BackendManager, BackendStatus, ServerView};
use crate::config::{
    AgentConfig, AgentStatus, PanelAnchor, PrismConfig, Rule, RuleDecision, RuleScope, ServerConfig,
};
use crate::error::{Error, Result};
use crate::events::{channel, EventReceiver, EventSender, GatewayEvent};
use crate::policy::{self, ToolAnnotations, Verdict};

/// Live gateway status for the desktop UI.
#[derive(Debug, Clone, Serialize)]
pub struct GatewayStatus {
    pub listen_port: u16,
    pub listening: bool,
    pub servers_running: usize,
    pub servers_total: usize,
    pub agent_count: usize,
    pub pending_count: usize,
    pub pending_agents: usize,
    pub auto_open_on_pending: bool,
}

/// The gateway URL and a generic `mcp.json` block pointing at it.
#[derive(Debug, Clone, Serialize)]
pub struct ConnectSnippet {
    pub url: String,
    pub mcp_json: String,
}

/// An agent as the panel sees it: its config plus whether a session is open right now.
#[derive(Debug, Clone, Serialize)]
pub struct AgentView {
    #[serde(flatten)]
    pub agent: AgentConfig,
    pub connected: bool,
}

/// One live MCP session and the agent it authenticated as.
struct SessionEntry {
    agent_id: String,
    peer: Peer<RoleServer>,
}

/// The MCP gateway agents connect to.
pub struct Gateway {
    config_path: PathBuf,
    config: RwLock<PrismConfig>,
    backends: BackendManager,
    approval: ApprovalRegistry,
    audit: AuditLog,
    events: EventSender,
    shutdown: CancellationToken,
    listen_port: u16,
    sessions: std::sync::Mutex<HashMap<String, SessionEntry>>,
}

impl Gateway {
    /// Load config, start backends, bind Streamable HTTP on 127.0.0.1:{port}.
    pub async fn start(
        config_path: impl AsRef<Path>,
        audit_path: impl AsRef<Path>,
    ) -> Result<Arc<Self>> {
        let _ = tracing_subscriber::fmt()
            .with_env_filter(
                tracing_subscriber::EnvFilter::try_from_default_env()
                    .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info")),
            )
            .try_init();

        let config_path = config_path.as_ref().to_path_buf();
        let config = if config_path.exists() {
            PrismConfig::load(&config_path)?
        } else {
            let fresh = PrismConfig::default();
            fresh.save(&config_path)?;
            fresh
        };
        let listen_port = config.listen_port;
        let (events, _) = channel();
        let audit = AuditLog::new(audit_path, events.clone())?;
        let backends = BackendManager::new(events.clone());
        let shutdown = CancellationToken::new();

        let gateway = Arc::new(Self {
            config_path,
            config: RwLock::new(config.clone()),
            backends,
            approval: ApprovalRegistry::new(),
            audit,
            events,
            shutdown: shutdown.clone(),
            listen_port,
            sessions: std::sync::Mutex::new(HashMap::new()),
        });

        for server in config.servers.into_iter().filter(|s| s.enabled) {
            gateway.backends.start(server).await;
        }

        spawn_http(gateway.clone(), listen_port, shutdown)?;
        info!(listen_port, "prism gateway listening on 127.0.0.1");
        Ok(gateway)
    }

    pub async fn status(&self) -> GatewayStatus {
        let config = self.config.read().await;
        GatewayStatus {
            listen_port: self.listen_port,
            listening: !self.shutdown.is_cancelled(),
            servers_running: self.backends.running_count().await,
            servers_total: config.servers.len(),
            agent_count: config.agents.len(),
            pending_count: self.approval.list().await.len(),
            pending_agents: config
                .agents
                .iter()
                .filter(|a| a.status == AgentStatus::Pending)
                .count(),
            auto_open_on_pending: config.auto_open_on_pending,
        }
    }

    pub async fn servers(&self) -> Vec<ServerView> {
        let live = self.backends.snapshot().await;
        let config = self.config.read().await;
        config
            .servers
            .iter()
            .map(|cfg| {
                let status = live
                    .iter()
                    .find(|(c, _)| c.id == cfg.id)
                    .map(|(_, s)| s.clone())
                    .unwrap_or(BackendStatus::Stopped);
                ServerView::from_parts(cfg.clone(), status)
            })
            .collect()
    }

    pub async fn add_server(&self, mut server: ServerConfig) -> Result<ServerConfig> {
        if server.id.is_empty() {
            server.id = uuid::Uuid::new_v4().to_string();
        }
        if server.name.trim().is_empty() {
            return Err(Error::Invalid("server name is required".into()));
        }
        if server.command.trim().is_empty() {
            return Err(Error::Invalid("server command is required".into()));
        }
        {
            let mut config = self.config.write().await;
            if config
                .servers
                .iter()
                .any(|s| s.id == server.id || s.name == server.name)
            {
                return Err(Error::AlreadyExists(format!("server {}", server.name)));
            }
            config.servers.push(server.clone());
            config.save(&self.config_path)?;
        }
        self.backends.start(server.clone()).await;
        Ok(server)
    }

    pub async fn remove_server(&self, server_id: &str) -> Result<()> {
        {
            let mut config = self.config.write().await;
            let before = config.servers.len();
            config.servers.retain(|s| s.id != server_id);
            if config.servers.len() == before {
                return Err(Error::NotFound(format!("server {server_id}")));
            }
            config.save(&self.config_path)?;
        }
        self.backends.remove(server_id).await;
        Ok(())
    }

    pub async fn restart_server(&self, server_id: &str) -> Result<()> {
        self.backends.restart(server_id).await
    }

    pub async fn agents(&self) -> Vec<AgentView> {
        let connected: std::collections::HashSet<String> = self
            .sessions
            .lock()
            .map(|s| s.values().map(|e| e.agent_id.clone()).collect())
            .unwrap_or_default();
        self.config
            .read()
            .await
            .agents
            .iter()
            .cloned()
            .map(|agent| AgentView {
                connected: connected.contains(&agent.id),
                agent,
            })
            .collect()
    }

    /// Approve or deny an agent. Every open session for that agent is told its tool list
    /// changed, so clients refetch and either see the tools or lose them.
    pub async fn decide_agent(&self, agent_id: &str, approve: bool) -> Result<()> {
        let status = if approve {
            AgentStatus::Approved
        } else {
            AgentStatus::Denied
        };
        {
            let mut config = self.config.write().await;
            let agent = config
                .agents
                .iter_mut()
                .find(|a| a.id == agent_id)
                .ok_or_else(|| Error::NotFound(format!("agent {agent_id}")))?;
            agent.status = status;
            agent.decided_at = Some(Utc::now());
            config.save(&self.config_path)?;
        }
        let _ = self.events.send(GatewayEvent::AgentDecided {
            agent_id: agent_id.to_string(),
            status,
        });
        self.notify_tools_changed(agent_id).await;
        Ok(())
    }

    pub async fn remove_agent(&self, agent_id: &str) -> Result<()> {
        {
            let mut config = self.config.write().await;
            let before = config.agents.len();
            config.agents.retain(|a| a.id != agent_id);
            if config.agents.len() == before {
                return Err(Error::NotFound(format!("agent {agent_id}")));
            }
            config.save(&self.config_path)?;
        }
        let _ = self.events.send(GatewayEvent::AgentDecided {
            agent_id: agent_id.to_string(),
            status: AgentStatus::Denied,
        });
        self.notify_tools_changed(agent_id).await;
        Ok(())
    }

    async fn notify_tools_changed(&self, agent_id: &str) {
        let peers: Vec<Peer<RoleServer>> = self
            .sessions
            .lock()
            .map(|s| {
                s.values()
                    .filter(|e| e.agent_id == agent_id)
                    .map(|e| e.peer.clone())
                    .collect()
            })
            .unwrap_or_default();
        for peer in peers {
            if let Err(err) = peer.notify_tool_list_changed().await {
                warn!(%err, agent_id, "could not notify session of tool list change");
            }
        }
    }

    /// Called on MCP `initialize`: map the client to an agent (registering it as pending if
    /// unknown) and remember the session so approval can reach it.
    async fn register_session(
        &self,
        session_id: &str,
        client_name: &str,
        client_version: Option<&str>,
        peer: Peer<RoleServer>,
    ) -> AgentConfig {
        let (agent, is_new) = {
            let mut config = self.config.write().await;
            let pair = config.find_or_request_agent(client_name, client_version);
            if let Err(err) = config.save(&self.config_path) {
                warn!(%err, "could not persist agent registration");
            }
            pair
        };
        if let Ok(mut sessions) = self.sessions.lock() {
            sessions.insert(
                session_id.to_string(),
                SessionEntry {
                    agent_id: agent.id.clone(),
                    peer,
                },
            );
        }
        if is_new {
            info!(agent = %agent.name, "new agent requested access");
            let _ = self
                .events
                .send(GatewayEvent::AgentRequested(agent.clone()));
        }
        let _ = self.events.send(GatewayEvent::AgentConnected {
            agent_id: agent.id.clone(),
        });
        agent
    }

    fn unregister_session(&self, session_id: &str) {
        let removed = self
            .sessions
            .lock()
            .ok()
            .and_then(|mut s| s.remove(session_id));
        if let Some(entry) = removed {
            let _ = self.events.send(GatewayEvent::AgentDisconnected {
                agent_id: entry.agent_id,
            });
        }
    }

    async fn agent_by_id(&self, agent_id: &str) -> Option<AgentConfig> {
        self.config
            .read()
            .await
            .agents
            .iter()
            .find(|a| a.id == agent_id)
            .cloned()
    }

    pub async fn pending(&self) -> Vec<PendingCall> {
        self.approval.list().await
    }

    pub async fn decide(&self, id: &str, decision: Decision) -> Result<()> {
        let call = self.approval.decide(id, decision).await?;
        let _ = self.events.send(GatewayEvent::CallDecided {
            id: id.to_string(),
            decision,
        });
        if matches!(
            decision.scope,
            DecisionScope::Session | DecisionScope::Always
        ) {
            self.append_rule_for(&call, decision).await?;
        }
        Ok(())
    }

    pub async fn rules(&self) -> Vec<Rule> {
        self.config.read().await.rules.clone()
    }

    pub async fn delete_rule(&self, rule_id: &str) -> Result<()> {
        {
            let mut config = self.config.write().await;
            let before = config.rules.len();
            config.rules.retain(|r| r.id != rule_id);
            if config.rules.len() == before {
                return Err(Error::NotFound(format!("rule {rule_id}")));
            }
            config.save(&self.config_path)?;
        }
        let _ = self.events.send(GatewayEvent::RulesChanged);
        Ok(())
    }

    pub async fn audit(&self, limit: usize) -> Vec<AuditEntry> {
        self.audit.list(limit)
    }

    /// Sync snapshot of the configured panel anchor for window placement callbacks.
    pub fn panel_anchor(&self) -> PanelAnchor {
        self.config
            .try_read()
            .map(|c| c.panel_anchor)
            .unwrap_or_default()
    }

    pub fn subscribe(&self) -> EventReceiver {
        self.events.subscribe()
    }

    pub async fn shutdown(&self) {
        self.shutdown.cancel();
        let ids: Vec<String> = self
            .backends
            .snapshot()
            .await
            .into_iter()
            .map(|(c, _)| c.id)
            .collect();
        for id in ids {
            self.backends.stop(&id).await;
        }
    }

    pub fn connect_snippet(&self) -> Result<ConnectSnippet> {
        let port = self.listen_port;
        let url = format!("http://127.0.0.1:{port}/mcp");
        let mcp = serde_json::json!({
            "mcpServers": { "prism": { "url": url } }
        });
        Ok(ConnectSnippet {
            url,
            mcp_json: serde_json::to_string_pretty(&mcp)?,
        })
    }

    async fn append_rule_for(&self, call: &PendingCall, decision: Decision) -> Result<()> {
        let rule = Rule {
            id: uuid::Uuid::new_v4().to_string(),
            agent_id: Some(call.agent_id.clone()),
            server_id: Some(call.server_id.clone()),
            tool: Some(call.tool.clone()),
            decision: match decision.verdict {
                DecisionVerdict::Allow => RuleDecision::Allow,
                DecisionVerdict::Deny => RuleDecision::Deny,
            },
            scope: match decision.scope {
                DecisionScope::Once => RuleScope::Session,
                DecisionScope::Session => RuleScope::Session,
                DecisionScope::Always => RuleScope::Always,
            },
            created_at: Utc::now(),
        };
        {
            let mut config = self.config.write().await;
            config.rules.push(rule);
            config.save(&self.config_path)?;
        }
        let _ = self.events.send(GatewayEvent::RulesChanged);
        Ok(())
    }

    pub(crate) async fn handle_list_tools(&self, agent_id: Option<&str>) -> ListToolsResult {
        let approved = match agent_id {
            Some(id) => self
                .agent_by_id(id)
                .await
                .map(|a| a.is_approved())
                .unwrap_or(false),
            None => false,
        };
        if !approved {
            return ListToolsResult::default();
        }
        let pairs = self.backends.list_tools(false).await;
        let tools = pairs
            .into_iter()
            .map(|(server, tool)| aggregate_tool(&server.name, tool))
            .collect();
        #[allow(clippy::field_reassign_with_default)]
        {
            let mut result = ListToolsResult::default();
            result.tools = tools;
            result
        }
    }

    pub(crate) async fn handle_call_tool(
        &self,
        request: CallToolRequestParams,
        agent_id: Option<&str>,
    ) -> std::result::Result<CallToolResult, McpError> {
        let started = Instant::now();
        let agent = match agent_id {
            Some(id) => self.agent_by_id(id).await,
            None => None,
        };
        let agent = match agent {
            Some(agent) => agent,
            None => {
                return Err(McpError::invalid_request(
                    "This session has no agent identity. Send MCP initialize first.",
                    None,
                ));
            }
        };
        if !agent.is_approved() {
            let message = format!(
                "Prism has not approved '{}' yet. Open the Prism panel and approve it, then retry.",
                agent.name
            );
            self.audit.record(AuditEntry {
                id: uuid::Uuid::new_v4().to_string(),
                at: Utc::now(),
                agent_id: agent.id.clone(),
                agent_name: agent.name.clone(),
                server_id: String::new(),
                tool: request.name.to_string(),
                verdict: AuditVerdict::Denied,
                source: AuditSource::Unapproved,
                duration_ms: started.elapsed().as_millis() as u64,
                error: Some(message.clone()),
            });
            return Ok(CallToolResult::error(vec![ContentBlock::text(message)]));
        }

        let aggregated = request.name.as_ref();
        let (server, original_tool) = match self.resolve_aggregated(aggregated).await {
            Some(pair) => pair,
            None => {
                return Err(McpError::invalid_params(
                    format!("unknown tool '{aggregated}'"),
                    None,
                ));
            }
        };

        let annotations = self
            .backends
            .find_tool(&server.id, &original_tool)
            .await
            .and_then(|t| t.annotations)
            .map(|a| ToolAnnotations::from(&a));

        let arguments = request
            .arguments
            .clone()
            .map(serde_json::Value::Object)
            .unwrap_or(serde_json::Value::Null);

        let (verdict, matched_rule) = {
            let config = self.config.read().await;
            let v = policy::evaluate(
                &config.rules,
                &agent.id,
                &server.id,
                &original_tool,
                annotations.as_ref(),
            );
            let rule = winning_rule(&config.rules, &agent.id, &server.id, &original_tool);
            (v, rule)
        };

        match verdict {
            Verdict::Allow => {
                self.forward_or_error(
                    &agent,
                    &server,
                    &original_tool,
                    arguments,
                    started,
                    AuditSource::Rule {
                        rule_id: matched_rule
                            .as_ref()
                            .map(|r| r.id.clone())
                            .unwrap_or_default(),
                    },
                    AuditVerdict::Allowed,
                )
                .await
            }
            Verdict::Deny => {
                let summary = matched_rule
                    .as_ref()
                    .map(rule_summary)
                    .unwrap_or_else(|| "deny".into());
                let message = format!("Denied by Prism policy: {summary}");
                self.audit.record(AuditEntry {
                    id: uuid::Uuid::new_v4().to_string(),
                    at: Utc::now(),
                    agent_id: agent.id,
                    agent_name: agent.name,
                    server_id: server.id,
                    tool: original_tool,
                    verdict: AuditVerdict::Denied,
                    source: AuditSource::Rule {
                        rule_id: matched_rule.map(|r| r.id).unwrap_or_default(),
                    },
                    duration_ms: started.elapsed().as_millis() as u64,
                    error: Some(message.clone()),
                });
                Ok(CallToolResult::error(vec![ContentBlock::text(message)]))
            }
            Verdict::Ask => {
                self.hold_then_forward(agent, server, original_tool, arguments, started)
                    .await
            }
        }
    }

    async fn hold_then_forward(
        &self,
        agent: AgentConfig,
        server: ServerConfig,
        tool: String,
        arguments: serde_json::Value,
        started: Instant,
    ) -> std::result::Result<CallToolResult, McpError> {
        let pending = PendingCall {
            id: uuid::Uuid::new_v4().to_string(),
            agent_id: agent.id.clone(),
            agent_name: agent.name.clone(),
            server_id: server.id.clone(),
            server_name: server.name.clone(),
            tool: tool.clone(),
            arguments: arguments.clone(),
            requested_at: Utc::now(),
        };
        let _ = self.events.send(GatewayEvent::PendingCall(pending.clone()));
        let outcome = self.approval.register(pending.clone()).await;
        match outcome {
            HoldOutcome::Timeout => {
                self.audit.record(AuditEntry {
                    id: uuid::Uuid::new_v4().to_string(),
                    at: Utc::now(),
                    agent_id: agent.id,
                    agent_name: agent.name,
                    server_id: server.id,
                    tool,
                    verdict: AuditVerdict::Timeout,
                    source: AuditSource::Timeout,
                    duration_ms: started.elapsed().as_millis() as u64,
                    error: Some(TIMEOUT_MESSAGE.into()),
                });
                Ok(CallToolResult::error(vec![ContentBlock::text(
                    TIMEOUT_MESSAGE,
                )]))
            }
            HoldOutcome::Decided(decision) => match decision.verdict {
                DecisionVerdict::Deny => {
                    self.audit.record(AuditEntry {
                        id: uuid::Uuid::new_v4().to_string(),
                        at: Utc::now(),
                        agent_id: agent.id,
                        agent_name: agent.name,
                        server_id: server.id,
                        tool,
                        verdict: AuditVerdict::Denied,
                        source: AuditSource::Human,
                        duration_ms: started.elapsed().as_millis() as u64,
                        error: Some("Denied by the user in Prism".into()),
                    });
                    Ok(CallToolResult::error(vec![ContentBlock::text(
                        "Denied by the user in Prism",
                    )]))
                }
                DecisionVerdict::Allow => {
                    self.forward_or_error(
                        &agent,
                        &server,
                        &tool,
                        arguments,
                        started,
                        AuditSource::Human,
                        AuditVerdict::Allowed,
                    )
                    .await
                }
            },
        }
    }

    #[allow(clippy::too_many_arguments)]
    async fn forward_or_error(
        &self,
        agent: &AgentConfig,
        server: &ServerConfig,
        tool: &str,
        arguments: serde_json::Value,
        started: Instant,
        source: AuditSource,
        verdict: AuditVerdict,
    ) -> std::result::Result<CallToolResult, McpError> {
        match self.backends.call_tool(&server.id, tool, arguments).await {
            Ok(result) => {
                let is_err = result.is_error.unwrap_or(false);
                self.audit.record(AuditEntry {
                    id: uuid::Uuid::new_v4().to_string(),
                    at: Utc::now(),
                    agent_id: agent.id.clone(),
                    agent_name: agent.name.clone(),
                    server_id: server.id.clone(),
                    tool: tool.to_string(),
                    verdict: if is_err { AuditVerdict::Error } else { verdict },
                    source,
                    duration_ms: started.elapsed().as_millis() as u64,
                    error: if is_err {
                        Some("backend tool returned isError".into())
                    } else {
                        None
                    },
                });
                Ok(result)
            }
            Err(err) => {
                let message = err.to_string();
                self.audit.record(AuditEntry {
                    id: uuid::Uuid::new_v4().to_string(),
                    at: Utc::now(),
                    agent_id: agent.id.clone(),
                    agent_name: agent.name.clone(),
                    server_id: server.id.clone(),
                    tool: tool.to_string(),
                    verdict: AuditVerdict::Error,
                    source,
                    duration_ms: started.elapsed().as_millis() as u64,
                    error: Some(message.clone()),
                });
                Ok(CallToolResult::error(vec![ContentBlock::text(message)]))
            }
        }
    }

    async fn resolve_aggregated(&self, aggregated: &str) -> Option<(ServerConfig, String)> {
        let live = self.backends.list_tools(false).await;
        let mut best: Option<(usize, ServerConfig, String)> = None;
        let mut seen = Vec::new();
        for (server, _) in &live {
            if seen.iter().any(|id: &String| id == &server.id) {
                continue;
            }
            seen.push(server.id.clone());
            let prefix = format!("{}__", server.name);
            if let Some(rest) = aggregated.strip_prefix(&prefix) {
                if rest.is_empty() {
                    continue;
                }
                let len = prefix.len();
                let take = best.as_ref().map(|(n, _, _)| len > *n).unwrap_or(true);
                if take {
                    best = Some((len, server.clone(), rest.to_string()));
                }
            }
        }
        best.map(|(_, s, t)| (s, t))
    }
}

/// One MCP session. rmcp builds a fresh proxy per session, so the agent identity learned at
/// `initialize` lives here and is dropped with the session.
struct PrismProxy {
    gateway: Arc<Gateway>,
    session_id: String,
    agent_id: std::sync::Mutex<Option<String>>,
}

impl PrismProxy {
    fn agent_id(&self) -> Option<String> {
        self.agent_id.lock().ok().and_then(|a| a.clone())
    }
}

impl Drop for PrismProxy {
    fn drop(&mut self) {
        self.gateway.unregister_session(&self.session_id);
    }
}

impl ServerHandler for PrismProxy {
    fn get_info(&self) -> ServerInfo {
        ServerInfo::new(
            ServerCapabilities::builder()
                .enable_tools()
                .enable_tool_list_changed()
                .build(),
        )
        .with_instructions(
            "Prism local MCP gateway. Tools appear once the human approves this agent in the \
             Prism panel; they are named {server}__{tool}.",
        )
    }

    async fn initialize(
        &self,
        request: InitializeRequestParams,
        context: RequestContext<RoleServer>,
    ) -> std::result::Result<InitializeResult, McpError> {
        let agent = self
            .gateway
            .register_session(
                &self.session_id,
                &request.client_info.name,
                Some(request.client_info.version.as_str()),
                context.peer.clone(),
            )
            .await;
        if let Ok(mut slot) = self.agent_id.lock() {
            *slot = Some(agent.id);
        }
        Ok(self.get_info())
    }

    async fn list_tools(
        &self,
        _request: Option<PaginatedRequestParams>,
        _context: RequestContext<RoleServer>,
    ) -> std::result::Result<ListToolsResult, McpError> {
        Ok(self
            .gateway
            .handle_list_tools(self.agent_id().as_deref())
            .await)
    }

    async fn call_tool(
        &self,
        request: CallToolRequestParams,
        _context: RequestContext<RoleServer>,
    ) -> std::result::Result<CallToolResponse, McpError> {
        self.gateway
            .handle_call_tool(request, self.agent_id().as_deref())
            .await
            .map(Into::into)
    }
}

fn spawn_http(gateway: Arc<Gateway>, port: u16, shutdown: CancellationToken) -> Result<()> {
    let gw = gateway.clone();
    let service: StreamableHttpService<PrismProxy, LocalSessionManager> =
        StreamableHttpService::new(
            move || {
                Ok(PrismProxy {
                    gateway: gw.clone(),
                    session_id: uuid::Uuid::new_v4().to_string(),
                    agent_id: std::sync::Mutex::new(None),
                })
            },
            LocalSessionManager::default().into(),
            StreamableHttpServerConfig::default().with_allowed_hosts([
                "127.0.0.1".to_string(),
                "localhost".to_string(),
                "::1".to_string(),
                format!("127.0.0.1:{port}"),
                format!("localhost:{port}"),
            ]),
        );

    let app = Router::new().nest_service("/mcp", service);

    let addr = SocketAddr::from(([127, 0, 0, 1], port));
    tokio::spawn(async move {
        let listener = match tokio::net::TcpListener::bind(addr).await {
            Ok(l) => l,
            Err(err) => {
                warn!(%err, %addr, "failed to bind gateway");
                return;
            }
        };
        if let Err(err) = axum::serve(listener, app)
            .with_graceful_shutdown(async move {
                shutdown.cancelled().await;
            })
            .await
        {
            warn!(%err, "gateway http server exited");
        }
    });
    Ok(())
}

fn aggregate_tool(server_name: &str, mut tool: Tool) -> Tool {
    tool.name = format!("{server_name}__{}", tool.name).into();
    tool
}

fn winning_rule(rules: &[Rule], agent_id: &str, server_id: &str, tool: &str) -> Option<Rule> {
    let verdict = policy::evaluate(rules, agent_id, server_id, tool, None);
    if matches!(verdict, Verdict::Ask) {
        return None;
    }
    rules
        .iter()
        .filter(|rule| {
            rule.agent_id.as_deref().is_none_or(|v| v == agent_id)
                && rule.server_id.as_deref().is_none_or(|v| v == server_id)
                && rule.tool.as_deref().is_none_or(|v| v == tool)
        })
        .cloned()
        .min_by_key(|rule| {
            let spec = match (
                rule.agent_id.is_some(),
                rule.server_id.is_some(),
                rule.tool.is_some(),
            ) {
                (true, true, true) => 0u8,
                (true, true, false) => 1,
                (false, true, true) => 2,
                (false, true, false) => 3,
                (true, false, false) => 4,
                (false, false, false) => 5,
                (true, false, true) => 2,
                (false, false, true) => 4,
            };
            (
                spec,
                if rule.decision == RuleDecision::Deny {
                    0
                } else {
                    1
                },
            )
        })
}

fn rule_summary(rule: &Rule) -> String {
    let agent = rule.agent_id.as_deref().unwrap_or("*");
    let server = rule.server_id.as_deref().unwrap_or("*");
    let tool = rule.tool.as_deref().unwrap_or("*");
    let decision = match rule.decision {
        RuleDecision::Allow => "allow",
        RuleDecision::Deny => "deny",
    };
    format!("{decision} agent={agent} server={server} tool={tool}")
}
