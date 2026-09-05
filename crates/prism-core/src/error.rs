use thiserror::Error;

/// Errors returned by the gateway and its supporting modules.
#[derive(Debug, Error)]
pub enum Error {
    #[error("io error: {0}")]
    Io(#[from] std::io::Error),
    #[error("json error: {0}")]
    Json(#[from] serde_json::Error),
    #[error("not found: {0}")]
    NotFound(String),
    #[error("already exists: {0}")]
    AlreadyExists(String),
    #[error("invalid argument: {0}")]
    Invalid(String),
    #[error("too many requests: {0}")]
    RateLimited(&'static str),
    #[error("backend error: {0}")]
    Backend(String),
    #[error("gateway error: {0}")]
    Gateway(String),
}

pub type Result<T> = std::result::Result<T, Error>;
