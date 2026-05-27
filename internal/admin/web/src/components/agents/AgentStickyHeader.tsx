// AgentStickyHeader — the always-visible top card on AgentDetail.
//
// Spec context: task-39 — replaces the old MetaRow with a denser, Duo-style
// header card that lives at the top of the agent page and STICKS to the top
// of the main content area as the operator scrolls. Past 200px scroll the
// card collapses into a 1-line compact summary; clicking the compact bar
// expands it again. Past 0px it stays expanded.
//
// Mechanics:
//   - position: sticky, top: 0 anchored inside .shell-content
//   - the host page passes a scroll container ref (or we fall back to
//     window) and we listen for scroll, swapping a CSS class when threshold
//     is crossed. CSS does the visual transition.
//   - clicking the compact bar forces .agent-sticky-expanded ON regardless
//     of scroll until the operator scrolls away.
//
// At-a-glance facts surfaced:
//   1. label + status dot
//   2. client_id / prism_id (copy)
//   3. groups list
//   4. effective capability count
//   5. last seen
//   6. sessions badge (opens SessionsModal) — "N active · M DPoP · last auth"
//   7. anchored chip buttons that smooth-scroll to in-page sections
//
// Sessions data comes from AgentGrantResolution.live_tokens (already loaded
// in AgentDetail). No new endpoints.

import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import type { Agent } from "../../api/types";
import type { AgentGrantResolution } from "../../api/analytics";
import { CopyId } from "../CopyId";
import { StatusCell } from "../StatusCell";
import { fmtAge, splitLabel } from "../../util/time";
import { RenamePencil } from "../identity/RenamePencil";

interface SectionAnchor {
  id: string;
  label: string;
}

interface Props {
  agent: Agent;
  /** Optional grant resolution; we only read live_tokens for the badge. */
  grant?: AgentGrantResolution;
  /** Total effective capability count, surfaced as a badge. */
  effectiveCount?: number;
  /** Anchors rendered as quick-jump chips. Smooth-scroll on click. */
  anchors: SectionAnchor[];
  /** Open the sessions modal. */
  onOpenSessions: () => void;
  /** Refresh the parent agent detail after a display-name rename. */
  onRenameSuccess?: () => void;
}

// COLLAPSE_THRESHOLD is the scroll distance (px) past which the sticky card
// switches to compact mode. 200px gives the operator a beat to absorb the
// full header before it shrinks; matches the contract.
const COLLAPSE_THRESHOLD = 200;

export function AgentStickyHeader({
  agent,
  grant,
  effectiveCount,
  anchors,
  onOpenSessions,
  onRenameSuccess,
}: Props) {
  const [collapsed, setCollapsed] = useState(false);
  // forceExpanded short-circuits collapse-on-scroll when the operator clicks
  // the compact bar to expand it. We reset the override the next time they
  // scroll, so the header returns to its natural collapse behavior.
  const [forceExpanded, setForceExpanded] = useState(false);
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    // Walk up to find the nearest scrollable ancestor (usually .shell-content
    // which has overflow-y: auto). Falls back to window if nothing matches.
    let target: HTMLElement | Window = window;
    let node: HTMLElement | null = ref.current;
    while (node && node.parentElement) {
      const parent = node.parentElement;
      const overflowY = window.getComputedStyle(parent).overflowY;
      if (overflowY === "auto" || overflowY === "scroll") {
        target = parent;
        break;
      }
      node = parent;
    }

    const getScroll = (): number => {
      if (target === window) {
        return window.scrollY || document.documentElement.scrollTop;
      }
      return (target as HTMLElement).scrollTop;
    };

    const onScroll = () => {
      const y = getScroll();
      setCollapsed(y > COLLAPSE_THRESHOLD);
      if (forceExpanded && y <= COLLAPSE_THRESHOLD) {
        setForceExpanded(false);
      }
    };

    onScroll();
    target.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      target.removeEventListener("scroll", onScroll);
    };
  }, [forceExpanded]);

  const [label] = splitLabel(
    agent.label || agent.description || agent.client_id,
  );
  const groups = agent.policy?.groups || [];
  const lastSeen = agent.last_used_at ? fmtAge(agent.last_used_at) : "never";

  const tokens = grant?.live_tokens || [];
  const dpopBound = useMemo(
    () => tokens.filter((t) => !!t.jkt).length,
    [tokens],
  );
  const lastAuth = useMemo(() => {
    const stamps = tokens
      .map((t) => t.auth_time)
      .filter((s): s is string => !!s)
      .map((s) => new Date(s).getTime())
      .filter((n) => Number.isFinite(n));
    if (stamps.length === 0) return null;
    return new Date(Math.max(...stamps)).toISOString();
  }, [tokens]);

  const showCompact = collapsed && !forceExpanded;

  const scrollTo = (anchorID: string) => {
    const el = document.getElementById(anchorID);
    if (!el) return;
    el.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  return (
    <div
      ref={ref}
      class={
        showCompact
          ? "agent-sticky agent-sticky-compact"
          : "agent-sticky agent-sticky-expanded"
      }
      // When compact, clicking anywhere on the bar (except buttons) expands
      // it back to full. Buttons keep their own onClick semantics.
      onClick={(e) => {
        if (!showCompact) return;
        const target = e.target as HTMLElement;
        if (target.closest("button, a")) return;
        setForceExpanded(true);
      }}
      role={showCompact ? "button" : undefined}
      aria-expanded={!showCompact}
      title={showCompact ? "click to expand header" : undefined}
    >
      {showCompact ? (
        <div class="agent-sticky-compact-row">
          <StatusCell agent={agent} />
          <span class="agent-sticky-compact-label">{label}</span>
          <span class="agent-sticky-compact-sep">·</span>
          <span class="agent-sticky-compact-meta">
            {groups.length} group{groups.length === 1 ? "" : "s"} · {effectiveCount ?? 0} caps · last seen {lastSeen}
          </span>
          <div class="agent-sticky-compact-spacer" />
          <button
            type="button"
            class="agent-sticky-session-chip"
            onClick={(e) => {
              e.stopPropagation();
              onOpenSessions();
            }}
            aria-label="view sessions"
            title="open sessions modal"
          >
            sessions: {tokens.length}{dpopBound ? ` · ${dpopBound} dpop` : ""}
          </button>
        </div>
      ) : (
        <div class="agent-sticky-full">
          <div class="agent-sticky-identity">
            <div class="agent-sticky-title-row">
              <StatusCell agent={agent} />
              <div class="agent-sticky-title">
                {agent.prism_id ? (
                  <RenamePencil
                    currentName={label}
                    entityId={agent.prism_id}
                    onSuccess={() => {
                      onRenameSuccess?.();
                    }}
                  />
                ) : (
                  label
                )}
              </div>
            </div>
            <div class="agent-sticky-id-row">
              <span class="agent-sticky-kind">
                {agent.dynamic ? "dynamic · oauth dcr" : "static · config"}
              </span>
              <span class="agent-sticky-sep">·</span>
              <CopyId
                value={agent.prism_id || agent.client_id}
                label={agent.prism_id ? "prism_id" : "client_id"}
              />
            </div>
          </div>

          <div class="agent-sticky-facts">
            <Fact label="groups">
              {groups.length === 0 ? (
                <span class="muted">none</span>
              ) : (
                groups.map((g, i) => (
                  <span key={g}>
                    {i > 0 ? ", " : ""}
                    <a
                      class="link-accent"
                      href={`/policy/groups/${encodeURIComponent(g)}`}
                    >
                      {g}
                    </a>
                  </span>
                ))
              )}
            </Fact>
            <Fact label="permissions">{effectiveCount ?? "—"}</Fact>
            <Fact label="last seen">{lastSeen}</Fact>
            <Fact label="sessions">
              <button
                type="button"
                class="agent-sticky-session-link"
                onClick={onOpenSessions}
                title="open sessions modal"
              >
                {tokens.length} active
                {dpopBound > 0 && ` · ${dpopBound} DPoP-bound`}
                {lastAuth && ` · last auth ${fmtAge(lastAuth)}`}
              </button>
            </Fact>
          </div>

          {anchors.length > 0 && (
            <div class="agent-sticky-anchors">
              {anchors.map((a) => (
                <button
                  type="button"
                  key={a.id}
                  class="agent-sticky-anchor"
                  onClick={() => scrollTo(a.id)}
                >
                  {a.label}
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Fact({
  label,
  children,
}: {
  label: string;
  children: preact.ComponentChildren;
}) {
  return (
    <div class="agent-sticky-fact">
      <div class="agent-sticky-fact-label">{label}</div>
      <div class="agent-sticky-fact-value">{children}</div>
    </div>
  );
}
