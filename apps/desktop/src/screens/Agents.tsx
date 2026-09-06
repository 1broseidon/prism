import * as api from "../api";
import { agents, errorMessage, native, push, status } from "../state";
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

/** Coverage in one word. Enforced arrives with phase 2; nothing claims it yet. */
export function coverageChip(agent: AgentConfig) {
  const st = native.value;
  if (agent.status === "denied") return <Chip tone="danger">revoked</Chip>;
  if (st?.observe_native && st.last_event_at) return <Chip tone="ok">observed</Chip>;
  return <Chip>MCP only</Chip>;
}

function HostRow({ agent }: { agent: AgentConfig }) {
  const st = native.value;
  const sub =
    agent.status === "denied"
      ? "Its hook events are refused"
      : st?.last_event_at
        ? `Last action ${relative(st.last_event_at)} · ${st.actions_7d} this week`
        : st?.hook_installed
          ? "Hook installed · waiting for the first action"
          : "Set up the hook to see what it does";
  return (
    <div class="item">
      <button type="button" class="title row-btn" onClick={() => push({ kind: "host", agentId: agent.id })}>
        <span class="host-mark" aria-hidden="true" />
        <span class="truncate">{agent.name}</span>
        {coverageChip(agent)}
        <span class="chev" aria-hidden="true">
          ›
        </span>
      </button>
      <div class="sub truncate">{sub}</div>
    </div>
  );
}

/** The hosts Prism knows how to observe, whether or not they have reported yet. */
const HOSTS = [{ id: "host:claude-code", name: "Claude Code", host: "claude-code" }];

export function AgentsScreen() {
  const all = agents.value;
  const list = all.filter((a) => !a.host);
  const hosts = HOSTS.map(
    (h) =>
      all.find((a) => a.id === h.id) ?? {
        id: h.id,
        name: h.name,
        client_name: h.host,
        client_version: null,
        status: "approved" as const,
        created_at: new Date(0).toISOString(),
        decided_at: null,
        posture: "trusted" as const,
        attention: "silent" as const,
        host: h.host,
        connected: false,
        tokens: [],
      },
  );

  return (
    <div class="screen">
      <Screen footer={<Button onClick={() => push({ kind: "connect-agent" })}>Connect an agent</Button>}>
        <Label right={<span>{hosts.length}</span>}>Agent hosts</Label>
        <div class="list">
          {hosts.map((agent) => (
            <HostRow key={agent.id} agent={agent} />
          ))}
        </div>
        <Label right={<span>{list.length}</span>}>MCP agents</Label>
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
