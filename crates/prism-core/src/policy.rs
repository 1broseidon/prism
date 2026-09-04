use crate::config::{Rule, RuleDecision};

/// Outcome of evaluating the current rule set against one tool call.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Verdict {
    Allow,
    Deny,
    Ask,
}

/// Optional tool hints used by [`evaluate`]. Unused for the default Ask
/// fallback; accepted so callers can pass through MCP annotations.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ToolAnnotations {
    pub title: Option<String>,
    pub read_only_hint: Option<bool>,
    pub destructive_hint: Option<bool>,
    pub idempotent_hint: Option<bool>,
    pub open_world_hint: Option<bool>,
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

/// Rank of a matching rule. Lower is more specific.
///
/// agent+server+tool > agent+server > server+tool > server > agent > global
fn specificity(rule: &Rule) -> u8 {
    match (
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
    }
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
        if wanted != tool_name {
            return false;
        }
    }
    true
}

/// Evaluate `rules` for one call. The most specific matching rule wins.
/// Deny beats Allow at equal specificity. Default when nothing matches: Ask.
pub fn evaluate(
    rules: &[Rule],
    agent_id: &str,
    server_id: &str,
    tool_name: &str,
    annotations: Option<&ToolAnnotations>,
) -> Verdict {
    let _ = annotations;
    let mut best_rank: Option<u8> = None;
    let mut best_decision: Option<RuleDecision> = None;

    for rule in rules {
        if !matches(rule, agent_id, server_id, tool_name) {
            continue;
        }
        let rank = specificity(rule);
        match best_rank {
            None => {
                best_rank = Some(rank);
                best_decision = Some(rule.decision);
            }
            Some(current) if rank < current => {
                best_rank = Some(rank);
                best_decision = Some(rule.decision);
            }
            Some(current) if rank == current => {
                if rule.decision == RuleDecision::Deny {
                    best_decision = Some(RuleDecision::Deny);
                }
            }
            Some(_) => {}
        }
    }

    match best_decision {
        Some(RuleDecision::Allow) => Verdict::Allow,
        Some(RuleDecision::Deny) => Verdict::Deny,
        None => Verdict::Ask,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::RuleScope;
    use chrono::Utc;

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
            scope: RuleScope::Always,
            created_at: Utc::now(),
        }
    }

    fn ev(rules: &[Rule]) -> Verdict {
        evaluate(rules, "agt", "srv", "tool", None)
    }

    #[test]
    fn default_is_ask() {
        assert_eq!(ev(&[]), Verdict::Ask);
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
    fn deny_beats_allow_at_equal_specificity() {
        let rules = vec![
            rule(
                "allow",
                Some("agt"),
                Some("srv"),
                Some("tool"),
                RuleDecision::Allow,
            ),
            rule(
                "deny",
                Some("agt"),
                Some("srv"),
                Some("tool"),
                RuleDecision::Deny,
            ),
        ];
        assert_eq!(ev(&rules), Verdict::Deny);

        let rules = vec![
            rule("deny", None, Some("srv"), None, RuleDecision::Deny),
            rule("allow", None, Some("srv"), None, RuleDecision::Allow),
        ];
        assert_eq!(ev(&rules), Verdict::Deny);
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
