import assert from "node:assert/strict";
import test from "node:test";
import {
  discardVolatile,
  panelTransition,
  preserveRecoverable,
  reconcileQueue,
  rememberVolatile,
} from "../src/lifecycle.ts";

test("dismissal and reopening reset while retaining a recoverable draft", () => {
  const draft = [{ kind: "add-server" }];
  const saved = preserveRecoverable(draft, null, true);

  assert.equal(panelTransition(true, false), "dismiss");
  assert.equal(panelTransition(false, true), "reopen");
  assert.deepEqual(saved, draft);
  assert.notEqual(saved, draft);
});

test("one-time tokens survive navigation until explicitly discarded", () => {
  const token = { agent_id: "agent-1", token: "shown-once" };
  const remembered = rememberVolatile({}, token.agent_id, token);
  const afterDismissal = preserveRecoverable(["connections:agent-1"], null, true);

  assert.equal(remembered[token.agent_id], token);
  assert.deepEqual(afterDismissal, ["connections:agent-1"]);
  assert.deepEqual(discardVolatile(remembered, token.agent_id), {});
});

test("new approval attention while visible refreshes without resetting navigation", () => {
  assert.equal(panelTransition(true, true), "refresh");
});

test("an incoming higher-priority approval does not move the visible queue item", () => {
  const selected = reconcileQueue(["call:one", "call:two"], "call:two", 1);
  const after = reconcileQueue(["agent:new", "call:one", "call:two"], selected.key, selected.index);

  assert.deepEqual(after, { index: 2, key: "call:two" });
});

test("external resolution advances an inspected request at the same queue position", () => {
  const before = ["agent:a", "call:one", "call:two"];
  const selected = reconcileQueue(before, "call:one", 0);
  assert.deepEqual(selected, { index: 1, key: "call:one" });

  const after = reconcileQueue(["agent:a", "call:two"], "call:one", selected.index);
  assert.deepEqual(after, { index: 1, key: "call:two" });
});

test("removing the last inspected request selects the remaining queue or rests empty", () => {
  assert.deepEqual(reconcileQueue(["agent:a"], "call:one", 1), { index: 0, key: "agent:a" });
  assert.deepEqual(reconcileQueue([], "call:one", 1), { index: 0, key: null });
});
