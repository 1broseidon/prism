import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { hostSetup, hostStatus } from "../hosts";
import { agents, errorMessage, native } from "../state";
import { relative } from "../time";
import { Button, Chip, CodeBlock, ConfirmButton, Label, Screen, describeError } from "../ui";

export function HarnessSetupScreen({ host }: { host: string }) {
  const codex = host === "codex";
  const name = codex ? "Codex" : "Claude Code";
  const setup = hostSetup(native.value, host);
  const seen = hostStatus(native.value, host);
  const agent = agents.value.find(a => a.id === `host:${host}`);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [details, setDetails] = useState(false);
  const [snippet, setSnippet] = useState("");
  const refresh = async () => { native.value = await api.getNativeStatus(); agents.value = await api.listAgents(); };
  useEffect(() => {
    void refresh().catch(e => { errorMessage.value = describeError(e); });
    const onFocus = () => { void refresh().catch(() => {}); };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [host]);
  const change = async (remove = false) => {
    if (busy) return;
    setBusy(true);
    setMessage("");
    try {
      const result = await (remove ? api.removeHarnessSetup(host) : api.setupHarness(host));
      await refresh();
      setMessage(remove ? "Removed. Restart the client to disconnect." : result.paths.length ? "Saved. Restart the client to load setup." : "Settings are current.");
    } catch (e) { errorMessage.value = describeError(e); }
    finally { setBusy(false); }
  };
  const configured = setup?.mcp_configured && setup.hook_installed;
  const receiving = !!native.value?.observe_native && setup?.events_received;
  const showDetails = async () => {
    setDetails(!details);
    if (!details) try { setSnippet(await api.getHostHookSnippet(host)); } catch (e) { errorMessage.value = describeError(e); }
  };
  return <div class="screen pushed"><Screen footer={
    <Button variant="primary" busy={busy} disabled={!setup || !!setup.problem} onClick={() => void change()}>{configured ? "Repair setup" : `Set up ${name}`}</Button>
  }>
    <p class="hint">Global MCP + native observation. Existing settings backed up.</p>
    <section class="section setup-status">
      <Label right={<Chip tone={agent?.connected ? "ok" : undefined}>{agent?.connected ? "connected" : setup?.mcp_configured ? "configured" : "not configured"}</Chip>}>MCP</Label>
      <p class="hint">{setup?.mcp_configured ? "Approve sign-in in Prism when the client connects." : "All projects use this machine’s gateway."}</p>
      <Label right={<Chip tone={receiving ? "ok" : undefined}>{!native.value?.observe_native || setup?.hooks_disabled ? "off" : receiving ? "receiving" : setup?.hook_installed ? "configured" : "not configured"}</Chip>}>Observation</Label>
      <p class="hint">{setup?.hooks_disabled ? `Hooks are disabled in ${name}. Enable them there to observe tools.` : receiving ? `Last event ${relative(seen?.last_event_at ?? "")}.` : codex && setup?.hook_installed ? "Review the hook in Codex: /hooks. Then run a tool." : "Run a tool in a new session to verify."}</p>
    </section>
    {configured && !agent?.connected ? <section class="section">
      <Label>Connect MCP</Label>
      <CodeBlock text={codex ? "codex mcp login prism" : "/mcp"} copyable />
      <p class="hint">{codex ? "Run in your terminal." : "Run inside Claude Code."}</p>
    </section> : null}
    {setup?.problem ? <p class="hint error" role="alert">{setup.problem}</p> : null}
    {message ? <p class="hint" role="status">{message}</p> : null}
    <button type="button" class="link" aria-expanded={details} onClick={() => void showDetails()}>{details ? "Hide details" : "Files and hook snippet"}</button>
    {details ? <section class="section">
      <p class="hint setup-paths">{setup?.mcp_path}<br />{setup?.settings_path}</p>
      <CodeBlock text={snippet} copyable />
      <p class="hint">Project overrides stay separate. Setup does not approve MCP access or trust hooks.</p>
    </section> : null}
    {setup?.setup_present ? <div class="actions update-actions">
      <ConfirmButton variant="quiet" class="danger" confirm="Remove global setup?" busy={busy} onConfirm={() => void change(true)}>Remove setup</ConfirmButton>
      <Button variant="quiet" busy={busy} onClick={() => void refresh().catch(e => { errorMessage.value = describeError(e); })}>Check status</Button>
    </div> : null}
  </Screen></div>;
}
