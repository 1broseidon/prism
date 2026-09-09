import assert from "node:assert/strict";
import test from "node:test";
import { conditionClause, explainAudit, offerCondition, shortPath } from "../src/condition-display.ts";

test("offers preserve the full prefix while their labels stay compact", () => {
  const offer = { kind: "path_under", value: "/home/person/Projects/prism/src", label: "Under ~/Projects/prism/src" };
  assert.deepEqual(offerCondition(offer), { path: { under: offer.value } });
  assert.equal(shortPath(offer.value), "~/…/prism/src");
  assert.equal(shortPath("/var/data/projects"), "…/data/projects");
  assert.deepEqual(offerCondition({ kind: "host", value: "api.github.com", label: "For api.github.com" }), { host: { in: ["api.github.com"] } });
});

test("condition clauses describe paths, hosts, tags and boolean structure", () => {
  assert.equal(conditionClause({ path: { under: "~/Projects" } }), "under ~/Projects");
  assert.equal(conditionClause({ host: { in: ["api.github.com"] } }), "host api.github.com");
  assert.equal(conditionClause({ tag: "write_outside_cwd" }), "when write_outside_cwd");
  assert.equal(conditionClause({ not: { path: { outside_cwd: true } } }), "not (outside cwd)");
  assert.equal(conditionClause({ arg: { pointer: "/secret", equals: "hidden" } }), "when argument /secret");
});

test("audit explanations work with old rows and removed rules", () => {
  const entry = { agent_name: "Claude Code", server_id: "github", tool: "create_issue", verdict: "allowed", source: { kind: "posture", posture: "first_use" } };
  assert.equal(explainAudit(entry, []), "First-use posture");
  for (const [kind, why] of [["tripwire", "Rate tripwire"], ["do_not_disturb", "Do not disturb"], ["timeout", "Nobody answered in time"]]) {
    assert.equal(explainAudit({ ...entry, source: { kind } }, []), why);
  }
  const ruleEntry = { ...entry, source: { kind: "rule", rule_id: "r" } };
  assert.equal(explainAudit(ruleEntry, []), "Rule: Claude Code · github · create_issue · Allow");
  assert.equal(explainAudit(ruleEntry, [{ id: "r", server_id: "github", tool: "create_*", decision: "allow", condition: { path: { under: "~/Projects", access: ["write"] } } }]), "Rule: Claude Code · github · create_* · under ~/Projects (write) · Allow");
});
