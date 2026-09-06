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
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, VecDeque};
use tokio::sync::RwLock;
use tokio_util::sync::CancellationToken;
use tracing::{info, warn};

use crate::approval::{
    ApprovalRegistry, Decision, DecisionScope, DecisionTarget, DecisionVerdict, HoldOutcome,
    HoldReason, PendingCall, TIMEOUT_MESSAGE,
};
use crate::audit::{AuditEntry, AuditLog, AuditSource, AuditVerdict};
use crate::backend::{BackendManager, BackendStatus, ServerView};
use crate::config::{
    AgentConfig, AgentStatus, Attention, HttpAuth, PanelAnchor, Posture, PrismConfig, Rule,
    RuleDecision, RuleScope, ServerConfig, TimeoutBehavior,
};
use crate::error::{Error, Result};
use crate::events::{channel, EventReceiver, EventSender, GatewayEvent};
use crate::oauth::{self, AuthenticatedAgent, OAuthState, TokenView};
use crate::policy::{self, Decider, ToolAnnotations, Verdict};

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
    /// Approved agents whose client is signing in again and waiting for consent.
    pub pending_signins: usize,
    pub auto_open_on_pending: bool,
    pub do_not_disturb: bool,
}

/// Operator-level knobs, the ones that are not about one agent or one rule.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Settings {
    pub on_timeout: TimeoutBehavior,
    pub do_not_disturb: bool,
    pub rate_limit_per_minute: Option<u32>,
    pub hold_timeout_secs: u64,
    pub auto_open_on_pending: bool,
}

/// A rule as the panel creates it. Same triple as an existing rule replaces that rule.
#[derive(Debug, Clone, Deserialize)]
pub struct NewRule {
    pub agent_id: Option<String>,
    pub server_id: Option<String>,
    pub tool: Option<String>,
    pub decision: RuleDecision,
    #[serde(default)]
    pub attention: Option<Attention>,
    #[serde(default = "always")]
    pub scope: RuleScope,
    /// Time box in minutes; `None` means until removed.
    #[serde(default)]
    pub minutes: Option<u32>,
}

fn always() -> RuleScope {
    RuleScope::Always
}

/// One tool on one server, as the panel lists it for per-tool overrides.
#[derive(Debug, Clone, Serialize)]
pub struct ToolInfo {
    pub name: String,
    pub description: Option<String>,
    pub read_only: bool,
    pub destructive: bool,
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
    /// Live tokens this agent holds. Empty for agents that connect unauthenticated.
    pub tokens: Vec<TokenView>,
    /// The OAuth clients that sign in as this agent: one per scope or install a harness
    /// registered from. Empty for manual agents and harnesses seen only through hooks.
    pub clients: Vec<ClientView>,
}

/// One registered OAuth client, as the panel lists it under its agent.
#[derive(Debug, Clone, Serialize)]
pub struct ClientView {
    pub client_id: String,
    pub client_name: String,
    pub created_at: chrono::DateTime<Utc>,
    pub origin: Option<String>,
    /// Holds a live token.
    pub signed_in: bool,
}

/// One live MCP session and the agent it authenticated as.
struct SessionEntry {
    agent_id: String,
    peer: Peer<RoleServer>,
}

/// The MCP gateway agents connect to.
pub struct Gateway {
    pub(crate) config_path: PathBuf,
    pub(crate) config: RwLock<PrismConfig>,
    backends: BackendManager,
    credentials: Arc<dyn crate::credentials::CredentialStore>,
    approval: ApprovalRegistry,
    audit: AuditLog,
    pub(crate) events: EventSender,
    shutdown: CancellationToken,
    pub(crate) listen_port: u16,
    pub(crate) oauth: OAuthState,
    sessions: std::sync::Mutex<HashMap<String, SessionEntry>>,
    /// Call timestamps per agent for the rate tripwire; trimmed to the last minute on each check.
    calls: std::sync::Mutex<HashMap<String, VecDeque<Instant>>>,
    /// Secret path segment of the host hook URL. Lives in the data dir; rotatable from the panel.
    hook_token: std::sync::RwLock<String>,
    hook_token_path: PathBuf,
    native_budget: std::sync::Mutex<crate::native::EventBudget>,
    native_last: std::sync::Mutex<HashMap<String, chrono::DateTime<Utc>>>,
}

impl Gateway {
    /// Load config, start backends, bind Streamable HTTP on 127.0.0.1:{port}.
    pub async fn start(
        config_path: impl AsRef<Path>,
        audit_path: impl AsRef<Path>,
    ) -> Result<Arc<Self>> {
        Self::start_with_credentials(
            config_path.as_ref().to_path_buf(),
            audit_path.as_ref().to_path_buf(),
            Arc::new(crate::credentials::NativeStore::default()),
        )
        .await
    }

    pub(crate) async fn start_with_credentials(
        config_path: PathBuf,
        audit_path: PathBuf,
        credentials: Arc<dyn crate::credentials::CredentialStore>,
    ) -> Result<Arc<Self>> {
        let _ = tracing_subscriber::fmt()
            .with_env_filter(
                tracing_subscriber::EnvFilter::try_from_default_env()
                    .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info"))
                    .add_directive("rmcp=off".parse().expect("valid log directive")),
            )
            .try_init();

        // Repair both locations even if a locked credential store later prevents startup.
        crate::storage::prepare(&config_path)?;
        let (events, _) = channel();
        let audit_events = events.clone();
        let hook_token_path = audit_path
            .parent()
            .map(|dir| dir.join("hook-token"))
            .unwrap_or_else(|| PathBuf::from("hook-token"));
        let token_path = hook_token_path.clone();
        let (audit, hook_token) = tokio::task::spawn_blocking(move || {
            let audit = AuditLog::new(audit_path, audit_events)?;
            let token = load_or_create_hook_token(&token_path)?;
            Ok::<_, Error>((audit, token))
        })
        .await
        .map_err(|_| Error::Gateway("audit storage setup could not complete".into()))??;
        let path = config_path.clone();
        let store = credentials.clone();
        let config = tokio::task::spawn_blocking(move || {
            crate::storage::prepare(&path)?;
            let mut config = if path.exists() {
                PrismConfig::load(&path)?
            } else {
                PrismConfig::default()
            };
            crate::oauth::prune_unused_clients(&mut config, chrono::Utc::now());
            crate::credentials::migrate(&config, &path, store.as_ref())
        })
        .await
        .map_err(|_| Error::Gateway("credential migration could not complete".into()))??;
        let listen_port = config.listen_port;
        let backends = BackendManager::new(events.clone(), credentials.clone());
        let shutdown = CancellationToken::new();

        let gateway = Arc::new(Self {
            config_path,
            config: RwLock::new(config.clone()),
            backends,
            credentials,
            approval: ApprovalRegistry::new(),
            audit,
            events,
            shutdown: shutdown.clone(),
            listen_port,
            sessions: std::sync::Mutex::new(HashMap::new()),
            calls: std::sync::Mutex::new(HashMap::new()),
            oauth: OAuthState::default(),
            hook_token: std::sync::RwLock::new(hook_token),
            hook_token_path,
            native_budget: std::sync::Mutex::new(Default::default()),
            native_last: std::sync::Mutex::new(HashMap::new()),
        });

        for server in config.servers.into_iter().filter(|s| s.enabled) {
            gateway.backends.start(server).await;
        }

        spawn_http(gateway.clone(), listen_port, shutdown)?;
        let weak = Arc::downgrade(&gateway);
        let stop = gateway.shutdown.clone();
        tokio::spawn(async move {
            loop {
                tokio::select! {
                    _ = stop.cancelled() => break,
                    _ = tokio::time::sleep(std::time::Duration::from_secs(60 * 60)) => {
                        let Some(gateway) = weak.upgrade() else { break; };
                        let _ = tokio::task::spawn_blocking(move || {
                            if gateway.audit.cleanup().is_err() {
                                warn!("audit retention cleanup failed");
                            }
                        }).await;
                    }
                }
            }
        });
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
            pending_signins: self
                .pending_signins()
                .iter()
                .filter(|s| s.needs_consent)
                .count(),
            auto_open_on_pending: config.auto_open_on_pending,
            do_not_disturb: config.do_not_disturb,
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
        if server.credential_ref.is_some() {
            return Err(Error::Invalid(
                "new servers must supply launch values, not credential references".into(),
            ));
        }
        if server.id.is_empty() {
            server.id = uuid::Uuid::new_v4().to_string();
        }
        if server.name.trim().is_empty() {
            return Err(Error::Invalid("server name is required".into()));
        }
        if server.oauth_ref.is_some() {
            return Err(Error::Invalid(
                "new servers must not carry an OAuth credential reference".into(),
            ));
        }
        match server.url.as_deref() {
            Some(url) => {
                server.url = Some(crate::remote::validate_url(url)?);
                if !server.command.trim().is_empty()
                    || !server.args.is_empty()
                    || !server.env.is_empty()
                {
                    return Err(Error::Invalid(
                        "a remote server has a URL, not a command".into(),
                    ));
                }
                server.command.clear();
                match server.auth {
                    HttpAuth::Header if server.headers.is_empty() => {
                        return Err(Error::Invalid("header auth needs a header".into()));
                    }
                    HttpAuth::Oauth => server.oauth_ref = Some(uuid::Uuid::new_v4().to_string()),
                    _ => {}
                }
            }
            None => {
                if server.command.trim().is_empty() {
                    return Err(Error::Invalid("server command is required".into()));
                }
                if !server.headers.is_empty() || server.auth != HttpAuth::None {
                    return Err(Error::Invalid(
                        "headers and auth apply to remote servers only".into(),
                    ));
                }
            }
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
            let store = self.credentials.clone();
            server = tokio::task::spawn_blocking(move || {
                crate::credentials::protect_server(store.as_ref(), &mut server)?;
                Ok::<_, Error>(server)
            })
            .await
            .map_err(|_| Error::Gateway("could not store server credentials".into()))??;
            let mut updated = config.clone();
            updated.servers.push(server.clone());
            // On an uncertain disk failure, keep the credential entry for recovery.
            updated.save(&self.config_path)?;
            *config = updated;
        }
        self.backends.start(server.clone()).await;
        Ok(server)
    }

    pub async fn remove_server(&self, server_id: &str) -> Result<()> {
        let removed = {
            let mut config = self.config.write().await;
            let server = config
                .servers
                .iter()
                .find(|s| s.id == server_id)
                .ok_or_else(|| Error::NotFound(format!("server {server_id}")))?
                .clone();
            let mut updated = config.clone();
            updated.servers.retain(|s| s.id != server_id);
            updated.save(&self.config_path)?;
            *config = updated;
            server
        };
        self.backends.remove(server_id).await;
        let store = self.credentials.clone();
        tokio::task::spawn_blocking(move || {
            if let Some(id) = &removed.credential_ref {
                crate::credentials::delete(store.as_ref(), id)?;
            }
            crate::remote::forget_tokens(store.as_ref(), &removed)
        })
        .await
        .map_err(|_| {
            Error::Gateway("server removed, but credential cleanup could not complete".into())
        })??;
        Ok(())
    }

    pub async fn restart_server(&self, server_id: &str) -> Result<()> {
        self.backends.restart(server_id).await
    }

    async fn server_config(&self, server_id: &str) -> Result<ServerConfig> {
        self.config
            .read()
            .await
            .servers
            .iter()
            .find(|s| s.id == server_id)
            .cloned()
            .ok_or_else(|| Error::NotFound(format!("server {server_id}")))
    }

    /// Start a browser sign-in for an OAuth server and return the URL to open. The server
    /// reconnects on its own once the browser comes back; a failure lands in its status.
    pub async fn sign_in_server(self: &Arc<Self>, server_id: &str) -> Result<String> {
        let config = self.server_config(server_id).await?;
        if config.auth != HttpAuth::Oauth {
            return Err(Error::Invalid("this server does not use OAuth".into()));
        }
        let sign_in = crate::remote::begin_sign_in(&config, self.credentials.clone()).await?;
        let url = sign_in.url;
        let gateway = self.clone();
        tokio::spawn(async move {
            let outcome = sign_in
                .done
                .await
                .unwrap_or_else(|_| Err(Error::Gateway("sign-in was cancelled".into())));
            match outcome {
                Ok(()) => {
                    gateway.backends.stop(&config.id).await;
                    gateway.backends.start(config).await;
                }
                Err(err) => {
                    gateway
                        .backends
                        .mark_failed(&config.id, err.to_string())
                        .await
                }
            }
        });
        Ok(url)
    }

    /// Forget an OAuth server's tokens and registration. It shows as needing a sign-in.
    pub async fn sign_out_server(&self, server_id: &str) -> Result<()> {
        let config = self.server_config(server_id).await?;
        if config.auth != HttpAuth::Oauth {
            return Err(Error::Invalid("this server does not use OAuth".into()));
        }
        self.backends.stop(server_id).await;
        let store = self.credentials.clone();
        let forget = config.clone();
        tokio::task::spawn_blocking(move || crate::remote::forget_tokens(store.as_ref(), &forget))
            .await
            .map_err(|_| Error::Gateway("could not reach the credential store".into()))??;
        self.backends.start(config).await;
        Ok(())
    }

    pub async fn agents(&self) -> Vec<AgentView> {
        let connected: std::collections::HashSet<String> = self
            .sessions
            .lock()
            .map(|s| s.values().map(|e| e.agent_id.clone()).collect())
            .unwrap_or_default();
        let now = Utc::now();
        let config = self.config.read().await;
        config
            .agents
            .iter()
            .cloned()
            .map(|agent| {
                let client_ids = config.agent_client_ids(&agent.id);
                AgentView {
                    connected: connected.contains(&agent.id),
                    tokens: config
                        .tokens
                        .iter()
                        .filter(|t| t.agent_id == agent.id && !t.is_expired(now))
                        .map(|t| TokenView {
                            kind: t.kind,
                            created_at: t.created_at,
                            expires_at: t.expires_at,
                        })
                        .collect(),
                    clients: config
                        .clients
                        .iter()
                        .filter(|c| client_ids.contains(&c.client_id))
                        .map(|c| ClientView {
                            client_id: c.client_id.clone(),
                            client_name: c.client_name.clone(),
                            created_at: c.created_at,
                            origin: c.origin.clone(),
                            signed_in: config.tokens.iter().any(|t| {
                                t.client_id.as_deref() == Some(c.client_id.as_str())
                                    && !t.is_expired(now)
                            }),
                        })
                        .collect(),
                    agent,
                }
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
            if !approve {
                // Deny is a sign-out too: nothing it holds keeps working.
                config.tokens.retain(|t| t.agent_id != agent_id);
            }
            config.save(&self.config_path)?;
        }
        self.resolve_authorization(agent_id, approve);
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
            Self::forget_credentials(&mut config, agent_id);
            config.agents.retain(|a| a.id != agent_id);
            if config.agents.len() == before {
                return Err(Error::NotFound(format!("agent {agent_id}")));
            }
            config.save(&self.config_path)?;
        }
        self.resolve_authorization(agent_id, false);
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

    /// Called on MCP `initialize` over a bearer-authenticated request: the token already
    /// names the agent, so nothing the client announces about itself is trusted for identity.
    async fn register_authenticated_session(
        &self,
        session_id: &str,
        agent_id: &str,
        client_version: Option<&str>,
        peer: Peer<RoleServer>,
    ) -> Option<AgentConfig> {
        let agent = {
            let mut config = self.config.write().await;
            let agent = config.agents.iter_mut().find(|a| a.id == agent_id)?;
            if let Some(v) = client_version {
                agent.client_version = Some(v.to_string());
            }
            agent.clone()
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
        let _ = self.events.send(GatewayEvent::AgentConnected {
            agent_id: agent.id.clone(),
        });
        Some(agent)
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
        if decision.scope != DecisionScope::Once {
            self.append_rule_for(&call, decision).await?;
        }
        Ok(())
    }

    /// Current rules, with expired time boxes pruned on the way out.
    pub async fn rules(&self) -> Vec<Rule> {
        let now = Utc::now();
        let stale = self
            .config
            .read()
            .await
            .rules
            .iter()
            .any(|r| r.is_expired(now));
        if stale {
            let mut config = self.config.write().await;
            config.rules.retain(|r| !r.is_expired(now));
            if let Err(err) = config.save(&self.config_path) {
                warn!(%err, "could not persist pruned rules");
            }
            drop(config);
            let _ = self.events.send(GatewayEvent::RulesChanged);
        }
        self.config.read().await.rules.clone()
    }

    /// Add a rule from the panel. A rule on the same agent, server, and tool is replaced.
    pub async fn add_rule(&self, new: NewRule) -> Result<Rule> {
        if new.agent_id.is_none() && new.server_id.is_none() && new.tool.is_none() {
            return Err(Error::Invalid(
                "a rule needs at least an agent, a server, or a tool".into(),
            ));
        }
        let now = Utc::now();
        let rule = Rule {
            id: uuid::Uuid::new_v4().to_string(),
            agent_id: new.agent_id,
            server_id: new.server_id,
            tool: new.tool.filter(|t| !t.trim().is_empty()),
            decision: new.decision,
            attention: new.attention,
            scope: new.scope,
            expires_at: new
                .minutes
                .map(|m| now + chrono::Duration::minutes(i64::from(m))),
            condition: None,
            created_at: now,
        };
        self.upsert_rule(rule.clone()).await?;
        Ok(rule)
    }

    async fn upsert_rule(&self, rule: Rule) -> Result<()> {
        {
            let mut config = self.config.write().await;
            config.rules.retain(|r| {
                !(r.agent_id == rule.agent_id
                    && r.server_id == rule.server_id
                    && r.tool == rule.tool)
            });
            config.rules.push(rule);
            config.save(&self.config_path)?;
        }
        let _ = self.events.send(GatewayEvent::RulesChanged);
        Ok(())
    }

    /// Change how an agent behaves when no rule matches, and how loudly its calls are surfaced.
    pub async fn set_agent_policy(
        &self,
        agent_id: &str,
        posture: Option<Posture>,
        attention: Option<Attention>,
    ) -> Result<AgentConfig> {
        let agent = {
            let mut config = self.config.write().await;
            let agent = config
                .agents
                .iter_mut()
                .find(|a| a.id == agent_id)
                .ok_or_else(|| Error::NotFound(format!("agent {agent_id}")))?;
            if let Some(p) = posture {
                agent.posture = p;
            }
            if let Some(a) = attention {
                agent.attention = a;
            }
            let agent = agent.clone();
            config.save(&self.config_path)?;
            agent
        };
        let _ = self.events.send(GatewayEvent::AgentUpdated {
            agent_id: agent_id.to_string(),
        });
        Ok(agent)
    }

    pub async fn settings(&self) -> Settings {
        let config = self.config.read().await;
        Settings {
            on_timeout: config.on_timeout,
            do_not_disturb: config.do_not_disturb,
            rate_limit_per_minute: config.rate_limit_per_minute,
            hold_timeout_secs: config.hold_timeout_secs,
            auto_open_on_pending: config.auto_open_on_pending,
        }
    }

    pub async fn set_settings(&self, settings: Settings) -> Result<()> {
        if settings.hold_timeout_secs < 10 {
            return Err(Error::Invalid(
                "hold timeout must be at least 10 seconds".into(),
            ));
        }
        {
            let mut config = self.config.write().await;
            config.on_timeout = settings.on_timeout;
            config.do_not_disturb = settings.do_not_disturb;
            config.rate_limit_per_minute = settings.rate_limit_per_minute.filter(|n| *n > 0);
            config.hold_timeout_secs = settings.hold_timeout_secs;
            config.auto_open_on_pending = settings.auto_open_on_pending;
            config.save(&self.config_path)?;
        }
        let _ = self.events.send(GatewayEvent::SettingsChanged);
        Ok(())
    }

    /// Tools one server currently exposes, for per-tool overrides in the panel.
    pub async fn server_tools(&self, server_id: &str) -> Vec<ToolInfo> {
        self.backends
            .list_tools(false)
            .await
            .into_iter()
            .filter(|(server, _)| server.id == server_id)
            .map(|(_, tool)| ToolInfo {
                name: tool.name.to_string(),
                description: tool.description.as_ref().map(|d| d.to_string()),
                read_only: tool
                    .annotations
                    .as_ref()
                    .and_then(|a| a.read_only_hint)
                    .unwrap_or(false),
                destructive: tool
                    .annotations
                    .as_ref()
                    .and_then(|a| a.destructive_hint)
                    .unwrap_or(false),
            })
            .collect()
    }

    /// Record one call attempt and report whether the agent is over `limit` per minute.
    fn rate_tripped(&self, agent_id: &str, limit: u32) -> bool {
        let now = Instant::now();
        let mut calls = self.calls.lock().unwrap_or_else(|e| e.into_inner());
        let window = calls.entry(agent_id.to_string()).or_default();
        while window
            .front()
            .is_some_and(|t| now.duration_since(*t) > std::time::Duration::from_secs(60))
        {
            window.pop_front();
        }
        window.push_back(now);
        window.len() as u32 > limit
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

    /// The last `days` local days summed up: totals, what needed a person, per agent, per day.
    pub async fn activity(&self, days: u32) -> crate::activity::ActivitySummary {
        crate::activity::summarize(self.audit.list(usize::MAX).iter(), days, Utc::now())
    }

    // ----- native actions (observe) ------------------------------------------------------

    pub(crate) fn hook_token_matches(&self, candidate: &str) -> bool {
        self.hook_token
            .read()
            .map(|t| crate::native::token_eq(&t, candidate))
            .unwrap_or(false)
    }

    /// The URL an agent host posts its hook events to. Contains the secret; treat like a token.
    pub fn hook_url(&self, host: &str) -> String {
        let token = self
            .hook_token
            .read()
            .map(|t| t.clone())
            .unwrap_or_default();
        format!("http://127.0.0.1:{}/hooks/{host}/{token}", self.listen_port)
    }

    /// Replace the hook secret. The old URL stops working at once; hosts need the new one.
    pub async fn rotate_hook_token(&self) -> Result<()> {
        let token = crate::native::new_token();
        let path = self.hook_token_path.clone();
        let bytes = token.clone();
        tokio::task::spawn_blocking(move || crate::storage::atomic_write(&path, bytes.as_bytes()))
            .await
            .map_err(|_| Error::Gateway("hook token write could not complete".into()))??;
        if let Ok(mut current) = self.hook_token.write() {
            *current = token;
        }
        let _ = self.events.send(GatewayEvent::SettingsChanged);
        Ok(())
    }

    pub async fn set_observe_native(&self, on: bool) -> Result<()> {
        {
            let mut config = self.config.write().await;
            config.observe_native = on;
            config.save(&self.config_path)?;
        }
        let _ = self.events.send(GatewayEvent::SettingsChanged);
        Ok(())
    }

    /// Coverage and the last seven days of counts, computed from the in-memory ring.
    pub async fn native_status(&self) -> crate::native::NativeStatus {
        let observe_native = self.config.read().await.observe_native;
        let cutoff = Utc::now() - chrono::Duration::days(7);
        let mut actions = 0usize;
        let mut per_host: HashMap<String, usize> = HashMap::new();
        let mut counts: HashMap<String, usize> = HashMap::new();
        let mut last = self
            .native_last
            .lock()
            .map(|l| l.clone())
            .unwrap_or_default();
        for entry in self.audit.list(usize::MAX) {
            let Some(native) = entry.native.as_ref() else {
                continue;
            };
            if entry.at < cutoff || native.via_prism {
                continue;
            }
            actions += 1;
            *per_host.entry(native.host.clone()).or_default() += 1;
            let seen = last.entry(native.host.clone()).or_insert(entry.at);
            *seen = (*seen).max(entry.at);
            if let Some(reason) = &native.would_hold {
                *counts.entry(reason.clone()).or_default() += 1;
            }
        }
        let hosts = crate::native::HOSTS
            .iter()
            .map(|host| crate::native::HostStatus {
                host: host.to_string(),
                hook_url: self.hook_url(host),
                last_event_at: last.get(*host).copied(),
                actions_7d: per_host.get(*host).copied().unwrap_or(0),
            })
            .collect();
        let mut by_reason: Vec<crate::native::ReasonCount> = counts
            .into_iter()
            .map(|(reason, count)| crate::native::ReasonCount { reason, count })
            .collect();
        by_reason.sort_by(|a, b| b.count.cmp(&a.count).then(a.reason.cmp(&b.reason)));
        crate::native::NativeStatus {
            observe_native,
            last_event_at: last.values().max().copied(),
            actions_7d: actions,
            would_hold_7d: by_reason.iter().map(|r| r.count).sum(),
            by_reason,
            rules: crate::native::shadow::RULES.to_vec(),
            hosts,
        }
    }

    /// JSONL of the native entries the shadow list would have held, newest first.
    pub async fn native_export(&self, days: i64) -> String {
        let cutoff = Utc::now() - chrono::Duration::days(days);
        let mut out = String::new();
        for entry in self.audit.list(usize::MAX) {
            let hit = entry
                .native
                .as_ref()
                .is_some_and(|n| n.would_hold.is_some() && !n.via_prism);
            if hit && entry.at >= cutoff {
                if let Ok(line) = serde_json::to_string(&entry) {
                    out.push_str(&line);
                    out.push('\n');
                }
            }
        }
        out
    }

    /// One hook event becomes one observed audit entry. Creates the host's agent record on first
    /// contact. A revoked host is refused, so the hook stops being accepted once you deny it.
    pub(crate) async fn record_native(
        &self,
        host: &str,
        event: crate::native::HookEvent,
    ) -> Result<()> {
        if !self
            .native_budget
            .lock()
            .map(|mut b| b.admit())
            .unwrap_or(false)
        {
            return Ok(());
        }
        let agent_id = format!("host:{host}");
        let now = Utc::now();
        let (observe, agent_name, created) = {
            let mut config = self.config.write().await;
            let created = match config.agents.iter().find(|a| a.id == agent_id) {
                Some(agent) if agent.status == AgentStatus::Denied => {
                    return Err(Error::NotFound(format!("host {host} is revoked")));
                }
                Some(_) => false,
                None => {
                    config.agents.push(AgentConfig::harness(
                        host,
                        None,
                        AgentStatus::Approved,
                        now,
                    ));
                    config.save(&self.config_path)?;
                    true
                }
            };
            let name = config
                .agents
                .iter()
                .find(|a| a.id == agent_id)
                .map(|a| a.name.clone())
                .unwrap_or_else(|| host_display_name(host));
            (config.observe_native, name, created)
        };
        if created {
            let _ = self.events.send(GatewayEvent::AgentUpdated {
                agent_id: agent_id.clone(),
            });
        }
        if !observe {
            return Ok(());
        }
        let home = home_dir();
        let cwd = event.cwd.as_deref().map(Path::new);
        let subject =
            crate::native::subject(&event.tool_name, &event.tool_input, cwd, home.as_deref());
        let would_hold = crate::native::shadow::evaluate(
            &event.tool_name,
            &event.tool_input,
            cwd,
            home.as_deref(),
        )
        .map(str::to_string);
        let via_prism = if event.tool_name.starts_with("mcp__") {
            self.backends
                .list_tools(false)
                .await
                .iter()
                .any(|(server, tool)| {
                    event
                        .tool_name
                        .ends_with(&format!("__{}__{}", server.name, tool.name))
                })
        } else {
            false
        };
        if let Ok(mut last) = self.native_last.lock() {
            last.insert(host.to_string(), now);
        }
        self.audit.record(AuditEntry {
            id: uuid::Uuid::new_v4().to_string(),
            at: now,
            agent_id,
            agent_name,
            server_id: host.to_string(),
            tool: event.tool_name,
            verdict: AuditVerdict::Allowed,
            source: AuditSource::Observed,
            duration_ms: 0,
            error: None,
            attention: Attention::Silent,
            native: Some(crate::audit::NativeDetail {
                host: host.to_string(),
                session: event.session_id,
                cwd: event.cwd,
                subject,
                would_hold,
                agent_type: event.agent_type,
                via_prism,
            }),
        });
        Ok(())
    }

    /// Sync snapshot of the configured panel anchor for window placement callbacks.
    pub fn panel_anchor(&self) -> PanelAnchor {
        self.config
            .try_read()
            .map(|c| c.panel_anchor)
            .unwrap_or_default()
    }

    /// The configured panel shortcut, if the user set one.
    pub fn panel_shortcut(&self) -> Option<String> {
        self.config
            .try_read()
            .ok()
            .and_then(|c| c.panel_shortcut.clone())
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
        self.audit.close();
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
        let now = Utc::now();
        let (scope, expires_at) = match decision.scope {
            DecisionScope::Once => return Ok(()),
            DecisionScope::Session => (RuleScope::Session, None),
            DecisionScope::Always => (RuleScope::Always, None),
            DecisionScope::For { minutes } => (
                RuleScope::Always,
                Some(now + chrono::Duration::minutes(i64::from(minutes.max(1)))),
            ),
        };
        let (server_id, tool) = match decision.target {
            DecisionTarget::Tool => (Some(call.server_id.clone()), Some(call.tool.clone())),
            DecisionTarget::Server => (Some(call.server_id.clone()), None),
            DecisionTarget::Agent => (None, None),
        };
        let rule = Rule {
            id: uuid::Uuid::new_v4().to_string(),
            agent_id: Some(call.agent_id.clone()),
            server_id,
            tool,
            decision: match decision.verdict {
                DecisionVerdict::Allow => RuleDecision::Allow,
                DecisionVerdict::Deny => RuleDecision::Deny,
            },
            attention: None,
            scope,
            expires_at,
            condition: None,
            created_at: now,
        };
        self.upsert_rule(rule).await
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
                attention: Attention::Silent,
                native: None,
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

        let (eval, dnd, on_timeout, rate_limit) = {
            let config = self.config.read().await;
            let eval = policy::evaluate(
                &config.rules,
                &agent,
                &server.id,
                &original_tool,
                annotations.as_ref(),
                Utc::now(),
            );
            (
                eval,
                config.do_not_disturb,
                config.on_timeout,
                config.rate_limit_per_minute,
            )
        };

        // The tripwire counts every attempt and turns an allow into an ask once an agent runs hot.
        let tripped = rate_limit.is_some_and(|limit| self.rate_tripped(&agent.id, limit));
        let (verdict, reason) = match (eval.verdict, tripped) {
            (Verdict::Allow, true) => (Verdict::Ask, HoldReason::RateLimit),
            (v, _) => (v, HoldReason::Policy),
        };
        let source = match &eval.decider {
            Decider::Rule { rule_id } => AuditSource::Rule {
                rule_id: rule_id.clone(),
            },
            Decider::Posture(posture) => AuditSource::Posture { posture: *posture },
        };
        // While do-not-disturb is on, nothing louder than a badge gets through.
        let attention = if dnd {
            eval.attention.min(Attention::Badge)
        } else {
            eval.attention
        };

        match verdict {
            Verdict::Allow => {
                self.forward_or_error(
                    &agent,
                    &server,
                    &original_tool,
                    arguments,
                    started,
                    source,
                    AuditVerdict::Allowed,
                    attention,
                )
                .await
            }
            Verdict::Deny => {
                let summary = match &eval.decider {
                    Decider::Rule { rule_id } => {
                        let config = self.config.read().await;
                        config
                            .rules
                            .iter()
                            .find(|r| &r.id == rule_id)
                            .map(rule_summary)
                            .unwrap_or_else(|| "deny".into())
                    }
                    Decider::Posture(p) => format!("{p:?} posture"),
                };
                let message = format!("Denied by Prism policy: {summary}");
                self.audit.record(AuditEntry {
                    id: uuid::Uuid::new_v4().to_string(),
                    at: Utc::now(),
                    agent_id: agent.id,
                    agent_name: agent.name,
                    server_id: server.id,
                    tool: original_tool,
                    verdict: AuditVerdict::Denied,
                    source,
                    duration_ms: started.elapsed().as_millis() as u64,
                    error: Some(message.clone()),
                    attention,
                    native: None,
                });
                Ok(CallToolResult::error(vec![ContentBlock::text(message)]))
            }
            Verdict::Ask if dnd => {
                self.resolve_unattended(
                    &agent,
                    &server,
                    &original_tool,
                    arguments,
                    started,
                    annotations.as_ref(),
                    on_timeout,
                    AuditSource::DoNotDisturb,
                    "Prism is in do-not-disturb and this call needed a human. Retry later or ask the operator.",
                )
                .await
            }
            Verdict::Ask => {
                self.hold_then_forward(
                    agent,
                    server,
                    original_tool,
                    arguments,
                    started,
                    annotations.as_ref(),
                    reason,
                )
                .await
            }
        }
    }

    /// Nobody can answer (timeout or do-not-disturb): apply the configured fallback.
    #[allow(clippy::too_many_arguments)]
    async fn resolve_unattended(
        &self,
        agent: &AgentConfig,
        server: &ServerConfig,
        tool: &str,
        arguments: serde_json::Value,
        started: Instant,
        annotations: Option<&ToolAnnotations>,
        behavior: TimeoutBehavior,
        source: AuditSource,
        message: &str,
    ) -> std::result::Result<CallToolResult, McpError> {
        let read_only = annotations.is_some_and(ToolAnnotations::is_read_only);
        if behavior == TimeoutBehavior::AllowReadOnly && read_only {
            return self
                .forward_or_error(
                    agent,
                    server,
                    tool,
                    arguments,
                    started,
                    source,
                    AuditVerdict::Allowed,
                    Attention::Badge,
                )
                .await;
        }
        let verdict = if source == AuditSource::Timeout {
            AuditVerdict::Timeout
        } else {
            AuditVerdict::Denied
        };
        self.audit.record(AuditEntry {
            id: uuid::Uuid::new_v4().to_string(),
            at: Utc::now(),
            agent_id: agent.id.clone(),
            agent_name: agent.name.clone(),
            server_id: server.id.clone(),
            tool: tool.to_string(),
            verdict,
            source,
            duration_ms: started.elapsed().as_millis() as u64,
            error: Some(message.to_string()),
            attention: Attention::Badge,
            native: None,
        });
        Ok(CallToolResult::error(vec![ContentBlock::text(message)]))
    }

    #[allow(clippy::too_many_arguments)]
    async fn hold_then_forward(
        &self,
        agent: AgentConfig,
        server: ServerConfig,
        tool: String,
        arguments: serde_json::Value,
        started: Instant,
        annotations: Option<&ToolAnnotations>,
        reason: HoldReason,
    ) -> std::result::Result<CallToolResult, McpError> {
        let (hold, on_timeout) = {
            let config = self.config.read().await;
            (
                std::time::Duration::from_secs(config.hold_timeout_secs),
                config.on_timeout,
            )
        };
        let now = Utc::now();
        let pending = PendingCall {
            id: uuid::Uuid::new_v4().to_string(),
            agent_id: agent.id.clone(),
            agent_name: agent.name.clone(),
            server_id: server.id.clone(),
            server_name: server.name.clone(),
            tool: tool.clone(),
            arguments: arguments.clone(),
            requested_at: now,
            deadline: Some(now + chrono::Duration::from_std(hold).unwrap_or_default()),
            posture: agent.posture,
            reason,
        };
        let _ = self.events.send(GatewayEvent::PendingCall(pending.clone()));
        let outcome = self.approval.register_for(pending.clone(), hold).await;
        match outcome {
            HoldOutcome::Timeout => {
                self.resolve_unattended(
                    &agent,
                    &server,
                    &tool,
                    arguments,
                    started,
                    annotations,
                    on_timeout,
                    AuditSource::Timeout,
                    TIMEOUT_MESSAGE,
                )
                .await
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
                        attention: Attention::Silent,
                        native: None,
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
                        Attention::Silent,
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
        attention: Attention,
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
                    attention,
                    native: None,
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
                    attention,
                    native: None,
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

    /// The identity on this request must be the one that opened the session. The HTTP layer
    /// enforces the same thing; this is the check that survives a transport change.
    fn caller(
        &self,
        context: &RequestContext<RoleServer>,
    ) -> std::result::Result<Option<String>, McpError> {
        let on_request = context
            .extensions
            .get::<http::request::Parts>()
            .and_then(|parts| parts.extensions.get::<AuthenticatedAgent>())
            .map(|a| a.agent_id.clone());
        let session = self.agent_id();
        if on_request.is_none() || on_request != session {
            return Err(McpError::invalid_request(
                "this session belongs to another agent",
                None,
            ));
        }
        Ok(session)
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
        let authenticated = context
            .extensions
            .get::<http::request::Parts>()
            .and_then(|parts| parts.extensions.get::<AuthenticatedAgent>())
            .map(|a| a.agent_id.clone());
        let version = Some(request.client_info.version.as_str());
        let authenticated = authenticated
            .ok_or_else(|| McpError::invalid_request("a bearer token is required", None))?;
        let agent_id = self
            .gateway
            .register_authenticated_session(
                &self.session_id,
                &authenticated,
                version,
                context.peer.clone(),
            )
            .await
            .map(|a| a.id)
            .ok_or_else(|| McpError::invalid_request("unknown agent", None))?;
        if let Ok(mut slot) = self.agent_id.lock() {
            *slot = Some(agent_id);
        }
        Ok(self.get_info())
    }

    async fn list_tools(
        &self,
        _request: Option<PaginatedRequestParams>,
        context: RequestContext<RoleServer>,
    ) -> std::result::Result<ListToolsResult, McpError> {
        let agent = self.caller(&context)?;
        Ok(self.gateway.handle_list_tools(agent.as_deref()).await)
    }

    async fn call_tool(
        &self,
        request: CallToolRequestParams,
        context: RequestContext<RoleServer>,
    ) -> std::result::Result<CallToolResponse, McpError> {
        let agent = self.caller(&context)?;
        self.gateway
            .handle_call_tool(request, agent.as_deref())
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
            // The outer HTTP guard enforces one Host policy for MCP and OAuth alike.
            StreamableHttpServerConfig::default().disable_allowed_hosts(),
        );

    let app = Router::new()
        .nest_service("/mcp", service)
        .layer(axum::middleware::from_fn_with_state(
            gateway.clone(),
            oauth::require_bearer,
        ))
        .merge(oauth::router(gateway.clone()))
        .merge(crate::native::router(gateway.clone()))
        .layer(axum::middleware::from_fn_with_state(
            gateway.clone(),
            crate::http_security::guard,
        ));

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

fn rule_summary(rule: &Rule) -> String {
    let agent = rule.agent_id.as_deref().unwrap_or("*");
    let server = rule.server_id.as_deref().unwrap_or("*");
    let tool = rule.tool.as_deref().unwrap_or("*");
    let decision = match rule.decision {
        RuleDecision::Allow => "allow",
        RuleDecision::Deny => "deny",
        RuleDecision::Ask => "ask",
    };
    format!("{decision} agent={agent} server={server} tool={tool}")
}

fn host_display_name(host: &str) -> String {
    crate::native::harness_display_name(host).to_string()
}

fn home_dir() -> Option<PathBuf> {
    std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .map(PathBuf::from)
}

/// The hook secret persists so the URL in the host's settings keeps working across restarts.
fn load_or_create_hook_token(path: &Path) -> Result<String> {
    if path.exists() {
        let mut text = String::new();
        std::io::Read::read_to_string(&mut crate::storage::read(path)?, &mut text)?;
        let token = text.trim().to_string();
        if token.len() >= 32 {
            return Ok(token);
        }
    }
    let token = crate::native::new_token();
    crate::storage::atomic_write(path, token.as_bytes())?;
    Ok(token)
}
