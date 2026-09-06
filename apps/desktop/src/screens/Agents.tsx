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
      return null;
    case "denied":
      return <Chip tone="danger">refused</Chip>;
    default:
      return <Chip tone="accent">pending</Chip>;
  }
}

/** Coverage in one word. Enforced arrives with phase 2; nothing claims it yet. */
export function coverageChip(agent: AgentConfig) {
  const st = native.value;
  const hs = hostStatus(st, agent.host ?? "");
  const setup = hostSetup(st, agent.host ?? "");
  if (agent.status === "denied") return null;
  if (st?.observe_native && hs?.last_event_at) return <Chip tone="ok">observed</Chip>;
  if (setup?.hook_installed) return <Chip tone="ok">hooked</Chip>;
  return <Chip>not hooked</Chip>;
}

function plural(n: number, word: string) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

/** One row per agent. A harness is one row however many places it registered from. */
function AgentRow({ agent }: { agent: AgentConfig }) {
  const act = async (fn: () => Promise<void>) => {
    try {
      await fn();
      await refresh();
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };
  const harness = !!agent.host;
  const manual = !harness && agent.clients.length === 0;
  const hs = hostStatus(native.value, agent.host ?? "");
  const parts: string[] = [];
  if (harness) {
    parts.push(agent.clients.length > 0 ? plural(agent.clients.length, "connection") : "no MCP connection");
    if (hs?.last_event_at) parts.push(`${hs.actions_7d} actions this week`);
  } else if (manual) {
    parts.push(agent.tokens.some((t) => t.kind === "manual") ? "manual token" : "token needed");
  }
  if (!harness && agent.client_version) parts.push(`v${agent.client_version}`);
  if (agent.status === "approved") parts.push(postureLabel(agent.posture).toLowerCase());
  else parts.push(agent.decided_at ? `${agent.status} ${relative(agent.decided_at)}` : `asked ${relative(agent.created_at)}`);

  return (
    <div class="item">
      <button type="button" class="title row-btn" onClick={() => push({ kind: "agent", agentId: agent.id })}>
        {harness ? (
          <span class="host-mark" aria-hidden="true" />
        ) : (
          <span class={`dot ${agent.connected ? "ok" : ""}`} title={agent.connected ? "Session open" : "No open session"} />
        )}
        <span class="truncate">{agent.name}</span>
        {harness && agent.connected ? <span class="dot ok" title="Session open" /> : null}
        {statusChip(agent)}
        {harness ? coverageChip(agent) : null}
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
      <div class="sub truncate">{parts.join(" · ")}</div>
    </div>
  );
}

export function AgentsScreen() {
  const all = agents.value;
  // Known harnesses first, in a fixed order, present or not; then any harness seen from
  // elsewhere; then everything else that connected over MCP.
  const known = HOSTS.map((h) => all.find((a) => a.id === h.id) ?? placeholderHost(h));
  const otherHosts = all.filter((a) => a.host && !HOSTS.some((h) => h.id === a.id));
  const rest = all.filter((a) => !a.host);
  const list = [...known, ...otherHosts, ...rest];

  return (
    <div class="screen">
      <Screen footer={<Button onClick={() => push({ kind: "connect-agent" })}>Connect an agent</Button>}>
        <Label right={<span>{list.length}</span>}>Agents</Label>
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
