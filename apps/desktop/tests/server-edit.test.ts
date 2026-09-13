import assert from "node:assert/strict";
import test from "node:test";
import { newServerEdit, serverEditArgs } from "../src/server-edit.ts";
import type { ServerView } from "../src/types.ts";

const remote: ServerView = { id: "one", name: "server", url: "https://one.example/mcp", auth: "header", command: "", args: [], env: {}, credentials_stored: true, enabled: true, status: { kind: "running", tool_count: 1 }, hidden_tools: ["hidden"] };

test("unchanged keys stay omitted and changing their header requires re-entry", () => {
  const draft = newServerEdit(remote);
  assert.equal(draft.secret, "");
  assert.equal(serverEditArgs(remote, { ...draft, name: " renamed " }).headers, undefined);
  assert.throws(() => serverEditArgs(remote, { ...draft, header: "X-Api-Key" }), /key again/);
  assert.deepEqual(serverEditArgs(remote, { ...draft, header: "X-Api-Key", secret: "replacement" }).headers, { "X-Api-Key": "replacement" });
  assert.deepEqual(serverEditArgs(remote, { ...draft, secret: "replacement" }).headers, { Authorization: "Bearer replacement" });
});

test("a new origin or auth mode needs an explicit key; same-origin edits can retain it", () => {
  const draft = newServerEdit(remote);
  assert.throws(() => serverEditArgs(remote, { ...draft, url: "https://two.example/mcp" }), /API key/);
  assert.equal(serverEditArgs(remote, { ...draft, url: "https://one.example/other" }).headers, undefined);
  assert.throws(() => serverEditArgs({ ...remote, auth: "oauth" }, draft), /API key/);
  assert.deepEqual(serverEditArgs(remote, { ...draft, url: "https://two.example/mcp", secret: "Bearer fresh" }).headers, { Authorization: "Bearer fresh" });
});

test("stdio edit distinguishes unchanged and explicitly cleared secrets and rejects malformed environment", () => {
  const local = { ...remote, url: null, auth: "none" as const, command: "server" };
  const draft = newServerEdit(local);
  assert.equal(serverEditArgs(local, draft).args, undefined);
  assert.equal(serverEditArgs(local, draft).env, undefined);
  assert.deepEqual(serverEditArgs(local, { ...draft, argsTouched: true }).args, []);
  assert.deepEqual(serverEditArgs(local, { ...draft, clearEnv: true }).env, {});
  assert.deepEqual(serverEditArgs(local, { ...draft, env: "KEY=value=with-equals\nEMPTY=" }).env, { KEY: "value=with-equals", EMPTY: "" });
  assert.throws(() => serverEditArgs(local, { ...draft, env: "KEY=value\nnot an assignment" }), /KEY=value/);
});
