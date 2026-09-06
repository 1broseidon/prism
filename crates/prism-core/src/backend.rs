use std::collections::HashMap;
use std::sync::Arc;
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
    Running {
        tool_count: usize,
    },
    Failed {
        error: String,
    },
    Stopped,
    /// A remote OAuth server with no usable tokens. Sign in from the panel.
    SignInRequired,
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
    credentials: Arc<dyn crate::credentials::CredentialStore>,
}

impl BackendManager {
    pub(crate) fn new(
        events: EventSender,
        credentials: Arc<dyn crate::credentials::CredentialStore>,
    ) -> Self {
        Self {
            backends: RwLock::new(HashMap::new()),
            events,
            credentials,
        }
    }

    pub async fn start(&self, config: ServerConfig) {
        if !config.enabled {
            self.insert_stopped(config).await;
            return;
        }
        let id = config.id.clone();
        self.set_starting(config.clone()).await;
        match connect(&config, self.credentials.clone()).await {
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
                let status = match err {
                    Error::SignInRequired => {
                        info!(server = %config.name, "backend needs a sign-in");
                        BackendStatus::SignInRequired
                    }
                    err => {
                        let message = err.to_string();
                        error!(server = %config.name, %message, "backend failed to start");
                        BackendStatus::Failed { error: message }
                    }
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
                if client
                    .close_with_timeout(Duration::from_secs(3))
                    .await
                    .is_err()
                {
                    warn!(
                        server_id,
                        "backend close failed; details omitted to protect credentials"
                    );
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

    /// Record a failure that happened outside `start`, such as a sign-in that did not finish.
    pub async fn mark_failed(&self, server_id: &str, error: String) {
        let mut map = self.backends.write().await;
        if let Some(backend) = map.get_mut(server_id) {
            backend.client = None;
            backend.tools.clear();
            backend.status = BackendStatus::Failed { error };
            let _ = self.events.send(GatewayEvent::ServerStatus {
                server_id: server_id.to_string(),
                status: backend.status.clone(),
            });
        }
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
        client.call_tool(params).await.map_err(|_| {
            Error::Backend(
                "tool call failed; server error details omitted to protect credentials".into(),
            )
        })
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
            let listed = client.list_tools(Default::default()).await.map_err(|_| {
                Error::Backend(
                    "tool listing failed; server error details omitted to protect credentials"
                        .into(),
                )
            })?;
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

async fn connect(
    config: &ServerConfig,
    store: Arc<dyn crate::credentials::CredentialStore>,
) -> Result<(McpClient, Vec<Tool>)> {
    let protected = config.clone();
    let blocking_store = store.clone();
    let launch = tokio::task::spawn_blocking(move || {
        crate::credentials::resolve(blocking_store.as_ref(), &protected)
    })
    .await
    .map_err(|_| Error::Backend("could not retrieve server credentials".into()))??;
    let client = if config.is_remote() {
        crate::remote::connect(config, &launch, store).await?
    } else {
        let mut command = server_command(config, &launch, std::env::vars_os());
        command.kill_on_drop(true);
        // Set this on the transport builder: its defaults override Command stdio settings.
        // Servers can print credentials to stderr. Do not forward it to application logs.
        let (transport, _) = TokioChildProcess::builder(command)
            .stderr(std::process::Stdio::null())
            .spawn()
            .map_err(|err| {
                Error::Backend(format!(
                    "could not spawn server ({:?}); check its executable",
                    err.kind()
                ))
            })?;
        ().serve(transport).await.map_err(|_| {
            Error::Backend("server handshake failed; check its launch settings".into())
        })?
    };
    let listed = client.list_tools(Default::default()).await.map_err(|_| {
        Error::Backend("initial tool listing failed; check server configuration".into())
    })?;
    Ok((client, listed.tools))
}

fn inherited_env_allowed(name: &std::ffi::OsStr) -> bool {
    let Some(name) = name.to_str() else {
        return false;
    };
    #[cfg(windows)]
    let name = name.to_ascii_uppercase();
    #[cfg(windows)]
    let name = name.as_str();
    let common = matches!(
        name,
        "PATH"
            | "HOME"
            | "USER"
            | "LOGNAME"
            | "LANG"
            | "LC_ALL"
            | "LC_CTYPE"
            | "TZ"
            | "TMPDIR"
            | "TMP"
            | "TEMP"
            | "XDG_CACHE_HOME"
            | "XDG_CONFIG_HOME"
            | "XDG_DATA_HOME"
            | "UV_CACHE_DIR"
            | "NPM_CONFIG_CACHE"
            | "npm_config_cache"
    );
    #[cfg(windows)]
    let platform = matches!(
        name,
        "SYSTEMROOT"
            | "WINDIR"
            | "COMSPEC"
            | "PATHEXT"
            | "USERPROFILE"
            | "USERNAME"
            | "HOMEDRIVE"
            | "HOMEPATH"
            | "APPDATA"
            | "LOCALAPPDATA"
    );
    #[cfg(target_os = "linux")]
    let platform = matches!(
        name,
        "DISPLAY" | "WAYLAND_DISPLAY" | "XAUTHORITY" | "XDG_RUNTIME_DIR"
    );
    #[cfg(not(any(windows, target_os = "linux")))]
    let platform = false;
    common || platform
}

fn server_command(
    config: &ServerConfig,
    launch: &crate::credentials::LaunchSettings,
    parent: impl IntoIterator<Item = (std::ffi::OsString, std::ffi::OsString)>,
) -> Command {
    let mut command = Command::new(&config.command);
    command.env_clear();
    command.envs(
        parent
            .into_iter()
            .filter(|(name, _)| inherited_env_allowed(name)),
    );
    command.envs(&launch.env);
    command.args(&launch.args);
    command
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
    pub credentials_stored: bool,
    pub enabled: bool,
    pub status: BackendStatus,
    /// Endpoint of a remote server; `None` for a stdio one.
    pub url: Option<String>,
    pub auth: crate::config::HttpAuth,
}

impl ServerView {
    pub fn from_parts(config: ServerConfig, status: BackendStatus) -> Self {
        Self {
            id: config.id,
            name: config.name,
            command: config.command,
            args: Vec::new(),
            env: Default::default(),
            credentials_stored: config.credential_ref.is_some(),
            enabled: config.enabled,
            status,
            url: config.url,
            auth: config.auth,
        }
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use crate::credentials::{protect_server, tests::MemoryStore};

    #[tokio::test]
    async fn child_receives_only_allowlisted_and_explicit_environment() {
        let config = ServerConfig {
            id: "env-test".into(),
            name: "env-test".into(),
            command: "python3".into(),
            args: vec![],
            env: Default::default(),
            enabled: true,
            credential_ref: None,
            url: None,
            auth: crate::config::HttpAuth::None,
            headers: Default::default(),
            oauth_ref: None,
        };
        let launch = crate::credentials::LaunchSettings {
            args: vec![
                "-c".into(),
                "import json,os; print(json.dumps(dict(os.environ)))".into(),
            ],
            env: std::collections::BTreeMap::from([
                ("HOME".into(), "/explicit/home".into()),
                ("CUSTOM_SERVER_TOKEN".into(), "explicit-secret".into()),
            ]),
            headers: Default::default(),
        };
        let parent = vec![
            ("PATH".into(), std::env::var_os("PATH").unwrap()),
            ("HOME".into(), "/inherited/home".into()),
            ("UV_CACHE_DIR".into(), "/cache/uv".into()),
            ("PRISM_UNRELATED_SECRET".into(), "must-not-leak".into()),
            ("NPM_CONFIG_TOKEN".into(), "must-not-leak".into()),
        ];
        let output = server_command(&config, &launch, parent)
            .output()
            .await
            .unwrap();
        assert!(output.status.success());
        let env: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(env["HOME"], "/explicit/home");
        assert_eq!(env["CUSTOM_SERVER_TOKEN"], "explicit-secret");
        assert_eq!(env["UV_CACHE_DIR"], "/cache/uv");
        assert!(env.get("PRISM_UNRELATED_SECRET").is_none());
        assert!(env.get("NPM_CONFIG_TOKEN").is_none());
    }

    #[tokio::test]
    #[ignore = "manually checks a configured server using the native credential store"]
    async fn configured_server_lists_tools() {
        let path = std::env::var_os("PRISM_TEST_CONFIG").expect("PRISM_TEST_CONFIG is required");
        // Read-only: do not migrate or rewrite the running desktop application's config.
        let config: crate::config::PrismConfig =
            serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
        let name = std::env::var("PRISM_TEST_SERVER").unwrap_or_else(|_| "filesystem".into());
        let server = config
            .servers
            .iter()
            .find(|server| server.name == name)
            .expect("test server configured");
        let (mut client, tools) = tokio::time::timeout(
            Duration::from_secs(60),
            connect(server, Arc::new(crate::credentials::NativeStore::default())),
        )
        .await
        .expect("server startup timed out")
        .expect("server startup failed");
        assert!(!tools.is_empty());
        if name == "filesystem" {
            assert!(tools.iter().any(|tool| tool.name == "list_directory"));
            assert!(tools.iter().any(|tool| tool.name == "read_file"));
        }
        println!(
            "{} server listed {} tools with restricted environment",
            name,
            tools.len()
        );
        client
            .close_with_timeout(Duration::from_secs(3))
            .await
            .unwrap();
    }

    #[tokio::test]
    async fn manual_agent_tool_calls_still_require_permission() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("prism.json");
        crate::PrismConfig {
            listen_port: 0,
            ..Default::default()
        }
        .save(&path)
        .unwrap();
        let gateway = crate::Gateway::start_with_credentials(
            path,
            dir.path().join("audit.jsonl"),
            Arc::new(MemoryStore::default()),
        )
        .await
        .unwrap();
        let script = r#"
import json, sys
for line in sys.stdin:
    req = json.loads(line)
    if 'id' not in req: continue
    if req['method'] == 'initialize':
        result = {'protocolVersion':'2025-06-18','capabilities':{'tools':{}},'serverInfo':{'name':'fixture','version':'1'}}
    elif req['method'] == 'tools/list':
        result = {'tools':[{'name':'verify','inputSchema':{'type':'object'}}]}
    else:
        result = {'content':[{'type':'text','text':'tool executed'}]}
    print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}), flush=True)
"#;
        gateway
            .add_server(ServerConfig {
                id: "fixture".into(),
                name: "fixture".into(),
                command: "python3".into(),
                args: vec!["-u".into(), "-c".into(), script.into()],
                env: Default::default(),
                enabled: true,
                credential_ref: None,
                url: None,
                auth: crate::config::HttpAuth::None,
                headers: Default::default(),
                oauth_ref: None,
            })
            .await
            .unwrap();
        let token = gateway.create_manual_agent("manual").await.unwrap();
        let agent_id = gateway.authenticate(&token.token).await.unwrap();
        for verdict in ["deny", "allow"] {
            let gateway_for_call = gateway.clone();
            let caller = agent_id.clone();
            let call = tokio::spawn(async move {
                gateway_for_call
                    .handle_call_tool(CallToolRequestParams::new("fixture__verify"), Some(&caller))
                    .await
                    .unwrap()
            });
            let pending = tokio::time::timeout(Duration::from_secs(3), async {
                loop {
                    if let Some(pending) = gateway.pending().await.into_iter().next() {
                        break pending;
                    }
                    tokio::time::sleep(Duration::from_millis(10)).await;
                }
            })
            .await
            .unwrap();
            assert_eq!(pending.agent_id, agent_id);
            gateway
                .decide(
                    &pending.id,
                    serde_json::from_value(serde_json::json!({"verdict":verdict,"scope":"once"}))
                        .unwrap(),
                )
                .await
                .unwrap();
            let result = call.await.unwrap();
            assert_eq!(result.is_error.unwrap_or(false), verdict == "deny");
            assert_eq!(
                serde_json::to_string(&result)
                    .unwrap()
                    .contains("tool executed"),
                verdict == "allow"
            );
        }
        gateway.shutdown().await;
    }

    #[tokio::test]
    async fn migrated_launch_settings_start_restart_and_do_not_leak_backend_errors() {
        // A small real stdio peer verifies that the child received its original argv/env.
        let script = r#"
import json, os, sys
for line in sys.stdin:
    request = json.loads(line)
    if 'id' not in request:
        continue
    method = request['method']
    if method == 'initialize':
        result = {'protocolVersion': '2025-06-18', 'capabilities': {'tools': {}}, 'serverInfo': {'name': 'fixture', 'version': '1'}}
    elif method == 'tools/list':
        result = {'tools': [{'name': 'verify', 'inputSchema': {'type': 'object'}}]}
    else:
        assert sys.argv[1] == 'argument-secret'
        assert os.environ['CUSTOM_VALUE'] == 'environment-secret'
        if request['params']['name'] == 'leak':
            print(json.dumps({'jsonrpc': '2.0', 'id': request['id'], 'error': {'code': -32603, 'message': 'environment-secret argument-secret'}}), flush=True)
            continue
        result = {'content': [{'type': 'text', 'text': 'credentials verified'}]}
    print(json.dumps({'jsonrpc': '2.0', 'id': request['id'], 'result': result}), flush=True)
"#;
        let store = Arc::new(MemoryStore::default());
        let mut server = ServerConfig {
            id: "fixture".into(),
            name: "fixture".into(),
            command: "python3".into(),
            args: vec![
                "-u".into(),
                "-c".into(),
                script.into(),
                "argument-secret".into(),
            ],
            env: std::collections::BTreeMap::from([(
                "CUSTOM_VALUE".into(),
                "environment-secret".into(),
            )]),
            enabled: true,
            credential_ref: None,
            url: None,
            auth: crate::config::HttpAuth::None,
            headers: Default::default(),
            oauth_ref: None,
        };
        protect_server(store.as_ref(), &mut server).unwrap();
        let (events, _) = crate::events::channel();
        let manager = BackendManager::new(events, store);
        manager.start(server.clone()).await;
        assert_eq!(manager.running_count().await, 1);
        let result = manager
            .call_tool("fixture", "verify", serde_json::json!({}))
            .await
            .unwrap();
        assert!(serde_json::to_string(&result)
            .unwrap()
            .contains("credentials verified"));
        let error = manager
            .call_tool("fixture", "leak", serde_json::json!({}))
            .await
            .unwrap_err()
            .to_string();
        assert!(error.contains("details omitted"));
        assert!(!error.contains("environment-secret"));
        assert!(!error.contains("argument-secret"));
        manager.restart("fixture").await.unwrap();
        assert_eq!(manager.running_count().await, 1);
        let view =
            serde_json::to_string(&ServerView::from_parts(server, BackendStatus::Stopped)).unwrap();
        assert!(!view.contains("argument-secret"));
        assert!(!view.contains("environment-secret"));
        manager.remove("fixture").await;
    }
}
