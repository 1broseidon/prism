import { loadActivity } from "../events";
import { useEffect } from "preact/hooks";
import * as api from "../api";
import { activity, activityError, agents, errorMessage, pending, push, signins, status, tab } from "../state";
import { mmss, now, relative, secondsUntil } from "../time";
import type { ActivitySummary, AgentConfig, DayActivity, Decision, PendingCall, PendingSignIn } from "../types";
import { Button, Chip, CodeBlock, Empty, Label, Screen, describeError } from "../ui";

/** Fallback when a call carries no deadline; mirrors DEFAULT_HOLD_TIMEOUT in prism-core. */
const HOLD_SECONDS = 120;

async function decide(call: PendingCall, verdict: Decision["verdict"], scope: Decision["scope"], target: Decision["target"] = "tool") {
  try {
    await api.decide(call.id, { verdict, scope, target });
  } catch (err) {
    errorMessage.value = describeError(err);
  }
}

/** Under first-use the answer is remembered by default; everywhere else it is one call at a time. */
function primaryScope(call: PendingCall): Decision["scope"] {
  return call.posture === "first_use" ? "always" : "once";
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

async function decideSignin(signin: PendingSignIn, approve: boolean) {
  try {
    await api.decideSignin(signin.id, approve);
    signins.value = (await api.listSignins()).filter((s) => s.needs_consent);
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
      <div class="top">
        <span class="eyebrow">{agent.client_id ? "New agent" : "Manual client"}</span>
        <span class="when">{relative(agent.created_at)}</span>
      </div>
      <div class="ask">
        <b>{agent.name}</b> wants to connect
      </div>
      <div class="via">
        {agent.client_version ? (
          <>
            <code>{agent.client_version}</code> ·{" "}
          </>
        ) : null}
        {agent.connected ? "connected" : "offline"}
        {!agent.client_id ? (
          <>
            {" "}
            <Chip>needs token</Chip>
          </>
        ) : null}
      </div>
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

/** An approved agent's client is signing in again. A public client id proves nothing, so this asks. */
function SignInCard({ signin, first }: { signin: PendingSignIn; first: boolean }) {
  return (
    <section class="hold" aria-live="polite">
      <div class="top">
        <span class="eyebrow">Sign-in</span>
        <span class="when">{relative(signin.requested_at)}</span>
      </div>
      <div class="ask">
        <b>{signin.agent_name}</b> {signin.new_client ? "wants to connect from a new place" : "wants to sign in again"}
      </div>
      <div class="via">
        client <code>{signin.client_name}</code> · a browser is waiting
      </div>
      <p class="note">{signin.new_client ? "A new install or project scope. If you didn't start this, refuse." : "If you didn't start this, refuse."}</p>
      <div class="actions">
        <Button variant="primary" hint={first ? "A" : undefined} autoFocus={first} onClick={() => void decideSignin(signin, true)}>
          Allow
        </Button>
        <Button variant="danger" hint={first ? "D" : undefined} onClick={() => void decideSignin(signin, false)}>
          Refuse
        </Button>
      </div>
    </section>
  );
}

function HoldCard({ call, first }: { call: PendingCall; first: boolean }) {
  const left = call.deadline
    ? secondsUntil(call.deadline)
    : Math.max(0, HOLD_SECONDS - (now.value - Date.parse(call.requested_at)) / 1000);
  const remembers = call.posture === "first_use";
  const args = call.arguments && Object.keys(call.arguments as object).length > 0
    ? JSON.stringify(call.arguments, null, 2)
    : "";

  return (
    <section class="hold" aria-live="polite">
      <div class="top">
        <span class="eyebrow">{call.reason === "rate_limit" ? "Running hot" : "Waiting for you"}</span>
        <span class={`countdown ${left < 20 ? "late" : ""}`} aria-label={`${Math.floor(left)} seconds left`}>
          {mmss(left)}
        </span>
      </div>
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
          title={remembers ? "Remembered for this tool" : "This call only"}
          onClick={() => void decide(call, "allow", primaryScope(call))}
        >
          Allow
        </Button>
        <Button variant="danger" hint={first ? "D" : undefined} onClick={() => void decide(call, "deny", "once")}>
          Deny
        </Button>
      </div>
      <div class="actions-caption">{remembers ? "Allow is remembered for this tool." : "Allow is this call only."}</div>
      <div class="actions secondary">
        {remembers ? (
          <Button variant="quiet" onClick={() => void decide(call, "allow", "once")}>
            Once
          </Button>
        ) : (
          <Button variant="quiet" onClick={() => void decide(call, "allow", "always")}>
            Always
          </Button>
        )}
        <Button variant="quiet" onClick={() => void decide(call, "allow", { for: { minutes: 30 } })}>
          30 min
        </Button>
        <Button variant="quiet" onClick={() => void decide(call, "allow", "always", "server")}>
          All of {call.server_name}
        </Button>
      </div>
    </section>
  );
}

function dayLabel(day: DayActivity, today: boolean): string {
  if (today) return "Now";
  return new Date(`${day.date}T12:00:00`).toLocaleDateString(undefined, { weekday: "narrow" });
}

/** One bar per day. Routine actions in ink, the ones that needed a person in amber on top. Each bar is a door. */
function DailyChart({ days, at }: { days: DayActivity[]; at: string }) {
  const max = Math.max(1, ...days.map((d) => d.routine + d.attention));
  const last = days.length - 1;
  return (
    <div class="daily" role="group" aria-label="Actions per day">
      {days.map((d, i) => {
        const total = d.routine + d.attention;
        const title = `${total} action${total === 1 ? "" : "s"}${d.attention ? `, ${d.attention} needed attention` : ""}`;
        return (
          <button
            type="button"
            class={`day ${i === last ? "today" : ""}`}
            key={d.date}
            title={title}
            disabled={total === 0}
            onClick={() => push({ kind: "activity", day: d.date, at })}
          >
            <span class="bar">
              <span
                class="attention"
                style={{ height: `${(d.attention / max) * 100}%` }}
                title={d.attention ? `${d.attention} needed attention` : undefined}
                onClick={(e) => {
                  if (!d.attention) return;
                  e.stopPropagation();
                  push({ kind: "activity", day: d.date, attention: true, at });
                }}
              />
              <span class="routine" style={{ height: `${(d.routine / max) * 100}%` }} />
            </span>
            <span class="lbl">{dayLabel(d, i === last)}</span>
          </button>
        );
      })}
    </div>
  );
}

/** The week at a glance. Every number is a door into the list, not the list itself. */
function ActivityBlock({ summary }: { summary: ActivitySummary }) {
  const at = summary.window.snapshot_at;
  const days = summary.days;
  const top = summary.agents.slice(0, 3);
  const widest = Math.max(1, ...top.map((a) => a.total));
  return (
    <>
      <div class="activity-head">
        <button type="button" class="stat" onClick={() => push({ kind: "activity", at, days })}>
          <b>{summary.total}</b>
          <span>actions</span>
        </button>
        <button type="button" class="stat" disabled={summary.attention === 0} onClick={() => push({ kind: "activity", attention: true, at, days })}>
          <b class={summary.attention ? "accent" : ""}>{summary.attention}</b>
          <span>needed attention</span>
        </button>
      </div>
      <DailyChart days={summary.daily} at={at} />
      <div class="agent-bars">
        {top.map((a) => (
          <button
            type="button"
            class="agent-bar"
            key={a.id}
            title={a.attention ? `${a.attention} of ${a.total} needed attention` : `${a.total} actions`}
            onClick={() => push(a.attention ? { kind: "activity", agentId: a.id, attention: true, at, days } : { kind: "activity", agentId: a.id, at, days })}
          >
            <span class="name">
              {a.name}

            </span>
            <span class="track">
              <span class="share" style={{ width: `${(a.total / widest) * 100}%` }} />
              <span class="attention" style={{ width: `${(a.attention / widest) * 100}%` }} />
            </span>
            <span class="agent-count">
              {a.attention ? <b class="accent">{a.attention}</b> : null}
              {a.attention ? "/" : ""}
              {a.total}
            </span>
          </button>
        ))}
      </div>
      <div class="activity-foot">
        <button type="button" class="link" onClick={() => push({ kind: "activity", at, days })}>
          All {summary.total} ›
        </button>
      </div>
    </>
  );
}

export function NowScreen() {
  const st = status.value;
  const calls = pending.value;
  const requests = agents.value.filter((a) => a.status === "pending");
  const logins = signins.value;
  const summary = activity.value;

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (isTyping() || event.metaKey || event.ctrlKey || event.altKey) return;
      const key = event.key.toLowerCase();
      if (key !== "a" && key !== "d") return;
      const firstAgent = agents.value.find((a) => a.status === "pending");
      const firstSignin = signins.value[0];
      const firstCall = pending.value[0];
      if (firstAgent) {
        event.preventDefault();
        void decideAgent(firstAgent, key === "a");
      } else if (firstSignin) {
        event.preventDefault();
        void decideSignin(firstSignin, key === "a");
      } else if (firstCall) {
        event.preventDefault();
        void decide(firstCall, key === "a" ? "allow" : "deny", key === "a" ? primaryScope(firstCall) : "once");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const nothing = calls.length === 0 && requests.length === 0 && logins.length === 0;

  return (
    <div class="screen">
      <Screen>
        {nothing ? (
          <Empty title="Nothing waiting." />
        ) : (
          <>
            {requests.map((agent, i) => (
              <AgentCard key={agent.id} agent={agent} first={i === 0} />
            ))}
            {logins.map((signin, i) => (
              <SignInCard key={signin.id} signin={signin} first={requests.length === 0 && i === 0} />
            ))}
            {calls.map((call, i) => (
              <HoldCard key={call.id} call={call} first={requests.length === 0 && logins.length === 0 && i === 0} />
            ))}
          </>
        )}

        <div class="section activity">
          <Label
            right={
              st ? (
                <span class="counts">
                  <button type="button" class="link" onClick={() => (tab.value = "servers")}>
                    {st.servers_running}/{st.servers_total} servers
                  </button>
                  {" · "}
                  <button type="button" class="link" onClick={() => (tab.value = "agents")}>
                    {st.agent_count} agents
                  </button>
                </span>
              ) : null
            }
          >
            Last {summary?.days ?? 7} days · retained
          </Label>
          {activityError.value ? (
            <Button variant="quiet" onClick={() => loadActivity()}>History unavailable · Retry</Button>
          ) : summary === null ? (
            <div class="muted small">Loading…</div>
          ) : summary.total === 0 ? (
            <div class="muted small">No actions yet.</div>
          ) : (
            <ActivityBlock summary={summary} />
          )}
        </div>
      </Screen>
    </div>
  );
}
