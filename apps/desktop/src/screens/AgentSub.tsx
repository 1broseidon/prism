import { useEffect } from "preact/hooks";
import * as api from "../api";
import { ManualTokenDetails } from "../ManualTokenDetails";
import { findRule, setAccess } from "../policy";
import {
  agents,
  clearResumableNavigation,
  discardManualToken,
  errorMessage,
  issueManualToken,
  issuingManualTokens,
  manualTokens,
  pop,
  push,
  rules,
  servers,
  status,
} from "../state";
import { relative, remaining } from "../time";
import type { AgentConfig, Rule, RuleDecision } from "../types";
import { Button, ChevronIcon, Chip, ConfirmButton, Pager, Screen, Segmented, StatusText, describeError, usePage } from "../ui";
import { HarnessSections } from "./Host";

/** Rows per page on an agent subscreen: six fit the budget with the pager in the footer. */
export const PAGE = 6;

export async function refresh(): Promise<void> {
  agents.value = await api.listAgents();
  status.value = await api.getStatus();
}

/** Run one agent action, then reload what it changed. Failures land in the notice. */
export async function act(fn: () => Promise<unknown>): Promise<void> {
  try {
    await fn();
    await refresh();
  } catch (err) {
    errorMessage.value = describeError(err);
  }
}

/** The agent behind a screen. Leaves once the first load shows it is gone (forgotten elsewhere). */
export function useAgent(agentId: string): AgentConfig | undefined {
  const agent = agents.value.find((a) => a.id === agentId);
  const loaded = status.value !== null;
  useEffect(() => {
    if (loaded && !agent) pop();
  }, [loaded, agent]);
  return agent;
}

export function decisionChip(d: RuleDecision) {
  return <Chip tone={d === "allow" ? "ok" : d === "deny" ? "danger" : "warn"}>{d[0].toUpperCase() + d.slice(1)}</Chip>;
}

/** Grants are the rules that say more than the server-wide default: a tool, or a time box. */
export function grantsOf(agentId: string): Rule[] {
  return rules.value.filter((r) => r.agent_id === agentId && (r.tool !== null || r.expires_at !== null));
}

/** Every MCP client that registered as this agent, and the sign-in behind them. */
export function AgentConnectionsScreen({ agentId }: { agentId: string }) {
  const agent = useAgent(agentId);
  const issued = manualTokens.value[agentId] ?? null;
  const tokenBusy = !!issuingManualTokens.value[agentId];
  const clients = agent?.clients ?? [];
  const { rows, offset, setOffset, total } = usePage(clients, PAGE, agentId);
  if (!agent) return <div class="screen pushed" />;
  if (issued) return <ManualTokenDetails issued={issued} onDone={() => {
    discardManualToken(agentId);
    clearResumableNavigation({ kind: "agent-connections", agentId });
  }} />;

  const harness = !!agent.host;
  const manual = !harness && clients.length === 0;
  const access = agent.tokens.filter((t) => t.kind === "access");
  const refreshes = agent.tokens.filter((t) => t.kind === "refresh");
  const signedIn = agent.tokens.length > 0;
  const replaceToken = async () => {
    if (tokenBusy) return;
    try {
      await issueManualToken(agent.id, () => api.replaceManualToken(agent.id));
      await refresh();
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };

  const footer = (
    <>
      {total > PAGE ? <Pager offset={offset} size={PAGE} total={total} onOffset={setOffset} /> : null}
      <span class="spacer" />
      {manual ? (
        agent.status === "approved" ? (
          <>
            {signedIn ? (
              <ConfirmButton variant="quiet" class="danger" confirm="Revoke?" onConfirm={() => void act(() => api.revokeAgentTokens(agent.id))}>
                Revoke token
              </ConfirmButton>
            ) : null}
            <Button busy={tokenBusy} onClick={() => void replaceToken()}>
              {signedIn ? "Replace token" : "Create token"}
            </Button>
          </>
        ) : null
      ) : signedIn ? (
        <ConfirmButton variant="quiet" class="danger" confirm="Sign out?" onConfirm={() => void act(() => api.revokeAgentTokens(agent.id))}>
          Sign out everywhere
        </ConfirmButton>
      ) : null}
    </>
  );

  return (
    <div class="screen pushed">
      <Screen footer={footer}>
        {signedIn ? (
          <p class="lede">
            {[
              access.length > 0 ? `Access ${remaining(access[access.length - 1].expires_at!)} left` : "Access expired",
              refreshes.length > 0 ? `refresh ${remaining(refreshes[refreshes.length - 1].expires_at!)} left` : null,
            ]
              .filter(Boolean)
              .join(" · ")}
            .
          </p>
        ) : manual ? (
          <p class="lede">{agent.status === "approved" ? "Needs a token." : "Approve first, then create a token."}</p>
        ) : null}
        {clients.length > 0 ? (
          <div class="list">
            {rows.map((client) => (
              <div class="item" key={client.client_id}>
                <div class="title">
                  <span class="truncate">{client.client_name}</span>
                  {client.signed_in ? <StatusText tone="ok">Signed in</StatusText> : <StatusText>Signed out</StatusText>}
                </div>
                <div class="side">
                  <ConfirmButton variant="quiet" class="danger" confirm="Forget?" onConfirm={() => void act(() => api.forgetClient(agent.id, client.client_id))}>
                    Forget
                  </ConfirmButton>
                </div>
                <div class="sub truncate">
                  {client.origin ? `from ${client.origin} · ` : ""}
                  registered {relative(client.created_at)}
                </div>
              </div>
            ))}
          </div>
        ) : harness ? (
          <p class="hint">No MCP client yet. Point it at Prism from Connect an agent.</p>
        ) : null}
        {harness ? <p class="hint">Every install or project scope that registers Prism lands here after one sign-in consent.</p> : null}
      </Screen>
    </div>
  );
}

/** Hook setup and the watch list for a harness. */
export function AgentHarnessScreen({ agentId }: { agentId: string }) {
  const agent = useAgent(agentId);
  if (!agent?.host) return <div class="screen pushed" />;
  return (
    <div class="screen pushed">
      <Screen>
        <HarnessSections agentId={agent.id} host={agent.host} />
      </Screen>
    </div>
  );
}

/** What this agent may touch on each server: the server-wide default, and a way into its tools. */
export function AgentServersScreen({ agentId }: { agentId: string }) {
  const agent = useAgent(agentId);
  const { rows, offset, setOffset, total } = usePage(servers.value, PAGE, agentId);
  if (!agent) return <div class="screen pushed" />;
  const mine = rules.value.filter((r) => r.agent_id === agent.id);
  return (
    <div class="screen pushed">
      <Screen footer={total > PAGE ? <Pager offset={offset} size={PAGE} total={total} onOffset={setOffset} /> : undefined}>
        {total === 0 ? (
          <p class="hint">No servers yet.</p>
        ) : (
          <div class="list">
            {rows.map((server) => {
              const rule = findRule(rules.value, agent.id, server.id, null);
              const overrides = mine.filter((r) => r.server_id === server.id && r.tool !== null).length;
              return (
                <div class="item" key={server.id}>
                  <button type="button" class="title row-btn" onClick={() => push({ kind: "agent-server", agentId: agent.id, serverId: server.id })}>
                    <span class="truncate">{server.name}</span>
                    <span class="chev"><ChevronIcon /></span>
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
      </Screen>
    </div>
  );
}

/** Standing and time-boxed grants for this agent, each removable. */
export function AgentGrantsScreen({ agentId }: { agentId: string }) {
  const agent = useAgent(agentId);
  const grants = grantsOf(agentId);
  const { rows, offset, setOffset, total } = usePage(grants, PAGE, agentId);
  if (!agent) return <div class="screen pushed" />;
  return (
    <div class="screen pushed">
      <Screen footer={total > PAGE ? <Pager offset={offset} size={PAGE} total={total} onOffset={setOffset} /> : undefined}>
        {total === 0 ? (
          <p class="hint">None. Grants appear here when a hold is allowed for longer than once.</p>
        ) : (
          <div class="list">
            {rows.map((rule) => (
              <div class="item" key={rule.id}>
                <div class="title">
                  {decisionChip(rule.decision)}
                  <span class="truncate mono small">
                    {servers.value.find((s) => s.id === rule.server_id)?.name ?? "any server"}
                    {rule.tool ? ` / ${rule.tool}` : ""}
                  </span>
                </div>
                <div class="side">
                  <ConfirmButton
                    variant="quiet"
                    class="danger"
                    confirm="Remove?"
                    onConfirm={() =>
                      void act(async () => {
                        await api.deleteRule(rule.id);
                        rules.value = await api.listRules();
                      })
                    }
                  >
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
      </Screen>
    </div>
  );
}
