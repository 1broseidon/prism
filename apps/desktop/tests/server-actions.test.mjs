import assert from "node:assert/strict";
import test from "node:test";
import { serverPrimaryAction } from "../src/server-actions.ts";

test("recovery stays on the row and depends on how the server authenticates", () => {
  assert.equal(serverPrimaryAction("oauth", "sign_in_required"), "sign-in");
  assert.equal(serverPrimaryAction("header", "sign_in_required"), "retry");
  assert.equal(serverPrimaryAction("none", "failed"), "retry");
  assert.equal(serverPrimaryAction("oauth", "stopped"), "sign-in");
  assert.equal(serverPrimaryAction("none", "stopped"), "retry");
  assert.equal(serverPrimaryAction("none", "running"), null);
  assert.equal(serverPrimaryAction("oauth", "starting"), null);
});
