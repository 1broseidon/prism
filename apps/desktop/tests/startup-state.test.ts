import assert from "node:assert/strict";
import test from "node:test";
import { changeStartup } from "../src/startup-state.ts";
const state = (enabled: boolean) => ({ enabled, needs_repair: false, can_enable: true, error: null });
test("startup uses the confirmed OS result, including a failed write that stayed off", async () => {
  const failed = { ...state(false), error: "write failed" };
  assert.deepEqual(await changeStartup(true, async () => failed, async () => state(true)), failed);
});
test("a lost reply re-reads OS state rather than undoing a committed startup change", async () => {
  const result = await changeStartup(true, async () => { throw Error("lost reply"); }, async () => state(true));
  assert.equal(result.enabled, true); assert.match(result.error!, /current OS state/);
});
test("startup becomes unknown when neither write nor verification can be confirmed", async () => {
  const fail = async () => { throw Error("unavailable"); };
  const result = await changeStartup(false, fail, fail);
  assert.equal(result.enabled, null); assert.equal(result.can_enable, false);
});
