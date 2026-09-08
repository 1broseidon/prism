import * as api from "../api";
import { hostSetup, hostStatus } from "../hosts";
import { ATTENTIONS, POSTURES } from "../policy";
import { native, pop, push, rules, servers } from "../state";
import { relative } from "../time";
import type { AgentConfig, Attention, Posture } from "../types";
import { Button, Chip, ConfirmButton, HubRow, Label, Screen, Segmented, StatusText } from "../ui";
import { coverageStatus } from "./Agents";
import { act, grantsOf, useAgent } from "./AgentSub";

function statusChip(agent: AgentConfig) {
  switch (agent.status) {
    case "approved":
      return <StatusText tone="ok">Approved</StatusText>;
    case "denied":
      return <Chip tone="danger">{agent.host ? "Refused" : "Denied"}</Chip>;
    default:
      return <Chip tone="accent">Pending</Chip>;
  }
}

/** One agent as a hub: its status, its posture and voice, then rows into what it connects with and may touch.
 *  A harness such as Claude Code is one agent however many installs or project scopes registered it. */
export function AgentScreen({ agentId }: { agentId: string }) {
  const agent = useAgent(agentId);
  if (!agent) return <div class="screen pushed" />;

  const setPolicy = (patch: { posture?: Posture; attention?: Attention }) => void act(() => api.setAgentPolicy(agent.id, patch));

  const harness = !!agent.host;
  const manual = !harness && agent.clients.length === 0;
  const hs = hostStatus(native.value, agent.host ?? "");
  const setup = hostSetup(native.value, agent.host ?? "");
  const mine = rules.value.filter((r) => r.agent_id === agent.id);
  const grants = grantsOf(agent.id);
  const set = new Set(mine.filter((r) => r.tool === null && r.expires_at === null).map((r) => r.server_id)).size;
  const flagged = (hs?.by_reason ?? []).reduce((n, r) => n + r.count, 0);
  const postureHint = POSTURES.find((p) => p.value === agent.posture)?.hint;
  const attentionHint = ATTENTIONS.find((a) => a.value === agent.attention)?.hint;
  const when = agent.decided_at ? `${agent.status} ${relative(agent.decided_at)}` : `asked ${relative(agent.created_at)}`;
  const signedIn = agent.tokens.length > 0;
  const setupText = !native.value?.observe_native || setup?.hooks_disabled ? "observation off" : setup?.events_received ? "observed" : setup?.hook_installed ? "configured" : "not configured";

  const footer =
    agent.status === "approved" ? (
      <ConfirmButton variant="danger" confirm={harness ? "Refuse?" : "Revoke?"} onConfirm={() => void act(() => api.decideAgent(agent.id, false))}>
        {harness ? "Refuse" : "Revoke access"}
      </ConfirmButton>
    ) : agent.status === "pending" ? (
      <>
        <Button variant="danger" onClick={() => void act(() => api.decideAgent(agent.id, false))}>
          Deny
        </Button>
        <Button variant="primary" onClick={() => void act(() => api.decideAgent(agent.id, true))}>
          Approve
        </Button>
      </>
    ) : (
      <>
        {harness ? null : (
          <ConfirmButton
            variant="danger"
            confirm="Forget?"
            onConfirm={() =>
              void act(async () => {
                await api.removeAgent(agent.id);
                pop();
              })
            }
          >
            Forget
          </ConfirmButton>
        )}
        <Button variant="primary" onClick={() => void act(() => api.decideAgent(agent.id, true))}>
          {harness ? "Restore" : "Approve"}
        </Button>
      </>
    );

  return (
    <div class="screen pushed">
      <Screen footer={footer}>
        <div class="agent-head">
          <span class={`dot ${agent.connected ? "ok" : ""}`} title={agent.connected ? "Session open" : "No open session"} />
          {statusChip(agent)}
          {harness ? coverageStatus(agent) : null}
          {manual ? <StatusText>Manual token</StatusText> : null}
          <span class="grow" />
          {harness && hs?.last_event_at ? (
            <button type="button" class="link" onClick={() => push({ kind: "activity", agentId })}>
              {hs.actions_7d} this week · {relative(hs.last_event_at)} ›
            </button>
          ) : (
            <span class="sub mono">
              {agent.client_version ? `v${agent.client_version} · ` : ""}
              {when}
            </span>
          )}
        </div>

        <section class="section">
          <Label>Posture</Label>
          <Segmented label="Posture" value={agent.posture} options={POSTURES} onChange={(posture) => setPolicy({ posture })} />
          <p class="hint">{postureHint}</p>
        </section>

        <section class="section">
          <Label>Attention</Label>
          <Segmented label="Attention" value={agent.attention} options={ATTENTIONS} onChange={(attention) => setPolicy({ attention })} />
          <p class="hint">{attentionHint}</p>
        </section>

        <section class="section hub">
          <HubRow
            label={manual ? "Sign-in" : "Connections"}
            value={manual ? (signedIn ? "token" : "needs a token") : `${agent.clients.length}${agent.clients.length > 0 ? (signedIn ? " · signed in" : " · signed out") : ""}`}
            onClick={() => push({ kind: "agent-connections", agentId: agent.id })}
          />
          {harness ? (
            <HubRow
              label="Setup"
              value={`${setupText}${flagged > 0 ? ` · ${flagged} flagged` : ""}`}
              tone={flagged > 0 ? "accent" : undefined}
              onClick={() => push({ kind: "agent-harness", agentId: agent.id })}
            />
          ) : null}
          <HubRow
            label="Servers"
            value={`${servers.value.length}${set > 0 ? ` · ${set} set here` : ""}`}
            onClick={() => push({ kind: "agent-servers", agentId: agent.id })}
          />
          <HubRow label="Grants" value={String(grants.length)} onClick={() => push({ kind: "agent-grants", agentId: agent.id })} />
        </section>
      </Screen>
    </div>
  );
}
