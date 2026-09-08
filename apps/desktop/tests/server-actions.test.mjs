import assert from "node:assert/strict";
import test from "node:test";
import {
  serverFocusIndexAfterRemoval,
  serverPrimaryAction,
  toggleServerDisclosure,
} from "../src/server-actions.ts";

test("server recovery stays visible while routine actions move under Manage", () => {
  assert.equal(serverPrimaryAction("oauth", "sign_in_required"), "sign-in");
  assert.equal(serverPrimaryAction("none", "sign_in_required"), "retry");
  assert.equal(serverPrimaryAction("header", "sign_in_required"), "retry");
  assert.equal(serverPrimaryAction("oauth", "stopped"), "sign-in");
  assert.equal(serverPrimaryAction("none", "failed"), "retry");
  assert.equal(serverPrimaryAction("header", "stopped"), "retry");
  assert.equal(serverPrimaryAction("none", "running"), null);
  assert.equal(serverPrimaryAction("oauth", "running"), null);
  assert.equal(serverPrimaryAction("none", "starting"), null);
});

test("only one server maintenance disclosure can be open", () => {
  assert.equal(toggleServerDisclosure(null, "one"), "one");
  assert.equal(toggleServerDisclosure("one", "two"), "two");
  assert.equal(toggleServerDisclosure("two", "two"), null);
});

test("removal focus follows the deleted row when possible", () => {
  assert.equal(serverFocusIndexAfterRemoval(1, 4), 1);
  assert.equal(serverFocusIndexAfterRemoval(4, 4), 3);
  assert.equal(serverFocusIndexAfterRemoval(0, 0), null);
});
