import assert from "node:assert/strict";
import { after, before, beforeEach, test } from "node:test";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import { LatestRequest } from "../src/latest-request.ts";

let vite;
let state;
before(async () => {
  globalThis.location = { hash: "" };
  vite = await createServer({
    configFile: false,
    root: fileURLToPath(new URL("..", import.meta.url)),
    server: { middlewareMode: true, hmr: false, ws: false },
    appType: "custom",
  });
  state = await vite.ssrLoadModule("/src/state.ts");
});
after(async () => { await vite?.close(); delete globalThis.location; });
beforeEach(() => {
  state.stack.value = [];
  state.resumableNavigation.value = null;
  state.manualTokens.value = {};
  state.issuingManualTokens.value = {};
});

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

test("late setup completion preserves the current Inspect screen and clears only its saved flow", async () => {
  const saving = deferred();
  state.stack.value = [{ kind: "add-server" }];
  const finish = saving.promise.then(() => state.completeScreen({ kind: "add-server" }));
  state.resetNavigation();
  state.push({ kind: "inspect-call", callId: "waiting" });
  saving.resolve();
  await finish;
  assert.deepEqual(state.stack.value, [{ kind: "inspect-call", callId: "waiting" }]);
  assert.equal(state.resumableNavigation.value, null);
});

test("completing a foreground flow still returns to its parent", () => {
  state.stack.value = [{ kind: "agent", agentId: "one" }, { kind: "connect-agent" }];
  state.completeScreen({ kind: "connect-agent" });
  assert.deepEqual(state.stack.value, [{ kind: "agent", agentId: "one" }]);
});

test("replacement issuance survives hide before response and resumes the exact token", async () => {
  const issuing = deferred();
  const token = { agent_id: "one", token: "test-only-credential" };
  state.stack.value = [{ kind: "agent-connections", agentId: "one" }];
  const done = state.issueManualToken("one", () => issuing.promise);
  state.resetNavigation();
  assert.equal(state.issuingManualTokens.value.one, true);
  assert.equal(state.resumableNavigation.value.stack.at(-1).agentId, "one");
  // Remounting must not allow another issuance to invalidate the pending credential.
  await state.issueManualToken("one", () => { throw new Error("duplicate issuance"); });
  issuing.resolve(token);
  await done;
  state.resumeNavigation();
  assert.equal(state.stack.value.at(-1).agentId, "one");
  assert.equal(state.manualTokens.value.one, token);
  assert.equal(state.issuingManualTokens.value.one, undefined);
  state.discardResumableNavigation();
  assert.equal(state.manualTokens.value.one, undefined);
});

test("failed issuance clears its empty recovery entry but leaves unrelated navigation alone", async () => {
  const issuing = deferred();
  state.stack.value = [{ kind: "agent-connections", agentId: "one" }];
  const done = state.issueManualToken("one", () => issuing.promise);
  state.resetNavigation();
  state.push({ kind: "settings" });
  issuing.reject(new Error("keyring unavailable"));
  await assert.rejects(done, /keyring unavailable/);
  assert.equal(state.resumableNavigation.value, null);
  assert.deepEqual(state.stack.value, [{ kind: "settings" }]);
  assert.equal(state.issuingManualTokens.value.one, undefined);
});

test("old history page cannot overwrite a newer filter or report an obsolete error", async () => {
  const oldPage = deferred();
  const newPage = deferred();
  const requests = new LatestRequest();
  const visible = [], errors = [];
  const first = requests.run(() => oldPage.promise, p => visible.push(p), e => errors.push(e));
  requests.invalidate(); // Filter view unmounts synchronously.
  const next = requests.run(() => newPage.promise, p => visible.push(p), e => errors.push(e));
  newPage.resolve({ filter: "attention", rows: ["risky"] });
  await next;
  oldPage.resolve({ filter: "all", rows: ["routine"] });
  await first;
  assert.deepEqual(visible, [{ filter: "attention", rows: ["risky"] }]);
  assert.deepEqual(errors, []);
  const staleFailure = deferred();
  const last = requests.run(() => staleFailure.promise, p => visible.push(p), e => errors.push(e));
  requests.invalidate();
  staleFailure.reject(new Error("obsolete failure"));
  await last;
  assert.deepEqual(errors, []);
});
