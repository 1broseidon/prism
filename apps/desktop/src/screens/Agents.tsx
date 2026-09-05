import * as api from "../api";
import { agents, errorMessage, push, status } from "../state";
import { postureLabel } from "../policy";
import { relative } from "../time";
import type { AgentConfig } from "../types";
import { Button, Chip, Empty, Label, Screen, describeError } from "../ui";

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
      <button type="button" class="title row-btn" onClick={() => push({ kind: "agent", agentId: agent.id })}>
        <span class={`dot ${agent.connected ? "ok" : ""}`} title={agent.connected ? "Session open" : "No open session"} />
        <span class="truncate">{agent.name}</span>
        {statusChip(agent)}
        <span class="chev" aria-hidden="true">
          ›
        </span>
      </button>
      {agent.status === "pending" ? (
        <div class="side">
          <Button variant="quiet" onClick={() => void act(() => api.decideAgent(agent.id, true))}>
            Approve
          </Button>
          <Button variant="quiet" class="danger" onClick={() => void act(() => api.decideAgent(agent.id, false))}>
            Deny
          </Button>
        </div>
      ) : null}
      <div class="sub truncate">
        {!agent.client_id ? agent.tokens.some((t) => t.kind === "manual") ? "Manual token · " : "Token needed · " : ""}
        {agent.client_version ? `v${agent.client_version} · ` : ""}
        {agent.status === "approved" ? `${postureLabel(agent.posture).toLowerCase()} · ` : ""}
        {when}
      </div>
    </div>
  );
}

export function AgentsScreen() {
  const list = agents.value;

  return (
    <div class="screen">
      <Screen footer={<Button onClick={() => push({ kind: "connect-agent" })}>Connect an agent</Button>}>
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
      </Screen>
    </div>
  );
}
