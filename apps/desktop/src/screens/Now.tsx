import { harness, hostSetup } from "../hosts";
import { loadActivity } from "../events";
import { signal } from "@preact/signals";
import { useLayoutEffect, useRef, useState } from "preact/hooks";
import * as api from "../api";
import { harnessSetupPending, reconcileQueue } from "../lifecycle";
import { activity, activityError, agents, connectAgentDraft, discardResumableNavigation, errorMessage, issuingManualTokens, manualTokens, native, pending, push, queueCursor, queuePosition, resumableNavigation, resumeNavigation, signins, status, tab } from "../state";
import { mmss, now, relative, secondsUntil } from "../time";
import type { ActivitySummary, AgentConfig, DayActivity, Decision, PendingCall, PendingSignIn } from "../types";
import { Button, ChevronIcon, Chip, Label, Screen, describeError, useCopy } from "../ui";

/** Fallback when a call carries no deadline; mirrors DEFAULT_HOLD_TIMEOUT in prism-core. */
const HOLD_SECONDS = 120;
let lastDecisionAt = -Infinity;
export const decisionBusy = signal(false);

function beginDecision(): boolean {
  const at = performance.now();
  if (decisionBusy.value || at - lastDecisionAt < 400) return false;
  lastDecisionAt = at;
  decisionBusy.value = true;
  return true;
}

export async function decide(call: PendingCall, verdict: Decision["verdict"], scope: Decision["scope"], target: Decision["target"] = "tool"): Promise<boolean> {
  if (!pending.value.some((p) => p.id === call.id) || !beginDecision()) return false;
  try {
    await api.decide(call.id, { verdict, scope, target });
    // Also advance in the browser mock, where there is no call_decided event.
    pending.value = pending.value.filter((p) => p.id !== call.id);
    return true;
  } catch (err) {
    errorMessage.value = describeError(err);
    return false;
  } finally {
    decisionBusy.value = false;
  }
}

/** Under first-use the answer is remembered by default; everywhere else it is one call at a time. */
export function primaryScope(call: PendingCall): Decision["scope"] {
  return call.posture === "first_use" ? "always" : "once";
}

async function decideAgent(agent: AgentConfig, approve: boolean) {
  if (!agents.value.some((a) => a.id === agent.id && a.status === "pending") || !beginDecision()) return;
  try {
    await api.decideAgent(agent.id, approve);
    agents.value = await api.listAgents();
    status.value = await api.getStatus();
  } catch (err) {
    errorMessage.value = describeError(err);
  } finally {
    decisionBusy.value = false;
  }
}

async function decideSignin(signin: PendingSignIn, approve: boolean) {
  if (!signins.value.some((s) => s.id === signin.id && s.needs_consent) || !beginDecision()) return;
  try {
    await api.decideSignin(signin.id, approve);
    signins.value = (await api.listSignins()).filter((s) => s.needs_consent);
    status.value = await api.getStatus();
  } catch (err) {
    errorMessage.value = describeError(err);
  } finally {
    decisionBusy.value = false;
  }
}

function isTyping(): boolean {
  const el = document.activeElement;
  return el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement
    || (el instanceof HTMLElement && el.isContentEditable);
}

/** Both request screens bind shortcuts to the item currently on screen. */
export function useDecisionKeys(onDecision: (approve: boolean) => void, onMove?: (delta: number) => void) {
  useLayoutEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.repeat || isTyping() || event.metaKey || event.ctrlKey || event.altKey) return;
      const key = event.key.toLowerCase();
      if (key === "a" || key === "d") {
        event.preventDefault();
        onDecision(key === "a");
      } else if (onMove && (key === "arrowleft" || key === "arrowright")) {
        event.preventDefault();
        onMove(key === "arrowleft" ? -1 : 1);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onDecision, onMove]);
}

export type QueueItem =
  | { key: string; kind: "agent"; agent: AgentConfig }
  | { key: string; kind: "signin"; signin: PendingSignIn }
  | { key: string; kind: "call"; call: PendingCall };

/** Inspect keeps this mounted too, so returning after a removal preserves the position. */
export function useRequestQueue() {
  const items: QueueItem[] = [
    ...agents.value.filter((a) => a.status === "pending")
      .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at))
      .map((agent) => ({ key: `agent:${agent.id}`, kind: "agent" as const, agent })),
    ...signins.value.filter((s) => s.needs_consent)
      .sort((a, b) => Date.parse(a.requested_at) - Date.parse(b.requested_at))
      .map((signin) => ({ key: `signin:${signin.id}`, kind: "signin" as const, signin })),
    ...pending.value.slice().sort((a, b) => Date.parse(a.requested_at) - Date.parse(b.requested_at))
      .map((call) => ({ key: `call:${call.id}`, kind: "call" as const, call })),
  ];
  const selection = reconcileQueue(items.map((item) => item.key), queueCursor.value, queuePosition.value);
  const index = selection.index;
  const current = items[index];
  useLayoutEffect(() => {
    queuePosition.value = index;
    queueCursor.value = selection.key;
  }, [index, selection.key]);
  const move = (delta: number) => {
    const nextIndex = Math.max(0, Math.min(index + delta, items.length - 1));
    const next = items[nextIndex];
    if (next) {
      queuePosition.value = nextIndex;
      queueCursor.value = next.key;
    }
  };
  return { items, index, current, move };
}

export function callSecondsLeft(call: PendingCall): number {
  return call.deadline
    ? secondsUntil(call.deadline)
    : Math.max(0, HOLD_SECONDS - (now.value - Date.parse(call.requested_at)) / 1000);
}

/** An unknown MCP client asked to connect. Approving is what makes tools visible to it. */
function AgentCard({ agent }: { agent: AgentConfig }) {
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
            <Chip>Needs token</Chip>
          </>
        ) : null}
      </div>
    </section>
  );
}

/** An approved agent's client is signing in again. A public client id proves nothing, so this asks. */
function SignInCard({ signin }: { signin: PendingSignIn }) {
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
    </section>
  );
}

function HoldCard({ call }: { call: PendingCall }) {
  const left = callSecondsLeft(call);
  const well = useRef<HTMLPreElement>(null);
  const [overflow, setOverflow] = useState(false);
  const [copyState, copy] = useCopy();
  const args = call.arguments && Object.keys(call.arguments as object).length > 0
    ? JSON.stringify(call.arguments, null, 2)
    : "";

  useLayoutEffect(() => {
    const el = well.current;
    if (!el) return;
    const measure = () => setOverflow(el.scrollHeight > el.clientHeight);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [args]);

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
      <div class="code-wrap">
        <pre ref={well} class={`code ${args ? "" : "empty-args"}`}>{args || "no arguments"}</pre>
        {args ? (
          <div class="code-actions">
            {overflow ? (
              <Button variant="quiet" class="copy" onClick={() => push({ kind: "inspect-call", callId: call.id })}>
                Inspect
              </Button>
            ) : null}
            <Button variant="quiet" class="copy" state={copyState} onClick={() => void copy(args)}>
              {copyState === "success" ? "Copied" : copyState === "error" ? "Copy failed" : "Copy"}
            </Button>
          </div>
        ) : null}
      </div>
    </section>
  );
}

function DecisionFooter({ item }: { item: QueueItem }) {
  if (item.kind === "agent") {
    return <div class="approval-footer primary-only">
      <div class="approval-primary">
        <Button variant="primary" busy={decisionBusy.value} hint="A" autoFocus onClick={() => void decideAgent(item.agent, true)}>Approve</Button>
        <Button variant="danger" busy={decisionBusy.value} hint="D" onClick={() => void decideAgent(item.agent, false)}>Deny</Button>
      </div>
    </div>;
  }
  if (item.kind === "signin") {
    return <div class="approval-footer primary-only">
      <div class="approval-primary">
        <Button variant="primary" busy={decisionBusy.value} hint="A" autoFocus onClick={() => void decideSignin(item.signin, true)}>Allow</Button>
        <Button variant="danger" busy={decisionBusy.value} hint="D" onClick={() => void decideSignin(item.signin, false)}>Refuse</Button>
      </div>
    </div>;
  }

  const call = item.call;
  const remembers = call.posture === "first_use";
  return <div class="approval-footer">
    <div class="approval-primary">
      <Button variant="primary" busy={decisionBusy.value} hint="A" autoFocus title={remembers ? "Remembered for this tool" : "This call only"} onClick={() => void decide(call, "allow", primaryScope(call))}>Allow</Button>
      <Button variant="danger" busy={decisionBusy.value} hint="D" onClick={() => void decide(call, "deny", "once")}>Deny</Button>
    </div>
    <div class="approval-caption">{remembers ? "Allow remembers this tool." : "Allow is this call only."}</div>
    <div class="approval-secondary">
      {remembers ? (
        <Button variant="quiet" busy={decisionBusy.value} onClick={() => void decide(call, "allow", "once")}>Once</Button>
      ) : (
        <Button variant="quiet" busy={decisionBusy.value} onClick={() => void decide(call, "allow", "always")}>Always</Button>
      )}
      <Button variant="quiet" busy={decisionBusy.value} onClick={() => void decide(call, "allow", { for: { minutes: 30 } })}>30 min</Button>
      <Button variant="quiet" busy={decisionBusy.value} title={`Allow all of ${call.server_name}`} onClick={() => void decide(call, "allow", "always", "server")}>All server</Button>
    </div>
  </div>;
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

const MAX_AGENT_ROWS = 5;

/** Agents fill the space between the chart and the foot: as many whole rows as fit, up to five, never a clipped one. */
function useAgentSlots() {
  const ref = useRef<HTMLDivElement>(null);
  const [slots, setSlots] = useState(MAX_AGENT_ROWS);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => {
      const row = parseFloat(getComputedStyle(el).getPropertyValue("--row-h")) || 44;
      setSlots(Math.max(1, Math.min(MAX_AGENT_ROWS, Math.floor(el.clientHeight / row))));
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return { ref, slots };
}

/** The week's shape and who made it. Every bar is a door into the list; the totals live there, not here. */
function ActivityBlock({ summary }: { summary: ActivitySummary }) {
  const at = summary.window.snapshot_at;
  const days = summary.days;
  const bars = useAgentSlots();
  const top = summary.agents.slice(0, bars.slots);
  const widest = Math.max(1, ...top.map((a) => a.total));
  return (
    <>
      <DailyChart days={summary.daily} at={at} />
      <div class="agent-bars" ref={bars.ref}>
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
          All actions ›
        </button>
      </div>
    </>
  );
}

/** An unfinished setup or a one-time token parked by a hide. Lives in memory only; resume or discard it here. */
function ResumeRow() {
  const saved = resumableNavigation.value;
  if (!saved) return null;
  const top = saved.stack[saved.stack.length - 1];
  if (top?.kind === "harness-setup" && !harnessSetupPending(hostSetup(native.value, top.host))) return null;
  const token = (top?.kind === "agent-connections" && manualTokens.value[top.agentId] !== undefined)
    || (top?.kind === "connect-agent" && !!connectAgentDraft.value.issuedAgentId);
  const issuing = top?.kind === "agent-connections" && !!issuingManualTokens.value[top.agentId];
  const what = token ? "Saved token"
    : top?.kind === "add-server" ? "Add server"
    : top?.kind === "connect-agent" ? "Connect an agent"
    : top?.kind === "harness-setup" ? `Set up ${harness(top.host)?.name ?? "agent"}`
    : "Setup";
  return (
    <div class="resume-row" role="status">
      <span class="resume-what">
        <span class="resume-eyebrow">{token ? "Not yet copied" : "Unfinished"}</span>
        {what}
      </span>
      <Button variant="quiet" disabled={issuing} title={issuing ? "Token creation is still running" : undefined} onClick={discardResumableNavigation}>
        Discard
      </Button>
      <Button variant="quiet" class="resume" onClick={resumeNavigation}>
        Resume
      </Button>
    </div>
  );
}

export function NowScreen() {
  const st = status.value;
  const { items, index, current, move } = useRequestQueue();
  const summary = activity.value;

  useDecisionKeys((approve) => {
    if (current?.kind === "agent") void decideAgent(current.agent, approve);
    else if (current?.kind === "signin") void decideSignin(current.signin, approve);
    else if (current?.kind === "call") {
      void decide(current.call, approve ? "allow" : "deny", approve ? primaryScope(current.call) : "once");
    }
  }, move);

  return (
    <div class="screen">
      <Screen fill={!current} footer={current ? <DecisionFooter key={current.key} item={current} /> : undefined}>
        <ResumeRow />
        {current ? (
          <>
            {items.length > 1 ? (
              <div class="queue-strip">
                <span>{index + 1} of {items.length} waiting</span>
                <div>
                  <Button variant="icon" aria-label="Previous request" disabled={index === 0} onClick={() => move(-1)}><ChevronIcon direction="left" /></Button>
                  <Button variant="icon" aria-label="Next request" disabled={index === items.length - 1} onClick={() => move(1)}><ChevronIcon /></Button>
                </div>
              </div>
            ) : null}
            {current.kind === "agent" ? <AgentCard key={current.key} agent={current.agent} /> : null}
            {current.kind === "signin" ? <SignInCard key={current.key} signin={current.signin} /> : null}
            {current.kind === "call" ? <HoldCard key={current.key} call={current.call} /> : null}
          </>
        ) : (
          <>
            <div class="opener">
              <strong>All clear.</strong>
              {st ? (
                <span class="health">
                  <button type="button" class="link" onClick={() => (tab.value = "servers")}>
                    {st.servers_running}/{st.servers_total} servers
                  </button>
                  {" · "}
                  <button type="button" class="link" onClick={() => (tab.value = "agents")}>
                    {st.agent_count} {st.agent_count === 1 ? "agent" : "agents"}
                  </button>
                </span>
              ) : null}
            </div>
            <div class="section activity">
              <Label>Last {summary?.days ?? 7} days · retained</Label>
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
          </>
        )}
      </Screen>
    </div>
  );
}
