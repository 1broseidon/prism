use std::collections::VecDeque;
use std::fs::{File, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::Mutex;

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use tracing::error;

use crate::error::Result;
use crate::events::{EventSender, GatewayEvent};

const RING_CAP: usize = 1000;

/// How a call was resolved.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum AuditVerdict {
    Allowed,
    Denied,
    Timeout,
    Error,
}

/// Who or what produced the verdict.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum AuditSource {
    Rule {
        rule_id: String,
    },
    Human,
    Timeout,
    /// The agent has not been approved in the panel yet.
    Unapproved,
}

/// One audited tool-call attempt.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct AuditEntry {
    pub id: String,
    pub at: DateTime<Utc>,
    pub agent_id: String,
    pub agent_name: String,
    pub server_id: String,
    pub tool: String,
    pub verdict: AuditVerdict,
    pub source: AuditSource,
    pub duration_ms: u64,
    pub error: Option<String>,
}

/// In-memory ring of the last 1000 entries plus an append-only JSONL file.
pub struct AuditLog {
    path: PathBuf,
    ring: Mutex<VecDeque<AuditEntry>>,
    file: Mutex<File>,
    events: EventSender,
}

impl AuditLog {
    pub fn new(path: impl AsRef<Path>, events: EventSender) -> Result<Self> {
        let path = path.as_ref().to_path_buf();
        if let Some(parent) = path.parent() {
            if !parent.as_os_str().is_empty() {
                std::fs::create_dir_all(parent)?;
            }
        }
        let file = OpenOptions::new().create(true).append(true).open(&path)?;
        Ok(Self {
            path,
            ring: Mutex::new(VecDeque::with_capacity(RING_CAP)),
            file: Mutex::new(file),
            events,
        })
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn record(&self, entry: AuditEntry) {
        if let Ok(mut ring) = self.ring.lock() {
            if ring.len() == RING_CAP {
                ring.pop_front();
            }
            ring.push_back(entry.clone());
        }
        match serde_json::to_string(&entry) {
            Ok(line) => {
                if let Ok(mut file) = self.file.lock() {
                    if writeln!(file, "{line}").is_err() {
                        error!("failed to append audit jsonl");
                    }
                }
            }
            Err(err) => error!(%err, "failed to serialize audit entry"),
        }
        let _ = self.events.send(GatewayEvent::Audit(entry));
    }

    pub fn list(&self, limit: usize) -> Vec<AuditEntry> {
        let Ok(ring) = self.ring.lock() else {
            return Vec::new();
        };
        ring.iter().rev().take(limit).cloned().collect()
    }
}
