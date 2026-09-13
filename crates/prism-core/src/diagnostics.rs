//! Safe startup diagnostics. Provider text, headers and URLs never enter this type.
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ConnectionFailureKind {
    Protocol,
    HostRejected,
    Forbidden,
    Http,
    Rpc,
    InvalidResponse,
    Timeout,
    Unreachable,
    Closed,
    Launch,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ConnectionFailure {
    pub category: ConnectionFailureKind,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub http_status: Option<u16>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub rpc_code: Option<i32>,
}

impl ConnectionFailure {
    pub(crate) fn new(category: ConnectionFailureKind) -> Self {
        Self {
            category,
            http_status: None,
            rpc_code: None,
        }
    }

    pub(crate) fn http(status: u16) -> Self {
        let category = match status {
            421 => ConnectionFailureKind::HostRejected,
            403 => ConnectionFailureKind::Forbidden,
            408 | 504 => ConnectionFailureKind::Timeout,
            _ => ConnectionFailureKind::Http,
        };
        Self {
            http_status: Some(status),
            ..Self::new(category)
        }
    }

    pub(crate) fn rpc(code: i32) -> Self {
        let category = if matches!(code, -32600 | -32601) {
            ConnectionFailureKind::Protocol
        } else {
            ConnectionFailureKind::Rpc
        };
        Self {
            rpc_code: Some(code),
            ..Self::new(category)
        }
    }

    /// Keep the last attempted protocol's cause; discovery rejection may be normal on older servers.
    pub(crate) fn initialize(error: &rmcp::service::ClientInitializeError) -> Self {
        use rmcp::service::ClientInitializeError as E;
        use rmcp::transport::streamable_http_client::{
            InsufficientScopeError, StreamableHttpError,
        };
        match error {
            E::LegacyFallbackFailed { fallback, .. } => Self::initialize(fallback),
            E::JsonRpcError(error) => Self::rpc(error.code.0),
            E::NoCompatibleProtocolVersion { .. } => Self::new(ConnectionFailureKind::Protocol),
            E::ConnectionClosed(_) => Self::new(ConnectionFailureKind::Closed),
            E::TransportError { error, .. } => {
                let mut source: Option<&(dyn std::error::Error + 'static)> =
                    Some(error.error.as_ref());
                while let Some(error) = source {
                    if error.is::<InsufficientScopeError>() {
                        return Self::http(403);
                    }
                    if let Some(StreamableHttpError::Client(error)) =
                        error.downcast_ref::<StreamableHttpError<reqwest::Error>>()
                    {
                        return Self::request(error);
                    }
                    if let Some(error) = error.downcast_ref::<reqwest::Error>() {
                        return Self::request(error);
                    }
                    source = error.source();
                }
                Self::new(ConnectionFailureKind::InvalidResponse)
            }
            _ => Self::new(ConnectionFailureKind::InvalidResponse),
        }
    }

    pub(crate) fn request(error: &reqwest::Error) -> Self {
        if let Some(status) = error.status() {
            return Self::http(status.as_u16());
        }
        Self::new(if error.is_timeout() {
            ConnectionFailureKind::Timeout
        } else if error.is_connect() {
            ConnectionFailureKind::Unreachable
        } else {
            ConnectionFailureKind::InvalidResponse
        })
    }
}

impl std::fmt::Display for ConnectionFailure {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        use ConnectionFailureKind::*;
        let message = match self.category {
            Protocol => "MCP initialization was rejected. Check protocol compatibility and update the server or Prism.",
            HostRejected => "The server rejected the requested host. Check the endpoint and the server's Host allowlist.",
            Forbidden => "The server refused access. Check this credential's permissions and the server's access policy.",
            Http => "The endpoint returned an HTTP error. Check the server or proxy configuration.",
            Rpc => "The server rejected MCP initialization. Check its configuration and supported MCP methods.",
            InvalidResponse => "The server did not return a valid MCP response. Check the endpoint or command; command servers must reserve stdout for MCP.",
            Timeout => "The server did not finish MCP initialization in time. Check that it is reachable and ready, then retry.",
            Unreachable => "Could not connect to the server. Check its address, network access and TLS configuration.",
            Closed => "The server closed the connection during MCP initialization. Check its launch settings or endpoint.",
            Launch => "Could not start the command server. Check that its executable is installed and can be run.",
        };
        if let Some(status) = self.http_status {
            write!(f, "HTTP {status}. ")?;
        }
        if let Some(code) = self.rpc_code {
            write!(f, "JSON-RPC {code}. ")?;
        }
        f.write_str(message)
    }
}

impl std::error::Error for ConnectionFailure {}
