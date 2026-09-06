//! Remote MCP servers over Streamable HTTP: plain, header-authenticated, or OAuth 2.1.
//!
//! OAuth uses rmcp's client: discovery from the server's 401 challenge (RFC 9728 and
//! RFC 8414), dynamic client registration (RFC 7591) as a public native client, the
//! authorization code flow with PKCE, and refresh. Prism signs in through the system browser
//! and takes the code back on a loopback listener bound for that one sign-in. The registered
//! client id and the tokens live in the OS credential store under the server's `oauth_ref`.

use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

use axum::extract::{Query, State};
use axum::response::{Html, IntoResponse};
use axum::routing::get;
use axum::Router;
use rmcp::transport::auth::{
    AuthClient, AuthError, AuthorizationManager, AuthorizationRequest, CredentialRefreshGuard,
    CredentialStore as TokenStore, OAuthState as RmcpOAuthState, StoredCredentials,
};
use rmcp::transport::streamable_http_client::{
    StreamableHttpClientTransport, StreamableHttpClientTransportConfig,
};
use rmcp::ServiceExt;
use tokio::sync::{oneshot, Mutex};
use tracing::warn;

use crate::config::{HttpAuth, ServerConfig};
use crate::credentials::{self, CredentialStore, LaunchSettings};
use crate::error::{Error, Result};

/// Name registered with the authorization server; it is what the consent page shows.
pub const CLIENT_NAME: &str = "Prism";
/// How long a browser sign-in may stay open before the loopback listener gives up.
pub const SIGN_IN_TIMEOUT: Duration = Duration::from_secs(10 * 60);
const HANDSHAKE_TIMEOUT: Duration = Duration::from_secs(30);

pub(crate) type McpClient = rmcp::service::RunningService<rmcp::RoleClient, ()>;

fn http_client() -> Result<reqwest::Client> {
    // No global timeout: SSE responses stay open for as long as the session lives.
    reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(10))
        .user_agent(format!("Prism/{}", env!("CARGO_PKG_VERSION")))
        .build()
        .map_err(|_| Error::Gateway("could not build an HTTP client".into()))
}

/// The remote endpoint must be https, or plain http on this machine only.
pub(crate) fn validate_url(raw: &str) -> Result<String> {
    let trimmed = raw.trim();
    let url = reqwest::Url::parse(trimmed)
        .map_err(|_| Error::Invalid("server URL is not valid".into()))?;
    let host = url
        .host_str()
        .ok_or_else(|| Error::Invalid("server URL needs a host".into()))?;
    let loopback = matches!(host, "127.0.0.1" | "localhost" | "[::1]" | "::1");
    match url.scheme() {
        "https" => {}
        "http" if loopback => {}
        "http" => {
            return Err(Error::Invalid(
                "plain http is only allowed for servers on this machine; use https".into(),
            ))
        }
        _ => return Err(Error::Invalid("server URL must start with https://".into())),
    }
    Ok(trimmed.to_string())
}

fn transport_config(
    url: &str,
    headers: &std::collections::BTreeMap<String, String>,
) -> Result<StreamableHttpClientTransportConfig> {
    let mut custom = HashMap::new();
    for (name, value) in headers {
        let name = http::HeaderName::from_bytes(name.trim().as_bytes())
            .map_err(|_| Error::Invalid(format!("invalid header name {name:?}")))?;
        let value = http::HeaderValue::from_str(value.trim())
            .map_err(|_| Error::Invalid(format!("invalid value for header {}", name.as_str())))?;
        custom.insert(name, value);
    }
    Ok(
        StreamableHttpClientTransportConfig::with_uri(url.to_string())
            .custom_headers(custom)
            .reinit_on_expired_session(true),
    )
}

fn describe(err: AuthError) -> Error {
    // Never echo the server's text: it may carry URLs with codes or tokens.
    match err {
        AuthError::AuthorizationRequired => Error::SignInRequired,
        AuthError::NoAuthorizationSupport => {
            Error::Gateway("this server does not offer OAuth sign-in".into())
        }
        AuthError::PkceUnsupported => {
            Error::Gateway("this server's sign-in does not support PKCE".into())
        }
        AuthError::RegistrationFailed(_) => {
            Error::Gateway("the server refused to register Prism as a client".into())
        }
        AuthError::MetadataError(_) => {
            Error::Gateway("could not read the server's sign-in settings".into())
        }
        _ => Error::Gateway("sign-in failed".into()),
    }
}

async fn handshake<F>(serve: F) -> Result<McpClient>
where
    F: std::future::Future<
        Output = std::result::Result<McpClient, rmcp::service::ClientInitializeError>,
    >,
{
    tokio::time::timeout(HANDSHAKE_TIMEOUT, serve)
        .await
        .map_err(|_| Error::Backend("server handshake timed out".into()))?
        .map_err(|err| {
            let text = err.to_string();
            if text.contains("401") || text.contains("Unauthorized") || text.contains("auth") {
                Error::SignInRequired
            } else {
                Error::Backend("server handshake failed; check the URL and its sign-in".into())
            }
        })
}

/// Open a session to a remote server. `SignInRequired` means the operator must sign in first.
pub(crate) async fn connect(
    config: &ServerConfig,
    launch: &LaunchSettings,
    store: Arc<dyn CredentialStore>,
) -> Result<McpClient> {
    let url = config
        .url
        .as_deref()
        .ok_or_else(|| Error::Invalid("remote server has no URL".into()))?;
    let transport_config = transport_config(url, &launch.headers)?;
    match config.auth {
        HttpAuth::None | HttpAuth::Header => {
            let transport =
                StreamableHttpClientTransport::with_client(http_client()?, transport_config);
            handshake(().serve(transport)).await
        }
        HttpAuth::Oauth => {
            let manager = authorized_manager(config, store).await?;
            let client = AuthClient::new(http_client()?, manager);
            let transport = StreamableHttpClientTransport::with_client(client, transport_config);
            handshake(().serve(transport)).await
        }
    }
}

/// An authorization manager loaded from stored tokens, or `SignInRequired`.
async fn authorized_manager(
    config: &ServerConfig,
    store: Arc<dyn CredentialStore>,
) -> Result<AuthorizationManager> {
    let url = config.url.as_deref().unwrap_or_default();
    let mut manager = AuthorizationManager::new(url).await.map_err(describe)?;
    manager.with_client(http_client()?).map_err(describe)?;
    manager.set_credential_store(Tokens::new(store, config.oauth_ref.clone())?);
    let ready = tokio::time::timeout(HANDSHAKE_TIMEOUT, manager.initialize_from_store())
        .await
        .map_err(|_| Error::Backend("server handshake timed out".into()))?
        .map_err(describe)?;
    if !ready {
        return Err(Error::SignInRequired);
    }
    Ok(manager)
}

/// rmcp's credential store on top of the OS credential store, one record per server.
struct Tokens {
    store: Arc<dyn CredentialStore>,
    id: String,
    refresh: Arc<Mutex<()>>,
}

impl Tokens {
    fn new(store: Arc<dyn CredentialStore>, id: Option<String>) -> Result<Self> {
        let id =
            id.ok_or_else(|| Error::Invalid("OAuth server has no credential reference".into()))?;
        uuid::Uuid::parse_str(&id)
            .map_err(|_| Error::Invalid("invalid OAuth credential reference".into()))?;
        Ok(Self {
            store,
            id,
            refresh: Arc::new(Mutex::new(())),
        })
    }
}

#[async_trait::async_trait]
impl TokenStore for Tokens {
    async fn load(&self) -> std::result::Result<Option<StoredCredentials>, AuthError> {
        let (store, id) = (self.store.clone(), self.id.clone());
        let bytes = tokio::task::spawn_blocking(move || credentials::get_blob(store.as_ref(), &id))
            .await
            .map_err(|_| AuthError::CredentialStoreError("credential store task failed".into()))?;
        match bytes {
            // A missing record and a locked store look the same to the keyring; both mean
            // "sign in", and a locked store surfaces on the save that follows.
            Err(_) => Ok(None),
            Ok(bytes) => serde_json::from_slice(&bytes).map(Some).map_err(|_| {
                AuthError::CredentialStoreError("stored tokens are unreadable".into())
            }),
        }
    }

    async fn save(&self, creds: StoredCredentials) -> std::result::Result<(), AuthError> {
        let bytes = serde_json::to_vec(&creds)
            .map_err(|_| AuthError::CredentialStoreError("could not encode tokens".into()))?;
        let (store, id) = (self.store.clone(), self.id.clone());
        tokio::task::spawn_blocking(move || credentials::put_blob(store.as_ref(), &id, &bytes))
            .await
            .map_err(|_| AuthError::CredentialStoreError("credential store task failed".into()))?
            .map_err(|_| AuthError::CredentialStoreError("credential store is unavailable".into()))
    }

    async fn clear(&self) -> std::result::Result<(), AuthError> {
        let (store, id) = (self.store.clone(), self.id.clone());
        tokio::task::spawn_blocking(move || credentials::delete_if_present(store.as_ref(), &id))
            .await
            .map_err(|_| AuthError::CredentialStoreError("credential store task failed".into()))?
            .map_err(|_| AuthError::CredentialStoreError("credential store is unavailable".into()))
    }

    async fn acquire_refresh_guard(
        &self,
    ) -> std::result::Result<Option<CredentialRefreshGuard>, AuthError> {
        Ok(Some(CredentialRefreshGuard::new(
            self.refresh.clone().lock_owned().await,
        )))
    }
}

/// Forget a server's registration and tokens.
pub(crate) fn forget_tokens(store: &dyn CredentialStore, config: &ServerConfig) -> Result<()> {
    match &config.oauth_ref {
        Some(id) => credentials::delete_if_present(store, id),
        None => Ok(()),
    }
}

/// A browser sign-in in progress: the URL to open, and the outcome once the browser comes back.
pub(crate) struct SignIn {
    pub url: String,
    pub done: oneshot::Receiver<Result<()>>,
}

#[derive(Clone)]
struct CallbackState {
    tx: Arc<std::sync::Mutex<Option<oneshot::Sender<String>>>>,
    server_name: String,
}

/// The page the browser lands on. Same tokens as the panel; nothing loaded from anywhere.
const CALLBACK_PAGE: &str = include_str!("callback.html");

fn html_escape(raw: &str) -> String {
    let mut out = String::with_capacity(raw.len());
    for ch in raw.chars() {
        match ch {
            '&' => out.push_str("&amp;"),
            '<' => out.push_str("&lt;"),
            '>' => out.push_str("&gt;"),
            '"' => out.push_str("&quot;"),
            '\'' => out.push_str("&#39;"),
            _ => out.push(ch),
        }
    }
    out
}

/// Fill the callback page for one outcome. `lede` may carry the escaped server name in a `<b>`.
fn callback_page(
    state: &str,
    eyebrow_class: &str,
    eyebrow: &str,
    title: &str,
    lede: &str,
) -> String {
    CALLBACK_PAGE
        .replace("{{state}}", state)
        .replace("{{eyebrow_class}}", eyebrow_class)
        .replace("{{eyebrow}}", eyebrow)
        .replace("{{title}}", title)
        .replace("{{lede}}", lede)
}

async fn callback(
    State(state): State<CallbackState>,
    Query(params): Query<HashMap<String, String>>,
) -> axum::response::Response {
    let query = params
        .iter()
        .map(|(k, v)| format!("{}={}", urlencode(k), urlencode(v)))
        .collect::<Vec<_>>()
        .join("&");
    let delivered = state
        .tx
        .lock()
        .ok()
        .and_then(|mut slot| slot.take())
        .map(|tx| tx.send(query).is_ok())
        .unwrap_or(false);
    let name = html_escape(&state.server_name);
    let page = if params.contains_key("error") {
        callback_page(
            "refused",
            "warn",
            "Not connected",
            "Sign-in refused.",
            &format!(
                "<b>{name}</b> did not grant access, so Prism has nothing stored. The server stays as \
                 <b>needs sign-in</b> until you try again from the panel."
            ),
        )
    } else if delivered {
        callback_page(
            "ok",
            "",
            "Connected",
            "Signed in.",
            &format!(
                "Prism has what it needs from <b>{name}</b>. Its tools appear in the Servers tab in a \
                 moment, available to your agents through the rules you set."
            ),
        )
    } else {
        callback_page(
            "done",
            "quiet",
            "Already finished",
            "This sign-in already finished.",
            "Prism took the code the first time this page opened. Check the Servers tab for the result.",
        )
    };
    Html(page).into_response()
}

fn urlencode(raw: &str) -> String {
    let mut out = String::with_capacity(raw.len());
    for byte in raw.bytes() {
        match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(byte as char)
            }
            _ => out.push_str(&format!("%{byte:02X}")),
        }
    }
    out
}

/// Start a browser sign-in for an OAuth server. Returns the URL to open; the receiver
/// resolves once the browser has come back and the tokens are stored, or on failure.
pub(crate) async fn begin_sign_in(
    config: &ServerConfig,
    store: Arc<dyn CredentialStore>,
) -> Result<SignIn> {
    let url = config
        .url
        .clone()
        .ok_or_else(|| Error::Invalid("remote server has no URL".into()))?;
    if config.auth != HttpAuth::Oauth {
        return Err(Error::Invalid("this server does not use OAuth".into()));
    }
    let tokens = Tokens::new(store, config.oauth_ref.clone())?;

    let listener = tokio::net::TcpListener::bind(("127.0.0.1", 0)).await?;
    let port = listener.local_addr()?.port();
    let redirect_uri = format!("http://127.0.0.1:{port}/callback");

    let mut state = RmcpOAuthState::new(url.as_str(), Some(http_client()?))
        .await
        .map_err(describe)?;
    if let RmcpOAuthState::Unauthorized(manager) = &mut state {
        manager.set_credential_store(tokens);
    }
    let request = AuthorizationRequest::new(redirect_uri.clone()).with_client_name(CLIENT_NAME);
    tokio::time::timeout(HANDSHAKE_TIMEOUT, state.start_authorization(request))
        .await
        .map_err(|_| Error::Backend("the server's sign-in settings took too long to load".into()))?
        .map_err(describe)?;
    let auth_url = state.get_authorization_url().await.map_err(describe)?;

    let (query_tx, query_rx) = oneshot::channel::<String>();
    let (done_tx, done_rx) = oneshot::channel::<Result<()>>();
    let app = Router::new()
        .route("/callback", get(callback))
        .with_state(CallbackState {
            tx: Arc::new(std::sync::Mutex::new(Some(query_tx))),
            server_name: config.name.clone(),
        });
    let server_name = config.name.clone();
    tokio::spawn(async move {
        let (stop_tx, stop_rx) = oneshot::channel::<()>();
        let serve = tokio::spawn(async move {
            let _ = axum::serve(listener, app)
                .with_graceful_shutdown(async {
                    let _ = stop_rx.await;
                })
                .await;
        });
        let outcome = match tokio::time::timeout(SIGN_IN_TIMEOUT, query_rx).await {
            Ok(Ok(query)) => {
                let callback_url = format!("{redirect_uri}?{query}");
                match state.handle_callback_url(&callback_url).await {
                    Ok(()) => Ok(()),
                    Err(err) => {
                        warn!(server = %server_name, "OAuth sign-in failed: {}", kind(&err));
                        Err(describe(err))
                    }
                }
            }
            Ok(Err(_)) => Err(Error::Gateway("sign-in was cancelled".into())),
            Err(_) => Err(Error::Gateway(
                "sign-in timed out; the browser never came back".into(),
            )),
        };
        // Let the browser receive its page before the listener goes away.
        tokio::time::sleep(Duration::from_millis(300)).await;
        let _ = stop_tx.send(());
        let _ = serve.await;
        let _ = done_tx.send(outcome);
    });

    Ok(SignIn {
        url: auth_url,
        done: done_rx,
    })
}

fn kind(err: &AuthError) -> &'static str {
    match err {
        AuthError::AuthorizationRequired => "authorization required",
        AuthError::NoAuthorizationSupport => "no authorization support",
        AuthError::PkceUnsupported => "pkce unsupported",
        AuthError::RegistrationFailed(_) => "registration failed",
        AuthError::MetadataError(_) => "metadata error",
        AuthError::CredentialStoreError(_) => "credential store error",
        _ => "other",
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn urls_must_be_https_unless_local() {
        assert!(validate_url("https://mcp.example.com/mcp").is_ok());
        assert!(validate_url("http://127.0.0.1:8080/mcp").is_ok());
        assert!(validate_url("http://localhost:8080/mcp").is_ok());
        assert!(validate_url("http://mcp.example.com/mcp").is_err());
        assert!(validate_url("ftp://mcp.example.com/mcp").is_err());
        assert!(validate_url("not a url").is_err());
    }

    #[test]
    fn header_names_and_values_are_checked() {
        let mut headers = std::collections::BTreeMap::new();
        headers.insert("Authorization".to_string(), "Bearer abc".to_string());
        assert!(transport_config("https://x.example/mcp", &headers).is_ok());
        headers.insert("bad header".to_string(), "x".to_string());
        assert!(transport_config("https://x.example/mcp", &headers).is_err());
    }

    #[test]
    fn callback_page_escapes_the_server_name_and_marks_the_outcome() {
        let name = html_escape("acme <b>&</b> \"co\"");
        let page = callback_page(
            "ok",
            "",
            "Connected",
            "Signed in.",
            &format!("from <b>{name}</b>"),
        );
        assert!(page.contains("data-state=\"ok\""));
        assert!(page.contains("<h1>Signed in.</h1>"));
        assert!(page.contains("from <b>acme &lt;b&gt;&amp;&lt;/b&gt; &quot;co&quot;</b>"));
        assert!(!page.contains("{{"), "every placeholder is filled");
        assert!(
            !page.contains("http"),
            "the page loads nothing from anywhere"
        );
    }

    #[test]
    fn callback_query_is_reencoded() {
        assert_eq!(urlencode("a b/c"), "a%20b%2Fc");
        assert_eq!(urlencode("safe-_.~"), "safe-_.~");
    }

    use crate::backend::{BackendManager, BackendStatus};
    use crate::credentials::tests::MemoryStore;
    use crate::gateway::Gateway;
    use rmcp::handler::server::ServerHandler;
    use rmcp::model::{
        ListToolsResult, PaginatedRequestParams, ServerCapabilities, ServerInfo, Tool,
    };
    use rmcp::service::RequestContext;
    use rmcp::transport::streamable_http_server::session::local::LocalSessionManager;
    use rmcp::transport::streamable_http_server::tower::StreamableHttpService;
    use rmcp::transport::StreamableHttpServerConfig;
    use rmcp::{ErrorData as McpError, RoleServer};
    use std::collections::BTreeMap;

    const TEST_KEY: &str = "Bearer test-key";

    /// A one-tool server behind a fixed API key.
    #[derive(Clone)]
    struct Pinger;

    impl ServerHandler for Pinger {
        fn get_info(&self) -> ServerInfo {
            ServerInfo::new(ServerCapabilities::builder().enable_tools().build())
        }

        async fn list_tools(
            &self,
            _request: Option<PaginatedRequestParams>,
            _context: RequestContext<RoleServer>,
        ) -> std::result::Result<ListToolsResult, McpError> {
            #[allow(clippy::field_reassign_with_default)]
            {
                let mut result = ListToolsResult::default();
                result.tools = vec![Tool::new("ping", "pong", serde_json::Map::new())];
                Ok(result)
            }
        }
    }

    async fn require_key(
        request: axum::extract::Request,
        next: axum::middleware::Next,
    ) -> axum::response::Response {
        let ok = request
            .headers()
            .get(http::header::AUTHORIZATION)
            .and_then(|v| v.to_str().ok())
            == Some(TEST_KEY);
        if ok {
            next.run(request).await
        } else {
            (
                http::StatusCode::UNAUTHORIZED,
                [(http::header::WWW_AUTHENTICATE, "Bearer")],
                "no",
            )
                .into_response()
        }
    }

    async fn pinger_on_loopback() -> u16 {
        let service: StreamableHttpService<Pinger, LocalSessionManager> =
            StreamableHttpService::new(
                || Ok(Pinger),
                LocalSessionManager::default().into(),
                StreamableHttpServerConfig::default().disable_allowed_hosts(),
            );
        let app = Router::new()
            .nest_service("/mcp", service)
            .layer(axum::middleware::from_fn(require_key));
        let listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
            .await
            .unwrap();
        let port = listener.local_addr().unwrap().port();
        tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        });
        port
    }

    fn remote(
        name: &str,
        url: String,
        auth: HttpAuth,
        headers: BTreeMap<String, String>,
    ) -> ServerConfig {
        ServerConfig {
            id: name.to_string(),
            name: name.to_string(),
            command: String::new(),
            args: Vec::new(),
            env: BTreeMap::new(),
            credential_ref: None,
            enabled: true,
            url: Some(url),
            auth,
            headers,
            oauth_ref: None,
        }
    }

    async fn status_of(manager: &BackendManager, id: &str) -> BackendStatus {
        manager
            .snapshot()
            .await
            .into_iter()
            .find(|(c, _)| c.id == id)
            .map(|(_, s)| s)
            .unwrap()
    }

    #[tokio::test]
    async fn header_auth_reaches_a_remote_server_and_the_key_stays_in_the_store() {
        let port = pinger_on_loopback().await;
        let store = Arc::new(MemoryStore::default());
        let (events, _rx) = crate::events::channel();
        let manager = BackendManager::new(events, store.clone());
        let url = format!("http://127.0.0.1:{port}/mcp");

        let mut good = remote(
            "good",
            url.clone(),
            HttpAuth::Header,
            BTreeMap::from([("Authorization".to_string(), TEST_KEY.to_string())]),
        );
        credentials::protect_server(store.as_ref(), &mut good).unwrap();
        assert!(
            good.headers.is_empty(),
            "the key must not stay in the config"
        );
        assert!(good.credential_ref.is_some());
        manager.start(good.clone()).await;
        assert_eq!(
            status_of(&manager, "good").await,
            BackendStatus::Running { tool_count: 1 }
        );
        let tools = manager.list_tools(false).await;
        assert_eq!(tools.len(), 1);
        assert_eq!(tools[0].1.name.as_ref(), "ping");

        let mut bad = remote(
            "bad",
            url.clone(),
            HttpAuth::Header,
            BTreeMap::from([("Authorization".to_string(), "Bearer wrong".to_string())]),
        );
        credentials::protect_server(store.as_ref(), &mut bad).unwrap();
        manager.start(bad).await;
        assert!(
            matches!(
                status_of(&manager, "bad").await,
                BackendStatus::Failed { .. } | BackendStatus::SignInRequired
            ),
            "a refused key must not show as running"
        );

        let none = remote("none", url, HttpAuth::None, BTreeMap::new());
        manager.start(none).await;
        assert!(!matches!(
            status_of(&manager, "none").await,
            BackendStatus::Running { .. }
        ));
    }

    async fn gateway_on_loopback() -> (Arc<Gateway>, u16, tempfile::TempDir) {
        let dir = tempfile::tempdir().unwrap();
        let port = std::net::TcpListener::bind("127.0.0.1:0")
            .unwrap()
            .local_addr()
            .unwrap()
            .port();
        let config = crate::config::PrismConfig {
            listen_port: port,
            ..Default::default()
        };
        let path = dir.path().join("prism.json");
        config.save(&path).unwrap();
        let gateway = Gateway::start_with_credentials(
            path,
            dir.path().join("audit.jsonl"),
            Arc::new(MemoryStore::default()),
        )
        .await
        .unwrap();
        for _ in 0..50 {
            if tokio::net::TcpStream::connect(("127.0.0.1", port))
                .await
                .is_ok()
            {
                break;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        (gateway, port, dir)
    }

    async fn wait_for<F: Fn(&crate::backend::BackendStatus) -> bool>(
        gateway: &Gateway,
        id: &str,
        what: &str,
        pred: F,
    ) -> BackendStatus {
        for _ in 0..200 {
            let status = gateway
                .servers()
                .await
                .into_iter()
                .find(|s| s.id == id)
                .map(|s| s.status)
                .unwrap();
            if pred(&status) {
                return status;
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
        panic!("server never reached {what}");
    }

    /// The whole OAuth path against Prism's own authorization server: discovery from the
    /// 401 challenge, dynamic registration, the browser hop with consent in the upstream
    /// panel, the loopback callback, the token exchange, and a live session afterwards.
    #[tokio::test]
    async fn oauth_sign_in_against_another_prism() {
        let (upstream, upstream_port, _up_dir) = gateway_on_loopback().await;
        let (gateway, _port, _dir) = gateway_on_loopback().await;

        let added = gateway
            .add_server(remote(
                "upstream",
                format!("http://127.0.0.1:{upstream_port}/mcp"),
                HttpAuth::Oauth,
                BTreeMap::new(),
            ))
            .await
            .unwrap();
        assert!(added.oauth_ref.is_some());
        wait_for(&gateway, &added.id, "sign-in required", |s| {
            matches!(s, BackendStatus::SignInRequired)
        })
        .await;

        let auth_url = gateway.sign_in_server(&added.id).await.unwrap();
        assert!(
            auth_url.starts_with(&format!("http://127.0.0.1:{upstream_port}/authorize")),
            "{auth_url}"
        );

        // The browser: parks on /authorize until the upstream operator approves "Prism".
        let browser = reqwest::Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .unwrap();
        let parked = tokio::spawn({
            let browser = browser.clone();
            async move { browser.get(auth_url).send().await.unwrap() }
        });
        let mut pending = None;
        for _ in 0..100 {
            pending = upstream.agents().await.into_iter().find(|a| {
                a.agent.name == CLIENT_NAME && a.agent.status == crate::config::AgentStatus::Pending
            });
            if pending.is_some() {
                break;
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
        let pending = pending.expect("upstream saw Prism asking to connect");
        upstream
            .decide_agent(&pending.agent.id, true)
            .await
            .unwrap();

        let redirect = tokio::time::timeout(Duration::from_secs(10), parked)
            .await
            .unwrap()
            .unwrap();
        assert_eq!(redirect.status(), 303);
        let location = redirect.headers()["location"].to_str().unwrap().to_string();
        assert!(location.starts_with("http://127.0.0.1:"), "{location}");
        assert!(location.contains("/callback?"), "{location}");
        let landed = browser.get(&location).send().await.unwrap();
        assert_eq!(landed.status(), 200);
        let landed = landed.text().await.unwrap();
        assert!(landed.contains("Signed in"));
        assert!(landed.contains("data-state=\"ok\""));
        assert!(landed.contains("<b>upstream</b>"), "{landed}");

        let status = wait_for(&gateway, &added.id, "running", |s| {
            matches!(s, BackendStatus::Running { .. })
        })
        .await;
        assert_eq!(status, BackendStatus::Running { tool_count: 0 });

        // Signing out forgets the tokens; the server asks again.
        gateway.sign_out_server(&added.id).await.unwrap();
        wait_for(&gateway, &added.id, "sign-in required again", |s| {
            matches!(s, BackendStatus::SignInRequired)
        })
        .await;
    }
}
