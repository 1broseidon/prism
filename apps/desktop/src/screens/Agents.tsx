import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { agents, errorMessage, status } from "../state";
import { relative } from "../time";
import type { AgentConfig, ConnectSnippet } from "../types";
import { Button, Chip, CodeBlock, Empty, Label, describeError } from "../ui";

async function refresh() {
  agents.value = await api.listAgents();
  status.value = await api.getStatus();
}

function statusChip(agent: AgentConfig) {
  switch (agent.status) {
    case "approved":
      return <Chip tone="ok">approved</Chip>;
    case "denied":
      return <Chip tone="danger">denied</Chip>;
    default:
      return <Chip tone="accent">pending</Chip>;
  }
}

function AgentRow({ agent }: { agent: AgentConfig }) {
  const act = async (fn: () => Promise<void>) => {
    try {
      await fn();
      await refresh();
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };
  const when = agent.decided_at ? `${agent.status} ${relative(agent.decided_at)}` : `asked ${relative(agent.created_at)}`;

  return (
    <div class="item">
      <div class="title">
        <span class={`dot ${agent.connected ? "ok" : ""}`} title={agent.connected ? "Session open" : "No open session"} />
        <span class="truncate">{agent.name}</span>
        {statusChip(agent)}
      </div>
      <div class="side">
        {agent.status !== "approved" ? (
          <Button variant="quiet" onClick={() => void act(() => api.decideAgent(agent.id, true))}>
            Approve
          </Button>
        ) : null}
        {agent.status !== "denied" ? (
          <Button variant="quiet" class="danger" onClick={() => void act(() => api.decideAgent(agent.id, false))}>
            {agent.status === "approved" ? "Revoke" : "Deny"}
          </Button>
        ) : null}
        {agent.status === "denied" ? (
          <Button variant="quiet" class="danger" onClick={() => void act(() => api.removeAgent(agent.id))}>
            Forget
          </Button>
        ) : null}
      </div>
      <div class="sub truncate">
        {agent.client_version ? `v${agent.client_version} · ` : ""}
        {when}
      </div>
    </div>
  );
}

export function AgentsScreen() {
  const [snippet, setSnippet] = useState<ConnectSnippet | null>(null);
  const list = agents.value;

  useEffect(() => {
    api.getConnectSnippet().then(setSnippet).catch((err) => {
      errorMessage.value = describeError(err);
    });
  }, []);

  return (
    <div>
      <Label right={<span>{list.length}</span>}>Agents</Label>
      {list.length === 0 ? (
        <Empty title="No agents yet.">Point any MCP client at Prism. It shows up here the first time it connects, and you approve it before it sees a single tool.</Empty>
      ) : (
        <div class="list">
          {list.map((agent) => (
            <AgentRow key={agent.id} agent={agent} />
          ))}
        </div>
      )}

      <div class="section">
        <Label>Connect an agent</Label>
        <p class="muted small" style={{ margin: "0 0 var(--space-2)" }}>
          No keys. Give any MCP client this URL, or drop the block into its mcp.json. Prism asks you once per client.
        </p>
        {snippet ? (
          <>
            <Label>URL</Label>
            <CodeBlock text={snippet.url} copyable />
            <Label>mcp.json</Label>
            <CodeBlock text={snippet.mcp_json} copyable />
          </>
        ) : null}
      </div>
    </div>
  );
}
