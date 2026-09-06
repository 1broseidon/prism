use chrono::{DateTime, Utc};

use crate::config::{AgentConfig, Attention, Posture, Rule, RuleDecision};

/// Outcome of evaluating the current rule set against one tool call.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Verdict {
    Allow,
    Deny,
    Ask,
}

/// Optional tool hints used by [`evaluate`] for the `guided` posture; passed through from MCP
/// annotations. They come from the server, so they are advice, not proof.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ToolAnnotations {
    pub title: Option<String>,
    pub read_only_hint: Option<bool>,
    pub destructive_hint: Option<bool>,
    pub idempotent_hint: Option<bool>,
    pub open_world_hint: Option<bool>,
}

impl ToolAnnotations {
    pub fn is_read_only(&self) -> bool {
        self.read_only_hint == Some(true)
    }
}

impl From<&rmcp::model::ToolAnnotations> for ToolAnnotations {
    fn from(value: &rmcp::model::ToolAnnotations) -> Self {
        Self {
            title: value.title.clone(),
            read_only_hint: value.read_only_hint,
            destructive_hint: value.destructive_hint,
            idempotent_hint: value.idempotent_hint,
            open_world_hint: value.open_world_hint,
        }
    }
}

/// What decided a call, so the audit trail and the panel can say so.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Decider {
    Rule { rule_id: String },
    Posture(Posture),
}

/// The full result of evaluating one call: what to do, who said so, how loudly to tell the operator.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Evaluation {
    pub verdict: Verdict,
    pub decider: Decider,
    pub attention: Attention,
}

/// Rank of a matching rule. Lower is more specific.
///
/// agent+server+tool > agent+server > server+tool > server > agent > global
/// An exact tool name beats a glob at the same rung.
fn specificity(rule: &Rule) -> u8 {
    let rung = match (
        rule.agent_id.is_some(),
        rule.server_id.is_some(),
        rule.tool.is_some(),
    ) {
        (true, true, true) => 0,
        (true, true, false) => 1,
        (false, true, true) => 2,
        (false, true, false) => 3,
        (true, false, false) => 4,
        (false, false, false) => 5,
        // Not on the documented ladder; keep them less specific than server.
        (true, false, true) => 2,
        (false, false, true) => 4,
    };
    rung * 2 + u8::from(rule.tool_is_glob())
}

/// Deny beats Ask beats Allow when two rules tie on specificity.
fn strictness(decision: RuleDecision) -> u8 {
    match decision {
        RuleDecision::Deny => 0,
        RuleDecision::Ask => 1,
        RuleDecision::Allow => 2,
    }
}

/// `*` matches any run of characters, including none. Nothing else is special.
pub fn glob_match(pattern: &str, name: &str) -> bool {
    if !pattern.contains('*') {
        return pattern == name;
    }
    let parts: Vec<&str> = pattern.split('*').collect();
    let (first, last) = (parts[0], parts[parts.len() - 1]);
    if !name.starts_with(first) || !name.ends_with(last) {
        return false;
    }
    if name.len() < first.len() + last.len() && parts.len() == 2 {
        return false;
    }
    let mut rest = &name[first.len()..name.len() - last.len()];
    for part in &parts[1..parts.len() - 1] {
        match rest.find(part) {
            Some(idx) => rest = &rest[idx + part.len()..],
            None => return false,
        }
    }
    true
}

fn matches(rule: &Rule, agent_id: &str, server_id: &str, tool_name: &str) -> bool {
    if let Some(ref wanted) = rule.agent_id {
        if wanted != agent_id {
            return false;
        }
    }
    if let Some(ref wanted) = rule.server_id {
        if wanted != server_id {
            return false;
        }
    }
    if let Some(ref wanted) = rule.tool {
        if !glob_match(wanted, tool_name) {
            return false;
        }
    }
    true
}

/// Find the rule that governs this call, if any. Expired rules never match.
pub fn winning_rule<'a>(
    rules: &'a [Rule],
    agent_id: &str,
    server_id: &str,
    tool_name: &str,
    now: DateTime<Utc>,
) -> Option<&'a Rule> {
    rules
        .iter()
        .filter(|rule| !rule.is_expired(now) && matches(rule, agent_id, server_id, tool_name))
        .min_by_key(|rule| (specificity(rule), strictness(rule.decision)))
}

/// Evaluate one call. The most specific matching rule wins; with no rule, the agent's posture
/// decides. Attention comes from the rule when it sets one, otherwise from the agent.
pub fn evaluate(
    rules: &[Rule],
    agent: &AgentConfig,
    server_id: &str,
    tool_name: &str,
    annotations: Option<&ToolAnnotations>,
    now: DateTime<Utc>,
) -> Evaluation {
    if let Some(rule) = winning_rule(rules, &agent.id, server_id, tool_name, now) {
        let verdict = match rule.decision {
            RuleDecision::Allow => Verdict::Allow,
            RuleDecision::Deny => Verdict::Deny,
            RuleDecision::Ask => Verdict::Ask,
        };
        return Evaluation {
            verdict,
            decider: Decider::Rule {
                rule_id: rule.id.clone(),
            },
            attention: rule.attention.unwrap_or(agent.attention),
        };
    }

    let verdict = match agent.posture {
        Posture::Supervised | Posture::FirstUse => Verdict::Ask,
        Posture::Guided => {
            if annotations.is_some_and(ToolAnnotations::is_read_only) {
                Verdict::Allow
            } else {
                Verdict::Ask
            }
        }
        Posture::Trusted => Verdict::Allow,
    };
    Evaluation {
        verdict,
        decider: Decider::Posture(agent.posture),
        attention: agent.attention,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::{AgentStatus, RuleScope};
    use chrono::Duration;

    fn agent(posture: Posture) -> AgentConfig {
        AgentConfig {
            id: "agt".into(),
            name: "Agent".into(),
            client_name: "agent".into(),
            client_version: None,
            status: AgentStatus::Approved,
            created_at: Utc::now(),
            decided_at: None,
            posture,
            attention: Attention::Silent,
            client_id: None,
            host: None,
        }
    }

    fn rule(
        id: &str,
        agent_id: Option<&str>,
        server_id: Option<&str>,
        tool: Option<&str>,
        decision: RuleDecision,
    ) -> Rule {
        Rule {
            id: id.into(),
            agent_id: agent_id.map(str::to_string),
            server_id: server_id.map(str::to_string),
            tool: tool.map(str::to_string),
            decision,
            attention: None,
            scope: RuleScope::Always,
            expires_at: None,
            condition: None,
            created_at: Utc::now(),
        }
    }

    fn ev(rules: &[Rule]) -> Verdict {
        evaluate(
            rules,
            &agent(Posture::Supervised),
            "srv",
            "tool",
            None,
            Utc::now(),
        )
        .verdict
    }

    fn read_only() -> ToolAnnotations {
        ToolAnnotations {
            title: None,
            read_only_hint: Some(true),
            destructive_hint: Some(false),
            idempotent_hint: None,
            open_world_hint: None,
        }
    }

    #[test]
    fn supervised_default_is_ask() {
        assert_eq!(ev(&[]), Verdict::Ask);
    }

    #[test]
    fn posture_decides_when_no_rule_matches() {
        let now = Utc::now();
        let trusted = evaluate(&[], &agent(Posture::Trusted), "srv", "tool", None, now);
        assert_eq!(trusted.verdict, Verdict::Allow);
        assert_eq!(trusted.decider, Decider::Posture(Posture::Trusted));

        let guided_ro = evaluate(
            &[],
            &agent(Posture::Guided),
            "srv",
            "tool",
            Some(&read_only()),
            now,
        );
        assert_eq!(guided_ro.verdict, Verdict::Allow);
        let guided_unknown = evaluate(&[], &agent(Posture::Guided), "srv", "tool", None, now);
        assert_eq!(guided_unknown.verdict, Verdict::Ask);

        let first_use = evaluate(&[], &agent(Posture::FirstUse), "srv", "tool", None, now);
        assert_eq!(first_use.verdict, Verdict::Ask);
    }

    #[test]
    fn rule_beats_posture_and_carries_attention() {
        let mut r = rule("r", Some("agt"), None, None, RuleDecision::Deny);
        r.attention = Some(Attention::Open);
        let out = evaluate(
            &[r],
            &agent(Posture::Trusted),
            "srv",
            "tool",
            None,
            Utc::now(),
        );
        assert_eq!(out.verdict, Verdict::Deny);
        assert_eq!(out.attention, Attention::Open);
        assert_eq!(
            out.decider,
            Decider::Rule {
                rule_id: "r".into()
            }
        );
    }

    #[test]
    fn attention_inherits_from_agent() {
        let mut a = agent(Posture::Trusted);
        a.attention = Attention::Notify;
        let r = rule("r", Some("agt"), None, None, RuleDecision::Allow);
        let out = evaluate(&[r], &a, "srv", "tool", None, Utc::now());
        assert_eq!(out.attention, Attention::Notify);
        let out = evaluate(&[], &a, "srv", "tool", None, Utc::now());
        assert_eq!(out.attention, Attention::Notify);
    }

    #[test]
    fn most_specific_wins() {
        let rules = vec![
            rule("global", None, None, None, RuleDecision::Deny),
            rule("agent", Some("agt"), None, None, RuleDecision::Deny),
            rule("server", None, Some("srv"), None, RuleDecision::Deny),
            rule(
                "server-tool",
                None,
                Some("srv"),
                Some("tool"),
                RuleDecision::Deny,
            ),
            rule(
                "agent-server",
                Some("agt"),
                Some("srv"),
                None,
                RuleDecision::Deny,
            ),
            rule(
                "triple",
                Some("agt"),
                Some("srv"),
                Some("tool"),
                RuleDecision::Allow,
            ),
        ];
        assert_eq!(ev(&rules), Verdict::Allow);
    }

    #[test]
    fn precedence_ladder() {
        // agent+server beats server+tool
        assert_eq!(
            ev(&[
                rule("st", None, Some("srv"), Some("tool"), RuleDecision::Deny),
                rule("as", Some("agt"), Some("srv"), None, RuleDecision::Allow),
            ]),
            Verdict::Allow
        );
        // server+tool beats server
        assert_eq!(
            ev(&[
                rule("s", None, Some("srv"), None, RuleDecision::Deny),
                rule("st", None, Some("srv"), Some("tool"), RuleDecision::Allow),
            ]),
            Verdict::Allow
        );
        // server beats agent
        assert_eq!(
            ev(&[
                rule("a", Some("agt"), None, None, RuleDecision::Deny),
                rule("s", None, Some("srv"), None, RuleDecision::Allow),
            ]),
            Verdict::Allow
        );
        // agent beats global
        assert_eq!(
            ev(&[
                rule("g", None, None, None, RuleDecision::Deny),
                rule("a", Some("agt"), None, None, RuleDecision::Allow),
            ]),
            Verdict::Allow
        );
    }

    #[test]
    fn strictness_breaks_ties() {
        let triple = |id: &str, d| rule(id, Some("agt"), Some("srv"), Some("tool"), d);
        assert_eq!(
            ev(&[
                triple("allow", RuleDecision::Allow),
                triple("deny", RuleDecision::Deny)
            ]),
            Verdict::Deny
        );
        assert_eq!(
            ev(&[
                triple("allow", RuleDecision::Allow),
                triple("ask", RuleDecision::Ask)
            ]),
            Verdict::Ask
        );
        assert_eq!(
            ev(&[
                rule("deny", None, Some("srv"), None, RuleDecision::Deny),
                rule("allow", None, Some("srv"), None, RuleDecision::Allow),
            ]),
            Verdict::Deny
        );
    }

    #[test]
    fn globs_match_and_exact_beats_glob() {
        assert!(glob_match("create_*", "create_issue"));
        assert!(glob_match("*", "anything"));
        assert!(glob_match("*_file", "read_file"));
        assert!(glob_match("read_*_v*", "read_big_v2"));
        assert!(!glob_match("create_*", "delete_issue"));
        assert!(!glob_match("a*b", "ab_"));
        assert!(!glob_match("abc", "abcd"));

        let rules = vec![
            rule(
                "glob",
                Some("agt"),
                Some("srv"),
                Some("*"),
                RuleDecision::Deny,
            ),
            rule(
                "exact",
                Some("agt"),
                Some("srv"),
                Some("tool"),
                RuleDecision::Allow,
            ),
        ];
        assert_eq!(ev(&rules), Verdict::Allow);
        let rules = vec![rule(
            "glob",
            Some("agt"),
            Some("srv"),
            Some("to*"),
            RuleDecision::Deny,
        )];
        assert_eq!(ev(&rules), Verdict::Deny);
    }

    #[test]
    fn expired_rules_are_ignored() {
        let mut r = rule(
            "r",
            Some("agt"),
            Some("srv"),
            Some("tool"),
            RuleDecision::Allow,
        );
        r.expires_at = Some(Utc::now() - Duration::seconds(1));
        assert_eq!(ev(&[r.clone()]), Verdict::Ask);
        r.expires_at = Some(Utc::now() + Duration::minutes(30));
        assert_eq!(ev(&[r]), Verdict::Allow);
    }

    #[test]
    fn unmatched_fields_do_not_apply() {
        let rules = vec![rule(
            "other-agent",
            Some("someone-else"),
            None,
            None,
            RuleDecision::Deny,
        )];
        assert_eq!(ev(&rules), Verdict::Ask);
    }
}
