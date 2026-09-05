//! Browser-facing checks for the loopback listener. These do not replace OAuth.
use std::sync::Arc;

use axum::extract::{Request, State};
use axum::http::{header, Method, StatusCode};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};

use crate::Gateway;

fn authority_allowed(value: &str, port: u16) -> bool {
    let Ok(authority) = value.parse::<http::uri::Authority>() else {
        return false;
    };
    if value.contains('@') || authority.port_u16().unwrap_or(80) != port {
        return false;
    }
    matches!(authority.host(), "127.0.0.1" | "[::1]")
        || authority.host().eq_ignore_ascii_case("localhost")
}

fn origin_allowed(value: &str, port: u16) -> bool {
    let Ok(uri) = value.parse::<http::Uri>() else {
        return false;
    };
    uri.scheme_str() == Some("http")
        && uri
            .authority()
            .is_some_and(|authority| authority_allowed(authority.as_str(), port))
        && uri.path_and_query().is_none_or(|path| path.as_str() == "/")
}

pub(crate) async fn guard(
    State(gateway): State<Arc<Gateway>>,
    req: Request,
    next: Next,
) -> Response {
    let headers = req.headers();
    if headers.get_all(header::HOST).iter().count() != 1
        || !headers
            .get(header::HOST)
            .and_then(|host| host.to_str().ok())
            .is_some_and(|host| authority_allowed(host, gateway.listen_port))
    {
        return (StatusCode::FORBIDDEN, "unexpected gateway host").into_response();
    }

    let path = req.uri().path();
    let check_origin = path == "/mcp"
        || path.starts_with("/mcp/")
        || (req.method() == Method::POST && matches!(path, "/register" | "/token" | "/revoke"));
    // Native clients omit Origin. /authorize is deliberately navigable from other sites.
    // A form POST can arrive without a CORS preflight, so OAuth writes need this too.
    if check_origin
        && headers.contains_key(header::ORIGIN)
        && (headers.get_all(header::ORIGIN).iter().count() != 1
            || !headers
                .get(header::ORIGIN)
                .and_then(|origin| origin.to_str().ok())
                .is_some_and(|origin| origin_allowed(origin, gateway.listen_port)))
    {
        return (StatusCode::FORBIDDEN, "unexpected browser origin").into_response();
    }
    next.run(req).await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_only_listener_authorities_and_origins() {
        for host in [
            "127.0.0.1:9086",
            "localhost:9086",
            "LOCALHOST:9086",
            "[::1]:9086",
        ] {
            assert!(authority_allowed(host, 9086));
            assert!(origin_allowed(&format!("http://{host}"), 9086));
        }
        for host in [
            "evil.example:9086",
            "localhost.evil.example:9086",
            "evil@localhost:9086",
            "localhost:8080",
            "localhost",
        ] {
            assert!(!authority_allowed(host, 9086));
        }
        for origin in [
            "null",
            "https://localhost:9086",
            "http://localhost:9086/path",
            "http://localhost:9086?x=1",
            "http://localhost:9086.evil.example",
        ] {
            assert!(!origin_allowed(origin, 9086));
        }
    }
}
