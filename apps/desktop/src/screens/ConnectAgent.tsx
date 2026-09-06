import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { ManualTokenDetails } from "../ManualTokenDetails";
import { HOSTS, hostSetup } from "../hosts";
import { loadNativeStatus } from "../events";
import { native, agents, errorMessage, pop, push, status } from "../state";
import type { ConnectSnippet, ManualToken } from "../types";
import { Button, Chip, CodeBlock, Label, Screen, Segmented, describeError } from "../ui";

export function ConnectAgentScreen() {
  const [custom, setCustom] = useState(false);
  const [snippet, setSnippet] = useState<ConnectSnippet | null>(null);
  const [mode, setMode] = useState<"oauth" | "manual">("oauth");
  const [busy, setBusy] = useState(false);
  const [issued, setIssued] = useState<ManualToken | null>(null);

  useEffect(() => {
    loadNativeStatus();
    api.getConnectSnippet().then(setSnippet).catch((err) => { errorMessage.value = describeError(err); });
  }, []);

  const create = async (event: Event) => {
    event.preventDefault();
    if (busy) return;
    const name = String(new FormData(event.currentTarget as HTMLFormElement).get("name") ?? "");
    setBusy(true);
    try {
      const token = await api.createManualAgent(name);
      setIssued(token);
      agents.value = await api.listAgents();
      status.value = await api.getStatus();
    } catch (err) { errorMessage.value = describeError(err); }
    finally { setBusy(false); }
  };

  if (issued) return <ManualTokenDetails issued={issued} onDone={() => {
    const agentId = issued.agent_id;
    setIssued(null);
    pop();
    push({ kind: "agent", agentId });
  }} />;

  if (!custom) return <div class="screen pushed"><Screen>
    <Label>Choose your agent</Label>
    <div class="list harness-picker">
      {HOSTS.map((h) => {
        const configured = hostSetup(native.value, h.host);
        return <button key={h.host} type="button" class="item harness-choice" onClick={() => push({ kind: "harness-setup", host: h.host })}>
          <span class="host-mark" aria-hidden="true" /><span><strong>{h.name}</strong><small>MCP + native observation</small></span>
          {configured?.mcp_configured && configured.hook_installed ? <Chip>configured</Chip> : null}<span class="chev">›</span>
        </button>;
      })}
      <button type="button" class="item harness-choice" onClick={() => setCustom(true)}>
        <span class="host-mark" aria-hidden="true" /><span><strong>Other</strong><small>Connect any MCP client</small></span><span class="chev">›</span>
      </button>
    </div>
    <p class="hint">Set up once for all your projects.</p>
  </Screen></div>;

  return (
    <div class="screen pushed">
      <Screen footer={mode === "manual" ? <Button variant="primary" type="submit" form="manual-client" busy={busy} disabled={busy}>Create token</Button> : undefined}>
        <Segmented label="Connection method" value={mode} options={[{ value: "oauth", label: "OAuth sign-in" }, { value: "manual", label: "Manual token" }]} onChange={setMode} />
        {mode === "oauth" ? <>
          <p class="lede">Add to your client. Approve it here when it signs in.</p>
          {snippet ? <>
            <section class="section"><Label>URL</Label><CodeBlock text={snippet.url} copyable /></section>
            <section class="section"><Label>mcp.json</Label><CodeBlock text={snippet.mcp_json} copyable /></section>
          </> : null}
        </> : <form id="manual-client" onSubmit={create}>
          <label class="field"><span>Client name</span><input class="input" required maxLength={80} name="name" placeholder="My script" /></label>
          <p class="hint">Creating the token approves the client.</p>
        </form>}
      </Screen>
    </div>
  );
}
