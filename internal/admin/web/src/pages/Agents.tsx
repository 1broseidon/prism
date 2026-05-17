import { useMemo, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { agents } from "../state";
import { deleteJSON } from "../api/client";
import { withToast } from "../state/toasts";
import { canMutate } from "../state/me";
import { StatusCell } from "../components/StatusCell";
import { CopyId } from "../components/CopyId";
import { splitLabel } from "../util/time";
import { agentDetailHref } from "../util/agentRoute";
import type { Agent } from "../api/types";

function policyLabel(a: Agent): string {
  if (a.dynamic && !a.prism_id) return "pending consent";
  if (!a.policy) return "defaults";
  const parts: string[] = [];
  const g = a.policy.groups || [];
  const gr = a.policy.grant || [];
  const de = a.policy.deny || [];
  if (g.length) parts.push(g.join(", "));
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

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">agents</div>
          <div class="page-subtitle">
            {ag.length} agent{ag.length === 1 ? "" : "s"} registered
          </div>
        </div>
      </div>

      <AgentsSection agents={ag} />
    </div>
  );
}

function AgentsSection({ agents: ag }: { agents: Agent[] }) {
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
    <div class="section">
      <div class="section-header">
        <span class="section-title">agents ({ag.length})</span>
        <div class="section-actions">
          {ag.length > 0 && (
            <input
              type="search"
              class="search-input"
              placeholder="search agents…"
              value={query}
              onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
            />
          )}
          {canMutate() && (
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
        <div class="agent-list">
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
  const display = a.label || a.description || a.client_id;
  const [name, ctx] = splitLabel(display);
  const detailHref = agentDetailHref(a);
  const canOpen = !!detailHref;
  const copyVal = a.prism_id || a.client_id;
  const copyLabel = a.prism_id ? "id" : "cid";

  const onClick = () => {
    if (detailHref) {
      loc.route(detailHref);
    }
  };

  return (
    <button
      class={canOpen ? "agent-row" : "agent-row agent-row-static"}
      onClick={onClick}
      disabled={!canOpen}
    >
      <div class="agent-row-main">
        <div class="agent-name-row">
          <span class="agent-label">{name}</span>
          {ctx && <span class="agent-ctx">{ctx}</span>}
          <CopyId value={copyVal} label={copyLabel} />
        </div>
        <div class="agent-row-meta">
          {a.dynamic ? (
            <span class="policy-summary">{policyLabel(a)}</span>
          ) : (
            <span class="scope-lock">config-managed</span>
          )}
        </div>
      </div>
      <div class="agent-row-right">
        <StatusCell agent={a} />
        {canOpen && <span class="server-row-chevron">›</span>}
      </div>
    </button>
  );
}
