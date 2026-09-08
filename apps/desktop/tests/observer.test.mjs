import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import test from "node:test";

const source = await readFile(new URL("../src-tauri/src/observers/opencode.js", import.meta.url), "utf8");
async function observer(url) {
  const code = source.replace("__PRISM_HOOK_URL__", url);
  const { PrismObserver } = await import(`data:text/javascript;base64,${Buffer.from(code).toString("base64")}`);
  return (await PrismObserver({ directory: "/fixture" }))["tool.execute.before"];
}

test("OpenCode reports call identity without file contents or changing tool input", async () => {
  const received = [];
  const server = createServer(async (req, res) => {
    let bytes = "";
    for await (const chunk of req) bytes += chunk;
    received.push(JSON.parse(bytes));
    // Even an unexpected permission response must have no effect on the tool.
    res.end('{"permission":"deny","updated_input":{"command":"wrong"}}');
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  try {
    const run = await observer(`http://127.0.0.1:${server.address().port}/hooks/opencode/test`);
    const input = { tool: "write", sessionID: "session", callID: "call" };
    const output = { args: { filePath: "/fixture/file", content: "private-file-content".repeat(10000) } };
    const original = structuredClone(output);
    assert.equal(await run(input, output), undefined);
    assert.deepEqual(output, original);
    assert.deepEqual(received[0], { hook_event_name: "PreToolUse", session_id: "session", tool_use_id: "call", cwd: "/fixture", tool_name: "write", tool_input: { filePath: "/fixture/file" } });
    await run({ ...input, tool: "apply_patch", callID: "patch" }, { args: { patchText: "*** Update File: old\n*** Move to: new\n+private-content" } });
    assert.equal(received[1].tool_input.patchText, "*** Update File: old\n*** Move to: new");
  } finally { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
});

test("OpenCode observer is neutral and bounded when Prism is unavailable", async () => {
  const stopped = await observer("http://127.0.0.1:9/hooks/opencode/test");
  assert.equal(await stopped({ tool: "bash", sessionID: "s" }, { args: { command: "echo ok" } }), undefined);
  const server = createServer(() => {});
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  try {
    const run = await observer(`http://127.0.0.1:${server.address().port}/hooks/opencode/slow`);
    const at = performance.now();
    assert.equal(await run({ tool: "bash" }, { args: { command: "echo ok" } }), undefined);
    assert.ok(performance.now() - at < 2000);
  } finally { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
});
