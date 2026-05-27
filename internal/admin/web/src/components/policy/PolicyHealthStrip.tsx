// PolicyHealthStrip — four SecOps-aligned tiles above every /policy page.
//
// Task-46 collapses the original task-41 six-tile shape into a tighter set
// that maps to the operator's daily question "what's protecting me, what's
// biting me, and is today normal?". The four tiles, in order, are:
//
//   1. Calls (24h)               — total authorized decisions in the window,
//                                  with the 7-day daily average as the
//                                  trend baseline ("is today normal?").
//   2. Denials (24h)             — count, denial rate as the sub-line,
//                                  plus the dominant deny_dim so operators
//                                  see "what's biting me the most".
//   3. Drift events (24h)        — workspace-drift denials specifically;
//                                  surfaced as its own tile because it's
//                                  the most security-significant deny_dim
//                                  and operators triage it differently.
//   4. Permissions in force      — composite count of bindings + agent
//                                  scope grants ("how much authorization
//                                  is currently configured?"). Deep-links
//                                  to /policy/advanced/bindings so the
//                                  operator can audit it.
//
// The backend response still carries the deprecated fields
// (median_freshness_seconds, dpop_bound_agents, active_templates) for
// backwards compatibility with external consumers — we simply don't render
// tiles for them anymore.
//
// Behavior contract is unchanged from task-41:
//   - Single endpoint fetch with a 30s background refresh.
//   - Errors render an inline retry chip but never block the rest of the
//     /policy page.
//   - Empty state (no events yet) shows all-zeros + a small hint so a fresh
//     install doesn't look broken.
//
// Re-uses the existing `tile-grid` / `tile-interactive` styles. The polling
// pattern matches state/polling.ts but is kept local because the strip
// mounts on a single page and starts/stops with it.

import { useLocation } from "preact-iso";
import { getPolicyHealth, type PolicyHealth } from "../../api/policy";
import { usePolledFetch } from "../../hooks/usePolledFetch";

const REFRESH_INTERVAL_MS = 30_000;

interface TileSpec {
  label: string;
  value: string | number;
  sub: string;
  href?: string;
  tone?: "default" | "warn";
}

export function PolicyHealthStrip() {
  // Initial fetch + 30s interval handled by the hook. Cleared on unmount
  // so leaving /policy doesn't keep firing requests.
  const { data, error, loading, retry } = usePolledFetch<PolicyHealth>(
    getPolicyHealth,
    REFRESH_INTERVAL_MS,
  );

  if (loading && !data) {
    // First-paint placeholder — keeps the page from jumping when the
    // numbers arrive. We deliberately don't show "loading…" inside each
    // tile because the strip swap is fast enough that flashing labels
    // costs more than a brief blank.
    return <div class="policy-health-strip policy-health-strip-loading" />;
  }

  if (error && !data) {
    return (
      <div class="policy-health-strip-error" role="alert">
        <span>policy health unavailable: {error}</span>
        <button
          type="button"
          class="policy-health-retry"
          onClick={retry}
        >
          retry
        </button>
      </div>
    );
  }

  if (!data) return null;

  const tiles = buildTiles(data);
  const allZero =
    data.calls_24h === 0 &&
    data.denials_24h === 0 &&
    data.drift_events_24h === 0 &&
    data.permissions_in_force === 0;

  return (
    <div class="policy-health-strip" aria-label="Policy health (last 24 hours)">
      <div class="policy-health-tiles">
        {tiles.map((t) => (
          <HealthTile key={t.label} spec={t} />
        ))}
      </div>
      {allZero && (
        <div class="policy-health-empty-hint">
          Policy events will appear here as agents make calls.
        </div>
      )}
    </div>
  );
}

function buildTiles(d: PolicyHealth): TileSpec[] {
  const denialRate = formatDenialRate(d.denial_rate_24h, d.calls_24h);
  const denialSub =
    d.denials_24h > 0 && d.top_deny_dim
      ? `${denialRate} · top: ${d.top_deny_dim}`
      : denialRate;
  const callsSub =
    d.calls_7d_avg > 0
      ? `${d.calls_7d_avg}/day avg over 7d`
      : "authorization decisions";
  return [
    {
      label: "calls (24h)",
      value: d.calls_24h,
      sub: callsSub,
    },
    {
      label: "denials",
      value: d.denials_24h,
      sub: denialSub,
      href: "/activity?outcome=denied",
      tone: d.denials_24h > 0 ? "warn" : "default",
    },
    {
      label: "drift events",
      value: d.drift_events_24h,
      sub: d.drift_events_24h > 0 ? "workspace drift" : "no drift",
      href: "/activity?deny_dim=workspace_drift",
      tone: d.drift_events_24h > 0 ? "warn" : "default",
    },
    {
      label: "permissions in force",
      value: d.permissions_in_force,
      sub: "across all subjects",
      href: "/policy/advanced/bindings",
    },
  ];
}

// formatDenialRate avoids "NaN%" / "0.0%" when there are zero calls. The
// backend already protects denial_rate_24h with max(calls, 1), so we treat
// 0 calls as a clean "—".
function formatDenialRate(rate: number, calls: number): string {
  if (calls === 0) return "—";
  if (rate >= 10) return `${rate.toFixed(0)}% of calls`;
  return `${rate.toFixed(1)}% of calls`;
}

function HealthTile({ spec }: { spec: TileSpec }) {
  const loc = useLocation();
  const interactive = Boolean(spec.href);
  const onClick = () => {
    if (spec.href) loc.route(spec.href);
  };
  return (
    <div
      class={interactive ? "tile tile-interactive" : "tile"}
      onClick={interactive ? onClick : undefined}
      role={interactive ? "button" : undefined}
      tabIndex={interactive ? 0 : undefined}
      onKeyDown={
        interactive
          ? (e: KeyboardEvent) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onClick();
              }
            }
          : undefined
      }
    >
      <div class="tile-label">{spec.label}</div>
      <div
        class={
          spec.tone === "warn" ? "tile-value tile-value-warn" : "tile-value"
        }
      >
        {spec.value}
      </div>
      <div class="tile-sub">{spec.sub}</div>
    </div>
  );
}
