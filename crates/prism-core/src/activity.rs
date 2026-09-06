//! What agents did over the last few days, summed up: how much, how much needed a person, who,
//! and when. The panel shows this instead of a list nobody reads; the list is one tap away.

use std::collections::{HashMap, HashSet};

use chrono::{DateTime, Local, NaiveDate, Utc};
use serde::Serialize;

use crate::audit::{
    canonical_agent_id_excluding, AuditEntry, AuditSource, AuditVerdict, AuditWindow,
};

#[derive(Debug, Clone, Serialize)]
pub struct ActivitySummary {
    pub days: u32,
    pub window: AuditWindow,
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
    summarize_with_exclusions(entries, days, now, &HashSet::new())
}

/// Keep known manual registrations separate while grouping historical harness registrations.
/// Exclusions affect presentation only; the entries' authenticated ids are never modified.
pub(crate) fn summarize_with_exclusions<'a>(
    entries: impl IntoIterator<Item = &'a AuditEntry>,
    days: u32,
    now: DateTime<Utc>,
    exclusions: &HashSet<String>,
) -> ActivitySummary {
    let mut window = AuditWindow::new(days, now);
    let days = window.days;
    let first = window.first_day;
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
    let mut agents: HashMap<String, AgentActivity> = HashMap::new();

    for entry in entries {
        let date = entry.at.with_timezone(&Local).date_naive();
        if !window.contains(entry) {
            continue;
        }
        if entry.native.as_ref().is_some_and(|n| n.via_prism) {
            continue;
        }
        window.oldest_available_at = Some(
            window
                .oldest_available_at
                .map_or(entry.at, |at| at.min(entry.at)),
        );
        window.newest_available_at = Some(
            window
                .newest_available_at
                .map_or(entry.at, |at| at.max(entry.at)),
        );
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
        let id = canonical_agent_id_excluding(entry, exclusions).into_owned();
        let host = id.strip_prefix("host:").and_then(|id| {
            crate::native::harness_for_client_name(id.split('@').next().unwrap_or(id))
        });
        let name = match host {
            Some(host) if !id.contains('@') => {
                crate::native::harness_display_name(host).to_string()
            }
            _ => entry.agent_name.clone(),
        };
        let agent = agents.entry(id.clone()).or_insert_with(|| AgentActivity {
            id,
            name,
            host: host.is_some() || entry.native.is_some(),
            total: 0,
            attention: 0,
        });
        agent.host |= entry.native.is_some();
        agent.total += 1;
        if flagged {
            agent.attention += 1;
        }
    }

    let mut agents: Vec<AgentActivity> = agents.into_values().collect();
    agents.sort_by(|a, b| {
        b.total
            .cmp(&a.total)
            .then_with(|| a.name.cmp(&b.name))
            .then_with(|| a.id.cmp(&b.id))
    });
    ActivitySummary {
        days,
        window,
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
        let mcp = s.agents.iter().find(|agent| agent.id == "a").unwrap();
        assert_eq!(mcp.total, 2);
        let host = s
            .agents
            .iter()
            .find(|agent| agent.id == "host:codex")
            .unwrap();
        assert!(host.host);
        assert_eq!(host.attention, 1);
    }

    #[test]
    fn local_calendar_bounds_and_legacy_harness_rows_agree_with_queries() {
        use crate::audit::AuditQuery;
        use chrono::TimeZone;
        let now = Local
            .with_ymd_and_hms(2026, 9, 6, 12, 0, 0)
            .unwrap()
            .with_timezone(&Utc);
        let first = Local
            .with_ymd_and_hms(2026, 8, 31, 0, 0, 0)
            .unwrap()
            .with_timezone(&Utc);
        let mut legacy = entry(first, "old-uuid", "allowed", "rule", None);
        legacy.agent_name = "Claude Code 2.1".into();
        let rows = [
            legacy,
            entry(
                now,
                "host:claude-code",
                "allowed",
                "observed",
                Some(native(Some("sudo"), false)),
            ),
            entry(
                first - chrono::Duration::milliseconds(1),
                "old",
                "allowed",
                "rule",
                None,
            ),
            entry(
                now + chrono::Duration::milliseconds(1),
                "future",
                "denied",
                "rule",
                None,
            ),
        ];
        let summary = summarize(&rows, 7, now);
        assert_eq!(summary.total, 2);
        assert_eq!(summary.agents.len(), 1);
        assert_eq!(summary.agents[0].id, "host:claude-code");
        assert_eq!(summary.agents[0].name, "Claude Code");
        assert_eq!(summary.agents[0].attention, 1);
        assert!(summary.agents[0].host);
        assert_eq!(summary.daily[0].routine, 1);
        assert_eq!(summary.daily[6].attention, 1);
        for day in &summary.daily {
            let query = AuditQuery {
                day: Some(day.date),
                ..Default::default()
            };
            assert_eq!(
                rows.iter()
                    .filter(|entry| query.matches(entry, &summary.window))
                    .count(),
                day.routine + day.attention
            );
        }
        assert_eq!(summarize(&rows, u32::MAX, now).daily.len(), 30);
    }
}
