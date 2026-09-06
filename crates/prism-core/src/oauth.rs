//! OAuth 2.1 on the loopback listener, so a route is never open just because it is local.
//!
//! Prism is both the resource server (`/mcp`) and the authorization server. Clients register
//! themselves (RFC 7591), which grants nothing; the operator's approval in the panel is the
//! consent step. A denied agent gets an `access_denied` redirect and no token, ever. Tokens
//! are opaque, stored hashed, short-lived for access and rotating for refresh.

use std::collections::{HashMap, HashSet, VecDeque};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use axum::extract::{DefaultBodyLimit, Form, Query, Request, State};
use axum::http::{header, HeaderMap, StatusCode};
use axum::middleware::Next;
use axum::response::{Html, IntoResponse, Redirect, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use base64::Engine;
use chrono::{DateTime, Utc};
use rand::RngCore;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tokio::sync::oneshot;
use tracing::{info, warn};

use crate::config::{AgentConfig, AgentStatus, OAuthClient, TokenKind, TokenRecord};
use crate::error::{Error, Result};
use crate::events::GatewayEvent;
use crate::gateway::Gateway;

const ACCESS_TTL_SECS: i64 = 60 * 60;
const REFRESH_TTL_SECS: i64 = 30 * 24 * 60 * 60;
const CODE_TTL_SECS: i64 = 5 * 60;
/// How long an authorization may wait for the operator before it gives up.
const AUTHORIZE_WAIT: Duration = Duration::from_secs(10 * 60);
const SCOPE: &str = "mcp";
const BROWSER_RESULT_TTL: Duration = Duration::from_secs(60);
const MAX_BROWSER_FLOWS: usize = 32;

/// Set on `/mcp` requests by the bearer check. The proxy reads it back through the request
/// parts rmcp attaches to every MCP message.
#[derive(Debug, Clone)]
pub struct AuthenticatedAgent {
    pub agent_id: String,
    /// Used to re-check authority while a stateless notification stream is open.
    pub(crate) token_hash: String,
}

/// A token as the panel sees it: what kind, when it was minted, when it dies. Never the value.
#[derive(Debug, Clone, Serialize)]
pub struct TokenView {
    pub kind: TokenKind,
    pub created_at: DateTime<Utc>,
    pub expires_at: Option<DateTime<Utc>>,
}

/// Returned only when a manual token is created or replaced. Never logged or listed.
#[derive(Serialize)]
pub struct ManualToken {
    pub agent_id: String,
    pub token: String,
}

fn install_manual_token(config: &mut crate::config::PrismConfig, agent_id: &str) -> ManualToken {
    let token = format!("prism_{}", random_token());
    config.tokens.retain(|t| t.agent_id != agent_id);
    config.tokens.push(TokenRecord {
        hash: hash_token(&token),
        kind: TokenKind::Manual,
        agent_id: agent_id.to_string(),
        client_id: None,
        created_at: Utc::now(),
        expires_at: None,
    });
    ManualToken {
        agent_id: agent_id.to_string(),
        token,
    }
}

struct AuthCode {
    client_id: String,
    agent_id: String,
    redirect_uri: String,
    code_challenge: String,
    expires_at: DateTime<Utc>,
}

/// A sign-in waiting on the operator. For a new agent the agent card is the consent; for an
/// agent that is already approved this is its own card, because a public client id proves
/// nothing about who is asking.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PendingSignIn {
    pub id: String,
    pub agent_id: String,
    pub agent_name: String,
    pub client_name: String,
    #[serde(default)]
    pub client_id: String,
    pub requested_at: DateTime<Utc>,
    /// True when the agent was already approved, so this needs its own answer.
    pub needs_consent: bool,
    /// True when this client has never held a token: a harness connecting from a new scope or
    /// install rather than the same one signing in again.
    #[serde(default)]
    pub new_client: bool,
}

struct SignInEntry {
    view: PendingSignIn,
    tx: oneshot::Sender<bool>,
}

struct AuthorizationWait {
    signin: PendingSignIn,
    redirect_uri: String,
    code_challenge: String,
    state: Option<String>,
    rx: oneshot::Receiver<bool>,
}

#[derive(Default)]
struct BrowserFlow {
    outcome: Option<AuthorizeOutcome>,
    polls: RateWindow,
}

/// In-memory half of the authorization server: unredeemed codes, parked browsers, and which
/// identity opened each MCP session.
#[derive(Default)]
pub(crate) struct OAuthState {
    codes: Mutex<HashMap<String, AuthCode>>,
    signins: Mutex<HashMap<String, SignInEntry>>,
    browsers: Mutex<HashMap<String, BrowserFlow>>,
    /// A request on a session must carry the identity that created it.
    session_owners: Mutex<HashMap<String, String>>,
    rates: Mutex<RateLimits>,
}

const MAX_TRACKED_SESSIONS: usize = 4096;
const MAX_UNUSED_CLIENTS: usize = 64;
const MAX_PENDING_SIGNINS: usize = 16;
const UNUSED_CLIENT_HOURS: i64 = 24;

#[derive(Default)]
struct RateWindow(VecDeque<Instant>);

impl RateWindow {
    fn take(&mut self, now: Instant, limit: usize) -> Option<u64> {
        let window = Duration::from_secs(60);
        while self
            .0
            .front()
            .is_some_and(|at| now.duration_since(*at) >= window)
        {
            self.0.pop_front();
        }
        if self.0.len() >= limit {
            let remaining = window.saturating_sub(now.duration_since(*self.0.front().unwrap()));
            return Some(remaining.as_secs() + u64::from(remaining.subsec_nanos() > 0));
        }
        self.0.push_back(now);
        None
    }
}

#[derive(Default)]
struct RateLimits {
    register: RateWindow,
    authorize: RateWindow,
    token: RateWindow,
    revoke: RateWindow,
    status: RateWindow,
}

fn decided_clients(config: &crate::config::PrismConfig) -> HashSet<String> {
    config
        .clients
        .iter()
        .filter(|client| {
            config
                .client_agent_id(&client.client_id)
                .and_then(|id| config.agents.iter().find(|a| a.id == id))
                .is_some_and(|agent| agent.status != AgentStatus::Pending)
        })
        .map(|client| client.client_id.clone())
        .collect()
}

/// Expire abandoned registrations and their pending agent records, preserving decisions.
pub(crate) fn prune_unused_clients(config: &mut crate::config::PrismConfig, now: DateTime<Utc>) {
    let decided = decided_clients(config);
    let cutoff = now - chrono::Duration::hours(UNUSED_CLIENT_HOURS);
    let removed: HashSet<String> = config
        .clients
        .iter()
        .filter(|client| client.created_at <= cutoff && !decided.contains(&client.client_id))
        .map(|client| client.client_id.clone())
        .collect();
    config
        .clients
        .retain(|client| !removed.contains(&client.client_id));
    // An agent goes with its clients only when every client it had is gone and it is not a
    // harness record, which outlives any one registration.
    let removed_agents: HashSet<String> = config
        .agents
        .iter()
        .filter(|agent| agent.host.is_none())
        .filter(|agent| {
            let ids = config.agent_client_ids(&agent.id);
            !ids.is_empty() && ids.iter().all(|id| removed.contains(id))
        })
        .map(|agent| agent.id.clone())
        .collect();
    config
        .agents
        .retain(|agent| !removed_agents.contains(&agent.id));
    config.rules.retain(|rule| {
        !rule
            .agent_id
            .as_ref()
            .is_some_and(|id| removed_agents.contains(id))
    });
    config.tokens.retain(|token| {
        !token
            .client_id
            .as_ref()
            .is_some_and(|id| removed.contains(id))
    });
}

// ---------------------------------------------------------------------------------------------
// Wire shapes

/// RFC 7591 registration request. Only the fields Prism acts on; the rest is ignored.
#[derive(Debug, Clone, Deserialize)]
pub struct RegisterRequest {
    #[serde(default)]
    pub client_name: Option<String>,
    #[serde(default)]
    pub redirect_uris: Vec<String>,
    #[serde(default)]
    pub token_endpoint_auth_method: Option<String>,
    #[serde(default)]
    pub grant_types: Vec<String>,
}

#[derive(Debug, Clone, Serialize)]
pub struct RegisterResponse {
    pub client_id: String,
    pub client_id_issued_at: i64,
    pub client_name: String,
    pub redirect_uris: Vec<String>,
    pub grant_types: Vec<String>,
    pub response_types: Vec<String>,
    pub token_endpoint_auth_method: String,
}

#[derive(Debug, Clone, Deserialize)]
pub struct AuthorizeParams {
    #[serde(default)]
    pub response_type: Option<String>,
    #[serde(default)]
    pub client_id: Option<String>,
    #[serde(default)]
    pub redirect_uri: Option<String>,
    #[serde(default)]
    pub code_challenge: Option<String>,
    #[serde(default)]
    pub code_challenge_method: Option<String>,
    #[serde(default)]
    pub state: Option<String>,
    #[serde(default)]
    pub scope: Option<String>,
    #[serde(default)]
    pub resource: Option<String>,
}

/// Where `/authorize` ends up. Anything wrong with the client or redirect URI is fatal and
/// rendered in place; everything else goes back to the client as an OAuth error redirect.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum AuthorizeOutcome {
    Redirect(String),
    Invalid(String),
}

#[derive(Debug, Clone, Deserialize)]
pub struct TokenRequest {
    #[serde(default)]
    pub grant_type: Option<String>,
    #[serde(default)]
    pub code: Option<String>,
    #[serde(default)]
    pub code_verifier: Option<String>,
    #[serde(default)]
    pub redirect_uri: Option<String>,
    #[serde(default)]
    pub client_id: Option<String>,
    #[serde(default)]
    pub refresh_token: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
pub struct TokenResponse {
    pub access_token: String,
    pub token_type: &'static str,
    pub expires_in: i64,
    pub refresh_token: String,
    pub scope: &'static str,
}

/// RFC 6749 §5.2 error body.
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct OAuthError {
    pub error: &'static str,
    pub error_description: String,
}

impl OAuthError {
    fn new(error: &'static str, description: impl Into<String>) -> Self {
        Self {
            error,
            error_description: description.into(),
        }
    }
}

#[derive(Debug, Clone, Deserialize)]
pub struct RevokeRequest {
    #[serde(default)]
    pub token: Option<String>,
}

// ---------------------------------------------------------------------------------------------
// Primitives

fn random_token() -> String {
    let mut bytes = [0u8; 32];
    rand::rng().fill_bytes(&mut bytes);
    base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(bytes)
}

/// SHA-256 as lowercase hex. Tokens are random and high-entropy, so a plain hash is enough.
pub fn hash_token(token: &str) -> String {
    let digest = Sha256::digest(token.as_bytes());
    digest.iter().map(|b| format!("{b:02x}")).collect()
}

/// PKCE S256: `BASE64URL(SHA256(verifier)) == challenge`.
pub fn pkce_matches(verifier: &str, challenge: &str) -> bool {
    if !(43..=128).contains(&verifier.len()) {
        return false;
    }
    let digest = Sha256::digest(verifier.as_bytes());
    let computed = base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(digest);
    constant_time_eq(
        computed.as_bytes(),
        challenge.trim_end_matches('=').as_bytes(),
    )
}

pub(crate) fn constant_time_eq(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    a.iter().zip(b).fold(0u8, |acc, (x, y)| acc | (x ^ y)) == 0
}

/// Native-app redirect targets: loopback over http, https, or a private-use scheme.
/// Plain http to any other host would hand the code to the network.
pub fn redirect_uri_allowed(uri: &str) -> bool {
    let Some((scheme, rest)) = uri.split_once("://") else {
        return false;
    };
    if scheme.is_empty() || rest.is_empty() || uri.contains('#') {
        return false;
    }
    match scheme {
        "https" => true,
        "http" => {
            let authority = rest.split(['/', '?']).next().unwrap_or("");
            let host = authority.rsplit_once(':').map_or(authority, |(h, port)| {
                if port.chars().all(|c| c.is_ascii_digit()) {
                    h
                } else {
                    authority
                }
            });
            matches!(host, "127.0.0.1" | "localhost" | "[::1]")
        }
        _ => !scheme.eq_ignore_ascii_case("javascript") && !scheme.eq_ignore_ascii_case("data"),
    }
}

fn urlencode(value: &str) -> String {
    let mut out = String::with_capacity(value.len());
    for byte in value.bytes() {
        match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(byte as char)
            }
            _ => out.push_str(&format!("%{byte:02X}")),
        }
    }
    out
}

fn with_query(uri: &str, params: &[(&str, &str)]) -> String {
    let mut out = String::from(uri);
    let mut sep = if uri.contains('?') { '&' } else { '?' };
    for (k, v) in params {
        out.push(sep);
        out.push_str(k);
        out.push('=');
        out.push_str(&urlencode(v));
        sep = '&';
    }
    out
}

/// `issuer` is `http://127.0.0.1:PORT` with no trailing slash. Accepts that origin
/// (with or without a slash) and the `/mcp` endpoint, which is what clients dial.
fn resource_matches(issuer: &str, resource: &str) -> bool {
    let got = resource.trim_end_matches('/');
    got == issuer || got == format!("{issuer}/mcp")
}

fn error_redirect(
    redirect_uri: &str,
    error: &str,
    description: &str,
    state: Option<&str>,
) -> String {
    let mut params = vec![("error", error), ("error_description", description)];
    if let Some(s) = state {
        params.push(("state", s));
    }
    with_query(redirect_uri, &params)
}

// ---------------------------------------------------------------------------------------------
// Gateway operations

impl Gateway {
    fn issuer(&self) -> String {
        format!("http://127.0.0.1:{}", self.listen_port)
    }

    /// RFC 8707 resource indicator: the origin, with a trailing slash.
    ///
    /// Claude Code's MCP SDK checks `checkResourceAllowed({ requested: serverUrl,
    /// configured: metadata.resource })` — the URL the client dialed must be a
    /// path under the advertised resource. Advertising `/mcp` fails when the
    /// client treats the server as `http://127.0.0.1:PORT/` (the origin). The
    /// origin accepts both that and `…/mcp`.
    fn resource(&self) -> String {
        format!("{}/", self.issuer())
    }

    fn accepts_resource(&self, resource: &str) -> bool {
        resource_matches(&self.issuer(), resource)
    }

    pub fn protected_resource_metadata(&self) -> serde_json::Value {
        serde_json::json!({
            "resource": self.resource(),
            "authorization_servers": [self.issuer()],
            "bearer_methods_supported": ["header"],
            "scopes_supported": [SCOPE],
            "resource_name": "Prism",
        })
    }

    pub fn authorization_server_metadata(&self) -> serde_json::Value {
        let issuer = self.issuer();
        serde_json::json!({
            "issuer": issuer,
            "authorization_endpoint": format!("{issuer}/authorize"),
            "token_endpoint": format!("{issuer}/token"),
            "registration_endpoint": format!("{issuer}/register"),
            "revocation_endpoint": format!("{issuer}/revoke"),
            "response_types_supported": ["code"],
            "response_modes_supported": ["query"],
            "grant_types_supported": ["authorization_code", "refresh_token"],
            "code_challenge_methods_supported": ["S256"],
            "token_endpoint_auth_methods_supported": ["none"],
            "revocation_endpoint_auth_methods_supported": ["none"],
            "scopes_supported": [SCOPE],
        })
    }

    /// Panel-only provisioning: one token, using the agent's ordinary tool permissions.
    pub async fn create_manual_agent(&self, name: &str) -> Result<ManualToken> {
        let name = name.trim();
        if name.is_empty() || name.chars().count() > 80 {
            return Err(Error::Invalid(
                "choose a client name between 1 and 80 characters".into(),
            ));
        }
        let now = Utc::now();
        let agent = AgentConfig {
            id: uuid::Uuid::new_v4().to_string(),
            name: name.to_string(),
            client_name: name.to_string(),
            client_version: None,
            client_id: None,
            host: None,
            status: AgentStatus::Approved,
            created_at: now,
            decided_at: Some(now),
            posture: Default::default(),
            attention: Default::default(),
        };
        let mut config = self.config.write().await;
        let mut updated = config.clone();
        updated.agents.push(agent.clone());
        let issued = install_manual_token(&mut updated, &agent.id);
        updated.save(&self.config_path)?;
        *config = updated;
        drop(config);
        let _ = self
            .events
            .send(GatewayEvent::AgentUpdated { agent_id: agent.id });
        Ok(issued)
    }

    /// Also provisions existing legacy agents. Their rules and identity remain unchanged.
    pub async fn replace_manual_token(&self, agent_id: &str) -> Result<ManualToken> {
        let mut config = self.config.write().await;
        let agent = config
            .agents
            .iter()
            .find(|a| a.id == agent_id)
            .ok_or_else(|| Error::NotFound(format!("agent {agent_id}")))?;
        if !config.agent_client_ids(agent_id).is_empty() || agent.status != AgentStatus::Approved {
            return Err(Error::Invalid(
                "tokens can only be created for approved manual clients".into(),
            ));
        }
        let mut updated = config.clone();
        let issued = install_manual_token(&mut updated, agent_id);
        updated.save(&self.config_path)?;
        *config = updated;
        drop(config);
        let _ = self.events.send(GatewayEvent::AgentUpdated {
            agent_id: agent_id.to_string(),
        });
        Ok(issued)
    }

    /// Open dynamic registration. Anyone on the machine can register; nobody gets a tool
    /// until the operator approves the agent that signs in with the client.
    pub async fn register_client(&self, req: RegisterRequest) -> Result<OAuthClient> {
        if req.redirect_uris.is_empty() {
            return Err(Error::Invalid("redirect_uris is required".into()));
        }
        if req.redirect_uris.len() > 8 || req.redirect_uris.iter().any(|uri| uri.len() > 2048) {
            return Err(Error::Invalid(
                "redirect_uris allows at most 8 URIs of 2048 bytes each".into(),
            ));
        }
        if let Some(bad) = req
            .redirect_uris
            .iter()
            .find(|uri| !redirect_uri_allowed(uri))
        {
            return Err(Error::Invalid(format!(
                "redirect_uri {bad} must be loopback http, https, or a private-use scheme"
            )));
        }
        if let Some(method) = req.token_endpoint_auth_method.as_deref() {
            if method != "none" {
                return Err(Error::Invalid(
                    "only public clients (token_endpoint_auth_method=none) are supported".into(),
                ));
            }
        }
        if let Some(bad) = req
            .grant_types
            .iter()
            .find(|g| !matches!(g.as_str(), "authorization_code" | "refresh_token"))
        {
            return Err(Error::Invalid(format!("unsupported grant_type {bad}")));
        }
        let name = req
            .client_name
            .as_deref()
            .map(str::trim)
            .filter(|n| !n.is_empty())
            .unwrap_or("unknown")
            .chars()
            .take(80)
            .collect::<String>();
        let client = OAuthClient {
            client_id: uuid::Uuid::new_v4().to_string(),
            client_name: name,
            redirect_uris: req.redirect_uris,
            created_at: Utc::now(),
            agent_id: None,
            origin: None,
        };
        let mut config = self.config.write().await;
        let mut updated = config.clone();
        prune_unused_clients(&mut updated, Utc::now());
        let decided = decided_clients(&updated);
        if updated
            .clients
            .iter()
            .filter(|client| !decided.contains(&client.client_id))
            .count()
            >= MAX_UNUSED_CLIENTS
        {
            return Err(Error::RateLimited("too many unused client registrations; finish an existing sign-in or retry after expiry"));
        }
        updated.clients.push(client.clone());
        updated.save(&self.config_path)?;
        *config = updated;
        info!(client = %client.client_name, "oauth client registered");
        Ok(client)
    }

    /// The authorization step. Approval in the panel is the consent screen; a browser that
    /// arrives while the agent is still pending waits here for the answer.
    pub async fn authorize(&self, params: AuthorizeParams) -> AuthorizeOutcome {
        match self.start_authorization(params).await {
            Ok(wait) => self.finish_authorization(wait, AUTHORIZE_WAIT).await,
            Err(outcome) => outcome,
        }
    }

    async fn start_authorization(
        &self,
        params: AuthorizeParams,
    ) -> std::result::Result<AuthorizationWait, AuthorizeOutcome> {
        let Some(client_id) = params.client_id.as_deref().filter(|c| !c.is_empty()) else {
            return Err(AuthorizeOutcome::Invalid("client_id is required".into()));
        };
        let client = match self
            .config
            .read()
            .await
            .clients
            .iter()
            .find(|c| c.client_id == client_id)
            .cloned()
        {
            Some(c) => c,
            None => {
                return Err(AuthorizeOutcome::Invalid(
                    "unknown client_id; register first at /register".into(),
                ))
            }
        };
        let redirect_uri = match params.redirect_uri.as_deref() {
            Some(uri) if client.redirect_uris.iter().any(|r| r == uri) => uri.to_string(),
            Some(_) => {
                return Err(AuthorizeOutcome::Invalid(
                    "redirect_uri does not match the registered client".into(),
                ))
            }
            None if client.redirect_uris.len() == 1 => client.redirect_uris[0].clone(),
            None => return Err(AuthorizeOutcome::Invalid("redirect_uri is required".into())),
        };
        let state = params.state.as_deref();
        let fail = |error: &str, description: &str| {
            Err(AuthorizeOutcome::Redirect(error_redirect(
                &redirect_uri,
                error,
                description,
                state,
            )))
        };

        if params.response_type.as_deref() != Some("code") {
            return fail(
                "unsupported_response_type",
                "only response_type=code is supported",
            );
        }
        let Some(challenge) = params.code_challenge.as_deref().filter(|c| !c.is_empty()) else {
            return fail("invalid_request", "code_challenge is required (PKCE)");
        };
        if params.code_challenge_method.as_deref().unwrap_or("plain") != "S256" {
            return fail("invalid_request", "code_challenge_method must be S256");
        }
        if let Some(resource) = params.resource.as_deref() {
            if !self.accepts_resource(resource) {
                return fail(
                    "invalid_target",
                    "resource must be this gateway's origin or /mcp URL",
                );
            }
        }

        // Reserve the waiter before publishing the card, under the same lock as agent
        // creation. A second public-client request must never share the first one's consent.
        let (agent, is_new, signin, rx) = {
            let mut config = self.config.write().await;
            let mut signins = self.oauth.signins.lock().expect("sign-in lock poisoned");
            self.prune_signins(&mut signins);
            if signins.len() >= MAX_PENDING_SIGNINS
                || signins
                    .values()
                    .any(|entry| entry.view.client_id == client_id)
            {
                return fail(
                    "temporarily_unavailable",
                    "a sign-in is already pending or Prism is busy; retry later",
                );
            }
            let (agent, is_new) = config.find_or_request_agent_for_client(&client);
            if agent.status == AgentStatus::Denied {
                return fail("access_denied", "the operator denied this agent in Prism");
            }
            if is_new && config.save(&self.config_path).is_err() {
                config.agents.retain(|a| a.id != agent.id);
                return fail("server_error", "could not save the agent request");
            }
            let (tx, rx) = oneshot::channel();
            let new_client = !config
                .tokens
                .iter()
                .any(|t| t.client_id.as_deref() == Some(client_id));
            let signin = PendingSignIn {
                id: uuid::Uuid::new_v4().to_string(),
                agent_id: agent.id.clone(),
                agent_name: agent.name.clone(),
                client_name: client.client_name.clone(),
                client_id: client_id.to_string(),
                requested_at: Utc::now(),
                needs_consent: agent.status == AgentStatus::Approved,
                new_client,
            };
            signins.insert(
                signin.id.clone(),
                SignInEntry {
                    view: signin.clone(),
                    tx,
                },
            );
            (agent, is_new, signin, rx)
        };
        if is_new {
            info!(agent = %agent.name, "new agent requested access via oauth");
            let _ = self
                .events
                .send(GatewayEvent::AgentRequested(agent.clone()));
        }
        if signin.needs_consent {
            let _ = self
                .events
                .send(GatewayEvent::SignInRequested(signin.clone()));
        }
        Ok(AuthorizationWait {
            signin,
            redirect_uri,
            code_challenge: challenge.to_string(),
            state: params.state,
            rx,
        })
    }

    async fn finish_authorization(
        &self,
        wait: AuthorizationWait,
        timeout: Duration,
    ) -> AuthorizeOutcome {
        let AuthorizationWait {
            signin,
            redirect_uri,
            code_challenge,
            state,
            rx,
        } = wait;
        let state = state.as_deref();
        let fail = |error: &str, description: &str| {
            AuthorizeOutcome::Redirect(error_redirect(&redirect_uri, error, description, state))
        };
        let answer = tokio::time::timeout(timeout, rx).await;
        if let Ok(mut signins) = self.oauth.signins.lock() {
            signins.remove(&signin.id);
        }
        let approved = match answer {
            Ok(Ok(answer)) => answer,
            Ok(Err(_)) => false,
            Err(_) => {
                let _ = self.events.send(GatewayEvent::SignInDecided {
                    id: signin.id.clone(),
                    approved: false,
                });
                return fail("access_denied", "nobody answered in Prism in time");
            }
        };
        if !approved {
            return fail("access_denied", "the operator denied this sign-in in Prism");
        }

        let code = random_token();
        if let Ok(mut codes) = self.oauth.codes.lock() {
            let now = Utc::now();
            codes.retain(|_, c| c.expires_at > now);
            codes.insert(
                code.clone(),
                AuthCode {
                    client_id: signin.client_id.clone(),
                    agent_id: signin.agent_id.clone(),
                    redirect_uri: redirect_uri.clone(),
                    code_challenge,
                    expires_at: now + chrono::Duration::seconds(CODE_TTL_SECS),
                },
            );
        }
        let mut params = vec![("code", code.as_str())];
        if let Some(s) = state {
            params.push(("state", s));
        }
        AuthorizeOutcome::Redirect(with_query(&redirect_uri, &params))
    }

    /// Answer the single pending sign-in when the operator decides its agent.
    pub(crate) fn resolve_authorization(&self, agent_id: &str, approved: bool) {
        let id = self.oauth.signins.lock().ok().and_then(|signins| {
            signins
                .values()
                .find(|entry| entry.view.agent_id == agent_id)
                .map(|entry| entry.view.id.clone())
        });
        if let Some(id) = id {
            // If it ended meanwhile, its unique request ID cannot match a later sign-in.
            let _ = self.decide_signin(&id, approved);
        }
    }

    fn prune_signins(&self, signins: &mut HashMap<String, SignInEntry>) {
        let cutoff = Utc::now() - chrono::Duration::seconds(AUTHORIZE_WAIT.as_secs() as i64);
        signins.retain(|id, entry| {
            let keep = !entry.tx.is_closed() && entry.view.requested_at > cutoff;
            if !keep {
                let _ = self.events.send(GatewayEvent::SignInDecided {
                    id: id.clone(),
                    approved: false,
                });
            }
            keep
        });
    }

    /// Sign-ins waiting for an answer, oldest first.
    pub fn pending_signins(&self) -> Vec<PendingSignIn> {
        let mut list: Vec<PendingSignIn> = self
            .oauth
            .signins
            .lock()
            .map(|mut s| {
                self.prune_signins(&mut s);
                s.values().map(|e| e.view.clone()).collect()
            })
            .unwrap_or_default();
        list.sort_by_key(|s| s.requested_at);
        list
    }

    /// Answer one sign-in. Only the browser that asked gets the code.
    pub fn decide_signin(&self, id: &str, approve: bool) -> Result<()> {
        let entry = self
            .oauth
            .signins
            .lock()
            .ok()
            .and_then(|mut s| s.remove(id))
            .ok_or_else(|| Error::NotFound(format!("sign-in {id}")))?;
        let _ = entry.tx.send(approve);
        let _ = self.events.send(GatewayEvent::SignInDecided {
            id: id.to_string(),
            approved: approve,
        });
        Ok(())
    }

    /// Who opened an MCP session, once known.
    pub(crate) fn session_owner(&self, session_id: &str) -> Option<String> {
        self.oauth
            .session_owners
            .lock()
            .ok()
            .and_then(|o| o.get(session_id).cloned())
    }

    fn remember_session(&self, session_id: &str, owner: String) {
        if let Ok(mut owners) = self.oauth.session_owners.lock() {
            if owners.len() >= MAX_TRACKED_SESSIONS {
                // Sessions rmcp already dropped without a DELETE; forget an arbitrary batch
                // rather than grow without bound. A forgotten session is refused, not opened.
                let stale: Vec<String> = owners
                    .keys()
                    .take(MAX_TRACKED_SESSIONS / 4)
                    .cloned()
                    .collect();
                for key in stale {
                    owners.remove(&key);
                }
            }
            owners.insert(session_id.to_string(), owner);
        }
    }

    fn forget_session(&self, session_id: &str) {
        if let Ok(mut owners) = self.oauth.session_owners.lock() {
            owners.remove(session_id);
        }
    }

    pub async fn token(&self, req: TokenRequest) -> std::result::Result<TokenResponse, OAuthError> {
        match req.grant_type.as_deref() {
            Some("authorization_code") => self.redeem_code(req).await,
            Some("refresh_token") => self.refresh(req).await,
            Some(other) => Err(OAuthError::new(
                "unsupported_grant_type",
                format!("grant_type {other} is not supported"),
            )),
            None => Err(OAuthError::new("invalid_request", "grant_type is required")),
        }
    }

    async fn redeem_code(
        &self,
        req: TokenRequest,
    ) -> std::result::Result<TokenResponse, OAuthError> {
        let code = req
            .code
            .as_deref()
            .filter(|c| !c.is_empty())
            .ok_or_else(|| OAuthError::new("invalid_request", "code is required"))?;
        let verifier = req
            .code_verifier
            .as_deref()
            .ok_or_else(|| OAuthError::new("invalid_request", "code_verifier is required"))?;
        // Single use: the code leaves the map whether or not the rest checks out.
        let auth = self
            .oauth
            .codes
            .lock()
            .ok()
            .and_then(|mut codes| codes.remove(code))
            .ok_or_else(|| OAuthError::new("invalid_grant", "unknown or already used code"))?;
        if auth.expires_at <= Utc::now() {
            return Err(OAuthError::new("invalid_grant", "code expired"));
        }
        if req.client_id.as_deref() != Some(auth.client_id.as_str()) {
            return Err(OAuthError::new(
                "invalid_client",
                "client_id does not match the code",
            ));
        }
        if let Some(uri) = req.redirect_uri.as_deref() {
            if uri != auth.redirect_uri {
                return Err(OAuthError::new(
                    "invalid_grant",
                    "redirect_uri does not match",
                ));
            }
        }
        if !pkce_matches(verifier, &auth.code_challenge) {
            return Err(OAuthError::new("invalid_grant", "PKCE verification failed"));
        }
        self.issue_tokens(&auth.agent_id, &auth.client_id).await
    }

    async fn refresh(&self, req: TokenRequest) -> std::result::Result<TokenResponse, OAuthError> {
        let presented = req
            .refresh_token
            .as_deref()
            .filter(|t| !t.is_empty())
            .ok_or_else(|| OAuthError::new("invalid_request", "refresh_token is required"))?;
        let hash = hash_token(presented);
        let now = Utc::now();
        let record = {
            let mut config = self.config.write().await;
            let idx = config
                .tokens
                .iter()
                .position(|t| t.hash == hash && t.kind == TokenKind::Refresh);
            let Some(idx) = idx else {
                return Err(OAuthError::new("invalid_grant", "unknown refresh token"));
            };
            // Rotation: the presented token dies here, whatever happens next.
            let record = config.tokens.remove(idx);
            if let Err(err) = config.save(&self.config_path) {
                warn!(%err, "could not persist refresh rotation");
            }
            record
        };
        if record.is_expired(now) {
            return Err(OAuthError::new("invalid_grant", "refresh token expired"));
        }
        if let Some(client_id) = req.client_id.as_deref() {
            if Some(client_id) != record.client_id.as_deref() {
                return Err(OAuthError::new(
                    "invalid_client",
                    "client_id does not match",
                ));
            }
        }
        let client_id = record
            .client_id
            .as_deref()
            .ok_or_else(|| OAuthError::new("invalid_grant", "refresh token has no client"))?;
        self.issue_tokens(&record.agent_id, client_id).await
    }

    async fn issue_tokens(
        &self,
        agent_id: &str,
        client_id: &str,
    ) -> std::result::Result<TokenResponse, OAuthError> {
        let now = Utc::now();
        let access = random_token();
        let refresh = random_token();
        let mut config = self.config.write().await;
        let approved = config
            .agents
            .iter()
            .any(|a| a.id == agent_id && a.status == AgentStatus::Approved);
        if !approved {
            return Err(OAuthError::new(
                "access_denied",
                "this agent is not approved in Prism",
            ));
        }
        config.tokens.retain(|t| !t.is_expired(now));
        config.tokens.push(TokenRecord {
            hash: hash_token(&access),
            kind: TokenKind::Access,
            agent_id: agent_id.to_string(),
            client_id: Some(client_id.to_string()),
            created_at: now,
            expires_at: Some(now + chrono::Duration::seconds(ACCESS_TTL_SECS)),
        });
        config.tokens.push(TokenRecord {
            hash: hash_token(&refresh),
            kind: TokenKind::Refresh,
            agent_id: agent_id.to_string(),
            client_id: Some(client_id.to_string()),
            created_at: now,
            expires_at: Some(now + chrono::Duration::seconds(REFRESH_TTL_SECS)),
        });
        config
            .save(&self.config_path)
            .map_err(|err| OAuthError::new("server_error", err.to_string()))?;
        drop(config);
        let _ = self.events.send(GatewayEvent::AgentUpdated {
            agent_id: agent_id.to_string(),
        });
        Ok(TokenResponse {
            access_token: access,
            token_type: "Bearer",
            expires_in: ACCESS_TTL_SECS,
            refresh_token: refresh,
            scope: SCOPE,
        })
    }

    /// The agent behind a bearer token, if the token is live and the agent still approved.
    pub async fn authenticate(&self, bearer: &str) -> Option<String> {
        let hash = hash_token(bearer.trim());
        self.authenticate_hash(&hash).await
    }

    pub(crate) async fn authenticate_hash(&self, hash: &str) -> Option<String> {
        let now = Utc::now();
        let config = self.config.read().await;
        let token = config.tokens.iter().find(|t| {
            t.hash == hash
                && matches!(t.kind, TokenKind::Access | TokenKind::Manual)
                && !t.is_expired(now)
        })?;
        config
            .agents
            .iter()
            .find(|a| a.id == token.agent_id && a.status == AgentStatus::Approved)
            .map(|a| a.id.clone())
    }

    /// RFC 7009: forget one token. Always succeeds, so callers learn nothing about validity.
    pub async fn revoke_token(&self, presented: &str) {
        let hash = hash_token(presented.trim());
        let mut config = self.config.write().await;
        let before = config.tokens.len();
        let owner = config
            .tokens
            .iter()
            .find(|t| t.hash == hash)
            .map(|t| t.agent_id.clone());
        config.tokens.retain(|t| t.hash != hash);
        if config.tokens.len() != before {
            if let Err(err) = config.save(&self.config_path) {
                warn!(%err, "could not persist token revocation");
            }
            drop(config);
            if let Some(agent_id) = owner {
                let _ = self.events.send(GatewayEvent::AgentUpdated { agent_id });
            }
        }
    }

    /// Sign an agent out everywhere: every token it holds is gone and its next request is a 401.
    pub async fn revoke_agent_tokens(&self, agent_id: &str) -> Result<()> {
        {
            let mut config = self.config.write().await;
            if !config.agents.iter().any(|a| a.id == agent_id) {
                return Err(Error::NotFound(format!("agent {agent_id}")));
            }
            config.tokens.retain(|t| t.agent_id != agent_id);
            config.save(&self.config_path)?;
        }
        let _ = self.events.send(GatewayEvent::AgentUpdated {
            agent_id: agent_id.to_string(),
        });
        Ok(())
    }

    /// Drop an agent's tokens and its client registration without touching the agent record.
    pub(crate) fn forget_credentials(config: &mut crate::config::PrismConfig, agent_id: &str) {
        let client_ids = config.agent_client_ids(agent_id);
        config.tokens.retain(|t| t.agent_id != agent_id);
        config
            .clients
            .retain(|c| !client_ids.contains(&c.client_id));
    }

    /// Drop one client registration and its tokens, leaving the agent and its other clients.
    /// A harness that registered from a project you no longer use is forgotten this way.
    pub async fn forget_client(&self, agent_id: &str, client_id: &str) -> Result<()> {
        {
            let mut config = self.config.write().await;
            if config.client_agent_id(client_id).as_deref() != Some(agent_id) {
                return Err(Error::NotFound(format!("client {client_id}")));
            }
            config
                .tokens
                .retain(|t| t.client_id.as_deref() != Some(client_id));
            config.clients.retain(|c| c.client_id != client_id);
            if let Some(agent) = config.agents.iter_mut().find(|a| a.id == agent_id) {
                if agent.client_id.as_deref() == Some(client_id) {
                    agent.client_id = None;
                }
            }
            config.save(&self.config_path)?;
        }
        let _ = self.events.send(GatewayEvent::AgentUpdated {
            agent_id: agent_id.to_string(),
        });
        Ok(())
    }

    pub async fn agent_tokens(&self, agent_id: &str) -> Vec<TokenView> {
        let now = Utc::now();
        self.config
            .read()
            .await
            .tokens
            .iter()
            .filter(|t| t.agent_id == agent_id && !t.is_expired(now))
            .map(|t| TokenView {
                kind: t.kind,
                created_at: t.created_at,
                expires_at: t.expires_at,
            })
            .collect()
    }
}

// ---------------------------------------------------------------------------------------------
// HTTP

pub(crate) fn router(gateway: Arc<Gateway>) -> Router {
    Router::new()
        .route(
            "/.well-known/oauth-protected-resource",
            get(protected_resource),
        )
        .route(
            "/.well-known/oauth-protected-resource/mcp",
            get(protected_resource),
        )
        .route(
            "/.well-known/oauth-authorization-server",
            get(authorization_server),
        )
        .route("/register", post(register))
        .route("/authorize", get(authorize))
        .route("/authorize/status", get(authorize_status))
        .route("/authorize/finish", get(authorize_finish))
        .route("/token", post(token))
        .route("/revoke", post(revoke))
        .layer(DefaultBodyLimit::max(32 * 1024))
        .layer(axum::middleware::from_fn_with_state(
            gateway.clone(),
            rate_limit,
        ))
        .with_state(gateway)
}

fn too_many_requests(message: &str, retry_after: u64) -> Response {
    (
        StatusCode::TOO_MANY_REQUESTS,
        [
            (header::RETRY_AFTER, retry_after.to_string()),
            (header::CACHE_CONTROL, "no-store".into()),
        ],
        Json(OAuthError::new("temporarily_unavailable", message)),
    )
        .into_response()
}

async fn rate_limit(State(gateway): State<Arc<Gateway>>, req: Request, next: Next) -> Response {
    let retry_after = {
        let mut rates = gateway
            .oauth
            .rates
            .lock()
            .expect("rate limit lock poisoned");
        let now = Instant::now();
        match (req.method().as_str(), req.uri().path()) {
            ("POST", "/register") => rates.register.take(now, 30),
            ("GET", "/authorize") => rates.authorize.take(now, 30),
            ("GET", "/authorize/status" | "/authorize/finish") => rates.status.take(now, 1800),
            ("POST", "/token") => rates.token.take(now, 120),
            ("POST", "/revoke") => rates.revoke.take(now, 60),
            _ => None,
        }
    };
    if let Some(seconds) = retry_after {
        return too_many_requests("too many requests; retry later", seconds);
    }
    next.run(req).await
}

/// Bearer check in front of `/mcp`. A missing token is a 401 that points at the resource
/// metadata, which is how MCP clients discover they need to sign in.
pub(crate) async fn require_bearer(
    State(gateway): State<Arc<Gateway>>,
    mut req: Request,
    next: Next,
) -> Response {
    let presented = req
        .headers()
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .and_then(|v| {
            let (scheme, rest) = v.split_once(' ')?;
            scheme.eq_ignore_ascii_case("bearer").then(|| rest.trim())
        })
        .filter(|t| !t.is_empty())
        .map(str::to_string);
    let token_hash = presented.as_deref().map(hash_token);
    let identity = match token_hash.as_deref() {
        Some(hash) => match gateway.authenticate_hash(hash).await {
            Some(agent_id) => agent_id,
            None => return challenge(&gateway, Some("invalid_token")),
        },
        None => return challenge(&gateway, None),
    };
    req.extensions_mut().insert(AuthenticatedAgent {
        agent_id: identity.clone(),
        token_hash: token_hash.expect("authenticated token has a hash"),
    });

    // A session belongs to whoever opened it. A valid token for another agent does not
    // let a caller ride an existing session, on any method.
    let session_id = req
        .headers()
        .get("mcp-session-id")
        .and_then(|v| v.to_str().ok())
        .map(str::to_string);
    if let Some(sid) = session_id.as_deref() {
        match gateway.session_owner(sid) {
            Some(owner) if owner == identity => {}
            Some(_) => {
                return (
                    StatusCode::FORBIDDEN,
                    Json(OAuthError::new(
                        "insufficient_scope",
                        "this session belongs to another agent",
                    )),
                )
                    .into_response();
            }
            // rmcp may still hold a session whose ownership record was evicted.
            // Require a new initialization rather than accept an unverified owner.
            None => return StatusCode::NOT_FOUND.into_response(),
        }
    }
    let method = req.method().clone();
    let response = next.run(req).await;
    match (session_id, method) {
        (None, _) => {
            if let Some(sid) = response
                .headers()
                .get("mcp-session-id")
                .and_then(|v| v.to_str().ok())
            {
                gateway.remember_session(sid, identity);
            }
        }
        (Some(sid), m) if m == axum::http::Method::DELETE && response.status().is_success() => {
            gateway.forget_session(&sid);
        }
        _ => {}
    }
    response
}

fn challenge(gateway: &Gateway, error: Option<&'static str>) -> Response {
    let metadata = format!("{}/.well-known/oauth-protected-resource", gateway.issuer());
    let value = match error {
        Some(e) => format!("Bearer error=\"{e}\", resource_metadata=\"{metadata}\""),
        None => format!("Bearer resource_metadata=\"{metadata}\""),
    };
    let body = Json(OAuthError::new(
        error.unwrap_or("unauthorized"),
        "sign in to Prism: register at /register and authorize at /authorize",
    ));
    (
        StatusCode::UNAUTHORIZED,
        [(header::WWW_AUTHENTICATE, value)],
        body,
    )
        .into_response()
}

async fn protected_resource(State(gateway): State<Arc<Gateway>>) -> Json<serde_json::Value> {
    Json(gateway.protected_resource_metadata())
}

async fn authorization_server(State(gateway): State<Arc<Gateway>>) -> Json<serde_json::Value> {
    Json(gateway.authorization_server_metadata())
}

async fn register(
    State(gateway): State<Arc<Gateway>>,
    Json(req): Json<RegisterRequest>,
) -> Response {
    match gateway.register_client(req).await {
        Ok(client) => (
            StatusCode::CREATED,
            Json(RegisterResponse {
                client_id: client.client_id,
                client_id_issued_at: client.created_at.timestamp(),
                client_name: client.client_name,
                redirect_uris: client.redirect_uris,
                grant_types: vec!["authorization_code".into(), "refresh_token".into()],
                response_types: vec!["code".into()],
                token_endpoint_auth_method: "none".into(),
            }),
        )
            .into_response(),
        Err(Error::RateLimited(msg)) => too_many_requests(msg, 60),
        Err(Error::Invalid(msg)) => (
            StatusCode::BAD_REQUEST,
            Json(OAuthError::new(
                if msg.starts_with("redirect_uri") {
                    "invalid_redirect_uri"
                } else {
                    "invalid_client_metadata"
                },
                msg,
            )),
        )
            .into_response(),
        Err(err) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(OAuthError::new("server_error", err.to_string())),
        )
            .into_response(),
    }
}

// Browsers explicitly ask for HTML; native clients retain the blocking 303 flow.
pub(crate) fn wants_html(headers: &HeaderMap) -> bool {
    headers
        .get_all(header::ACCEPT)
        .iter()
        .filter_map(|h| h.to_str().ok())
        .any(|h| {
            h.split(',').any(|part| {
                let mut parts = part.trim().split(';');
                parts
                    .next()
                    .is_some_and(|mime| mime.trim().eq_ignore_ascii_case("text/html"))
                    && !parts.any(|param| {
                        param
                            .trim()
                            .strip_prefix("q=")
                            .is_some_and(|q| q.parse::<f32>().unwrap_or(0.0) <= 0.0)
                    })
            })
        })
}

fn authorization_response(outcome: AuthorizeOutcome) -> Response {
    match outcome {
        AuthorizeOutcome::Redirect(uri) => private_response(Redirect::to(&uri).into_response()),
        AuthorizeOutcome::Invalid(reason) => browser_response(
            StatusCode::BAD_REQUEST,
            page("Prism can't continue", &reason),
        ),
    }
}

fn waiting_page(req: &str, agent: &str) -> String {
    let body = crate::remote::callback_page(
        "waiting", "", "Waiting for approval", "Open Prism in your tray.",
        &format!("Approve <b>{}</b> in the Prism panel to continue. This page will update after you decide.", crate::remote::html_escape(agent)),
    );
    body.replace(
        "</body>",
        &format!(
            "{}\n</body>",
            include_str!("authorize.html").replace("{{req}}", req)
        ),
    )
}

async fn authorize(
    State(gateway): State<Arc<Gateway>>,
    headers: HeaderMap,
    Query(params): Query<AuthorizeParams>,
) -> Response {
    if !wants_html(&headers) {
        return authorization_response(gateway.authorize(params).await);
    }
    if gateway
        .oauth
        .browsers
        .lock()
        .expect("browser lock poisoned")
        .len()
        >= MAX_BROWSER_FLOWS
    {
        return too_many_requests("too many browser sign-ins; retry later", 60);
    }
    let wait = match gateway.start_authorization(params).await {
        Ok(wait) => wait,
        Err(outcome) => return authorization_response(outcome),
    };
    // This capability is independent of the public tray/sign-in ID and OAuth state.
    let req = random_token();
    {
        let mut browsers = gateway
            .oauth
            .browsers
            .lock()
            .expect("browser lock poisoned");
        if browsers.len() >= MAX_BROWSER_FLOWS {
            drop(wait);
            gateway.pending_signins();
            return too_many_requests("too many browser sign-ins; retry later", 60);
        }
        browsers.insert(req.clone(), BrowserFlow::default());
    }
    let page = waiting_page(&req, &wait.signin.agent_name);
    tokio::spawn(complete_browser_authorization(
        gateway,
        req,
        wait,
        AUTHORIZE_WAIT,
        BROWSER_RESULT_TTL,
    ));
    browser_response(StatusCode::OK, page)
}

async fn complete_browser_authorization(
    gateway: Arc<Gateway>,
    req: String,
    wait: AuthorizationWait,
    timeout: Duration,
    retention: Duration,
) {
    let outcome = gateway.finish_authorization(wait, timeout).await;
    if let Some(flow) = gateway
        .oauth
        .browsers
        .lock()
        .expect("browser lock poisoned")
        .get_mut(&req)
    {
        flow.outcome = Some(outcome);
    }
    // Both abandoned requests and completed redirects have bounded lifetimes.
    tokio::time::sleep(retention).await;
    gateway
        .oauth
        .browsers
        .lock()
        .expect("browser lock poisoned")
        .remove(&req);
}

#[derive(Deserialize)]
struct BrowserRequest {
    req: String,
}

pub(crate) fn same_origin(headers: &HeaderMap) -> bool {
    if headers
        .get("sec-fetch-site")
        .is_some_and(|site| site != "same-origin" && site != "none")
    {
        return false;
    }
    match headers.get(header::ORIGIN) {
        None => true,
        Some(origin) => headers
            .get(header::HOST)
            .and_then(|h| h.to_str().ok())
            .is_some_and(|host| origin == format!("http://{host}").as_str()),
    }
}

async fn authorize_status(
    State(gateway): State<Arc<Gateway>>,
    headers: HeaderMap,
    Query(query): Query<BrowserRequest>,
) -> Response {
    // A custom header also prevents simple cross-origin fetches in older browsers.
    if !same_origin(&headers) || headers.get("x-prism-oauth").is_none_or(|v| v != "1") {
        return private_response(StatusCode::FORBIDDEN.into_response());
    }
    let mut browsers = gateway
        .oauth
        .browsers
        .lock()
        .expect("browser lock poisoned");
    let Some(flow) = browsers.get_mut(&query.req) else {
        return private_response(StatusCode::GONE.into_response());
    };
    if let Some(retry) = flow.polls.take(Instant::now(), 90) {
        return too_many_requests("polling too quickly; retry later", retry);
    }
    // The polling response never contains a code, token, or external redirect.
    private_response(
        Json(serde_json::json!({
            "status": if flow.outcome.is_some() { "ready" } else { "pending" }
        }))
        .into_response(),
    )
}

async fn authorize_finish(
    State(gateway): State<Arc<Gateway>>,
    headers: HeaderMap,
    Query(query): Query<BrowserRequest>,
) -> Response {
    if !same_origin(&headers) {
        return private_response(StatusCode::FORBIDDEN.into_response());
    }
    let mut browsers = gateway
        .oauth
        .browsers
        .lock()
        .expect("browser lock poisoned");
    match browsers.get(&query.req) {
        Some(flow) if flow.outcome.is_none() => {
            private_response(StatusCode::CONFLICT.into_response())
        }
        Some(_) => authorization_response(browsers.remove(&query.req).unwrap().outcome.unwrap()),
        None => browser_response(
            StatusCode::GONE,
            page(
                "This sign-in has ended.",
                "Return to your app and start sign-in again.",
            ),
        ),
    }
}

pub(crate) fn private_response(mut response: Response) -> Response {
    let headers = response.headers_mut();
    headers.insert(header::CACHE_CONTROL, "no-store".parse().unwrap());
    headers.insert(header::REFERRER_POLICY, "no-referrer".parse().unwrap());
    headers.insert(header::X_CONTENT_TYPE_OPTIONS, "nosniff".parse().unwrap());
    response
}

pub(crate) fn browser_response(status: StatusCode, body: String) -> Response {
    let nonce = random_token();
    let body = body.replace("<script>", &format!("<script nonce=\"{nonce}\">"));
    let mut response = private_response((status, Html(body)).into_response());
    response.headers_mut().insert(header::CONTENT_SECURITY_POLICY, format!(
        "default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-{nonce}'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
    ).parse().unwrap());
    response
        .headers_mut()
        .insert(header::X_FRAME_OPTIONS, "DENY".parse().unwrap());
    response
}

async fn token(State(gateway): State<Arc<Gateway>>, Form(req): Form<TokenRequest>) -> Response {
    match gateway.token(req).await {
        Ok(tokens) => ([(header::CACHE_CONTROL, "no-store")], Json(tokens)).into_response(),
        Err(err) => {
            let status = match err.error {
                "invalid_client" => StatusCode::UNAUTHORIZED,
                "server_error" => StatusCode::INTERNAL_SERVER_ERROR,
                _ => StatusCode::BAD_REQUEST,
            };
            (status, [(header::CACHE_CONTROL, "no-store")], Json(err)).into_response()
        }
    }
}

async fn revoke(State(gateway): State<Arc<Gateway>>, Form(req): Form<RevokeRequest>) -> StatusCode {
    if let Some(token) = req.token.as_deref() {
        gateway.revoke_token(token).await;
    }
    StatusCode::OK
}

fn page(title: &str, body: &str) -> String {
    crate::remote::callback_page(
        "failed",
        "warn",
        "Sign-in stopped",
        &crate::remote::html_escape(title),
        &crate::remote::html_escape(body),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn html_negotiation_keeps_native_clients_compatible() {
        let mut headers = HeaderMap::new();
        assert!(!wants_html(&headers));
        for (accept, expected) in [
            ("*/*", false),
            ("application/json", false),
            ("text/html;q=0", false),
            ("text/html;q=0.0,*/*", false),
            ("text/html,application/xhtml+xml;q=0.9", true),
        ] {
            headers.insert(header::ACCEPT, accept.parse().unwrap());
            assert_eq!(wants_html(&headers), expected);
        }
    }

    #[tokio::test]
    async fn browser_timeout_redirects_and_abandoned_results_expire() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("prism.json");
        crate::config::PrismConfig {
            listen_port: 0,
            ..Default::default()
        }
        .save(&path)
        .unwrap();
        let gateway = Gateway::start(&path, dir.path().join("audit.jsonl"))
            .await
            .unwrap();
        let client = gateway
            .register_client(
                serde_json::from_value(serde_json::json!({
                    "client_name":"timeout", "redirect_uris":["http://localhost/cb"]
                }))
                .unwrap(),
            )
            .await
            .unwrap();
        for finish in [true, false] {
            let wait = gateway.start_authorization(serde_json::from_value(serde_json::json!({
                "client_id":client.client_id, "response_type":"code", "code_challenge":"challenge",
                "code_challenge_method":"S256", "state":"kept"
            })).unwrap()).await.unwrap();
            let id = random_token();
            gateway
                .oauth
                .browsers
                .lock()
                .unwrap()
                .insert(id.clone(), BrowserFlow::default());
            let job = tokio::spawn(complete_browser_authorization(
                gateway.clone(),
                id.clone(),
                wait,
                Duration::from_millis(5),
                Duration::from_millis(100),
            ));
            tokio::time::timeout(Duration::from_secs(1), async {
                loop {
                    if gateway
                        .oauth
                        .browsers
                        .lock()
                        .unwrap()
                        .get(&id)
                        .unwrap()
                        .outcome
                        .is_some()
                    {
                        break;
                    }
                    tokio::task::yield_now().await;
                }
            })
            .await
            .unwrap();
            assert!(gateway.pending_signins().is_empty());
            assert!(gateway.oauth.codes.lock().unwrap().is_empty());
            if finish {
                let response = authorize_finish(
                    State(gateway.clone()),
                    HeaderMap::new(),
                    Query(BrowserRequest { req: id.clone() }),
                )
                .await;
                assert_eq!(response.status(), StatusCode::SEE_OTHER);
                let uri = response.headers()[header::LOCATION].to_str().unwrap();
                assert!(uri.contains("error=access_denied"));
                assert!(uri.contains("nobody%20answered"));
                assert!(uri.contains("state=kept"));
            }
            job.await.unwrap();
            assert!(gateway.oauth.browsers.lock().unwrap().is_empty());
            let response = authorize_finish(
                State(gateway.clone()),
                HeaderMap::new(),
                Query(BrowserRequest { req: id }),
            )
            .await;
            assert_eq!(response.status(), StatusCode::GONE);
        }
        // Unknown handles never create entries, and polling has a separate per-flow cap.
        let id = random_token();
        gateway
            .oauth
            .browsers
            .lock()
            .unwrap()
            .insert(id.clone(), BrowserFlow::default());
        let mut headers = HeaderMap::new();
        headers.insert("x-prism-oauth", "1".parse().unwrap());
        for _ in 0..90 {
            assert_eq!(
                authorize_status(
                    State(gateway.clone()),
                    headers.clone(),
                    Query(BrowserRequest { req: id.clone() })
                )
                .await
                .status(),
                StatusCode::OK
            );
        }
        assert_eq!(
            authorize_status(
                State(gateway.clone()),
                headers.clone(),
                Query(BrowserRequest { req: id })
            )
            .await
            .status(),
            StatusCode::TOO_MANY_REQUESTS
        );
        assert_eq!(
            authorize_status(
                State(gateway.clone()),
                headers,
                Query(BrowserRequest {
                    req: "unknown".into()
                })
            )
            .await
            .status(),
            StatusCode::GONE
        );
        assert_eq!(gateway.oauth.browsers.lock().unwrap().len(), 1);
        {
            let mut browsers = gateway.oauth.browsers.lock().unwrap();
            while browsers.len() < MAX_BROWSER_FLOWS {
                browsers.insert(random_token(), BrowserFlow::default());
            }
        }
        let mut headers = HeaderMap::new();
        headers.insert(header::ACCEPT, "text/html".parse().unwrap());
        let params = serde_json::from_value(serde_json::json!({
            "client_id":client.client_id, "response_type":"code", "code_challenge":"challenge",
            "code_challenge_method":"S256"
        }))
        .unwrap();
        let response = authorize(State(gateway.clone()), headers, Query(params)).await;
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert!(gateway.pending_signins().is_empty());
        assert_eq!(
            gateway.oauth.browsers.lock().unwrap().len(),
            MAX_BROWSER_FLOWS
        );
        gateway.shutdown().await;
    }

    #[test]
    fn rate_window_recovers_without_growing_on_rejected_requests() {
        let mut window = RateWindow::default();
        let now = Instant::now();
        assert_eq!(window.take(now, 2), None);
        assert_eq!(window.take(now, 2), None);
        for _ in 0..100 {
            assert_eq!(window.take(now, 2), Some(60));
        }
        assert_eq!(window.0.len(), 2);
        assert_eq!(window.take(now + Duration::from_millis(59_500), 2), Some(1));
        assert_eq!(window.take(now + Duration::from_secs(60), 2), None);
    }

    #[tokio::test]
    async fn unused_registration_cap_expires_abandoned_clients_but_keeps_decisions() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("prism.json");
        let mut config = crate::config::PrismConfig {
            listen_port: 0,
            ..Default::default()
        };
        for i in 0..MAX_UNUSED_CLIENTS {
            config.clients.push(OAuthClient {
                client_id: format!("client-{i}"),
                client_name: format!("Client {i}"),
                redirect_uris: vec!["http://localhost/cb".into()],
                created_at: Utc::now(),
                agent_id: None,
                origin: None,
            });
        }
        config.save(&path).unwrap();
        let gateway = Gateway::start(&path, dir.path().join("audit.jsonl"))
            .await
            .unwrap();
        let request = || RegisterRequest {
            client_name: None,
            redirect_uris: vec!["http://localhost/cb".into()],
            token_endpoint_auth_method: None,
            grant_types: vec![],
        };
        assert!(matches!(
            gateway.register_client(request()).await,
            Err(Error::RateLimited(_))
        ));
        {
            let mut config = gateway.config.write().await;
            config.clients[0].created_at = Utc::now() - chrono::Duration::hours(25);
            config.clients[1].created_at = Utc::now() - chrono::Duration::hours(25);
            let client = config.clients[1].clone();
            let (agent, _) = config.find_or_request_agent_for_client(&client);
            config
                .agents
                .iter_mut()
                .find(|a| a.id == agent.id)
                .unwrap()
                .status = AgentStatus::Approved;
        }
        gateway.register_client(request()).await.unwrap();
        let persisted = crate::config::PrismConfig::load(&path).unwrap();
        assert!(!persisted.clients.iter().any(|c| c.client_id == "client-0"));
        assert!(persisted.clients.iter().any(|c| c.client_id == "client-1"));
        assert_eq!(persisted.agents.len(), 1);
        gateway.shutdown().await;
    }

    #[tokio::test]
    async fn failed_manual_token_save_preserves_the_existing_token_and_agent_list() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("prism.json");
        crate::config::PrismConfig {
            listen_port: 0,
            ..Default::default()
        }
        .save(&path)
        .unwrap();
        let gateway = Gateway::start(&path, dir.path().join("audit.jsonl"))
            .await
            .unwrap();
        let original = gateway.create_manual_agent("manual").await.unwrap();
        let backup = dir.path().join("saved.json");
        std::fs::rename(&path, &backup).unwrap();
        std::fs::create_dir(&path).unwrap();
        assert!(gateway
            .replace_manual_token(&original.agent_id)
            .await
            .is_err());
        assert!(gateway.create_manual_agent("unsaved").await.is_err());
        assert_eq!(
            gateway.authenticate(&original.token).await.as_deref(),
            Some(original.agent_id.as_str())
        );
        assert_eq!(gateway.agents().await.len(), 1);
        std::fs::remove_dir(&path).unwrap();
        std::fs::rename(&backup, &path).unwrap();
        gateway.shutdown().await;
    }

    #[tokio::test]
    async fn forgotten_session_is_rejected_even_when_transport_still_has_it() {
        use tokio::io::{AsyncReadExt, AsyncWriteExt};

        async fn post(port: u16, bearer: &str, session: Option<&str>, body: &str) -> String {
            let mut stream = tokio::net::TcpStream::connect(("127.0.0.1", port))
                .await
                .unwrap();
            let session_header = session
                .map(|id| format!("Mcp-Session-Id: {id}\r\n"))
                .unwrap_or_default();
            let request = format!(
                "POST /mcp HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\nConnection: close\r\n\
                 Content-Type: application/json\r\nAccept: application/json, text/event-stream\r\n\
                 Authorization: Bearer {bearer}\r\n{session_header}Content-Length: {}\r\n\r\n{body}",
                body.len()
            );
            stream.write_all(request.as_bytes()).await.unwrap();
            let mut response = String::new();
            tokio::time::timeout(Duration::from_secs(5), stream.read_to_string(&mut response))
                .await
                .unwrap()
                .unwrap();
            response
        }

        let dir = tempfile::tempdir().unwrap();
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        drop(listener);
        let config = crate::config::PrismConfig {
            listen_port: port,
            ..Default::default()
        };
        let config_path = dir.path().join("prism.json");
        config.save(&config_path).unwrap();
        let gateway = Gateway::start(&config_path, dir.path().join("audit.jsonl"))
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
        let issued = gateway.create_manual_agent("session-test").await.unwrap();
        let initialized = post(port, &issued.token, None, r#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}"#).await;
        assert!(initialized.starts_with("HTTP/1.1 200"), "{initialized}");
        let session = initialized
            .lines()
            .find_map(|line| {
                let (name, value) = line.split_once(':')?;
                name.eq_ignore_ascii_case("mcp-session-id")
                    .then(|| value.trim())
            })
            .expect("session id");
        let list = r#"{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}"#;
        let before = post(port, &issued.token, Some(session), list).await;
        assert!(before.starts_with("HTTP/1.1 200"), "{before}");

        // Simulate ownership eviction without removing the live rmcp session.
        gateway.forget_session(session);
        let after = post(port, &issued.token, Some(session), list).await;
        gateway.shutdown().await;
        assert!(after.starts_with("HTTP/1.1 404"), "{after}");
    }

    #[test]
    fn pkce_s256_round_trip() {
        // RFC 7636 appendix B vectors.
        let verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk";
        let challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM";
        assert!(pkce_matches(verifier, challenge));
        assert!(!pkce_matches(
            "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXl",
            challenge
        ));
        assert!(!pkce_matches("short", challenge));
    }

    #[test]
    fn redirect_uris_are_native_app_shaped() {
        assert!(redirect_uri_allowed("http://127.0.0.1:53210/callback"));
        assert!(redirect_uri_allowed("http://localhost/cb?x=1"));
        assert!(redirect_uri_allowed("http://[::1]:8/x"));
        assert!(redirect_uri_allowed("https://vscode.dev/redirect"));
        assert!(redirect_uri_allowed(
            "cursor://anysphere.cursor-mcp/oauth/callback"
        ));
        assert!(!redirect_uri_allowed("http://example.com/callback"));
        assert!(!redirect_uri_allowed("http://localhost.evil.com/cb"));
        assert!(!redirect_uri_allowed("javascript://x"));
        assert!(!redirect_uri_allowed("http://127.0.0.1/cb#frag"));
        assert!(!redirect_uri_allowed("nonsense"));
    }

    #[test]
    fn query_appends_and_encodes() {
        assert_eq!(
            with_query("http://127.0.0.1/cb", &[("code", "a b&c"), ("state", "x")]),
            "http://127.0.0.1/cb?code=a%20b%26c&state=x"
        );
        assert_eq!(
            with_query("cursor://cb?y=1", &[("code", "z")]),
            "cursor://cb?y=1&code=z"
        );
    }

    #[test]
    fn hashes_are_stable_hex() {
        assert_eq!(hash_token("abc").len(), 64);
        assert_eq!(hash_token("abc"), hash_token("abc"));
        assert_ne!(hash_token("abc"), hash_token("abd"));
    }

    #[test]
    fn resource_matches_origin_or_mcp() {
        let issuer = "http://127.0.0.1:9086";
        assert!(resource_matches(issuer, "http://127.0.0.1:9086"));
        assert!(resource_matches(issuer, "http://127.0.0.1:9086/"));
        assert!(resource_matches(issuer, "http://127.0.0.1:9086/mcp"));
        assert!(resource_matches(issuer, "http://127.0.0.1:9086/mcp/"));
        assert!(!resource_matches(issuer, "http://127.0.0.1:9086/hooks"));
        assert!(!resource_matches(issuer, "http://127.0.0.1:1/"));
        assert!(!resource_matches(issuer, "http://localhost:9086/"));
    }
}
