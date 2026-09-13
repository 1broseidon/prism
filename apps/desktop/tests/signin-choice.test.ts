import assert from "node:assert/strict";
import test from "node:test";
import { signinAction, signinChoice } from "../src/signin-choice.ts";
import type { PendingSignIn } from "../src/types.ts";

const signin: PendingSignIn = {
  id: "request", agent_id: "agent", agent_name: "Workbench", client_name: "Workbench", client_id: "new",
  requested_at: "2026-09-13T00:00:00Z", needs_consent: true, new_client: true,
  suggested_group: { posture: "trusted", origin: null, connections: [{ client_id: "old", created_at: "2026-09-12T00:00:00Z" }] },
};

test("grouping consent preserves other connections by default", () => {
  assert.deepEqual(signinChoice(signin), { kind: "add" });
  assert.equal(signinAction(signin), "Add connection");
});
test("separate and replacement are explicit choices with matching action labels", () => {
  assert.deepEqual(signinChoice(signin, "separate"), { kind: "separate" });
  assert.equal(signinAction(signin, "separate"), "Create agent");
  assert.deepEqual(signinChoice(signin, "replace:old"), { kind: "replace", client_id: "old" });
  assert.equal(signinAction(signin, "replace:old"), "Replace connection");
});
test("a stale selection cannot replace an unlisted connection or affect a normal sign-in", () => {
  assert.throws(() => signinChoice(signin, "replace:somebody-else"), /no longer available/);
  assert.deepEqual(signinChoice({ ...signin, suggested_group: undefined }, "replace:old"), { kind: "add" });
  assert.equal(signinAction({ ...signin, suggested_group: undefined }), "Allow");
});
