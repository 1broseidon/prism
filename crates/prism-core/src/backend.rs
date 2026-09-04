use std::collections::HashMap;
use std::time::Duration;

use rmcp::model::{CallToolRequestParams, CallToolResult, Tool};
use rmcp::service::RunningService;
use rmcp::transport::TokioChildProcess;
use rmcp::{RoleClient, ServiceExt};
use serde::{Deserialize, Serialize};
use tokio::process::Command;
use tokio::sync::RwLock;
use tracing::{error, info, warn};

use crate::config::ServerConfig;
use crate::error::{Error, Result};
use crate::events::{EventSender, GatewayEvent};

/// Runtime status of one spawned MCP backend.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum BackendStatus {
    Starting,
    Running { tool_count: usize },
    Failed { error: String },
    Stopped,
}

type McpClient = RunningService<RoleClient, ()>;

struct Backend {
    config: ServerConfig,
    status: BackendStatus,
    client: Option<McpClient>,
    tools: Vec<Tool>,
}

/// Spawns and talks to stdio MCP servers via rmcp's child-process transport.
pub struct BackendManager {
    backends: RwLock<HashMap<String, Backend>>,
    events: EventSender,
}

impl BackendManager {
    pub fn new(events: EventSender) -> Self {
        Self {
            backends: RwLock::new(HashMap::new()),
            events,
        }
    }

    pub async fn start(&self, config: ServerConfig) {
        if !config.enabled {
            self.insert_stopped(config).await;
            return;
        }
        let id = config.id.clone();
        self.set_starting(config.clone()).await;
        match connect(&config).await {
            Ok((client, tools)) => {
                let tool_count = tools.len();
                info!(server = %config.name, tool_count, "backend running");
                let status = BackendStatus::Running { tool_count };
                let mut map = self.backends.write().await;
                map.insert(
                    id.clone(),
                    Backend {
                        config,
                        status: status.clone(),
                        client: Some(client),
                        tools,
                    },
                );
                let _ = self.events.send(GatewayEvent::ServerStatus {
                    server_id: id,
                    status,
                });
            }
            Err(err) => {
                let message = err.to_string();
                error!(server = %config.name, %message, "backend failed to start");
                let status = BackendStatus::Failed {
                    error: message.clone(),
                };
                let mut map = self.backends.write().await;
                map.insert(
                    id.clone(),
                    Backend {
                        config,
                        status: status.clone(),
                        client: None,
                        tools: Vec::new(),
                    },
                );
                let _ = self.events.send(GatewayEvent::ServerStatus {
                    server_id: id,
                    status,
                });
            }
        }
    }

    pub async fn stop(&self, server_id: &str) {
        let mut map = self.backends.write().await;
        if let Some(backend) = map.get_mut(server_id) {
            if let Some(mut client) = backend.client.take() {
                if let Err(err) = client.close_with_timeout(Duration::from_secs(3)).await {
                    warn!(%err, server_id, "backend close failed");
                }
            }
            backend.tools.clear();
            backend.status = BackendStatus::Stopped;
            let _ = self.events.send(GatewayEvent::ServerStatus {
                server_id: server_id.to_string(),
                status: BackendStatus::Stopped,
            });
        }
    }

    pub async fn remove(&self, server_id: &str) {
        self.stop(server_id).await;
        self.backends.write().await.remove(server_id);
    }

    pub async fn restart(&self, server_id: &str) -> Result<()> {
        let config = {
            let map = self.backends.read().await;
            map.get(server_id)
                .map(|b| b.config.clone())
                .ok_or_else(|| Error::NotFound(format!("server {server_id}")))?
        };
        self.stop(server_id).await;
        self.start(config).await;
        Ok(())
    }

    /// Cached tools across Running backends. `refresh` re-lists from the peer.
    pub async fn list_tools(&self, refresh: bool) -> Vec<(ServerConfig, Tool)> {
        if refresh {
            self.refresh_all().await;
        }
        let map = self.backends.read().await;
        let mut out = Vec::new();
        for backend in map.values() {
            if matches!(backend.status, BackendStatus::Running { .. }) {
                for tool in &backend.tools {
                    out.push((backend.config.clone(), tool.clone()));
                }
            }
        }
        out
    }

    pub async fn call_tool(
        &self,
        server_id: &str,
        name: &str,
        arguments: serde_json::Value,
    ) -> Result<CallToolResult> {
        let map = self.backends.read().await;
        let backend = map
            .get(server_id)
            .ok_or_else(|| Error::NotFound(format!("server {server_id}")))?;
        let client = backend
            .client
            .as_ref()
            .ok_or_else(|| Error::Backend(format!("server {server_id} is not running")))?;
        let params = call_params(name, arguments);
        client
            .call_tool(params)
            .await
            .map_err(|err| Error::Backend(err.to_string()))
    }

    pub async fn snapshot(&self) -> Vec<(ServerConfig, BackendStatus)> {
        let map = self.backends.read().await;
        map.values()
            .map(|b| (b.config.clone(), b.status.clone()))
            .collect()
    }

    pub async fn running_count(&self) -> usize {
        let map = self.backends.read().await;
        map.values()
            .filter(|b| matches!(b.status, BackendStatus::Running { .. }))
            .count()
    }

    pub async fn find_tool(&self, server_id: &str, tool_name: &str) -> Option<Tool> {
        let map = self.backends.read().await;
        map.get(server_id)?
            .tools
            .iter()
            .find(|t| t.name.as_ref() == tool_name)
            .cloned()
    }

    async fn refresh_all(&self) {
        let ids: Vec<String> = {
            let map = self.backends.read().await;
            map.iter()
                .filter(|(_, b)| b.client.is_some())
                .map(|(id, _)| id.clone())
                .collect()
        };
        for id in ids {
            if let Err(err) = self.refresh_one(&id).await {
                warn!(server_id = %id, %err, "tool refresh failed");
            }
        }
    }

    async fn refresh_one(&self, server_id: &str) -> Result<()> {
        let tools = {
            let map = self.backends.read().await;
            let backend = map
                .get(server_id)
                .ok_or_else(|| Error::NotFound(format!("server {server_id}")))?;
            let client = backend
                .client
                .as_ref()
                .ok_or_else(|| Error::Backend(format!("server {server_id} is not running")))?;
            let listed = client
                .list_tools(Default::default())
                .await
                .map_err(|err| Error::Backend(err.to_string()))?;
            listed.tools
        };
        let mut map = self.backends.write().await;
        if let Some(backend) = map.get_mut(server_id) {
            let tool_count = tools.len();
            backend.tools = tools;
            backend.status = BackendStatus::Running { tool_count };
            let _ = self.events.send(GatewayEvent::ServerStatus {
                server_id: server_id.to_string(),
                status: backend.status.clone(),
            });
        }
        Ok(())
    }

    async fn set_starting(&self, config: ServerConfig) {
        let id = config.id.clone();
        let mut map = self.backends.write().await;
        map.insert(
            id.clone(),
            Backend {
                config,
                status: BackendStatus::Starting,
                client: None,
                tools: Vec::new(),
            },
        );
        let _ = self.events.send(GatewayEvent::ServerStatus {
            server_id: id,
            status: BackendStatus::Starting,
        });
    }

    async fn insert_stopped(&self, config: ServerConfig) {
        let id = config.id.clone();
        let mut map = self.backends.write().await;
        map.insert(
            id.clone(),
            Backend {
                config,
                status: BackendStatus::Stopped,
                client: None,
                tools: Vec::new(),
            },
        );
        let _ = self.events.send(GatewayEvent::ServerStatus {
            server_id: id,
            status: BackendStatus::Stopped,
        });
    }
}

async fn connect(config: &ServerConfig) -> Result<(McpClient, Vec<Tool>)> {
    let mut command = Command::new(&config.command);
    command.args(&config.args);
    for (key, value) in &config.env {
        command.env(key, value);
    }
    command.kill_on_drop(true);
    command.stdin(std::process::Stdio::piped());
    command.stdout(std::process::Stdio::piped());
    command.stderr(std::process::Stdio::inherit());

    let transport = TokioChildProcess::new(command)
        .map_err(|err| Error::Backend(format!("spawn {}: {err}", config.command)))?;
    let client = ()
        .serve(transport)
        .await
        .map_err(|err| Error::Backend(format!("handshake {}: {err}", config.name)))?;
    let listed = client
        .list_tools(Default::default())
        .await
        .map_err(|err| Error::Backend(format!("tools/list {}: {err}", config.name)))?;
    Ok((client, listed.tools))
}

fn call_params(name: &str, arguments: serde_json::Value) -> CallToolRequestParams {
    let params = CallToolRequestParams::new(name.to_string());
    match arguments {
        serde_json::Value::Object(map) => params.with_arguments(map),
        serde_json::Value::Null => params,
        other => {
            let mut map = serde_json::Map::new();
            map.insert("value".into(), other);
            params.with_arguments(map)
        }
    }
}

/// Snapshot of a configured server plus its live status, for the desktop UI.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServerView {
    pub id: String,
    pub name: String,
    pub command: String,
    pub args: Vec<String>,
    pub env: std::collections::BTreeMap<String, String>,
    pub enabled: bool,
    pub status: BackendStatus,
}

impl ServerView {
    pub fn from_parts(config: ServerConfig, status: BackendStatus) -> Self {
        Self {
            id: config.id,
            name: config.name,
            command: config.command,
            args: config.args,
            env: config.env,
            enabled: config.enabled,
            status,
        }
    }
}
