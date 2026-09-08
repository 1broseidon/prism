import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { harness, hostSetup, hostStatus } from "../hosts";
import { agents, errorMessage, harnessSetupDraft, native, pop, push, updateHarnessSetupDraft } from "../state";
import { relative } from "../time";
import { Button, ChevronIcon, CodeBlock, ConfirmButton, Label, Screen, StatusText, useCopy, describeError } from "../ui";

export function HarnessSetupScreen({ host }: { host: string }) {
  const integration = harness(host);
  const name = integration?.name ?? "agent";
  const setup = hostSetup(native.value, host);
  const seen = hostStatus(native.value, host);
  const agent = agents.value.find(a => a.id === `host:${host}`);
  const [busy, setBusy] = useState(false);
  const draft = harnessSetupDraft(host);
  const { message } = draft;
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
    updateHarnessSetupDraft(host, { message: "" });
    try {
      const result = await (remove ? api.removeHarnessSetup(host) : api.setupHarness(host));
      await refresh();
      updateHarnessSetupDraft(host, { message: remove ? "Removed. Restart the client to disconnect." : result.paths.length ? "Saved. Restart the client to load setup." : "Settings are current." });
    } catch (e) { errorMessage.value = describeError(e); }
    finally { setBusy(false); }
  };
  const configured = setup?.mcp_configured && setup.hook_installed;
  const receiving = !!native.value?.observe_native && setup?.events_received;
  return <div class="screen pushed"><Screen footer={
    <Button variant="primary" busy={busy} disabled={!setup || !!setup.problem} onClick={() => void change()}>{configured ? "Repair setup" : `Set up ${name}`}</Button>
  }>
    <p class="hint">Global setup · {integration?.scope}</p>
    <section class="section setup-status">
      <Label right={<StatusText tone={agent?.connected ? "ok" : undefined}>{agent?.connected ? "Connected" : setup?.mcp_configured ? "Configured" : "Not configured"}</StatusText>}>MCP</Label>
      <p class="hint">{setup?.mcp_configured ? "Approve sign-in in Prism when the client connects." : "Uses this machine’s gateway."}</p>
      <Label right={<StatusText tone={receiving ? "ok" : undefined}>{!native.value?.observe_native || setup?.hooks_disabled ? "Off" : receiving ? "Receiving" : setup?.hook_installed ? "Configured" : "Not configured"}</StatusText>}>Observation</Label>
      <p class="hint">{setup?.hooks_disabled ? `Hooks are disabled in ${name}. Enable them there to observe tools.` : receiving ? `Last event ${relative(seen?.last_event_at ?? "")}.` : integration?.observation ?? "Run a tool to verify."}</p>
    </section>
    {configured && !agent?.connected ? <section class="section">
      <Label>Connect MCP</Label>
      {integration?.login ? <CodeBlock text={integration.login} copyable /> : null}
      <p class="hint">{integration?.loginHint}</p>
    </section> : null}
    {setup?.problem ? <p class="hint error" role="alert">{setup.problem}</p> : null}
    {message ? <p class="hint" role="status">{message}</p> : null}
    <button type="button" class="link disclosure-link" onClick={() => push({ kind: "harness-files", host })}>
      Files and observer<ChevronIcon direction="right" />
    </button>
    {setup?.setup_present ? <div class="actions update-actions">
      <ConfirmButton variant="quiet" class="danger" confirm="Remove global setup?" busy={busy} onConfirm={() => void change(true)}>Remove setup</ConfirmButton>
      <Button variant="quiet" busy={busy} onClick={() => void refresh().catch(e => { errorMessage.value = describeError(e); })}>Check status</Button>
    </div> : null}
  </Screen></div>;
}

export function HarnessFilesScreen({ host }: { host: string }) {
  const integration = harness(host);
  const setup = hostSetup(native.value, host);
  const [snippet, setSnippet] = useState("");
  const [copyState, copy] = useCopy();
  useEffect(() => {
    let active = true;
    api.getHostHookSnippet(host).then(value => { if (active) setSnippet(value); }).catch(e => { if (active) errorMessage.value = describeError(e); });
    return () => { active = false; };
  }, [host]);
  return <div class="screen pushed"><Screen footer={<Button onClick={pop}>Back to setup</Button>}>
    <section class="section"><Label>MCP settings</Label><CodeBlock text={setup?.mcp_path ?? ""} copyable /></section>
    <section class="section"><Label>Observer</Label><CodeBlock text={setup?.settings_path ?? ""} copyable />
      <Button variant="quiet" state={copyState} disabled={!snippet} onClick={() => void copy(snippet)}>{copyState === "success" ? "Copied" : "Copy observer"}</Button>
    </section>
    <p class="hint">{integration?.scope}. Project overrides stay separate.</p>
    <p class="hint">Settings are backed up. Setup does not approve access or trust hooks.</p>
  </Screen></div>;
}
