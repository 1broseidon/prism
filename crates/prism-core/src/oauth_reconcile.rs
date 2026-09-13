//! Metadata is a grouping suggestion, never an authentication credential.
use super::*;
use crate::config::{Attention, Posture, PrismConfig};

#[cfg(test)]
#[path = "oauth_reconcile_tests.rs"]
mod tests;

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum SignInChoice {
    #[default]
    Add,
    Separate,
    Replace {
        client_id: String,
    },
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SignInConnection {
    pub client_id: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SignInGroup {
    pub posture: Posture,
    pub origin: Option<String>,
    pub connections: Vec<SignInConnection>,
}

/// Normalize only DNS case and the ephemeral port. Keep scheme, loopback host,
/// path and query distinct. Multi-redirect registrations get no automatic suggestion.
fn redirect_shape(client: &OAuthClient) -> Option<(String, String, String)> {
    if client.redirect_uris.len() != 1 {
        return None;
    }
    let raw = &client.redirect_uris[0];
    if raw.contains('#') || raw.contains('@') {
        return None;
    }
    let uri: http::Uri = raw.parse().ok()?;
    let scheme = uri.scheme_str()?;
    if !matches!(scheme, "http" | "https") {
        return None;
    }
    let authority = uri.authority()?;
    let host = authority.host().to_ascii_lowercase();
    if !matches!(host.as_str(), "127.0.0.1" | "localhost" | "[::1]") {
        return None;
    }
    if authority.port().is_some() && authority.port_u16().is_none() {
        return None;
    }
    Some((
        scheme.into(),
        host,
        uri.path_and_query().map_or("/", |p| p.as_str()).into(),
    ))
}

pub(super) fn suggested_agent(config: &PrismConfig, client: &OAuthClient) -> Option<AgentConfig> {
    if config.client_agent_id(&client.client_id).is_some()
        || client.client_name == "unknown"
        || client.client_name.is_empty()
        || crate::native::harness_for_client_name(&client.client_name).is_some()
        || client.origin.as_deref() == Some("unknown")
    {
        return None;
    }
    let shape = redirect_shape(client)?;
    let mut ids = HashSet::new();
    for previous in &config.clients {
        if previous.client_id != client.client_id
            && previous.client_name == client.client_name
            && previous.origin == client.origin
            && redirect_shape(previous).as_ref() == Some(&shape)
        {
            if let Some(id) = config.client_agent_id(&previous.client_id) {
                ids.insert(id);
            }
        }
    }
    let candidates: Vec<_> = config
        .agents
        .iter()
        .filter(|agent| {
            ids.contains(&agent.id) && agent.host.is_none() && agent.status != AgentStatus::Pending
        })
        .collect();
    // A re-registration must not bypass a recorded denial, even if another matching
    // installation is approved. An operator can explicitly reconsider that agent.
    if let Some(agent) = candidates
        .iter()
        .find(|agent| agent.status == AgentStatus::Denied)
    {
        return Some((*agent).clone());
    }
    (candidates.len() == 1).then(|| candidates[0].clone())
}

pub(super) fn group_view(
    config: &PrismConfig,
    agent: &AgentConfig,
    client: &OAuthClient,
) -> SignInGroup {
    let ids = config.agent_client_ids(&agent.id);
    let mut connections: Vec<_> = config
        .clients
        .iter()
        .filter(|c| ids.contains(&c.client_id))
        .map(|c| SignInConnection {
            client_id: c.client_id.clone(),
            created_at: c.created_at,
        })
        .collect();
    connections.sort_by_key(|c| c.created_at);
    SignInGroup {
        posture: agent.posture,
        origin: client.origin.clone(),
        connections,
    }
}

pub(super) fn prune_with_live(
    config: &mut PrismConfig,
    now: DateTime<Utc>,
    live_clients: &HashSet<String>,
    live_agents: &HashSet<String>,
) {
    let cutoff = now - chrono::Duration::hours(UNUSED_CLIENT_HOURS);
    let eligible_agent = |agent: &AgentConfig| {
        agent.host.is_none() && agent.client_id.is_some() && agent.status == AgentStatus::Pending
            && agent.decided_at.is_none() && agent.created_at <= cutoff
            && agent.posture == Posture::default() && agent.attention == Attention::default()
            && !live_agents.contains(&agent.id)
            && !config.clients.iter().any(|client| client.last_authorized_at.is_some() && config.client_agent_id(&client.client_id).as_deref() == Some(agent.id.as_str()))
            && !config.rules.iter().any(|rule| rule.agent_id.as_deref() == Some(&agent.id))
            // Any token history is evidence this was more than an abandoned request.
            && !config.tokens.iter().any(|token| token.agent_id == agent.id)
    };
    let eligible: HashSet<_> = config
        .agents
        .iter()
        .filter(|a| eligible_agent(a))
        .map(|a| a.id.clone())
        .collect();
    let removed: HashSet<_> = config
        .clients
        .iter()
        .filter(|client| {
            client.created_at <= cutoff
                && client.last_authorized_at.is_none()
                && !live_clients.contains(&client.client_id)
                && !config
                    .tokens
                    .iter()
                    .any(|t| t.client_id.as_deref() == Some(&client.client_id))
                && config
                    .client_agent_id(&client.client_id)
                    .is_none_or(|id| eligible.contains(&id))
        })
        .map(|client| client.client_id.clone())
        .collect();
    let removed_agents: HashSet<_> = config
        .agents
        .iter()
        .filter(|a| eligible.contains(&a.id))
        .filter(|a| {
            let ids = config.agent_client_ids(&a.id);
            !ids.is_empty() && ids.iter().all(|id| removed.contains(id))
        })
        .map(|a| a.id.clone())
        .collect();
    config
        .clients
        .retain(|client| !removed.contains(&client.client_id));
    config
        .agents
        .retain(|agent| !removed_agents.contains(&agent.id));
    // Rules and history are never pruned as a side effect of registration cleanup.
}

impl Gateway {
    pub(super) fn prune_registration_state(&self, config: &mut PrismConfig, now: DateTime<Utc>) {
        let mut clients = HashSet::new();
        let mut agents = self.active_agent_ids();
        if let Ok(mut signins) = self.oauth.signins.lock() {
            self.prune_signins(&mut signins);
            for entry in signins.values() {
                clients.insert(entry.view.client_id.clone());
                agents.insert(entry.view.agent_id.clone());
            }
        }
        if let Ok(codes) = self.oauth.codes.lock() {
            for code in codes.values().filter(|code| code.expires_at > now) {
                clients.insert(code.client_id.clone());
                agents.insert(code.agent_id.clone());
            }
        }
        prune_with_live(config, now, &clients, &agents);
    }

    /// Persist consent and optional replacement together before releasing the browser.
    /// Metadata never binds an unapproved registration to an existing agent.
    pub async fn decide_signin_with_choice(
        &self,
        id: &str,
        approve: bool,
        choice: SignInChoice,
    ) -> Result<()> {
        let mut config = self.config.write().await;
        let mut signins = self.oauth.signins.lock().expect("sign-in lock poisoned");
        self.prune_signins(&mut signins);
        let signin = signins
            .get(id)
            .ok_or_else(|| Error::NotFound(format!("sign-in {id}")))?
            .view
            .clone();
        let mut updated = config.clone();
        let mut agent_id = signin.agent_id.clone();
        let mut removed_client = None;
        let mut removed_hashes = HashSet::new();
        if approve {
            let client = updated
                .clients
                .iter()
                .find(|c| c.client_id == signin.client_id)
                .cloned()
                .ok_or_else(|| Error::NotFound("registration was removed".into()))?;
            let agent = updated
                .agents
                .iter()
                .find(|a| a.id == signin.agent_id && a.status == AgentStatus::Approved)
                .cloned()
                .ok_or_else(|| Error::Invalid("the agent is no longer approved".into()))?;
            if let Some(group) = &signin.suggested_group {
                if suggested_agent(&updated, &client)
                    .as_ref()
                    .map(|a| a.id.as_str())
                    != Some(agent.id.as_str())
                {
                    return Err(Error::Invalid(
                        "the suggested group changed; restart this sign-in".into(),
                    ));
                }
                match choice {
                    SignInChoice::Separate => {
                        let (created, _) = updated.find_or_request_agent_for_client(&client);
                        agent_id = created.id;
                        let separate = updated
                            .agents
                            .iter_mut()
                            .find(|a| a.id == agent_id)
                            .unwrap();
                        separate.status = AgentStatus::Approved;
                        separate.decided_at = Some(Utc::now());
                    }
                    SignInChoice::Add => {}
                    SignInChoice::Replace { client_id } => {
                        if !group.connections.iter().any(|c| c.client_id == client_id)
                            || client_id == client.client_id
                            || updated.client_agent_id(&client_id).as_deref()
                                != Some(agent.id.as_str())
                            || !updated.clients.iter().any(|c| c.client_id == client_id)
                        {
                            return Err(Error::Invalid(
                                "the connection to replace changed; restart this sign-in".into(),
                            ));
                        }
                        removed_hashes = updated
                            .tokens
                            .iter()
                            .filter(|t| t.client_id.as_deref() == Some(&client_id))
                            .map(|t| t.hash.clone())
                            .collect();
                        updated
                            .tokens
                            .retain(|t| t.client_id.as_deref() != Some(&client_id));
                        updated.clients.retain(|c| c.client_id != client_id);
                        if let Some(agent) = updated.agents.iter_mut().find(|a| a.id == agent_id) {
                            if agent.client_id.as_deref() == Some(&client_id) {
                                agent.client_id = Some(client.client_id.clone());
                            }
                        }
                        removed_client = Some(client_id);
                    }
                }
                updated
                    .clients
                    .iter_mut()
                    .find(|c| c.client_id == client.client_id)
                    .unwrap()
                    .agent_id = Some(agent_id.clone());
            } else if choice != SignInChoice::Add
                || updated.client_agent_id(&client.client_id).as_deref() != Some(agent_id.as_str())
            {
                return Err(Error::Invalid(
                    "this sign-in does not offer a grouping choice".into(),
                ));
            }
            // Grouping changes durable state. An ordinary sign-in already has its
            // binding/approval persisted; token issuance will persist its new tokens.
            if signin.suggested_group.is_some() {
                // Leave the waiter and all live state unchanged if this write fails.
                updated.save(&self.config_path)?;
                *config = updated;
            }
        }
        let entry = signins.remove(id).expect("checked sign-in exists");
        if let Some(client_id) = &removed_client {
            signins.retain(|other_id, entry| {
                let keep = entry.view.client_id != *client_id;
                if !keep {
                    let _ = self.events.send(GatewayEvent::SignInDecided {
                        id: other_id.clone(),
                        approved: false,
                    });
                }
                keep
            });
            self.oauth
                .codes
                .lock()
                .expect("code lock poisoned")
                .retain(|_, code| code.client_id != *client_id);
        }
        drop(signins);
        drop(config);
        if removed_client.is_some() {
            let sessions: Vec<_> = {
                let mut owners = self
                    .oauth
                    .session_owners
                    .lock()
                    .expect("session owner lock poisoned");
                let sessions: Vec<_> = owners
                    .iter()
                    .filter(|(_, hash)| removed_hashes.contains(*hash))
                    .map(|(id, _)| id.clone())
                    .collect();
                owners.retain(|_, hash| !removed_hashes.contains(hash));
                sessions
            };
            for session in sessions {
                self.unregister_session(&session);
            }
        }
        let _ = entry.tx.send(approve.then_some(agent_id.clone()));
        let _ = self.events.send(GatewayEvent::SignInDecided {
            id: id.into(),
            approved: approve,
        });
        if approve {
            let _ = self.events.send(GatewayEvent::AgentUpdated { agent_id });
        }
        Ok(())
    }
}
