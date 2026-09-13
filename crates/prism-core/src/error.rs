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
    /// A remote server requires authentication; the hint describes the recovery.
    #[error("sign-in required")]
    SignInRequired(crate::backend::AuthHint),
    /// The edit is durable and active; callers must not offer to repeat the save.
    #[error("server saved, but old credentials could not be fully removed from the keyring")]
    ServerUpdatedCleanupFailed,
}

pub type Result<T> = std::result::Result<T, Error>;
