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
  state.serverEditDrafts.value = {};
  state.savingServerEdits.value = {};
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

test("auth detection chooses defaults for the current URL only", () => {
  state.clearAddServerDraft();
  state.updateAddServerDraft({ kind: "url", url: "https://one.example/mcp" });
  assert.equal(state.applyAddServerProbe("https://one.example/mcp", { kind: "bearer_likely", reason: "no_oauth_metadata" }), true);
  assert.equal(state.addServerDraft.value.auth, "header");
  assert.equal(state.addServerDraft.value.probedUrl, "https://one.example/mcp");
  state.updateAddServerDraft({ url: "https://two.example/mcp" });
  assert.equal(state.addServerDraft.value.probedUrl, null);
  assert.equal(state.applyAddServerProbe("https://one.example/mcp", { kind: "oauth_ready" }), false);
  assert.equal(state.addServerDraft.value.auth, "header");
  state.applyAddServerProbe("https://two.example/mcp", { kind: "open" });
  assert.equal(state.addServerDraft.value.auth, "none");
});

test("an explicit auth choice wins over a pending probe until the URL changes", async () => {
  state.clearAddServerDraft();
  state.updateAddServerDraft({ kind: "url", url: "https://one.example/mcp" });
  const request = deferred();
  const applying = request.promise.then((result) => state.applyAddServerProbe("https://one.example/mcp", result));
  state.updateAddServerDraft({ auth: "header", authTouched: true });
  request.resolve({ kind: "oauth_ready" });
  assert.equal(await applying, false);
  assert.equal(state.addServerDraft.value.auth, "header");
  assert.equal(state.addServerDraft.value.authTouched, true);
  state.updateAddServerDraft({ url: "https://two.example/mcp" });
  assert.equal(state.addServerDraft.value.authTouched, false);
  state.applyAddServerProbe("https://two.example/mcp", { kind: "oauth_ready" });
  assert.equal(state.addServerDraft.value.auth, "oauth");
});

test("unreachable probes preserve auth and command drafts ignore URL results", () => {
  state.clearAddServerDraft();
  state.updateAddServerDraft({ kind: "url", url: "https://one.example/mcp", auth: "header" });
  state.applyAddServerProbe("https://one.example/mcp", { kind: "unreachable" });
  assert.equal(state.addServerDraft.value.auth, "header");
  state.updateAddServerDraft({ kind: "command" });
  state.applyAddServerProbe("https://one.example/mcp", { kind: "oauth_ready" });
  assert.equal(state.addServerDraft.value.auth, "header");
});

test("auth hints explain the recovery and label configuration mismatches", async () => {
  globalThis.window = {};
  const { authenticationGuidance, statusChip } = await vite.ssrLoadModule("/src/screens/Servers.tsx");
  const server = (auth, hint) => ({ auth, status: { kind: "sign_in_required", hint } });
  assert.equal(authenticationGuidance(server("none", "bearer_rejected")), "Authentication is required. Edit the server and check whether it needs an API key.");
  assert.equal(authenticationGuidance(server("header", "bearer_rejected")), "The server rejected the API key. Edit the server and check it.");
  for (const auth of ["none", "header"]) {
    assert.equal(authenticationGuidance(server(auth, "oauth_available")), "This server offers browser sign-in. Edit the server and choose OAuth.");
    for (const hint of ["bearer_rejected", "oauth_available"]) assert.equal(statusChip(server(auth, hint)).props.children, "Needs auth change");
  }
  assert.equal(authenticationGuidance(server("oauth", "sign_in")), null);
  assert.equal(authenticationGuidance(server("header", "unknown")), "Check the API key, then retry.");
  assert.equal(authenticationGuidance(server("none", "unknown")), "The server returned 401. Check its setup, then retry.");
  delete globalThis.window;
});

test("broken OAuth discovery preserves the operator's auth choice", () => {
  state.clearAddServerDraft();
  state.updateAddServerDraft({ kind: "url", url: "https://one.example/mcp", auth: "oauth" });
  assert.equal(state.applyAddServerProbe("https://one.example/mcp", { kind: "bearer_likely", reason: "oauth_broken" }), false);
  assert.equal(state.addServerDraft.value.auth, "oauth");
});

function editFixture() {
  const server = { id: "one", name: "original", url: "https://one.example/mcp", auth: "header", command: "", args: [], env: {}, enabled: true, hidden_tools: [], credentials_stored: true, status: { kind: "running", tool_count: 1 } };
  state.servers.value = [server];
  state.stack.value = [{ kind: "server", serverId: server.id }];
  state.beginServerEdit(server);
  return server;
}

test("server edit and replacement key survive hide, resume and explicit discard", () => {
  editFixture();
  state.updateServerEdit("one", { name: "renamed", secret: "test-only-key" });
  state.resetNavigation();
  state.resumeNavigation();
  assert.equal(state.stack.value.at(-1).serverId, "one");
  assert.equal(state.serverEditDrafts.value.one.secret, "test-only-key");
  state.resetNavigation();
  state.discardResumableNavigation();
  assert.equal(state.serverEditDrafts.value.one, undefined);
  assert.equal(state.resumableNavigation.value, null);
});

test("committed edits clear secrets despite refresh failure and cannot be submitted twice across hides", async () => {
  const original = editFixture();
  state.updateServerEdit("one", { secret: "test-only-key" });
  const saving = deferred();
  const done = state.saveServerEdit("one", () => saving.promise, async () => { throw new Error("offline"); });
  state.resetNavigation();
  state.discardResumableNavigation();
  assert.ok(state.serverEditDrafts.value.one);
  await state.saveServerEdit("one", () => { throw new Error("duplicate save"); }, async () => {});
  state.push({ kind: "inspect-call", callId: "pending" });
  saving.resolve({ server: { ...original, name: "saved" }, warning: "Server saved, but old credentials could not be removed." });
  await done;
  assert.equal(state.serverEditDrafts.value.one, undefined);
  assert.equal(state.savingServerEdits.value.one, undefined);
  assert.equal(state.servers.value[0].name, "saved");
  assert.match(state.errorMessage.value, /Server saved/);
  assert.equal(state.resumableNavigation.value, null);
  assert.equal(state.stack.value.at(-1).kind, "inspect-call");
});

test("a rejected edit retains its draft for correction and releases the save guard", async () => {
  editFixture();
  state.updateServerEdit("one", { name: "retry", secret: "test-only-key" });
  await assert.rejects(state.saveServerEdit("one", async () => { throw new Error("keyring locked"); }, async () => {}), /keyring locked/);
  assert.equal(state.serverEditDrafts.value.one.secret, "test-only-key");
  assert.equal(state.savingServerEdits.value.one, undefined);
  state.discardServerEdit("one");
  assert.equal(state.serverEditDrafts.value.one, undefined);
});

test("removing a server during a pending edit clears its secret draft and recovery entry", async () => {
  editFixture();
  state.updateServerEdit("one", { secret: "test-only-key" });
  const saving = deferred();
  const done = state.saveServerEdit("one", () => saving.promise, async () => {});
  state.resetNavigation();
  state.servers.value = [];
  saving.reject(new Error("server removed"));
  await assert.rejects(done, /server removed/);
  assert.equal(state.serverEditDrafts.value.one, undefined);
  assert.equal(state.resumableNavigation.value, null);
});
