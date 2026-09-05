import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { ManualTokenDetails } from "../ManualTokenDetails";
import { agents, errorMessage, pop, push, status } from "../state";
import type { ConnectSnippet, ManualToken } from "../types";
import { Button, CodeBlock, Label, Screen, Segmented, describeError } from "../ui";

export function ConnectAgentScreen() {
  const [snippet, setSnippet] = useState<ConnectSnippet | null>(null);
  const [mode, setMode] = useState<"oauth" | "manual">("oauth");
  const [busy, setBusy] = useState(false);
  const [issued, setIssued] = useState<ManualToken | null>(null);

  useEffect(() => {
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

  return (
    <div class="screen pushed">
      <Screen footer={mode === "manual" ? <Button variant="primary" type="submit" form="manual-client" busy={busy} disabled={busy}>Create token</Button> : undefined}>
        <Segmented label="Connection method" value={mode} options={[{ value: "oauth", label: "OAuth sign-in" }, { value: "manual", label: "Manual token" }]} onChange={setMode} />
        {mode === "oauth" ? <>
          <p class="lede">Give your MCP client this URL. It opens a sign-in, and you approve it here before it gets access.</p>
          {snippet ? <>
            <section class="section"><Label>URL</Label><CodeBlock text={snippet.url} copyable /></section>
            <section class="section"><Label>mcp.json</Label><CodeBlock text={snippet.mcp_json} copyable /></section>
          </> : null}
        </> : <form id="manual-client" onSubmit={create}>
          <p class="lede">For clients that support a bearer token or custom HTTP headers. Create a token here, then paste it into your client.</p>
          <label class="field"><span>Client name</span><input class="input" required maxLength={80} name="name" placeholder="My script" /></label>
          <p class="hint">Creating a token approves this client. Its first use of each tool will ask you for permission.</p>
        </form>}
      </Screen>
    </div>
  );
}
