import assert from "node:assert/strict";
import test from "node:test";
import { serverPrimaryAction } from "../src/server-actions.ts";

test("server recovery stays visible while routine actions move under Manage", () => {
  assert.equal(serverPrimaryAction("oauth", "sign_in_required"), "sign-in");
  assert.equal(serverPrimaryAction("oauth", "stopped"), "sign-in");
  assert.equal(serverPrimaryAction("none", "failed"), "retry");
  assert.equal(serverPrimaryAction("header", "stopped"), "retry");
  assert.equal(serverPrimaryAction("none", "running"), null);
  assert.equal(serverPrimaryAction("oauth", "running"), null);
  assert.equal(serverPrimaryAction("none", "starting"), null);
});
