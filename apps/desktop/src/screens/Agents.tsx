import * as api from "../api";
import { agents, errorMessage, native, push, status } from "../state";
import { HOSTS, hostSetup, hostStatus, placeholderHost } from "../hosts";
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
  const hs = hostStatus(st, agent.host ?? "");
  const setup = hostSetup(st, agent.host ?? "");
  if (agent.status === "denied") return <Chip tone="danger">refused</Chip>;
  if (st?.observe_native && hs?.last_event_at) return <Chip tone="ok">observed</Chip>;
  if (setup?.hook_installed) return <Chip tone="ok">hooked</Chip>;
  return <Chip>not hooked up</Chip>;
}

function HostRow({ agent }: { agent: AgentConfig }) {
  const st = native.value;
  const hs = hostStatus(st, agent.host ?? "");
  const setup = hostSetup(st, agent.host ?? "");
  const sub =
    agent.status === "denied"
      ? "Refused"
      : hs?.last_event_at
        ? `${hs.actions_7d} this week · ${relative(hs.last_event_at)}`
        : setup?.hook_installed
          ? "Hooked · nothing yet"
          : "Not hooked up";
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

export function AgentsScreen() {
  const all = agents.value;
  const list = all.filter((a) => !a.host);
  const hosts = HOSTS.map((h) => all.find((a) => a.id === h.id) ?? placeholderHost(h));

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
          <Empty title="No agents yet." />
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
