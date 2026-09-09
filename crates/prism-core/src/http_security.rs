//! Browser-facing checks for the listener. These do not replace OAuth.
//!
//! The Host check is what stands between a DNS-rebinding page and the gateway: a browser
//! sends the attacker's name as Host, never an address. On loopback only loopback names pass.
//! Exposed on the network, any IP literal passes as well, since that is what agents elsewhere
//! dial and a name still gives the rebinding away.
use std::sync::Arc;

use axum::extract::{Request, State};
use axum::http::{header, Method, StatusCode};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};

use crate::config::ListenAddress;
use crate::Gateway;

/// The origin the client dialed, `http://host:port`, set once the Host header has passed.
/// OAuth metadata echoes it so a client sees itself as the resource it asked for, whether it
/// came in over loopback or over the network.
#[derive(Debug, Clone)]
pub(crate) struct RequestOrigin(pub(crate) String);

fn ip_literal(host: &str) -> bool {
    host.parse::<std::net::Ipv4Addr>().is_ok()
        || host
            .strip_prefix('[')
            .and_then(|h| h.strip_suffix(']'))
            .is_some_and(|h| h.parse::<std::net::Ipv6Addr>().is_ok())
}

pub(crate) fn authority_allowed(value: &str, port: u16, address: ListenAddress) -> bool {
    let Ok(authority) = value.parse::<http::uri::Authority>() else {
        return false;
    };
    if value.contains('@') || authority.port_u16().unwrap_or(80) != port {
        return false;
    }
    matches!(authority.host(), "127.0.0.1" | "[::1]")
        || authority.host().eq_ignore_ascii_case("localhost")
        || (address == ListenAddress::Network && ip_literal(authority.host()))
}

fn origin_allowed(value: &str, port: u16, address: ListenAddress) -> bool {
    let Ok(uri) = value.parse::<http::Uri>() else {
        return false;
    };
    uri.scheme_str() == Some("http")
        && uri
            .authority()
            .is_some_and(|authority| authority_allowed(authority.as_str(), port, address))
        && uri.path_and_query().is_none_or(|path| path.as_str() == "/")
}

pub(crate) async fn guard(
    State(gateway): State<Arc<Gateway>>,
    mut req: Request,
    next: Next,
) -> Response {
    let port = gateway.listen_port();
    let address = gateway.listen_address();
    let headers = req.headers();
    let host = headers
        .get(header::HOST)
        .filter(|_| headers.get_all(header::HOST).iter().count() == 1)
        .and_then(|host| host.to_str().ok())
        .filter(|host| authority_allowed(host, port, address))
        .map(|host| host.to_ascii_lowercase());
    let Some(host) = host else {
        return (StatusCode::FORBIDDEN, "unexpected gateway host").into_response();
    };

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
                .is_some_and(|origin| origin_allowed(origin, port, address)))
    {
        return (StatusCode::FORBIDDEN, "unexpected browser origin").into_response();
    }
    req.extensions_mut()
        .insert(RequestOrigin(format!("http://{host}")));
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
            assert!(authority_allowed(host, 9086, ListenAddress::Loopback));
            assert!(origin_allowed(
                &format!("http://{host}"),
                9086,
                ListenAddress::Loopback
            ));
        }
        for host in [
            "evil.example:9086",
            "localhost.evil.example:9086",
            "evil@localhost:9086",
            "localhost:8080",
            "localhost",
        ] {
            assert!(!authority_allowed(host, 9086, ListenAddress::Loopback));
        }
        for origin in [
            "null",
            "https://localhost:9086",
            "http://localhost:9086/path",
            "http://localhost:9086?x=1",
            "http://localhost:9086.evil.example",
        ] {
            assert!(!origin_allowed(origin, 9086, ListenAddress::Loopback));
        }
    }

    #[test]
    fn on_the_network_addresses_pass_and_names_still_do_not() {
        for host in ["192.168.1.20:9086", "10.0.0.1:9086", "[fe80::1]:9086"] {
            assert!(!authority_allowed(host, 9086, ListenAddress::Loopback));
            assert!(authority_allowed(host, 9086, ListenAddress::Network));
            assert!(origin_allowed(
                &format!("http://{host}"),
                9086,
                ListenAddress::Network
            ));
            assert!(!authority_allowed(host, 9087, ListenAddress::Network));
        }
        for host in [
            "prism.example:9086",
            "evil@10.0.0.1:9086",
            "fe80::1:9086",
            "192.168.1.20",
        ] {
            assert!(!authority_allowed(host, 9086, ListenAddress::Network));
        }
        assert!(!origin_allowed(
            "https://10.0.0.1:9086",
            9086,
            ListenAddress::Network
        ));
    }
}
