import { useEffect, useMemo, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { info, agents, backends, events } from "../state";
import { fmtTimeOfDay, splitLabel } from "../util/time";
import { mcpURLFromBase } from "../util/mcp";
import { getJSON } from "../api/client";
import type { NetworkSettings } from "../api/types";
import { CopyId } from "../components/CopyId";

export function Overview() {
  const i = info.data.value;
  const ag = agents.data.value || [];
  const be = backends.data.value || [];
  const ev = events.data.value || [];

  const totalTools = be.reduce((acc, b) => acc + (b.tools?.length ?? 0), 0);
  const connectedBackends = be.filter((b) => (b.tools?.length ?? 0) > 0).length;
  const errorBackends = be.filter(
    (b) => b.circuit_breaker === "open",
  ).length;
  const dynamicAgents = ag.filter((a) => a.dynamic).length;

  // Hour-window activity stats
  const hourAgo = Date.now() - 3600 * 1000;
  const recentCalls = ev.filter(
    (e) => new Date(e.ts).getTime() >= hourAgo,
  );
  const recentDenied = recentCalls.filter((e) => !e.allowed).length;
  const deniedPct = recentCalls.length
    ? Math.round((recentDenied / recentCalls.length) * 100)
    : 0;

  // Active agents in the last 5 minutes
  const activeWindow = Date.now() - 5 * 60 * 1000;
  const activeAgentIds = new Set(
    ev
      .filter((e) => new Date(e.ts).getTime() >= activeWindow)
      .map((e) => e.client_id),
  );

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">overview</div>
          <div class="page-subtitle">
            {i ? `${i.name} · v${i.version} · ${i.go_version}` : "loading…"}
          </div>
        </div>
      </div>

      <McpConnectBanner />

      <div class="tile-grid">
        <Tile
          label="backends"
          value={be.length}
          href="/servers"
          sub={
            be.length === 0
              ? "none connected"
              : errorBackends > 0
                ? `${connectedBackends} healthy · ${errorBackends} error`
                : `${connectedBackends} healthy`
          }
        />
        <Tile
          label="tools"
          value={totalTools}
          sub={
            be.length === 0
              ? "—"
              : `across ${be.length} backend${be.length === 1 ? "" : "s"}`
          }
        />
        <Tile
          label="agents"
          value={ag.length}
          href="/agents"
          sub={
            ag.length === 0
              ? "no agents"
              : `${activeAgentIds.size} active · ${dynamicAgents} dynamic`
          }
        />
        <Tile
          label="last hour"
          value={recentCalls.length}
          href="/audit"
          sub={
            recentCalls.length === 0
              ? "no calls"
              : recentDenied > 0
                ? `${recentDenied} denied · ${deniedPct}%`
                : "all allowed"
          }
          tone={recentDenied > 0 ? "warn" : "default"}
        />
      </div>

      <RecentActivity events={ev} nameCacheBuilder={ag} />
    </div>
  );
}

function McpConnectBanner() {
  const loc = useLocation();
  const [publicURL, setPublicURL] = useState<string | null>(null);

  useEffect(() => {
    getJSON<NetworkSettings>("/config/network")
      .then((v) => setPublicURL(v.public_url || ""))
      .catch(() => setPublicURL(""));
  }, []);

  if (publicURL === null) return null;
  const mcpURL = mcpURLFromBase(publicURL);

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">mcp endpoint</span>
        <span class="section-sub">
          give this url to agents (Claude, Cursor, …) to connect via OAuth + DCR
        </span>
      </div>
      <div class="card" style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">
        {mcpURL ? (
          <>
            <code style="font-size:14px;flex:1;min-width:0;overflow-wrap:anywhere">
              {mcpURL}
            </code>
            <CopyId value={mcpURL} label="copy" />
          </>
        ) : (
          <>
            <div class="hint-text" style="flex:1;min-width:0">
              set <strong>gateway public url</strong> in settings to publish
              an mcp endpoint url.
            </div>
            <button
              type="button"
              class="section-btn"
              onClick={() => loc.route("/settings/general")}
            >
              settings
            </button>
          </>
        )}
      </div>
    </div>
  );
}

function Tile({
  label,
  value,
  sub,
  href,
  tone,
}: {
  label: string;
  value: string | number;
  sub: string;
  href?: string;
  tone?: "default" | "warn" | "ok";
}) {
  const loc = useLocation();
  const interactive = !!href;
  const onClick = () => {
    if (href) loc.route(href);
  };
  return (
    <div
      class={interactive ? "tile tile-interactive" : "tile"}
      onClick={interactive ? onClick : undefined}
      role={interactive ? "button" : undefined}
      tabIndex={interactive ? 0 : undefined}
    >
      <div class="tile-label">{label}</div>
      <div
        class={tone === "warn" ? "tile-value tile-value-warn" : "tile-value"}
      >
        {value}
      </div>
      <div class="tile-sub">{sub}</div>
    </div>
  );
}

function RecentActivity({
  events: ev,
  nameCacheBuilder: ag,
}: {
  events: Array<{
    ts: string;
    client_id: string;
    namespace: string;
    tool: string;
    allowed: boolean;
    latency_ms: number;
  }>;
  nameCacheBuilder: Array<{
    client_id: string;
    label?: string;
    description?: string;
  }>;
}) {
  const recent = ev.slice(0, 10);
  const nameCache = useMemo(() => {
    const m = new Map<string, string>();
    for (const a of ag) {
      m.set(a.client_id, a.label || a.description || a.client_id);
    }
    return m;
  }, [ag]);

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">activity</span>
        {ev.length > 0 && (
          <a class="section-btn" href="/audit">
            view all
          </a>
        )}
      </div>
      {recent.length === 0 ? (
        <div class="activity-empty">
          <div class="activity-empty-text">no tool calls yet</div>
          <div class="activity-empty-sub">
            once an agent calls a tool through the gateway, you'll see live
            audit events here.
          </div>
        </div>
      ) : (
        <div class="activity-list">
          {recent.map((e, idx) => {
            const fullName =
              nameCache.get(e.client_id) || e.client_id || "anonymous";
            const [shortName] = splitLabel(fullName);
            const latency = e.allowed
              ? e.latency_ms === 0
                ? "<1ms"
                : `${e.latency_ms}ms`
              : "—";
            return (
              <div
                class={
                  e.allowed
                    ? "activity-row"
                    : "activity-row activity-row-denied"
                }
                key={`${e.ts}-${idx}`}
              >
                <div class="activity-time" title={e.ts}>
                  {fmtTimeOfDay(e.ts)}
                </div>
                <div class="activity-status">
                  {e.allowed ? (
                    <span class="status-pip status-pip-ok" />
                  ) : (
                    <span class="status-pip status-pip-error" />
                  )}
                </div>
                <div class="activity-agent" title={fullName}>
                  {shortName}
                </div>
                <div class="activity-tool">
                  <span class="ev-tool-ns">{e.namespace}__</span>
                  <span class="ev-tool-name">{e.tool}</span>
                </div>
                <div class="activity-latency">{latency}</div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

