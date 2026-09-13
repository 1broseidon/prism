use super::*;
use crate::credentials::{tests::MemoryStore, LaunchSettings};
use axum::response::IntoResponse;
use std::collections::BTreeMap;
use std::sync::atomic::{AtomicUsize, Ordering};

async fn fixture(build: impl FnOnce(String) -> Router) -> (String, tokio::task::JoinHandle<()>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!("http://{}", listener.local_addr().unwrap());
    let app = build(url.clone());
    let task = tokio::spawn(async move {
        axum::serve(listener, app).await.unwrap();
    });
    (url, task)
}

fn server(url: String, auth: HttpAuth) -> ServerConfig {
    serde_json::from_value(serde_json::json!({"id":"fixture", "name":"Fixture", "url":url, "auth":auth, "enabled":true, "oauth_ref":uuid::Uuid::new_v4().to_string()})).unwrap()
}

#[tokio::test]
async fn redirects_never_forward_custom_keys_authorization_or_request_bodies() {
    let requests = Arc::new(AtomicUsize::new(0));
    let received = requests.clone();
    let (destination, destination_task) = fixture(move |_| {
        Router::new().fallback(move || {
            received.fetch_add(1, Ordering::SeqCst);
            async { StatusCode::OK }
        })
    })
    .await;
    let (source, source_task) = fixture(move |_| {
        Router::new().fallback(move |uri: http::Uri| {
            let destination = destination.clone();
            async move {
                let code = uri.path().trim_start_matches('/').parse::<u16>().unwrap();
                (
                    StatusCode::from_u16(code).unwrap(),
                    [(http::header::LOCATION, destination)],
                )
            }
        })
    })
    .await;
    let headers = BTreeMap::from([
        ("X-Api-Key".into(), "fixture-secret".into()),
        ("Authorization".into(), "Bearer fixture-token".into()),
    ]);
    let client = http_client().unwrap();
    for code in [301, 302, 303, 307, 308] {
        let response = client
            .post(format!("{source}/{code}"))
            .header("X-Api-Key", "fixture-secret")
            .bearer_auth("fixture-token")
            .body("private MCP arguments")
            .send()
            .await
            .unwrap();
        assert_eq!(response.status().as_u16(), code);
    }
    let url = format!("{source}/307");
    let config = server(url.clone(), HttpAuth::Header);
    let launch = LaunchSettings {
        headers: headers.clone(),
        ..Default::default()
    };
    assert!(connect(&config, &launch, Arc::new(MemoryStore::default()))
        .await
        .is_err());
    assert!(
        diagnose_after_failure(&url, &headers, HttpAuth::Header, None)
            .await
            .is_some()
    );
    assert_eq!(probe_remote(&url).await.unwrap(), RemoteProbe::Unreachable);
    assert_eq!(
        requests.load(Ordering::SeqCst),
        0,
        "no request of any kind may reach the redirect destination"
    );
    source_task.abort();
    destination_task.abort();
}

#[tokio::test]
async fn advertised_oauth_is_probed_without_registration_even_when_registration_would_fail() {
    let registrations = Arc::new(AtomicUsize::new(0));
    let registered = registrations.clone();
    let (base, task) = fixture(move |base| {
        Router::new().fallback(move |method: http::Method, uri: http::Uri| {
            let base = base.clone();
            let registered = registered.clone();
            async move {
                match (method, uri.path()) {
                    (http::Method::POST, "/mcp") => (StatusCode::UNAUTHORIZED, [(http::header::WWW_AUTHENTICATE, format!("Bearer resource_metadata=\"{base}/.well-known/oauth-protected-resource\""))]).into_response(),
                    (http::Method::GET, "/.well-known/oauth-protected-resource") => axum::Json(serde_json::json!({"resource":format!("{base}/mcp"), "authorization_servers":[base]})).into_response(),
                    (http::Method::GET, "/.well-known/oauth-authorization-server") => axum::Json(serde_json::json!({"issuer":base,"authorization_endpoint":format!("{base}/authorize"),"token_endpoint":format!("{base}/token"),"registration_endpoint":format!("{base}/register"),"response_types_supported":["code"],"code_challenge_methods_supported":["S256"]})).into_response(),
                    (http::Method::POST, "/register") => {
                        registered.fetch_add(1, Ordering::SeqCst);
                        (StatusCode::FORBIDDEN, "registration needs operator setup").into_response()
                    }
                    _ => StatusCode::NOT_FOUND.into_response(),
                }
            }
        })
    }).await;
    let url = format!("{base}/mcp");
    for _ in 0..2 {
        assert_eq!(probe_remote(&url).await.unwrap(), RemoteProbe::OauthReady);
    }
    let config = server(url.clone(), HttpAuth::None);
    let error = connect(
        &config,
        &LaunchSettings::default(),
        Arc::new(MemoryStore::default()),
    )
    .await
    .err()
    .unwrap();
    assert!(matches!(
        error,
        Error::SignInRequired(AuthHint::OauthAvailable)
    ));
    assert!(matches!(
        diagnose_after_failure(&url, &BTreeMap::new(), HttpAuth::None, None).await,
        Some(Error::SignInRequired(AuthHint::OauthAvailable))
    ));
    assert_eq!(registrations.load(Ordering::SeqCst), 0);
    // Only the explicit sign-in attempts DCR and reports the provider's actual refusal.
    assert!(begin_sign_in(
        &server(url, HttpAuth::Oauth),
        Arc::new(MemoryStore::default())
    )
    .await
    .is_err());
    assert_eq!(registrations.load(Ordering::SeqCst), 1);
    task.abort();
}
