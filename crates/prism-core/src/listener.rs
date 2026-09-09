//! The loopback listener agents dial. It is bound before the gateway reports started, a port
//! clash is reported as state rather than hidden in a log line, and the operator can move it
//! to another port without restarting the app.
use std::net::{IpAddr, SocketAddr};
use std::sync::atomic::{AtomicBool, AtomicU16, Ordering};
use std::sync::Mutex;
use std::time::Duration;

use serde::Serialize;
use tokio_util::sync::CancellationToken;

use crate::config::ListenAddress;

/// Ports tried, starting one above the configured port, when the panel asks for a free one.
const SUGGEST_SPAN: u16 = 64;

/// Whether agents can reach the gateway right now, and if not, why.
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum ListenerState {
    Listening,
    /// Another process holds the configured port. `holder` names it when Prism can tell.
    PortInUse {
        port: u16,
        holder: Option<PortHolder>,
    },
    /// The bind failed for a reason other than a clash (a firewall, a port below 1024).
    Failed {
        port: u16,
        error: String,
    },
    Stopped,
}

/// What Prism could recognise on a port it failed to bind.
#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum PortHolder {
    /// The port answers with Prism's own OAuth metadata: a second copy of the app, most often
    /// a dev build next to the installed one.
    Prism,
}

/// Why a bind did not happen, before it is turned into state or an error.
#[derive(Debug)]
pub(crate) enum BindFailure {
    InUse { holder: Option<PortHolder> },
    Other(String),
}

impl BindFailure {
    pub(crate) async fn from_io(port: u16, err: std::io::Error) -> Self {
        if err.kind() == std::io::ErrorKind::AddrInUse {
            Self::InUse {
                holder: identify_holder(port).await,
            }
        } else {
            Self::Other(err.to_string())
        }
    }

    pub(crate) fn state(&self, port: u16) -> ListenerState {
        match self {
            Self::InUse { holder } => ListenerState::PortInUse {
                port,
                holder: *holder,
            },
            Self::Other(error) => ListenerState::Failed {
                port,
                error: error.clone(),
            },
        }
    }

    /// The operator-facing sentence for a bind that was asked for and refused.
    pub(crate) fn message(&self, port: u16) -> String {
        match self {
            Self::InUse {
                holder: Some(PortHolder::Prism),
            } => format!("Port {port} is in use by another copy of Prism"),
            Self::InUse { holder: None } => format!("Port {port} is in use"),
            Self::Other(error) => format!("Port {port} could not be opened: {error}"),
        }
    }
}

/// The bound port and the handle that stops its server. One per gateway; swapped on a move.
pub(crate) struct Listener {
    port: AtomicU16,
    /// Bound on every interface, not only loopback.
    exposed: AtomicBool,
    state: Mutex<ListenerState>,
    serving: Mutex<Option<Serving>>,
}

/// The live server: what stops it and the task to wait on once it is stopped.
pub(crate) struct Serving {
    pub(crate) stop: CancellationToken,
    pub(crate) task: tokio::task::JoinHandle<()>,
}

impl Listener {
    /// A listener that has not bound anything. `port` is what status reports meanwhile.
    pub(crate) fn idle(port: u16) -> Self {
        Self {
            port: AtomicU16::new(port),
            exposed: AtomicBool::new(false),
            state: Mutex::new(ListenerState::Stopped),
            serving: Mutex::new(None),
        }
    }

    /// The port agents dial: the bound one while listening, otherwise the configured one.
    pub(crate) fn port(&self) -> u16 {
        self.port.load(Ordering::Acquire)
    }

    /// Where the live server is bound.
    pub(crate) fn address(&self) -> ListenAddress {
        if self.exposed.load(Ordering::Acquire) {
            ListenAddress::Network
        } else {
            ListenAddress::Loopback
        }
    }

    pub(crate) fn state(&self) -> ListenerState {
        self.state
            .lock()
            .map(|s| s.clone())
            .unwrap_or(ListenerState::Stopped)
    }

    pub(crate) fn is_listening(&self) -> bool {
        self.state() == ListenerState::Listening
    }

    pub(crate) fn set_state(&self, state: ListenerState) {
        if let Ok(mut current) = self.state.lock() {
            *current = state;
        }
    }

    /// Record a new server as the live one and stop the previous one, if any.
    pub(crate) fn adopt(&self, address: ListenAddress, port: u16, serving: Serving) {
        self.port.store(port, Ordering::Release);
        self.exposed
            .store(address == ListenAddress::Network, Ordering::Release);
        self.set_state(ListenerState::Listening);
        let previous = self
            .serving
            .lock()
            .ok()
            .and_then(|mut slot| slot.replace(serving));
        if let Some(previous) = previous {
            previous.stop.cancel();
        }
    }

    /// Stop the live server and hand back its task, so the caller can wait for the port to
    /// be free again. Status reads as stopped until something is adopted.
    pub(crate) fn release(&self) -> Option<Serving> {
        let previous = self.serving.lock().ok().and_then(|mut slot| slot.take());
        if let Some(previous) = &previous {
            previous.stop.cancel();
            self.set_state(ListenerState::Stopped);
        }
        previous
    }
}

pub(crate) fn loopback(port: u16) -> SocketAddr {
    SocketAddr::from(([127, 0, 0, 1], port))
}

pub(crate) fn socket(address: ListenAddress, port: u16) -> SocketAddr {
    SocketAddr::new(address.ip(), port)
}

/// The address other machines on the network reach this one at: the source address of the
/// default route. Connecting a UDP socket sends nothing; it only asks the routing table.
/// `None` when there is no route out, which is when there is no network to expose to.
pub(crate) fn host_ip() -> Option<IpAddr> {
    let socket = std::net::UdpSocket::bind("0.0.0.0:0").ok()?;
    socket.connect("192.0.2.1:9").ok()?;
    let ip = socket.local_addr().ok()?.ip();
    (!ip.is_loopback() && !ip.is_unspecified()).then_some(ip)
}

/// Ask the port what it is. Prism answers its own OAuth metadata with itself as the issuer;
/// anything else, or no answer within a second, is an unknown program.
async fn identify_holder(port: u16) -> Option<PortHolder> {
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(1))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .ok()?;
    let url = format!("http://127.0.0.1:{port}/.well-known/oauth-authorization-server");
    let body: serde_json::Value = client.get(url).send().await.ok()?.json().await.ok()?;
    let issuer = body.get("issuer")?.as_str()?;
    (issuer == format!("http://127.0.0.1:{port}")).then_some(PortHolder::Prism)
}

/// The first free port above `from`, for the panel's "Use another port" suggestion. Probing
/// binds and releases each candidate, so the answer is a suggestion, not a reservation.
pub(crate) fn suggest_port(from: u16) -> Option<u16> {
    (1..=SUGGEST_SPAN)
        .filter_map(|offset| from.checked_add(offset))
        .find(|port| std::net::TcpListener::bind(loopback(*port)).is_ok())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn suggests_the_next_free_port() {
        let taken = std::net::TcpListener::bind(loopback(0)).unwrap();
        let port = taken.local_addr().unwrap().port();
        // Occupy the next one as well, so the suggestion has to skip it.
        let next = std::net::TcpListener::bind(loopback(port + 1)).ok();
        let suggested = suggest_port(port).unwrap();
        assert!(suggested > port);
        if next.is_some() {
            assert!(suggested > port + 1);
        }
        assert!(std::net::TcpListener::bind(loopback(suggested)).is_ok());
    }

    #[test]
    fn suggestion_stops_at_the_end_of_the_range() {
        assert_eq!(suggest_port(u16::MAX), None);
    }

    #[tokio::test]
    async fn a_plain_socket_is_an_unknown_holder() {
        let taken = std::net::TcpListener::bind(loopback(0)).unwrap();
        let port = taken.local_addr().unwrap().port();
        let failure = BindFailure::from_io(
            port,
            std::io::Error::new(std::io::ErrorKind::AddrInUse, "taken"),
        )
        .await;
        assert!(matches!(failure, BindFailure::InUse { holder: None }));
        assert_eq!(failure.message(port), format!("Port {port} is in use"));
        assert_eq!(
            failure.state(port),
            ListenerState::PortInUse { port, holder: None }
        );
    }

    #[tokio::test]
    async fn other_errors_keep_their_text() {
        let failure = BindFailure::from_io(
            80,
            std::io::Error::new(std::io::ErrorKind::PermissionDenied, "permission denied"),
        )
        .await;
        assert_eq!(
            failure.message(80),
            "Port 80 could not be opened: permission denied"
        );
        assert!(matches!(
            failure.state(80),
            ListenerState::Failed { port: 80, .. }
        ));
    }
}
