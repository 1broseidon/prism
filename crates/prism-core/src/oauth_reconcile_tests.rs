use super::*;
use crate::credentials::tests::MemoryStore;

const VERIFIER: &str = "fixture-code-verifier-long-enough-for-pkce-and-reproducible";

#[tokio::test]
async fn display_rename_preserves_oauth_binding_and_pending_consent() {
    let (dir, gateway) = gateway().await;
    let (client, agent, token) = signed(&gateway, 4431).await;
    let new_client = gateway.register_client(request(4432)).await.unwrap();
    let wait = gateway
        .start_authorization(params(&new_client))
        .await
        .unwrap();
    assert_eq!(wait.signin.agent_id, agent);
    let before = gateway
        .pending_signins()
        .into_iter()
        .find(|s| s.id == wait.signin.id)
        .unwrap();
    gateway
        .rename_agent(&agent, "Personal workspace")
        .await
        .unwrap();
    let renamed = gateway
        .pending_signins()
        .into_iter()
        .find(|s| s.id == wait.signin.id)
        .unwrap();
    let mut expected = before.clone();
    expected.agent_name = "Personal workspace".into();
    assert_eq!(
        serde_json::to_value(renamed).unwrap(),
        serde_json::to_value(&expected).unwrap()
    );
    // An uncommitted label must not leak into the consent card.
    let path = dir.path().join("prism.json");
    let saved = std::fs::read(&path).unwrap();
    std::fs::remove_file(&path).unwrap();
    std::fs::create_dir(&path).unwrap();
    assert!(gateway.rename_agent(&agent, "Failed draft").await.is_err());
    assert_eq!(
        gateway
            .pending_signins()
            .into_iter()
            .find(|s| s.id == wait.signin.id)
            .unwrap()
            .agent_name,
        "Personal workspace"
    );
    std::fs::remove_dir(&path).unwrap();
    std::fs::write(&path, saved).unwrap();
    assert_eq!(
        gateway.authenticate(&token.access_token).await.as_deref(),
        Some(agent.as_str())
    );
    assert_eq!(
        gateway
            .config
            .read()
            .await
            .client_agent_id(&client.client_id)
            .as_deref(),
        Some(agent.as_str())
    );
    gateway.decide_signin(&wait.signin.id, true).await.unwrap();
    let added = redeem(&gateway, &new_client, code(&gateway, wait).await)
        .await
        .unwrap();
    assert_eq!(
        gateway.authenticate(&added.access_token).await.as_deref(),
        Some(agent.as_str())
    );
    let another = gateway.start_authorization(params(&client)).await.unwrap();
    assert_eq!(another.signin.agent_id, agent);
    assert_eq!(another.signin.agent_name, "Personal workspace");
    gateway
        .decide_signin(&another.signin.id, false)
        .await
        .unwrap();
    assert_eq!(
        gateway
            .config
            .read()
            .await
            .agents
            .iter()
            .find(|a| a.id == agent)
            .unwrap()
            .client_name,
        "Workbench"
    );
    gateway.shutdown().await;
}

async fn gateway() -> (tempfile::TempDir, Arc<Gateway>) {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prism.json");
    PrismConfig {
        listen_port: 0,
        ..Default::default()
    }
    .save(&path)
    .unwrap();
    let gateway = Gateway::start_with_credentials(
        path,
        dir.path().join("audit.jsonl"),
        Arc::new(MemoryStore::default()),
    )
    .await
    .unwrap();
    (dir, gateway)
}

fn request(port: u16) -> RegisterRequest {
    serde_json::from_value(serde_json::json!({"client_name":"Workbench", "redirect_uris":[format!("http://127.0.0.1:{port}/cb")]})).unwrap()
}

fn params(client: &OAuthClient) -> AuthorizeParams {
    serde_json::from_value(serde_json::json!({
        "response_type":"code", "client_id":client.client_id, "redirect_uri":client.redirect_uris[0],
        "code_challenge":base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(Sha256::digest(VERIFIER.as_bytes())),
        "code_challenge_method":"S256"
    })).unwrap()
}

async fn code(gateway: &Gateway, wait: AuthorizationWait) -> String {
    let AuthorizeOutcome::Redirect(url) = gateway
        .finish_authorization(wait, Duration::from_secs(1))
        .await
    else {
        panic!("expected redirect");
    };
    reqwest::Url::parse(&url)
        .unwrap()
        .query_pairs()
        .find(|(key, _)| key == "code")
        .expect("authorization code")
        .1
        .into_owned()
}

async fn redeem(
    gateway: &Gateway,
    client: &OAuthClient,
    code: String,
) -> std::result::Result<TokenResponse, OAuthError> {
    gateway.token(serde_json::from_value(serde_json::json!({"grant_type":"authorization_code", "client_id":client.client_id, "code":code,"code_verifier":VERIFIER})).unwrap()).await
}

async fn signed(gateway: &Gateway, port: u16) -> (OAuthClient, String, TokenResponse) {
    let client = gateway.register_client(request(port)).await.unwrap();
    let wait = gateway.start_authorization(params(&client)).await.unwrap();
    let agent_id = wait.signin.agent_id.clone();
    if wait.signin.needs_consent {
        gateway.decide_signin(&wait.signin.id, true).await.unwrap();
    } else {
        gateway.decide_agent(&agent_id, true).await.unwrap();
    }
    let tokens = redeem(gateway, &client, code(gateway, wait).await)
        .await
        .unwrap();
    (client, agent_id, tokens)
}

#[tokio::test]
async fn three_port_registrations_share_one_agent_only_after_each_consent() {
    let (_dir, gateway) = gateway().await;
    let (first, agent, original) = signed(&gateway, 4401).await;
    {
        let mut config = gateway.config.write().await;
        config
            .agents
            .iter_mut()
            .find(|a| a.id == agent)
            .unwrap()
            .posture = Posture::Trusted;
    }
    let rule = gateway
        .add_rule(crate::NewRule {
            agent_id: Some(agent.clone()),
            server_id: None,
            tool: Some("restricted".into()),
            decision: crate::RuleDecision::Deny,
            attention: None,
            scope: crate::RuleScope::Always,
            minutes: None,
        })
        .await
        .unwrap();
    for port in [4402, 4403] {
        let client = gateway.register_client(request(port)).await.unwrap();
        assert!(gateway
            .config
            .read()
            .await
            .client_agent_id(&client.client_id)
            .is_none());
        assert!(gateway
            .issue_tokens(&agent, &client.client_id)
            .await
            .is_err());
        let wait = gateway.start_authorization(params(&client)).await.unwrap();
        assert_eq!(wait.signin.agent_id, agent);
        assert_eq!(
            wait.signin.suggested_group.as_ref().unwrap().posture,
            Posture::Trusted
        );
        assert!(gateway
            .config
            .read()
            .await
            .client_agent_id(&client.client_id)
            .is_none());
        gateway.decide_signin(&wait.signin.id, true).await.unwrap();
        let tokens = redeem(&gateway, &client, code(&gateway, wait).await)
            .await
            .unwrap();
        assert_eq!(
            gateway.authenticate(&tokens.access_token).await.as_deref(),
            Some(agent.as_str())
        );
        assert_eq!(
            gateway
                .authenticate(&original.access_token)
                .await
                .as_deref(),
            Some(agent.as_str())
        );
    }
    let config = gateway.config.read().await;
    assert_eq!(config.agents.len(), 1);
    assert_eq!(config.agent_client_ids(&agent).len(), 3);
    assert_eq!(config.rules[0].id, rule.id);
    assert!(config
        .clients
        .iter()
        .any(|c| c.client_id == first.client_id));
    drop(config);
    gateway.shutdown().await;
}

#[tokio::test]
async fn refusal_cancellation_timeout_and_failed_save_preserve_the_old_connection() {
    let (dir, gateway) = gateway().await;
    let (old, agent, token) = signed(&gateway, 4501).await;
    gateway.remember_session("old-session", hash_token(&token.access_token));
    for (port, scenario) in [
        (4502, "refuse"),
        (4503, "cancel"),
        (4504, "timeout"),
        (4505, "save"),
    ] {
        let client = gateway.register_client(request(port)).await.unwrap();
        let wait = gateway.start_authorization(params(&client)).await.unwrap();
        let id = wait.signin.id.clone();
        match scenario {
            "refuse" => {
                gateway.decide_signin(&id, false).await.unwrap();
                assert!(
                    matches!(gateway.finish_authorization(wait, Duration::from_secs(1)).await, AuthorizeOutcome::Redirect(url) if url.contains("access_denied"))
                );
            }
            "cancel" => {
                drop(wait);
                assert!(gateway
                    .decide_signin_with_choice(
                        &id,
                        true,
                        SignInChoice::Replace {
                            client_id: old.client_id.clone()
                        }
                    )
                    .await
                    .is_err());
            }
            "timeout" => {
                assert!(
                    matches!(gateway.finish_authorization(wait, Duration::ZERO).await, AuthorizeOutcome::Redirect(url) if url.contains("access_denied"))
                );
            }
            _ => {
                let path = dir.path().join("prism.json");
                let backup = dir.path().join("saved.json");
                std::fs::rename(&path, &backup).unwrap();
                std::fs::create_dir(&path).unwrap();
                assert!(gateway
                    .decide_signin_with_choice(
                        &id,
                        true,
                        SignInChoice::Replace {
                            client_id: old.client_id.clone()
                        }
                    )
                    .await
                    .is_err());
                assert!(gateway.pending_signins().iter().any(|s| s.id == id));
                assert_eq!(
                    gateway.authenticate(&token.access_token).await.as_deref(),
                    Some(agent.as_str())
                );
                assert!(gateway
                    .config
                    .read()
                    .await
                    .client_agent_id(&client.client_id)
                    .is_none());
                std::fs::remove_dir(&path).unwrap();
                std::fs::rename(&backup, &path).unwrap();
                gateway.decide_signin(&id, false).await.unwrap();
                drop(wait);
            }
        }
        assert!(gateway
            .config
            .read()
            .await
            .client_agent_id(&client.client_id)
            .is_none());
        assert_eq!(
            gateway.authenticate(&token.access_token).await.as_deref(),
            Some(agent.as_str())
        );
        assert!(gateway.session_owner("old-session").is_some());
    }
    let refresh: TokenRequest = serde_json::from_value(serde_json::json!({"grant_type":"refresh_token", "client_id":old.client_id,"refresh_token":token.refresh_token})).unwrap();
    assert!(gateway.token(refresh).await.is_ok());
    gateway.shutdown().await;
}

#[tokio::test]
async fn explicit_replacement_revokes_only_selected_tokens_sessions_codes_and_pending_signins() {
    let (dir, gateway) = gateway().await;
    let (old, agent, old_token) = signed(&gateway, 4601).await;
    let (_, _, concurrent) = signed(&gateway, 4602).await;
    gateway.remember_session("old-session", hash_token(&old_token.access_token));
    gateway.remember_session("other-session", hash_token(&concurrent.access_token));
    let old_wait = gateway.start_authorization(params(&old)).await.unwrap();
    gateway
        .decide_signin(&old_wait.signin.id, true)
        .await
        .unwrap();
    let old_code = code(&gateway, old_wait).await;
    let old_pending = gateway.start_authorization(params(&old)).await.unwrap();
    let new = gateway.register_client(request(4603)).await.unwrap();
    let wait = gateway.start_authorization(params(&new)).await.unwrap();
    gateway
        .decide_signin_with_choice(
            &wait.signin.id,
            true,
            SignInChoice::Replace {
                client_id: old.client_id.clone(),
            },
        )
        .await
        .unwrap();
    let new_token = redeem(&gateway, &new, code(&gateway, wait).await)
        .await
        .unwrap();
    assert!(gateway
        .authenticate(&old_token.access_token)
        .await
        .is_none());
    assert!(gateway.token(serde_json::from_value(serde_json::json!({"grant_type":"refresh_token","client_id":old.client_id,"refresh_token":old_token.refresh_token})).unwrap()).await.is_err());
    assert!(redeem(&gateway, &old, old_code).await.is_err());
    assert!(gateway.issue_tokens(&agent, &old.client_id).await.is_err());
    assert!(gateway.session_owner("old-session").is_none());
    assert!(gateway.session_owner("other-session").is_some());
    assert!(
        matches!(gateway.finish_authorization(old_pending, Duration::from_secs(1)).await, AuthorizeOutcome::Redirect(url) if url.contains("access_denied"))
    );
    assert_eq!(
        gateway
            .authenticate(&new_token.access_token)
            .await
            .as_deref(),
        Some(agent.as_str())
    );
    assert_eq!(
        gateway
            .authenticate(&concurrent.access_token)
            .await
            .as_deref(),
        Some(agent.as_str())
    );
    let config = PrismConfig::load(dir.path().join("prism.json")).unwrap();
    assert_eq!(config.agents.len(), 1);
    assert_eq!(config.agent_client_ids(&agent).len(), 2);
    assert!(!config.clients.iter().any(|c| c.client_id == old.client_id));
    gateway.shutdown().await;
    release_gateway(gateway).await;
    let reopened = Gateway::start_with_credentials(
        dir.path().join("prism.json"),
        dir.path().join("audit.jsonl"),
        Arc::new(MemoryStore::default()),
    )
    .await
    .unwrap();
    assert!(reopened
        .authenticate(&old_token.access_token)
        .await
        .is_none());
    assert_eq!(
        reopened
            .authenticate(&new_token.access_token)
            .await
            .as_deref(),
        Some(agent.as_str())
    );
    reopened.shutdown().await;
}

#[tokio::test]
async fn separate_choice_uses_default_policy_and_ambiguous_matches_stay_separate() {
    let (_dir, gateway) = gateway().await;
    let (_, first_id, old) = signed(&gateway, 4701).await;
    gateway.config.write().await.agents[0].posture = Posture::Trusted;
    let client = gateway.register_client(request(4702)).await.unwrap();
    let wait = gateway.start_authorization(params(&client)).await.unwrap();
    gateway
        .decide_signin_with_choice(&wait.signin.id, true, SignInChoice::Separate)
        .await
        .unwrap();
    let token = redeem(&gateway, &client, code(&gateway, wait).await)
        .await
        .unwrap();
    let new_id = gateway.authenticate(&token.access_token).await.unwrap();
    assert_ne!(new_id, first_id);
    assert_eq!(
        gateway
            .config
            .read()
            .await
            .agents
            .iter()
            .find(|a| a.id == new_id)
            .unwrap()
            .posture,
        Posture::FirstUse
    );
    assert_eq!(
        gateway.authenticate(&old.access_token).await,
        Some(first_id)
    );
    let ambiguous = gateway.register_client(request(4703)).await.unwrap();
    let wait = gateway
        .start_authorization(params(&ambiguous))
        .await
        .unwrap();
    assert!(wait.signin.suggested_group.is_none());
    assert!(!wait.signin.needs_consent);
    drop(wait);
    gateway.shutdown().await;
}

#[tokio::test]
async fn metadata_variants_are_separate_and_a_denied_identity_cannot_reset_itself() {
    let (_dir, gateway) = gateway().await;
    let (original, agent, _) = signed(&gateway, 4801).await;
    let config = gateway.config.read().await.clone();
    let mut candidate = original.clone();
    candidate.client_id = "fresh".into();
    candidate.agent_id = None;
    candidate.redirect_uris = vec!["http://127.0.0.1:4802/cb".into()];
    assert_eq!(suggested_agent(&config, &candidate).unwrap().id, agent);
    for uri in [
        "http://localhost:4802/cb",
        "http://[::1]:4802/cb",
        "https://127.0.0.1:4802/cb",
        "http://127.0.0.1:4802/cb/",
        "http://127.0.0.1:4802/other",
        "http://127.0.0.1:4802/cb?installation=two",
        "custom-app:/cb",
        "http://127.0.0.1.evil.example:4802/cb",
        "http://127.0.0.1:4802/a/../cb",
        "http://127.0.0.1:4802/%63b",
    ] {
        let mut different = candidate.clone();
        different.redirect_uris = vec![uri.into()];
        assert!(
            suggested_agent(&config, &different).is_none(),
            "must not group {uri}"
        );
    }
    for origin in [Some("192.0.2.1".into()), Some("unknown".into())] {
        let mut different = candidate.clone();
        different.origin = origin;
        assert!(suggested_agent(&config, &different).is_none());
    }
    let mut different = candidate.clone();
    different.client_name = "workbench".into();
    assert!(suggested_agent(&config, &different).is_none());
    different = candidate.clone();
    different.redirect_uris.push("http://localhost/cb".into());
    assert!(suggested_agent(&config, &different).is_none());
    gateway.decide_agent(&agent, false).await.unwrap();
    let fresh = gateway.register_client(request(4803)).await.unwrap();
    assert!(
        matches!(gateway.start_authorization(params(&fresh)).await, Err(AuthorizeOutcome::Redirect(url)) if url.contains("access_denied"))
    );
    assert_eq!(gateway.config.read().await.agents.len(), 1);
    gateway.shutdown().await;
}

#[tokio::test]
async fn concurrent_replacements_cannot_revoke_a_connection_not_offered_to_them() {
    let (_dir, gateway) = gateway().await;
    let (old, _, _) = signed(&gateway, 4901).await;
    let first = gateway.register_client(request(4902)).await.unwrap();
    let second = gateway.register_client(request(4903)).await.unwrap();
    let one = gateway.start_authorization(params(&first)).await.unwrap();
    let two = gateway.start_authorization(params(&second)).await.unwrap();
    gateway
        .decide_signin_with_choice(
            &one.signin.id,
            true,
            SignInChoice::Replace {
                client_id: old.client_id.clone(),
            },
        )
        .await
        .unwrap();
    let tokens = redeem(&gateway, &first, code(&gateway, one).await)
        .await
        .unwrap();
    assert!(gateway
        .decide_signin_with_choice(
            &two.signin.id,
            true,
            SignInChoice::Replace {
                client_id: old.client_id
            }
        )
        .await
        .is_err());
    assert!(gateway
        .decide_signin_with_choice(
            &two.signin.id,
            true,
            SignInChoice::Replace {
                client_id: first.client_id
            }
        )
        .await
        .is_err());
    assert!(gateway.authenticate(&tokens.access_token).await.is_some());
    gateway.decide_signin(&two.signin.id, true).await.unwrap();
    assert!(redeem(&gateway, &second, code(&gateway, two).await)
        .await
        .is_ok());
    gateway.shutdown().await;
}

#[tokio::test]
async fn registration_records_socket_origin_and_ignores_request_claims() {
    let (_dir, gateway) = gateway().await;
    let remote = gateway
        .register_client_from(request(5001), Some("192.0.2.10".into()))
        .await
        .unwrap();
    assert_eq!(remote.origin.as_deref(), Some("192.0.2.10"));
    let response = super::super::register(
        State(gateway.clone()),
        Some(Extension(axum::extract::ConnectInfo(
            "192.0.2.20:55000".parse().unwrap(),
        ))),
        Json(request(5002)),
    )
    .await;
    assert_eq!(response.status(), StatusCode::CREATED);
    assert_eq!(
        gateway
            .config
            .read()
            .await
            .clients
            .last()
            .unwrap()
            .origin
            .as_deref(),
        Some("192.0.2.20")
    );
    let local = reqwest::Client::new().post(format!("http://127.0.0.1:{}/register", gateway.listen_port())).header("X-Forwarded-For", "192.0.2.10").json(&serde_json::json!({"client_name":"Workbench", "redirect_uris":["http://127.0.0.1:5003/cb"], "origin":"192.0.2.10"})).send().await.unwrap();
    assert_eq!(local.status(), StatusCode::CREATED);
    assert_eq!(
        gateway.config.read().await.clients.last().unwrap().origin,
        None
    );
    gateway.shutdown().await;
}

#[tokio::test]
async fn cleanup_preserves_decisions_rules_tokens_harnesses_manual_agents_and_pending_consent() {
    let (dir, gateway) = gateway().await;
    let now = Utc::now();
    let old = now - chrono::Duration::hours(30);
    let mut config = PrismConfig {
        listen_port: 0,
        ..Default::default()
    };
    let mut ids = HashMap::new();
    for name in [
        "abandoned",
        "approved",
        "denied",
        "rule",
        "posture",
        "access",
        "refresh",
        "expired-history",
        "session",
        "consent",
        "manual",
        "harness",
    ] {
        let client = OAuthClient {
            last_authorized_at: None,
            client_id: name.into(),
            client_name: name.into(),
            redirect_uris: vec!["http://127.0.0.1:5100/cb".into()],
            created_at: old,
            agent_id: None,
            origin: None,
        };
        config.clients.push(client.clone());
        let (created, _) = config.find_or_request_agent_for_client(&client);
        let agent = config
            .agents
            .iter_mut()
            .find(|a| a.id == created.id)
            .unwrap();
        agent.created_at = old;
        ids.insert(name, agent.id.clone());
        match name {
            "approved" => agent.status = AgentStatus::Approved,
            "denied" => agent.status = AgentStatus::Denied,
            "posture" => agent.posture = Posture::Supervised,
            "manual" => {
                agent.client_id = None;
            }
            "harness" => {
                agent.host = Some("claude-code".into());
            }
            "access" | "refresh" | "expired-history" => config.tokens.push(TokenRecord {
                hash: name.into(),
                kind: if name == "access" {
                    TokenKind::Access
                } else {
                    TokenKind::Refresh
                },
                agent_id: agent.id.clone(),
                client_id: Some(name.into()),
                created_at: old,
                expires_at: Some(if name == "expired-history" {
                    old
                } else {
                    now + chrono::Duration::hours(1)
                }),
            }),
            _ => {}
        }
    }
    let manual_id = ids["manual"].clone();
    config.clients.retain(|c| c.client_id != "manual");
    config.rules.push(crate::Rule {
        id: "owned-rule".into(),
        agent_id: Some(ids["rule"].clone()),
        server_id: None,
        tool: None,
        decision: crate::RuleDecision::Deny,
        attention: None,
        scope: crate::RuleScope::Always,
        created_at: now,
        expires_at: None,
        condition: None,
        condition_error: None,
    });
    let live_agents = HashSet::from([ids["session"].clone()]);
    let live_clients = HashSet::from(["consent".into()]);
    prune_with_live(&mut config, now, &live_clients, &live_agents);
    assert!(!config.clients.iter().any(|c| c.client_id == "abandoned"));
    assert!(!config.agents.iter().any(|a| a.id == ids["abandoned"]));
    for (name, id) in &ids {
        if *name != "abandoned" {
            assert!(config.agents.iter().any(|a| &a.id == id), "removed {name}");
        }
    }
    assert!(config.agents.iter().any(|a| a.id == manual_id));
    assert_eq!(config.rules.len(), 1);

    // Exercise the runtime protection, with an old registration currently in consent.
    let waiting = gateway.register_client(request(5101)).await.unwrap();
    let wait = gateway.start_authorization(params(&waiting)).await.unwrap();
    {
        let mut live = gateway.config.write().await;
        live.clients
            .iter_mut()
            .find(|c| c.client_id == waiting.client_id)
            .unwrap()
            .created_at = old;
        live.agents
            .iter_mut()
            .find(|a| a.id == wait.signin.agent_id)
            .unwrap()
            .created_at = old;
    }
    gateway.register_client(request(5102)).await.unwrap();
    assert!(gateway
        .config
        .read()
        .await
        .clients
        .iter()
        .any(|c| c.client_id == waiting.client_id));
    drop(wait);
    gateway.register_client(request(5103)).await.unwrap();
    assert!(!gateway
        .config
        .read()
        .await
        .clients
        .iter()
        .any(|c| c.client_id == waiting.client_id));

    // After restart there are no sessions or browser waiters, but owned decisions survive.
    config.save(dir.path().join("prism.json")).unwrap();
    gateway.shutdown().await;
    release_gateway(gateway).await;
    let reopened = Gateway::start_with_credentials(
        dir.path().join("prism.json"),
        dir.path().join("audit.jsonl"),
        Arc::new(MemoryStore::default()),
    )
    .await
    .unwrap();
    let persisted = PrismConfig::load(dir.path().join("prism.json")).unwrap();
    for name in [
        "approved",
        "denied",
        "manual",
        "harness",
        "rule",
        "posture",
        "access",
        "refresh",
        "expired-history",
    ] {
        assert!(
            persisted.agents.iter().any(|a| a.id == ids[name]),
            "lost {name} at restart"
        );
    }
    assert_eq!(persisted.rules.len(), 1);
    reopened.shutdown().await;
}

async fn release_gateway(gateway: Arc<Gateway>) {
    let weak = Arc::downgrade(&gateway);
    drop(gateway);
    tokio::time::timeout(Duration::from_secs(2), async {
        while weak.upgrade().is_some() {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
}
