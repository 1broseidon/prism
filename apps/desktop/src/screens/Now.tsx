import { useEffect } from "preact/hooks";
import * as api from "../api";
import { agents, audit, errorMessage, pending, status } from "../state";
import { clock, mmss, now, relative } from "../time";
import type { AgentConfig, AuditEntry, Decision, PendingCall } from "../types";
import { Button, CodeBlock, Empty, Label, describeError } from "../ui";

/** Mirrors DEFAULT_HOLD_TIMEOUT in crates/prism-core/src/approval.rs. */
const HOLD_SECONDS = 120;

async function decide(call: PendingCall, verdict: Decision["verdict"], scope: Decision["scope"]) {
  try {
    await api.decide(call.id, { verdict, scope });
  } catch (err) {
    errorMessage.value = describeError(err);
  }
}

async function decideAgent(agent: AgentConfig, approve: boolean) {
  try {
    await api.decideAgent(agent.id, approve);
    agents.value = await api.listAgents();
    status.value = await api.getStatus();
  } catch (err) {
    errorMessage.value = describeError(err);
  }
}

function isTyping(): boolean {
  const el = document.activeElement;
  return el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement;
}

/** An unknown MCP client asked to connect. Approving is what makes tools visible to it. */
function AgentCard({ agent, first }: { agent: AgentConfig; first: boolean }) {
  return (
    <section class="hold" aria-live="polite">
      <Label right={<span class="mono">{relative(agent.created_at)}</span>}>
        <span class="accent">New agent</span>
      </Label>
      <div class="ask">
        <b>{agent.name}</b> wants to connect
      </div>
      <div class="via">
        {agent.client_version ? (
          <>
            version <code>{agent.client_version}</code> ·{" "}
          </>
        ) : null}
        {agent.connected ? "session open, waiting" : "not connected right now"}
      </div>
      <p class="muted small" style={{ margin: "var(--space-2) 0 0" }}>
        It sees no tools until you approve. Every call it makes afterwards still goes through your rules.
      </p>
      <div class="actions">
        <Button variant="primary" hint={first ? "A" : undefined} autoFocus={first} onClick={() => void decideAgent(agent, true)}>
          Approve
        </Button>
        <Button variant="danger" hint={first ? "D" : undefined} onClick={() => void decideAgent(agent, false)}>
          Deny
        </Button>
      </div>
    </section>
  );
}

function HoldCard({ call, first }: { call: PendingCall; first: boolean }) {
  const elapsed = (now.value - Date.parse(call.requested_at)) / 1000;
  const left = Math.max(0, HOLD_SECONDS - elapsed);
  const frac = left / HOLD_SECONDS;
  const args = call.arguments && Object.keys(call.arguments as object).length > 0
    ? JSON.stringify(call.arguments, null, 2)
    : "";

  return (
    <section class="hold" aria-live="polite">
      <Label right={<span class="mono">{mmss(left)}</span>}>
        <span class="accent">Waiting for you</span>
      </Label>
      <div class="ask">
        <b>{call.agent_name}</b> wants to call <span class="tool">{call.tool}</span>
      </div>
      <div class="via">
        on <code>{call.server_name}</code>
      </div>
      <CodeBlock text={args} emptyText="no arguments" />
      <div class="actions">
        <Button
          variant="primary"
          hint={first ? "A" : undefined}
          autoFocus={first}
          onClick={() => void decide(call, "allow", "once")}
        >
          Allow once
        </Button>
        <Button variant="danger" hint={first ? "D" : undefined} onClick={() => void decide(call, "deny", "once")}>
          Deny
        </Button>
      </div>
      <div class="actions secondary">
        <Button variant="quiet" onClick={() => void decide(call, "allow", "session")}>
          Allow for this session
        </Button>
        <Button variant="quiet" onClick={() => void decide(call, "allow", "always")}>
          Always allow this tool
        </Button>
      </div>
      <div class={`clock ${left < 20 ? "late" : ""}`} aria-hidden="true">
        <i style={{ transform: `scaleX(${frac})` }} />
      </div>
    </section>
  );
}

function verdictTone(entry: AuditEntry): string {
  switch (entry.verdict) {
    case "allowed":
      return "ok";
    case "denied":
      return "danger";
    case "timeout":
      return "warn";
    default:
      return "danger";
  }
}

function sourceText(entry: AuditEntry): string {
  switch (entry.source.kind) {
    case "human":
      return "you";
    case "rule":
      return "rule";
    case "unapproved":
      return "unapproved";
    default:
      return "timeout";
  }
}

export function NowScreen() {
  const st = status.value;
  const calls = pending.value;
  const requests = agents.value.filter((a) => a.status === "pending");

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (isTyping() || event.metaKey || event.ctrlKey || event.altKey) return;
      const key = event.key.toLowerCase();
      if (key !== "a" && key !== "d") return;
      const firstAgent = agents.value.find((a) => a.status === "pending");
      const firstCall = pending.value[0];
      if (firstAgent) {
        event.preventDefault();
        void decideAgent(firstAgent, key === "a");
      } else if (firstCall) {
        event.preventDefault();
        void decide(firstCall, key === "a" ? "allow" : "deny", "once");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const nothing = calls.length === 0 && requests.length === 0;

  return (
    <div>
      {nothing ? (
        <Empty title="Nothing waiting.">
          New agents and held calls show up here the moment they ask. Your rules decide the rest.
        </Empty>
      ) : (
        <>
          {requests.map((agent, i) => (
            <AgentCard key={agent.id} agent={agent} first={i === 0} />
          ))}
          {calls.map((call, i) => (
            <HoldCard key={call.id} call={call} first={requests.length === 0 && i === 0} />
          ))}
        </>
      )}

      <div class="section feed">
        <Label
          right={
            st ? (
              <span>
                {st.servers_running}/{st.servers_total} servers · {st.agent_count} agents
              </span>
            ) : null
          }
        >
          Recent
        </Label>
        {audit.value.length === 0 ? (
          <div class="muted small">No calls yet.</div>
        ) : (
          audit.value.map((entry) => (
            <div class="row" key={entry.id}>
              <span class={`dot ${verdictTone(entry)}`} />
              <time dateTime={entry.at}>{clock(entry.at)}</time>
              <span class="who">
                <b>{entry.agent_name}</b> · {entry.tool}
              </span>
              <span class="src">{sourceText(entry)}</span>
              {entry.error ? <span class="err">{entry.error}</span> : null}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
