import { useEffect } from "preact/hooks";
import * as api from "../api";
import { activity, agents, errorMessage, pending, push, signins, status } from "../state";
import { mmss, now, relative, secondsUntil } from "../time";
import type { ActivitySummary, AgentConfig, DayActivity, Decision, PendingCall, PendingSignIn } from "../types";
import { Button, CodeBlock, Empty, Label, Screen, describeError } from "../ui";

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
            version <code>{agent.client_version}</code> ·{" "}
          </>
        ) : null}
        {agent.connected ? "session open, waiting" : "not connected right now"}
      </div>
      <p class="note">It sees no tools until you approve. Every call it makes afterwards still goes through your rules.</p>
      {!agent.client_id ? <p class="note">This client also needs a manual token. Create one on its agent screen after approving it.</p> : null}
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
        <b>{signin.agent_name}</b> wants to sign in again
      </div>
      <div class="via">
        client <code>{signin.client_name}</code> · a browser is waiting on this
      </div>
      <p class="note">
        Expected when the client lost its tokens. If nothing on your side asked to sign in, refuse: an approved name is not proof of who is asking.
      </p>
      <div class="actions">
        <Button variant="primary" hint={first ? "A" : undefined} autoFocus={first} onClick={() => void decideSignin(signin, true)}>
          Allow sign-in
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
        <span class="eyebrow">{call.reason === "rate_limit" ? "Running hot · waiting for you" : "Waiting for you"}</span>
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
          onClick={() => void decide(call, "allow", primaryScope(call))}
        >
          {remembers ? "Allow" : "Allow once"}
        </Button>
        <Button variant="danger" hint={first ? "D" : undefined} onClick={() => void decide(call, "deny", "once")}>
          Deny
        </Button>
      </div>
      <div class="actions secondary">
        {remembers ? (
          <Button variant="quiet" onClick={() => void decide(call, "allow", "once")}>
            Just this once
          </Button>
        ) : (
          <Button variant="quiet" onClick={() => void decide(call, "allow", "always")}>
            Always allow this tool
          </Button>
        )}
        <Button variant="quiet" onClick={() => void decide(call, "allow", { for: { minutes: 30 } })}>
          Allow for 30 min
        </Button>
        <Button variant="quiet" onClick={() => void decide(call, "allow", "always", "server")}>
          Everything on {call.server_name}
        </Button>
      </div>
      {remembers ? <p class="note">First use: Prism remembers this answer for {call.tool} from now on.</p> : null}
    </section>
  );
}

function dayLabel(day: DayActivity, today: boolean): string {
  if (today) return "Now";
  return new Date(`${day.date}T12:00:00`).toLocaleDateString(undefined, { weekday: "narrow" });
}

/** One bar per day. Routine actions in ink, the ones that needed a person in amber on top. */
function DailyChart({ days }: { days: DayActivity[] }) {
  const max = Math.max(1, ...days.map((d) => d.routine + d.attention));
  const last = days.length - 1;
  return (
    <div class="daily" role="img" aria-label="Actions per day">
      {days.map((d, i) => {
        const total = d.routine + d.attention;
        const title = `${d.date}: ${total} action${total === 1 ? "" : "s"}${d.attention ? `, ${d.attention} needed attention` : ""}`;
        return (
          <div class={`day ${i === last ? "today" : ""}`} key={d.date} title={title}>
            <div class="bar">
              <span class="attention" style={{ height: `${(d.attention / max) * 100}%` }} />
              <span class="routine" style={{ height: `${(d.routine / max) * 100}%` }} />
            </div>
            <span class="lbl">{dayLabel(d, i === last)}</span>
          </div>
        );
      })}
    </div>
  );
}

/** The week at a glance. Every number is a door into the list, not the list itself. */
function ActivityBlock({ summary }: { summary: ActivitySummary }) {
  const top = summary.agents.slice(0, 3);
  const widest = Math.max(1, ...top.map((a) => a.total));
  const mcpBits = [
    `${summary.mcp.allowed} allowed`,
    summary.mcp.asked ? `${summary.mcp.asked} asked you` : null,
    summary.mcp.denied ? `${summary.mcp.denied} denied` : null,
    summary.mcp.errors ? `${summary.mcp.errors} failed` : null,
  ].filter(Boolean);
  return (
    <>
      <div class="activity-head">
        <div class="stat">
          <b>{summary.total}</b>
          <span>actions</span>
        </div>
        <div class="stat">
          <b class={summary.attention ? "accent" : ""}>{summary.attention}</b>
          <span>needed attention</span>
        </div>
      </div>
      <DailyChart days={summary.daily} />
      <div class="agent-bars">
        {top.map((a) => (
          <button
            type="button"
            class="agent-bar"
            key={a.id}
            title={`${a.total} actions, ${a.attention} needed attention. Show them.`}
            onClick={() => push({ kind: "activity", agentId: a.id })}
          >
            <span class="name">
              {a.name}
              {a.host ? <span class="kind"> native</span> : null}
            </span>
            <span class="track">
              <span class="share" style={{ width: `${(a.total / widest) * 100}%` }} />
              <span class="attention" style={{ width: `${(a.attention / widest) * 100}%` }} />
            </span>
            <span class="agent-count">{a.total}</span>
          </button>
        ))}
      </div>
      <div class="activity-foot">
        <span class="hint">{summary.mcp.allowed + summary.mcp.denied + summary.mcp.asked ? `MCP: ${mcpBits.join(" · ")}` : "No MCP calls yet"}</span>
        <Button variant="quiet" onClick={() => push({ kind: "activity" })}>
          Every action
        </Button>
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
        <Empty title="Nothing waiting.">
          New agents and held calls show up here the moment they ask. Your rules decide the rest.
        </Empty>
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
              <span>
                {st.servers_running}/{st.servers_total} servers · {st.agent_count} agents
              </span>
            ) : null
          }
        >
          Last {summary?.days ?? 7} days
        </Label>
        {summary === null ? (
          <div class="muted small">Loading…</div>
        ) : summary.total === 0 ? (
          <div class="muted small">No actions yet. Connect an agent, or set up a host hook under Agents.</div>
        ) : (
          <ActivityBlock summary={summary} />
        )}
      </div>
      </Screen>
    </div>
  );
}
