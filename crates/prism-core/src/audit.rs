use std::collections::VecDeque;
use std::fs::{self, File};
use std::io::{BufRead, BufReader, Read, Write};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use tracing::error;

use crate::config::{Attention, Posture};
use crate::error::Result;
use crate::events::{EventSender, GatewayEvent};

const RING_CAP: usize = 1000;
const MAX_FILE_BYTES: u64 = 5 * 1024 * 1024;
const ARCHIVES: usize = 3;
const RETENTION_DAYS: i64 = 30;
const MAX_RECORD_BYTES: usize = 32 * 1024;
const REDACTED_ERROR: &str = "Error details omitted to protect credentials";

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
    /// No rule matched; the agent's posture decided.
    Posture {
        posture: Posture,
    },
    /// Do-not-disturb was on, so the call resolved by the timeout behaviour without a hold.
    DoNotDisturb,
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
    /// How loudly the desktop should surface this entry. Silent for anything a human already saw.
    #[serde(default)]
    pub attention: Attention,
}

/// In-memory ring plus private, redacted, size- and age-bounded JSONL files.
pub struct AuditLog {
    path: PathBuf,
    ring: Mutex<VecDeque<AuditEntry>>,
    writer: Mutex<AuditWriter>,
    events: EventSender,
}

struct AuditWriter {
    file: Option<File>,
    bytes: u64,
    cleaned_at: Instant,
}

fn archive(path: &Path, index: usize) -> PathBuf {
    let mut name = path.as_os_str().to_os_string();
    name.push(format!(".{index}"));
    PathBuf::from(name)
}

fn sanitize(entry: &mut AuditEntry) {
    // Raw backend errors may echo arguments, URLs, tokens, or complete response bodies.
    // Omit them instead of relying on a list of recognizable secret formats.
    if entry.error.is_some() {
        entry.error = Some(REDACTED_ERROR.into());
    }
    for value in [
        &mut entry.agent_name,
        &mut entry.tool,
        &mut entry.agent_id,
        &mut entry.server_id,
    ] {
        *value = value.chars().take(512).collect();
    }
}

/// Also rewrites legacy logs so old error payloads do not remain in archives.
fn retain_file(path: &Path, now: DateTime<Utc>, max_bytes: u64) -> std::io::Result<()> {
    if !path.try_exists()? {
        return Ok(());
    }
    let mut reader = BufReader::new(crate::storage::read(path)?);
    let cutoff = now - chrono::Duration::days(RETENTION_DAYS);
    let mut retained = VecDeque::new();
    let mut total = 0usize;
    loop {
        let mut line = Vec::new();
        let read = (&mut reader)
            .take(MAX_RECORD_BYTES as u64 + 1)
            .read_until(b'\n', &mut line)?;
        if read == 0 {
            break;
        }
        if line.len() > MAX_RECORD_BYTES {
            if line.last() != Some(&b'\n') {
                reader.skip_until(b'\n')?;
            }
            continue;
        }
        let Ok(mut entry) = serde_json::from_slice::<AuditEntry>(&line) else {
            continue;
        };
        if entry.at < cutoff {
            continue;
        }
        sanitize(&mut entry);
        let mut line = serde_json::to_vec(&entry)?;
        line.push(b'\n');
        if line.len() as u64 > max_bytes {
            continue;
        }
        total += line.len();
        retained.push_back(line);
        while total as u64 > max_bytes {
            total -= retained
                .pop_front()
                .expect("nonempty retained entries")
                .len();
        }
    }
    drop(reader);
    crate::storage::atomic_write(path, &retained.into_iter().flatten().collect::<Vec<_>>())
}

fn maintain(path: &Path) -> std::io::Result<()> {
    let now = Utc::now();
    for index in 0..=ARCHIVES {
        let file = if index == 0 {
            path.to_path_buf()
        } else {
            archive(path, index)
        };
        retain_file(&file, now, MAX_FILE_BYTES)?;
        if index != 0 && file.try_exists()? && fs::metadata(&file)?.len() == 0 {
            fs::remove_file(file)?;
        }
    }
    Ok(())
}

fn rotate(path: &Path) -> std::io::Result<()> {
    for index in (1..=ARCHIVES).rev() {
        let target = archive(path, index);
        let source = if index == 1 {
            path.to_path_buf()
        } else {
            archive(path, index - 1)
        };
        crate::storage::prepare(&target)?;
        if target.try_exists()? {
            fs::remove_file(&target)?;
        }
        if source.try_exists()? {
            crate::storage::protect(&source)?;
            fs::rename(source, target)?;
        }
    }
    Ok(())
}

impl AuditLog {
    pub fn new(path: impl AsRef<Path>, events: EventSender) -> Result<Self> {
        let path = path.as_ref().to_path_buf();
        crate::storage::prepare(&path)?;
        maintain(&path)?;
        let file = crate::storage::append(&path)?;
        let bytes = file.metadata()?.len();
        Ok(Self {
            path,
            ring: Mutex::new(VecDeque::with_capacity(RING_CAP)),
            writer: Mutex::new(AuditWriter {
                file: Some(file),
                bytes,
                cleaned_at: Instant::now(),
            }),
            events,
        })
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    /// Release the file handle. A stopped gateway must not pin the log: on Windows an open handle
    /// makes the next start's retention rewrite fail with "access denied". Recording reopens it.
    pub(crate) fn close(&self) {
        if let Ok(mut writer) = self.writer.lock() {
            writer.file.take();
        }
    }

    pub(crate) fn cleanup(&self) -> std::io::Result<()> {
        if let Ok(mut writer) = self.writer.lock() {
            writer.file.take();
            maintain(&self.path)?;
            let file = crate::storage::append(&self.path)?;
            writer.bytes = file.metadata()?.len();
            writer.file = Some(file);
            writer.cleaned_at = Instant::now();
        }
        if let Ok(mut ring) = self.ring.lock() {
            let cutoff = Utc::now() - chrono::Duration::days(RETENTION_DAYS);
            ring.retain(|entry| entry.at >= cutoff);
        }
        Ok(())
    }

    pub fn record(&self, mut entry: AuditEntry) {
        sanitize(&mut entry);
        if let Ok(mut ring) = self.ring.lock() {
            if ring.len() == RING_CAP {
                ring.pop_front();
            }
            ring.push_back(entry.clone());
        }
        match serde_json::to_string(&entry) {
            Ok(line) => {
                if line.len() >= MAX_RECORD_BYTES {
                    error!("audit entry exceeds size limit");
                } else if let Ok(mut writer) = self.writer.lock() {
                    if self.append_line(&mut writer, &line).is_err() {
                        error!("failed to append private audit log");
                    }
                }
            }
            Err(err) => error!(%err, "failed to serialize audit entry"),
        }
        let _ = self.events.send(GatewayEvent::Audit(entry));
    }

    fn append_line(&self, writer: &mut AuditWriter, line: &str) -> std::io::Result<()> {
        if writer.cleaned_at.elapsed() >= Duration::from_secs(60 * 60) {
            writer.file.take();
            maintain(&self.path)?;
            writer.cleaned_at = Instant::now();
        }
        if writer.file.is_none() {
            let file = crate::storage::append(&self.path)?;
            writer.bytes = file.metadata()?.len();
            writer.file = Some(file);
        }
        if writer.bytes + line.len() as u64 + 1 > MAX_FILE_BYTES {
            writer.file.take();
            rotate(&self.path)?;
            writer.file = Some(crate::storage::append(&self.path)?);
            writer.bytes = 0;
        }
        writeln!(writer.file.as_mut().expect("opened audit log"), "{line}")?;
        writer.bytes += line.len() as u64 + 1;
        Ok(())
    }

    pub fn list(&self, limit: usize) -> Vec<AuditEntry> {
        let Ok(ring) = self.ring.lock() else {
            return Vec::new();
        };
        ring.iter().rev().take(limit).cloned().collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry(at: DateTime<Utc>, id: &str) -> AuditEntry {
        AuditEntry {
            id: id.into(),
            at,
            agent_id: "a".into(),
            agent_name: "agent".into(),
            server_id: "s".into(),
            tool: "read".into(),
            verdict: AuditVerdict::Error,
            source: AuditSource::Human,
            duration_ms: 1,
            error: Some("Bearer secret-token password=hidden".into()),
            attention: Attention::Silent,
        }
    }

    #[test]
    fn redacts_legacy_and_new_errors_and_expires_old_entries() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("audit.jsonl");
        let old = entry(Utc::now() - chrono::Duration::days(31), "expired");
        let recent = entry(Utc::now(), "retained");
        let bytes = format!(
            "{}\n{}\n",
            serde_json::to_string(&old).unwrap(),
            serde_json::to_string(&recent).unwrap()
        );
        fs::write(&path, &bytes).unwrap();
        fs::write(archive(&path, 1), &bytes).unwrap();
        let (events, _) = crate::events::channel();
        let audit = AuditLog::new(&path, events).unwrap();
        audit.record(entry(Utc::now(), "new"));
        for path in [&path, &archive(&path, 1)] {
            let text = fs::read_to_string(path).unwrap();
            assert!(!text.contains("secret-token"));
            assert!(!text.contains("hidden"));
            assert!(!text.contains("expired"));
            assert!(text.contains("retained"));
        }
        assert_eq!(audit.list(1)[0].error.as_deref(), Some(REDACTED_ERROR));
        // Age retention works while the application remains open, even without new calls.
        audit.record(old);
        audit.cleanup().unwrap();
        assert!(!fs::read_to_string(&path).unwrap().contains("expired"));
        assert!(audit.list(10).iter().all(|entry| entry.id != "expired"));
    }

    #[test]
    fn rotates_at_the_limit_and_keeps_only_three_archives() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("audit.jsonl");
        let (events, _) = crate::events::channel();
        let audit = AuditLog::new(&path, events).unwrap();
        for index in 0..6 {
            audit.record(entry(Utc::now(), &format!("batch-{index}")));
            // Set the actual on-disk length and accounting to the boundary, then append. A separate
            // write handle does the extending: on Windows an append-only handle may not set_len.
            let mut writer = audit.writer.lock().unwrap();
            fs::OpenOptions::new()
                .write(true)
                .open(&path)
                .unwrap()
                .set_len(MAX_FILE_BYTES)
                .unwrap();
            writer.bytes = MAX_FILE_BYTES;
        }
        audit.record(entry(Utc::now(), "last"));
        assert!(fs::metadata(&path).unwrap().len() <= MAX_FILE_BYTES);
        for index in 1..=ARCHIVES {
            assert!(fs::metadata(archive(&path, index)).unwrap().len() <= MAX_FILE_BYTES);
        }
        assert!(!archive(&path, ARCHIVES + 1).exists());
        assert!(fs::read_to_string(archive(&path, 3))
            .unwrap()
            .contains("batch-3"));
        assert!(fs::read_to_string(&path).unwrap().contains("last"));
    }
}
