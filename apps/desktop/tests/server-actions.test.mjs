import assert from "node:assert/strict";
import test from "node:test";
import { ExclusiveActions, persistExposure, serverPrimaryAction } from "../src/server-actions.ts";

test("rapid exposure changes cannot overlap, even before the control re-renders", async () => {
  const actions = new ExclusiveActions();
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  const writes = [];
  const first = actions.run("server/tool", async () => {
    writes.push(false);
    await gate;
  });
  assert.equal(await actions.run("server/tool", async () => { writes.push(true); }), false);
  assert.deepEqual(writes, [false]);
  // Other tools are independent.
  assert.equal(await actions.run("server/other", async () => {}), true);
  release();
  assert.equal(await first, true);
  assert.equal(await actions.run("server/tool", async () => { writes.push(true); }), true);
  assert.deepEqual(writes, [false, true]);
});

test("failed exposure saves release their claim for retry", async () => {
  const actions = new ExclusiveActions();
  await assert.rejects(actions.run("server/tool", async () => { throw new Error("save failed"); }));
  assert.equal(await actions.run("server/tool", async () => {}), true);
});

test("a rejected exposure write rolls back and does not refresh", async () => {
  let rollback = 0;
  let refresh = 0;
  await assert.rejects(persistExposure(
    async () => { throw new Error("save failed"); },
    async () => { refresh++; },
    () => { rollback++; },
  ), /save failed/);
  assert.equal(rollback, 1);
  assert.equal(refresh, 0);
});

test("a failed refresh cannot roll back a committed exposure write", async () => {
  let saved = false;
  let rollback = 0;
  await assert.rejects(persistExposure(
    async () => { saved = true; },
    async () => { throw new Error("read failed"); },
    () => { rollback++; },
  ), /read failed/);
  assert.equal(saved, true);
  assert.equal(rollback, 0);
});

test("recovery stays on the row and depends on how the server authenticates", () => {
  assert.equal(serverPrimaryAction("oauth", "sign_in_required"), "sign-in");
  assert.equal(serverPrimaryAction("header", "sign_in_required"), "retry");
  assert.equal(serverPrimaryAction("none", "failed"), "retry");
  assert.equal(serverPrimaryAction("oauth", "stopped"), "sign-in");
  assert.equal(serverPrimaryAction("none", "stopped"), "retry");
  assert.equal(serverPrimaryAction("none", "running"), null);
  assert.equal(serverPrimaryAction("oauth", "starting"), null);
});
