import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { ManualTokenDetails } from "../ManualTokenDetails";
import { HOSTS, hostSetup } from "../hosts";
import { loadNativeStatus } from "../events";
import {
  agents,
  clearConnectAgentDraft,
  completeScreen,
  connectAgentDraft,
  discardManualToken,
  errorMessage,
  manualTokens,
  native,
  push,
  rememberManualToken,
  status,
  updateConnectAgentDraft,
} from "../state";
import { Button, ChevronIcon, CodeBlock, Label, Screen, Segmented, StatusText, describeError } from "../ui";

export function ConnectAgentScreen() {
  const [busy, setBusy] = useState(false);
  const draft = connectAgentDraft.value;
  const { custom, mode, snippet } = draft;
  const issued = draft.issuedAgentId ? manualTokens.value[draft.issuedAgentId] ?? null : null;

  useEffect(() => {
    loadNativeStatus();
    if (!connectAgentDraft.value.snippet) {
      api.getConnectSnippet().then((value) => updateConnectAgentDraft({ snippet: value })).catch((err) => { errorMessage.value = describeError(err); });
    }
  }, []);

  const create = async (event: Event) => {
    event.preventDefault();
    if (busy) return;
    const name = draft.name;
    setBusy(true);
    try {
      const token = await api.createManualAgent(name);
      rememberManualToken(token);
      updateConnectAgentDraft({ issuedAgentId: token.agent_id });
      agents.value = await api.listAgents();
      status.value = await api.getStatus();
    } catch (err) { errorMessage.value = describeError(err); }
    finally { setBusy(false); }
  };

  if (issued) return <ManualTokenDetails issued={issued} onDone={() => {
    const agentId = issued.agent_id;
    completeScreen({ kind: "connect-agent" });
    discardManualToken(agentId);
    clearConnectAgentDraft();
    push({ kind: "agent", agentId });
  }} />;

  if (!custom) return <div class="screen pushed"><Screen>
    <section class="gateway-summary">
      <Label right={<StatusText tone={status.value?.listening ? "ok" : "danger"}>{status.value?.listening ? "Ready" : "Unavailable"}</StatusText>}>Local gateway</Label>
      <div class="gateway-address">
        <span class="mono">127.0.0.1:{status.value?.listen_port ?? "…"}</span>
        <span>{status.value?.listening ? "Agents connect through this machine." : "Connection setup is unavailable."}</span>
      </div>
    </section>
    <Label>Choose your agent</Label>
    <div class="list harness-picker">
      {HOSTS.map((h) => {
        const configured = hostSetup(native.value, h.host);
        return <button key={h.host} type="button" class="item harness-choice" onClick={() => push({ kind: "harness-setup", host: h.host })}>
          <span><strong>{h.name}</strong><small>MCP + native observation</small></span>
          {configured?.mcp_configured && configured.hook_installed ? <StatusText>Configured</StatusText> : null}<span class="chev"><ChevronIcon /></span>
        </button>;
      })}
      <button type="button" class="item harness-choice" onClick={() => updateConnectAgentDraft({ custom: true })}>
        <span><strong>Other</strong><small>Connect any MCP client</small></span><span class="chev"><ChevronIcon /></span>
      </button>
    </div>
    <p class="hint">Set up once for all your projects.</p>
  </Screen></div>;

  return (
    <div class="screen pushed">
      <Screen footer={mode === "manual" ? <Button variant="primary" type="submit" form="manual-client" busy={busy} disabled={busy}>Create token</Button> : undefined}>
        <Segmented label="Connection method" value={mode} options={[{ value: "oauth", label: "OAuth sign-in" }, { value: "manual", label: "Manual token" }]} onChange={(next) => updateConnectAgentDraft({ mode: next })} />
        {mode === "oauth" ? <>
          <p class="lede">Add to your client. Approve it here when it signs in.</p>
          {snippet ? <>
            <section class="section"><Label>URL</Label><CodeBlock text={snippet.url} copyable /></section>
            <section class="section"><Label>mcp.json</Label><CodeBlock text={snippet.mcp_json} copyable /></section>
          </> : null}
        </> : <form id="manual-client" onSubmit={create}>
          <label class="field"><span>Client name</span><input class="input" required maxLength={80} name="name" value={draft.name} onInput={(event) => updateConnectAgentDraft({ name: event.currentTarget.value })} placeholder="My script" /></label>
          <p class="hint">Creating the token approves the client.</p>
        </form>}
      </Screen>
    </div>
  );
}
