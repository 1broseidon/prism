// Prism-owned observer v1. Remove through Prism to remove its MCP entry too.
// OpenCode stable V1 plugin API. No tool arguments or permission decisions are changed.
export const PrismObserver = async ({ directory }) => ({
  "tool.execute.before": async (input, output) => {
    try {
      const args = output.args ?? {};
      const observed = {};
      for (const key of ["command", "filePath", "path", "url", "pattern", "workdir"]) {
        if (typeof args[key] === "string") observed[key] = args[key].slice(0, 16000);
      }
      if (typeof args.patchText === "string") {
        observed.patchText = args.patchText.split("\n")
          .filter(line => /^\*\*\* (Add File|Update File|Delete File|Move to): /.test(line))
          .join("\n").slice(0, 16000);
      }
      const body = JSON.stringify({
        hook_event_name: "PreToolUse",
        session_id: input.sessionID,
        tool_use_id: input.callID,
        cwd: directory,
        tool_name: input.tool,
        tool_input: observed,
      });
      // The receiver has the same cap; don't delay a large edit for a rejected POST.
      if (new TextEncoder().encode(body).length > 65536) return;
      await fetch("__PRISM_HOOK_URL__", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
        signal: AbortSignal.timeout(750),
        redirect: "error",
      });
    } catch {
      // Observation is best effort and never affects the tool's permission or result.
    }
  },
});
