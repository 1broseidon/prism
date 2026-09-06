//! What agents did over the last few days, summed up: how much, how much needed a person, who,
//! and when. The panel shows this instead of a list nobody reads; the list is one tap away.

use std::collections::HashMap;

use chrono::{DateTime, Local, NaiveDate, Utc};
use serde::Serialize;

use crate::audit::{AuditEntry, AuditSource, AuditVerdict};

#[derive(Debug, Clone, Serialize)]
pub struct ActivitySummary {
    pub days: u32,
    /// Every action in the window, MCP and native, minus MCP calls seen twice through a hook.
    pub total: usize,
    /// The part of `total` that needed a person; see [`needs_attention`].
    pub attention: usize,
    pub mcp: McpCounts,
    /// Busiest first.
    pub agents: Vec<AgentActivity>,
    /// Oldest first, one entry per local calendar day, today last.
    pub daily: Vec<DayActivity>,
}

#[derive(Debug, Clone, Default, Serialize)]
pub struct McpCounts {
    pub allowed: usize,
    pub denied: usize,
    /// Calls that were held for a person, whatever the answer was.
    pub asked: usize,
    pub errors: usize,
}

#[derive(Debug, Clone, Serialize)]
pub struct AgentActivity {
    pub id: String,
    pub name: String,
    /// An agent host seen through its hook rather than an MCP client.
    pub host: bool,
    pub total: usize,
    pub attention: usize,
}

#[derive(Debug, Clone, Serialize)]
pub struct DayActivity {
    pub date: NaiveDate,
    pub routine: usize,
    pub attention: usize,
}

/// Whether the entry needed a person: a held call that you or the clock decided, a denial from
/// any source, or a native action the shadow list would have asked about. Everything else is
/// routine. "Routine" is not "safe": Prism only knows what its rules and deny list know.
pub fn needs_attention(entry: &AuditEntry) -> bool {
    if let Some(native) = &entry.native {
        return native.would_hold.is_some();
    }
    matches!(entry.source, AuditSource::Human | AuditSource::Timeout)
        || matches!(entry.verdict, AuditVerdict::Denied)
}

/// Sum the entries that fall in the last `days` local calendar days, today included.
pub fn summarize<'a>(
    entries: impl IntoIterator<Item = &'a AuditEntry>,
    days: u32,
    now: DateTime<Utc>,
) -> ActivitySummary {
    let days = days.max(1);
    let today = now.with_timezone(&Local).date_naive();
    let first = today - chrono::Duration::days(i64::from(days) - 1);
    let mut daily: Vec<DayActivity> = (0..days)
        .map(|i| DayActivity {
            date: first + chrono::Duration::days(i64::from(i)),
            routine: 0,
            attention: 0,
        })
        .collect();
    let mut total = 0;
    let mut attention = 0;
    let mut mcp = McpCounts::default();
    let mut agents: HashMap<&str, AgentActivity> = HashMap::new();

    for entry in entries {
        let date = entry.at.with_timezone(&Local).date_naive();
        if date < first || date > today {
            continue;
        }
        if entry.native.as_ref().is_some_and(|n| n.via_prism) {
            continue;
        }
        let flagged = needs_attention(entry);
        total += 1;
        if flagged {
            attention += 1;
        }
        if entry.native.is_none() {
            match entry.verdict {
                AuditVerdict::Allowed => mcp.allowed += 1,
                AuditVerdict::Denied => mcp.denied += 1,
                AuditVerdict::Timeout => {}
                AuditVerdict::Error => mcp.errors += 1,
            }
            if matches!(entry.source, AuditSource::Human | AuditSource::Timeout) {
                mcp.asked += 1;
            }
        }
        let day = &mut daily[(date - first).num_days() as usize];
        if flagged {
            day.attention += 1;
        } else {
            day.routine += 1;
        }
        let agent = agents
            .entry(entry.agent_id.as_str())
            .or_insert_with(|| AgentActivity {
                id: entry.agent_id.clone(),
                name: entry.agent_name.clone(),
                host: entry.native.is_some(),
                total: 0,
                attention: 0,
            });
        agent.total += 1;
        if flagged {
            agent.attention += 1;
        }
    }

    let mut agents: Vec<AgentActivity> = agents.into_values().collect();
    agents.sort_by(|a, b| b.total.cmp(&a.total).then_with(|| a.name.cmp(&b.name)));
    ActivitySummary {
        days,
        total,
        attention,
        mcp,
        agents,
        daily,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry(
        at: DateTime<Utc>,
        agent: &str,
        verdict: &str,
        source: &str,
        native: Option<serde_json::Value>,
    ) -> AuditEntry {
        serde_json::from_value(serde_json::json!({
            "id": uuid::Uuid::new_v4().to_string(),
            "at": at,
            "agent_id": agent,
            "agent_name": agent,
            "server_id": "srv",
            "tool": "t",
            "verdict": verdict,
            "source": if source == "rule" { serde_json::json!({ "kind": "rule", "rule_id": "r1" }) } else { serde_json::json!({ "kind": source }) },
            "duration_ms": 1,
            "error": null,
            "attention": "silent",
            "native": native,
        }))
        .expect("entry shape")
    }

    fn native(would_hold: Option<&str>, via_prism: bool) -> serde_json::Value {
        serde_json::json!({
            "host": "codex", "subject": "ls", "would_hold": would_hold, "via_prism": via_prism
        })
    }

    #[test]
    fn sums_by_day_agent_and_kind() {
        let now = Utc::now();
        let entries = [
            entry(now, "a", "allowed", "rule", None),
            entry(now, "a", "denied", "rule", None),
            entry(now, "b", "allowed", "human", None),
            entry(
                now,
                "host:codex",
                "allowed",
                "observed",
                Some(native(None, false)),
            ),
            entry(
                now,
                "host:codex",
                "allowed",
                "observed",
                Some(native(Some("sudo"), false)),
            ),
            entry(
                now,
                "host:codex",
                "allowed",
                "observed",
                Some(native(None, true)),
            ),
            entry(
                now - chrono::Duration::days(30),
                "a",
                "allowed",
                "rule",
                None,
            ),
        ];
        let s = summarize(entries.iter(), 7, now);
        assert_eq!(s.total, 5);
        assert_eq!(s.attention, 3);
        assert_eq!((s.mcp.allowed, s.mcp.denied, s.mcp.asked), (2, 1, 1));
        assert_eq!(s.daily.len(), 7);
        assert_eq!(s.daily[6].routine + s.daily[6].attention, 5);
        assert_eq!(s.daily[0].routine + s.daily[0].attention, 0);
        assert_eq!(s.agents[0].id, "a");
        assert_eq!(s.agents[0].total, 2);
        assert_eq!(s.agents[1].id, "host:codex");
        assert!(s.agents[1].host);
        assert_eq!(s.agents[1].attention, 1);
    }
}
