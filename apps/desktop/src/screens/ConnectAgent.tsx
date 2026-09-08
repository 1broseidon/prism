import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { ManualTokenDetails } from "../ManualTokenDetails";
import { HOSTS, hostPresent } from "../hosts";
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
import { Button, ChevronIcon, CodeBlock, Empty, Label, Screen, Segmented, StatusText, Pager, usePage, describeError } from "../ui";

export function ConnectAgentScreen() {
  const [busy, setBusy] = useState(false);
  // Harnesses already on the Agents list are repaired from their own Setup row, not added twice.
  const available = HOSTS.filter((h) => !hostPresent(native.value, agents.value, h));
  const { rows, offset, setOffset, total } = usePage(available, 5);
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

  if (!custom) return <div class="screen pushed"><Screen footer={<>
    {total > 5 ? <Pager offset={offset} size={5} total={total} onOffset={setOffset} /> : undefined}
    <Button onClick={() => updateConnectAgentDraft({ custom: true })}>Other agent</Button>
  </>}>
    <section class="gateway-summary">
      <Label right={<StatusText tone={status.value?.listening ? "ok" : "danger"}>{status.value?.listening ? "Ready" : "Unavailable"}</StatusText>}>Local gateway</Label>
      <div class="gateway-address">
        <span class="mono">127.0.0.1:{status.value?.listen_port ?? "…"}</span>
        <span>{status.value?.listening ? "Agents connect through this machine." : "Connection setup is unavailable."}</span>
      </div>
    </section>
    <Label>Choose your agent</Label>
    {available.length === 0 ? <Empty title="Every known agent is set up.">Use Other agent for anything else.</Empty> : <div class="list harness-picker">
      {rows.map((h) => (
        <button key={h.host} type="button" class="item harness-choice" onClick={() => push({ kind: "harness-setup", host: h.host })}>
          <span><strong>{h.name}</strong><small>{h.scope}</small></span>
          <span class="chev"><ChevronIcon /></span>
        </button>
      ))}
    </div>}
    {available.length ? <p class="hint">Global MCP + observation. Project overrides stay separate.</p> : null}
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
