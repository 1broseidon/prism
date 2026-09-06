import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { ManualTokenDetails } from "../ManualTokenDetails";
import { ATTENTIONS, POSTURES, findRule, setAccess } from "../policy";
import { agents, errorMessage, pop, push, rules, servers, status } from "../state";
import { relative, remaining } from "../time";
import type { AgentConfig, Attention, Posture, Rule, RuleDecision, ManualToken } from "../types";
import { Button, Chip, ConfirmButton, Label, Screen, Segmented, describeError } from "../ui";

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

function decisionChip(d: RuleDecision) {
  return <Chip tone={d === "allow" ? "ok" : d === "deny" ? "danger" : "warn"}>{d}</Chip>;
}

/** One agent: its status, its posture, how loudly it speaks, and what it may touch. */
export function AgentScreen({ agentId }: { agentId: string }) {
  const [issued, setIssued] = useState<ManualToken | null>(null);
  const [tokenBusy, setTokenBusy] = useState(false);
  const agent = agents.value.find((a) => a.id === agentId);
  const loaded = status.value !== null;
  // Only leave once the first load has happened and the agent really is gone (forgotten elsewhere).
  useEffect(() => {
    if (loaded && !agent) pop();
  }, [loaded, agent]);
  if (!agent) return <div class="screen pushed" />;

  const act = async (fn: () => Promise<unknown>) => {
    try {
      await fn();
      await refresh();
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };

  const replaceToken = async () => {
    if (tokenBusy) return;
    setTokenBusy(true);
    try { setIssued(await api.replaceManualToken(agent.id)); await refresh(); }
    catch (err) { errorMessage.value = describeError(err); }
    finally { setTokenBusy(false); }
  };
  if (issued) return <ManualTokenDetails issued={issued} onDone={() => setIssued(null)} />;

  const setPolicy = (patch: { posture?: Posture; attention?: Attention }) => void act(() => api.setAgentPolicy(agent.id, patch));

  const mine = rules.value.filter((r) => r.agent_id === agent.id);
  const grants: Rule[] = mine.filter((r) => r.tool !== null || r.expires_at !== null);
  const postureHint = POSTURES.find((p) => p.value === agent.posture)?.hint;
  const attentionHint = ATTENTIONS.find((a) => a.value === agent.attention)?.hint;
  const when = agent.decided_at ? `${agent.status} ${relative(agent.decided_at)}` : `asked ${relative(agent.created_at)}`;
  const access = agent.tokens.filter((t) => t.kind === "access");
  const refreshes = agent.tokens.filter((t) => t.kind === "refresh");
  const signedIn = agent.tokens.length > 0;

  const footer =
    agent.status === "approved" ? (
      <ConfirmButton variant="danger" confirm="Revoke?" onConfirm={() => void act(() => api.decideAgent(agent.id, false))}>
        Revoke access
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
        <Button variant="primary" onClick={() => void act(() => api.decideAgent(agent.id, true))}>
          Approve
        </Button>
      </>
    );

  return (
    <div class="screen pushed">
      <Screen footer={footer}>
        <div class="agent-head">
          <span class={`dot ${agent.connected ? "ok" : ""}`} />
          {statusChip(agent)}
          {!agent.client_id ? <Chip tone="accent">manual token</Chip> : null}
          <span class="grow" />
          <span class="sub mono">
            {agent.client_version ? `v${agent.client_version} · ` : ""}
            {when}
          </span>
        </div>

        <section class="section">
          <Label right={signedIn ? <span>{agent.tokens.length}</span> : undefined}>Sign-in</Label>
          {agent.client_id ? (
            <div class="list">
              <div class="setting">
                <div>
                  <div class="setting-title">{signedIn ? "Signed in" : "Not signed in"}</div>
                  {signedIn ? (
                    <div class="hint">
                      {[
                        access.length > 0 ? `access ${remaining(access[access.length - 1].expires_at!)} left` : "access expired",
                        refreshes.length > 0 ? `refresh ${remaining(refreshes[refreshes.length - 1].expires_at!)} left` : null,
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </div>
                  ) : null}
                </div>
                {signedIn ? (
                  <ConfirmButton variant="quiet" class="danger" confirm="Sign out?" onConfirm={() => void act(() => api.revokeAgentTokens(agent.id))}>
                    Sign out
                  </ConfirmButton>
                ) : null}
              </div>
            </div>
          ) : (
            <>
              {signedIn ? null : <p class="hint">Needs a token.</p>}
              {agent.status === "approved" ? <div class="actions update-actions">
                <Button busy={tokenBusy} onClick={() => void replaceToken()}>{signedIn ? "Replace token" : "Create token"}</Button>
                {signedIn ? <ConfirmButton variant="quiet" class="danger" confirm="Revoke?" onConfirm={() => void act(() => api.revokeAgentTokens(agent.id))}>Revoke token</ConfirmButton> : null}
              </div> : <p class="hint">Approve first.</p>}
            </>
          )}
        </section>

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

        <section class="section">
          <Label right={<span>{servers.value.length}</span>}>Servers</Label>
          {servers.value.length === 0 ? (
            <p class="hint">No servers yet.</p>
          ) : (
            <div class="list">
              {servers.value.map((server) => {
                const rule = findRule(rules.value, agent.id, server.id, null);
                const overrides = mine.filter((r) => r.server_id === server.id && r.tool !== null).length;
                return (
                  <div class="item" key={server.id}>
                    <button
                      type="button"
                      class="title row-btn"
                      onClick={() => push({ kind: "agent-server", agentId: agent.id, serverId: server.id })}
                    >
                      <span class="truncate">{server.name}</span>
                      <span class="chev" aria-hidden="true">
                        ›
                      </span>
                    </button>
                    <div class="side">
                      <Segmented
                        small
                        label={`Access to ${server.name}`}
                        value={rule?.decision ?? null}
                        options={[
                          { value: "allow", label: "All" },
                          { value: "ask", label: "Ask" },
                          { value: "deny", label: "None" },
                        ]}
                        onChange={(next) => void act(() => setAccess(agent.id, server.id, null, rule?.decision === next ? null : next))}
                      />
                    </div>
                    <div class="sub">
                      {rule ? "set here" : "follows posture"}
                      {overrides > 0 ? ` · ${overrides} tool override${overrides === 1 ? "" : "s"}` : ""}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>

        <section class="section">
          <Label right={<span>{grants.length}</span>}>Grants</Label>
          {grants.length === 0 ? (
            <p class="hint">None.</p>
          ) : (
            <div class="list">
              {grants.map((rule) => (
                <div class="item" key={rule.id}>
                  <div class="title">
                    {decisionChip(rule.decision)}
                    <span class="truncate mono small">
                      {servers.value.find((s) => s.id === rule.server_id)?.name ?? "any server"}
                      {rule.tool ? ` / ${rule.tool}` : ""}
                    </span>
                  </div>
                  <div class="side">
                    <ConfirmButton variant="quiet" class="danger" confirm="Remove?" onConfirm={() => void act(async () => { await api.deleteRule(rule.id); rules.value = await api.listRules(); })}>
                      Remove
                    </ConfirmButton>
                  </div>
                  <div class="sub">
                    {rule.expires_at ? `${remaining(rule.expires_at)} left` : rule.scope === "session" ? "this session" : "always"}
                    {rule.attention ? ` · ${rule.attention}` : ""}
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </Screen>
    </div>
  );
}
