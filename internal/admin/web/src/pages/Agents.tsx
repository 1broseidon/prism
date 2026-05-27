import { useMemo, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { agents } from "../state";
import { deleteJSON } from "../api/client";
import { withToast } from "../state/toasts";
import { canMutate } from "../state/me";
import { splitLabel, fmtAge } from "../util/time";
import { agentDetailHref } from "../util/agentRoute";
import type { Agent, Group } from "../api/types";
import { groups as groupsState } from "../state";

// Resolve a group identifier (ULID or legacy name) back to its operator-
// facing display name using the cached groups list.
function groupLabel(idOrName: string, groupList: Group[]): string {
  const found =
    groupList.find((g) => g.id === idOrName) ||
    groupList.find((g) => g.name === idOrName) ||
    groupList.find((g) => g.display_name === idOrName);
  return found?.display_name || found?.name || idOrName;
}

function policyLabel(a: Agent, groupList: Group[]): string {
  if (a.dynamic && !a.prism_id) return "pending consent";
  if (!a.policy) return "defaults";
  const parts: string[] = [];
  const g = a.policy.groups || [];
  const gr = a.policy.grant || [];
  const de = a.policy.deny || [];
  if (g.length) parts.push(g.map((id) => groupLabel(id, groupList)).join(", "));
  if (gr.length) parts.push(`+${gr.length} grant${gr.length > 1 ? "s" : ""}`);
  if (de.length) parts.push(`−${de.length} deny`);
  return parts.join("  ") || "defaults";
}

export function Agents() {
  const ag = (agents.data.value || []).slice().sort((a, b) =>
    (a.label || a.description || a.client_id).localeCompare(
      b.label || b.description || b.client_id,
    ),
  );
  const [query, setQuery] = useState("");

  const filtered = useMemo(() => {
    const q = query.toLowerCase().trim();
    if (!q) return ag;
    return ag.filter((a) => {
      const name = (a.label || a.description || a.client_id).toLowerCase();
      return (
        name.includes(q) ||
        a.client_id.toLowerCase().includes(q) ||
        (a.prism_id || "").toLowerCase().includes(q)
      );
    });
  }, [ag, query]);

  const cleanStale = async () => {
    await withToast(async () => {
      await deleteJSON("/agents/stale");
      await agents.refresh();
    });
  };

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">agents</div>
          <div class="page-subtitle">
            {ag.length} agent{ag.length === 1 ? "" : "s"} registered
          </div>
        </div>
        <div class="page-header-actions">
          {ag.length > 0 && (
            <input
              type="search"
              class="search-input"
              placeholder="search agents…"
              value={query}
              onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
            />
          )}
          {canMutate() && ag.length > 0 && (
            <button class="section-btn" onClick={cleanStale}>
              clean stale
            </button>
          )}
        </div>
      </div>

      {ag.length === 0 ? (
        <div class="empty-callout">
          <div class="empty-callout-title">no agents yet</div>
          <div class="empty-callout-body">
            agents authenticate to prism via oauth client-credentials. they
            either register dynamically (dcr) or are defined in config under
            policy.agents.
          </div>
        </div>
      ) : filtered.length === 0 ? (
        <div class="empty-state">no agents match “{query}”.</div>
      ) : (
        <div class="server-list">
          {filtered.map((a) => (
            <AgentRow key={a.client_id} agent={a} />
          ))}
        </div>
      )}
    </div>
  );
}

function AgentRow({ agent: a }: { agent: Agent }) {
  const loc = useLocation();
  const groupList = groupsState.data.value || [];
  const display = a.label || a.description || a.client_id;
  const [name, ctx] = splitLabel(display);
  const detailHref = agentDetailHref(a);
  const canOpen = !!detailHref;

  // Right-side primary stat: number of effective scopes. Mirrors servers'
  // "X TOOLS" — the count of things this agent can reach.
  const effectiveScopes =
    a.breakdown?.effective?.length ?? a.scopes?.length ?? 0;
  const lastSeen = a.last_used_at || a.created_at;
  const policySummary = a.dynamic
    ? policyLabel(a, groupList)
    : "config-managed";

  const onClick = () => {
    if (detailHref) {
      loc.route(detailHref);
    }
  };

  return (
    <button
      class={canOpen ? "server-row" : "server-row server-row-static"}
      onClick={onClick}
      disabled={!canOpen}
    >
      <div class="server-row-main">
        <div class="server-row-header">
          <span class={`status-pip ${agentPipClass(a)}`} />
          <span class="server-row-name">{name}</span>
          {ctx && <span class="server-row-ns">/ {ctx}</span>}
          <span class="server-row-transport">{a.dynamic ? "dynamic" : "static"}</span>
        </div>
        <div class="server-row-meta">
          <span class="server-row-url">{policySummary}</span>
        </div>
      </div>
      <div class="server-row-stats">
        <div class="server-row-stat">
          <div class="server-row-stat-value">{effectiveScopes}</div>
          <div class="server-row-stat-label">
            scope{effectiveScopes === 1 ? "" : "s"}
          </div>
        </div>
        <div class="server-row-stat">
          <div class="server-row-stat-value">
            {lastSeen ? fmtAge(lastSeen) : "—"}
          </div>
          <div class="server-row-stat-label">last seen</div>
        </div>
        {canOpen && <span class="server-row-chevron">›</span>}
      </div>
    </button>
  );
}

function agentPipClass(a: Agent): string {
  const ts = a.last_used_at || a.created_at;
  if (!ts) return "status-pip-neutral";
  const age = Date.now() - new Date(ts).getTime();
  if (age < 5 * 60_000) return "status-pip-ok";
  if (age > 7 * 86_400_000) return "status-pip-neutral";
  return "status-pip-ok";
}
