//! Best-effort RFC 7009 revocation for Prism's public OAuth clients.
use super::*;

const REQUEST_TIMEOUT: Duration = Duration::from_secs(5);

// Deserialize only what sign-out needs. Never implement Debug for token values.
#[derive(serde::Deserialize)]
struct RevocableCredentials {
    client_id: String,
    issuer: Option<String>,
    token_response: Option<RevocableTokens>,
}

#[derive(serde::Deserialize)]
struct RevocableTokens {
    access_token: String,
    refresh_token: Option<String>,
}

pub(crate) async fn sign_out(config: &ServerConfig, store: Arc<dyn CredentialStore>) -> Result<()> {
    let (reader, id) = (store.clone(), config.oauth_ref.clone());
    let loaded = tokio::task::spawn_blocking(move || {
        id.map(|id| credentials::get_blob(reader.as_ref(), &id))
            .transpose()
    })
    .await;
    let revoked = match loaded {
        Ok(Ok(Some(bytes))) => match serde_json::from_slice::<RevocableCredentials>(&bytes) {
            Ok(creds) => revoke(config, creds).await,
            Err(_) => Err(()),
        },
        Ok(Ok(None)) => Ok(()),
        _ => Err(()),
    };
    if revoked.is_err() {
        warn!("upstream OAuth revocation could not be confirmed; revoke access at the provider if needed");
    }
    // This runs even after discovery, decoding, HTTP, or timeout failures.
    let config = config.clone();
    tokio::task::spawn_blocking(move || forget_tokens(store.as_ref(), &config))
        .await
        .map_err(|_| Error::Gateway("could not reach the credential store".into()))?
}

async fn revoke(config: &ServerConfig, creds: RevocableCredentials) -> std::result::Result<(), ()> {
    let Some(tokens) = creds.token_response else {
        return Ok(());
    };
    let url = config.url.as_deref().ok_or(())?;
    let manager = AuthorizationManager::new(url).await.map_err(|_| ())?;
    let discovery = tokio::time::timeout(REQUEST_TIMEOUT, manager.resolve_metadata())
        .await
        .map_err(|_| ())?
        .map_err(|_| ())?;
    if !discovery.source.is_discovered() {
        return Err(());
    }
    let metadata = discovery.metadata;
    // Never send a saved credential to a newly substituted issuer.
    let issuer = creds.issuer.as_deref().ok_or(())?;
    if metadata.issuer.as_deref() != Some(issuer) {
        return Err(());
    }
    let Some(endpoint) = metadata.additional_fields.get("revocation_endpoint") else {
        return Ok(()); // No invented endpoint for providers without revocation support.
    };
    let endpoint = endpoint.as_str().ok_or(())?;
    let endpoint = reqwest::Url::parse(&validate_url(endpoint).map_err(|_| ())?).map_err(|_| ())?;
    let original = reqwest::Url::parse(url).map_err(|_| ())?;
    if !endpoint.username().is_empty()
        || endpoint.password().is_some()
        || endpoint.fragment().is_some()
    {
        return Err(());
    }
    if endpoint.scheme() == "http" && original.scheme() != "http" {
        return Err(());
    }
    let client = reqwest::Client::builder()
        .timeout(REQUEST_TIMEOUT)
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|_| ())?;
    let mut failed = false;
    for (token, hint) in [
        (tokens.refresh_token.as_deref(), "refresh_token"),
        (Some(tokens.access_token.as_str()), "access_token"),
    ] {
        let Some(token) = token else { continue };
        let response = client
            .post(endpoint.clone())
            .form(&[
                ("token", token),
                ("token_type_hint", hint),
                ("client_id", &creds.client_id),
            ])
            .send()
            .await;
        // Don't read, log, or surface provider bodies that may echo credentials.
        if !matches!(response, Ok(response) if response.status() == StatusCode::OK) {
            failed = true;
        }
    }
    if failed {
        Err(())
    } else {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::credentials::tests::MemoryStore;
    use axum::{extract::Form, response::IntoResponse, routing::post, Json};
    use std::sync::Mutex;

    struct Provider {
        config: ServerConfig,
        store: Arc<MemoryStore>,
        requests: Arc<Mutex<Vec<HashMap<String, String>>>>,
        task: tokio::task::JoinHandle<()>,
    }
    impl Drop for Provider {
        fn drop(&mut self) {
            self.task.abort();
        }
    }
    impl Provider {
        async fn new(
            advertised: bool,
            status: StatusCode,
            issuer_matches: bool,
            slow: bool,
        ) -> Self {
            let listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
                .await
                .unwrap();
            let base = format!("http://{}", listener.local_addr().unwrap());
            let config: ServerConfig = serde_json::from_value(serde_json::json!({
                "id":"fixture", "name":"fixture", "url":format!("{base}/mcp"),
                "auth":"oauth", "oauth_ref":uuid::Uuid::new_v4().to_string()
            }))
            .unwrap();
            let mut metadata = serde_json::json!({
                "issuer":base, "authorization_endpoint":format!("{base}/authorize"),
                "token_endpoint":format!("{base}/token"), "response_types_supported":["code"],
                "code_challenge_methods_supported":["S256"]
            });
            if advertised {
                metadata["revocation_endpoint"] = serde_json::json!(format!("{base}/revoke"));
            }
            let resource = serde_json::json!({"resource":format!("{base}/mcp"), "authorization_servers":[base]});
            let requests = Arc::new(Mutex::new(Vec::new()));
            let captured = requests.clone();
            let redirected = requests.clone();
            let app = Router::new()
                .route(
                    "/.well-known/oauth-protected-resource/mcp",
                    get(move || {
                        let resource = resource.clone();
                        async move { Json(resource) }
                    }),
                )
                .route(
                    "/.well-known/oauth-authorization-server",
                    get(move || {
                        let metadata = metadata.clone();
                        async move { Json(metadata) }
                    }),
                )
                .route(
                    "/revoke",
                    post(move |Form(body): Form<HashMap<String, String>>| {
                        let captured = captured.clone();
                        async move {
                            captured.lock().unwrap().push(body);
                            if slow {
                                tokio::time::sleep(Duration::from_secs(60)).await;
                            }
                            (
                                status,
                                [(http::header::LOCATION, "/unexpected")],
                                "provider response deliberately not shown",
                            )
                                .into_response()
                        }
                    }),
                )
                .route(
                    "/unexpected",
                    post(move |Form(body): Form<HashMap<String, String>>| {
                        let redirected = redirected.clone();
                        async move {
                            redirected.lock().unwrap().push(body);
                            StatusCode::OK
                        }
                    }),
                );
            let task = tokio::spawn(async move {
                axum::serve(listener, app).await.unwrap();
            });
            let stored = serde_json::json!({
                "client_id":"public-fixture", "issuer":if issuer_matches { base.as_str() } else { "https://different.example" },
                "token_response":{"access_token":"fixture-access", "refresh_token":"fixture-refresh", "token_type":"bearer"}
            });
            // The minimal sign-out representation must accept the SDK's stored wire format.
            let sdk: StoredCredentials = serde_json::from_value(stored).unwrap();
            let bytes = serde_json::to_vec(&sdk).unwrap();
            let store = Arc::new(MemoryStore::default());
            credentials::put_blob(store.as_ref(), config.oauth_ref.as_ref().unwrap(), &bytes)
                .unwrap();
            Self {
                config,
                store,
                requests,
                task,
            }
        }
        fn locally_empty(&self) -> bool {
            self.store.0.lock().unwrap().is_empty()
        }
    }

    #[tokio::test]
    async fn advertised_revocation_sends_both_tokens_then_forgets_locally() {
        let provider = Provider::new(true, StatusCode::OK, true, false).await;
        sign_out(&provider.config, provider.store.clone())
            .await
            .unwrap();
        let requests = provider.requests.lock().unwrap();
        assert_eq!(requests.len(), 2);
        assert_eq!(requests[0]["token"], "fixture-refresh");
        assert_eq!(requests[0]["token_type_hint"], "refresh_token");
        assert_eq!(requests[1]["token"], "fixture-access");
        assert_eq!(requests[1]["token_type_hint"], "access_token");
        assert!(requests.iter().all(|r| r["client_id"] == "public-fixture"));
        assert!(provider.locally_empty());
    }

    #[tokio::test]
    async fn unsupported_revocation_only_clears_local_credentials() {
        let provider = Provider::new(false, StatusCode::OK, true, false).await;
        sign_out(&provider.config, provider.store.clone())
            .await
            .unwrap();
        assert!(provider.requests.lock().unwrap().is_empty());
        assert!(provider.locally_empty());
    }

    #[tokio::test]
    async fn failed_revocation_still_attempts_both_tokens_and_cleans_up() {
        for status in [
            StatusCode::BAD_REQUEST,
            StatusCode::INTERNAL_SERVER_ERROR,
            StatusCode::TEMPORARY_REDIRECT,
        ] {
            let provider = Provider::new(true, status, true, false).await;
            sign_out(&provider.config, provider.store.clone())
                .await
                .unwrap();
            assert_eq!(provider.requests.lock().unwrap().len(), 2);
            assert!(provider.locally_empty());
        }
    }

    #[tokio::test]
    async fn changed_issuer_gets_no_credentials_and_local_signout_succeeds() {
        let provider = Provider::new(true, StatusCode::OK, false, false).await;
        sign_out(&provider.config, provider.store.clone())
            .await
            .unwrap();
        assert!(provider.requests.lock().unwrap().is_empty());
        assert!(provider.locally_empty());
    }

    #[tokio::test]
    async fn unavailable_discovery_and_unreadable_record_do_not_block_cleanup() {
        let provider = Provider::new(true, StatusCode::OK, true, false).await;
        provider.task.abort();
        sign_out(&provider.config, provider.store.clone())
            .await
            .unwrap();
        assert!(provider.locally_empty());
        credentials::put_blob(
            provider.store.as_ref(),
            provider.config.oauth_ref.as_ref().unwrap(),
            b"not JSON",
        )
        .unwrap();
        sign_out(&provider.config, provider.store.clone())
            .await
            .unwrap();
        assert!(provider.locally_empty());
    }

    #[tokio::test]
    async fn removing_a_server_revokes_at_the_provider_then_forgets() {
        let provider = Provider::new(true, StatusCode::OK, true, false).await;
        let dir = tempfile::tempdir().unwrap();
        let listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
            .await
            .unwrap();
        let port = listener.local_addr().unwrap().port();
        drop(listener);
        let path = dir.path().join("prism.json");
        crate::PrismConfig {
            listen_port: port,
            ..Default::default()
        }
        .save(&path)
        .unwrap();
        let gateway = crate::Gateway::start_with_credentials(
            path,
            dir.path().join("audit.jsonl"),
            provider.store.clone(),
        )
        .await
        .unwrap();
        // The gateway assigns its own credential reference; move the fixture tokens under it.
        let mut server = provider.config.clone();
        server.oauth_ref = None;
        let added = gateway.add_server(server).await.unwrap();
        let fixture_ref = provider.config.oauth_ref.as_deref().unwrap();
        let bytes = credentials::get_blob(provider.store.as_ref(), fixture_ref).unwrap();
        credentials::delete_if_present(provider.store.as_ref(), fixture_ref).unwrap();
        credentials::put_blob(
            provider.store.as_ref(),
            added.oauth_ref.as_deref().unwrap(),
            &bytes,
        )
        .unwrap();
        assert!(provider.requests.lock().unwrap().is_empty());

        gateway.remove_server(&added.id).await.unwrap();
        gateway.shutdown().await;
        let requests = provider.requests.lock().unwrap();
        assert_eq!(requests.len(), 2);
        assert_eq!(requests[0]["token_type_hint"], "refresh_token");
        assert_eq!(requests[1]["token_type_hint"], "access_token");
        assert!(provider.locally_empty());
    }

    #[tokio::test]
    async fn stalled_provider_is_bounded_and_credentials_are_forgotten() {
        let provider = Provider::new(true, StatusCode::OK, true, true).await;
        // Two request timeouts plus discovery, with room for a slow runner.
        tokio::time::timeout(
            Duration::from_secs(20),
            sign_out(&provider.config, provider.store.clone()),
        )
        .await
        .unwrap()
        .unwrap();
        assert!(provider.locally_empty());
        assert_eq!(provider.requests.lock().unwrap().len(), 2);
    }
}
