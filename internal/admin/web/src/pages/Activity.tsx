import { useMemo, useState, useEffect } from "preact/hooks";
import { useLocation } from "preact-iso";
import { events, agents } from "../state";
import { fmtTimeOfDay, fmtAge, splitLabel } from "../util/time";
import type { AuditEvent } from "../api/types";

type Range = "5m" | "1h" | "6h" | "24h" | "all";
type Status = "all" | "allowed" | "denied";

const RANGE_LABELS: Record<Range, string> = {
  "5m": "5 min",
  "1h": "1 hour",
  "6h": "6 hours",
  "24h": "24 hours",
  all: "all",
};

const RANGE_MS: Record<Range, number | null> = {
  "5m": 5 * 60 * 1000,
  "1h": 60 * 60 * 1000,
  "6h": 6 * 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  all: null,
};

export function Activity() {
  const ev = events.data.value || [];
  const ag = agents.data.value || [];
  const loc = useLocation();

  // Initial filter state from URL params (e.g. /activity?namespace=exa)
  const initialAgent = loc.query?.agent || "";
  const initialNamespace = loc.query?.namespace || "";

  const [range, setRange] = useState<Range>("1h");
  const [status, setStatus] = useState<Status>("all");
  const [filterAgent, setFilterAgent] = useState(initialAgent);
  const [filterNamespace, setFilterNamespace] = useState(initialNamespace);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<number | null>(null);

  // Keep URL params in sync (only when filters change deliberately)
  useEffect(() => {
    const params = new URLSearchParams();
    if (filterAgent) params.set("agent", filterAgent);
    if (filterNamespace) params.set("namespace", filterNamespace);
    const next = params.toString();
    const url = next ? `/activity?${next}` : "/activity";
    if (loc.path === "/activity" && loc.url !== url) {
      window.history.replaceState(null, "", url);
    }
  }, [filterAgent, filterNamespace]);

  const nameCache = useMemo(() => {
    const m = new Map<string, string>();
    ag.forEach((a) =>
      m.set(a.client_id, a.label || a.description || a.client_id),
    );
    return m;
  }, [ag]);

  const namespaces = useMemo(
    () => Array.from(new Set(ev.map((e) => e.namespace).filter(Boolean))).sort(),
    [ev],
  );

  const filtered = useMemo(() => {
    const rangeMs = RANGE_MS[range];
    const cutoff = rangeMs ? Date.now() - rangeMs : 0;
    const q = query.toLowerCase().trim();
    return ev.filter((e) => {
      if (rangeMs && new Date(e.ts).getTime() < cutoff) return false;
      if (filterAgent && e.client_id !== filterAgent) return false;
      if (filterNamespace && e.namespace !== filterNamespace) return false;
      if (status === "allowed" && !e.allowed) return false;
      if (status === "denied" && e.allowed) return false;
      if (q) {
        const haystack = `${e.tool} ${e.namespace} ${e.client_id} ${
          nameCache.get(e.client_id) || ""
        }`.toLowerCase();
        if (!haystack.includes(q)) return false;
      }
      return true;
    });
  }, [ev, range, filterAgent, filterNamespace, status, query, nameCache]);

  const deniedCount = filtered.filter((e) => !e.allowed).length;

  const hasActiveFilter =
    filterAgent || filterNamespace || status !== "all" || query;

  const exportCSV = () => {
    if (filtered.length === 0) return;
    const escape = (s: string) => `"${String(s).replace(/"/g, '""')}"`;
    const header = [
      "timestamp",
      "agent_client_id",
      "agent_label",
      "namespace",
      "tool",
      "status",
      "latency_ms",
    ].join(",");
    const rows = filtered.map((e) =>
      [
        escape(e.ts),
        escape(e.client_id),
        escape(nameCache.get(e.client_id) || ""),
        escape(e.namespace),
        escape(e.tool),
        e.allowed ? "allowed" : "denied",
        e.allowed ? String(e.latency_ms) : "",
      ].join(","),
    );
    const csv = [header, ...rows].join("\n");
    const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    const ts = new Date().toISOString().replace(/[:.]/g, "-");
    a.href = url;
    a.download = `prism-audit-${ts}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">activity</div>
          <div class="page-subtitle">
            {filtered.length} of {ev.length} events ·{" "}
            {RANGE_LABELS[range]} window
          </div>
        </div>
        <div class="page-header-actions">
          <button
            class="audit-export"
            onClick={exportCSV}
            disabled={filtered.length === 0}
          >
            export csv
          </button>
        </div>
      </div>

      <div class="audit-stats">
        <AuditStat label="events" value={filtered.length} />
        <AuditStat
          label="denied"
          value={deniedCount}
          tone={deniedCount > 0 ? "warn" : "default"}
        />
      </div>

      <div class="audit-controls">
        <div class="range-selector">
          {(Object.keys(RANGE_LABELS) as Range[]).map((r) => (
            <button
              key={r}
              class={r === range ? "range-btn range-btn-active" : "range-btn"}
              onClick={() => setRange(r)}
            >
              {RANGE_LABELS[r]}
            </button>
          ))}
        </div>
        <input
          type="search"
          class="search-input audit-search"
          placeholder="search tool, agent, namespace…"
          value={query}
          onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
        />
        <select
          value={status}
          onChange={(e) =>
            setStatus((e.target as HTMLSelectElement).value as Status)
          }
          class="audit-select"
        >
          <option value="all">all status</option>
          <option value="allowed">allowed</option>
          <option value="denied">denied only</option>
        </select>
        <select
          value={filterAgent}
          onChange={(e) =>
            setFilterAgent((e.target as HTMLSelectElement).value)
          }
          class="audit-select"
        >
          <option value="">all agents</option>
          {ag.map((a) => (
            <option key={a.client_id} value={a.client_id}>
              {a.label || a.description || a.client_id}
            </option>
          ))}
        </select>
        <select
          value={filterNamespace}
          onChange={(e) =>
            setFilterNamespace((e.target as HTMLSelectElement).value)
          }
          class="audit-select"
        >
          <option value="">all namespaces</option>
          {namespaces.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
        {hasActiveFilter && (
          <button
            class="cancel-btn"
            onClick={() => {
              setQuery("");
              setStatus("all");
              setFilterAgent("");
              setFilterNamespace("");
            }}
          >
            clear filters
          </button>
        )}
      </div>

      {filtered.length === 0 ? (
        <div class="empty-callout">
          <div class="empty-callout-title">
            {ev.length === 0 ? "no audit events yet" : "no matching events"}
          </div>
          <div class="empty-callout-body">
            {ev.length === 0
              ? "audit events appear here as soon as agents call tools through the gateway. each call is logged with timestamp, agent, tool, status, and latency."
              : "try widening the time range or clearing one of the active filters."}
          </div>
        </div>
      ) : (
        <div class="audit-table">
          <div class="audit-table-header">
            <div>time</div>
            <div></div>
            <div>agent</div>
            <div>tool</div>
            <div class="right">latency</div>
          </div>
          {filtered.map((e, idx) => {
            const fullName =
              nameCache.get(e.client_id) || e.client_id || "anonymous";
            const [shortName] = splitLabel(fullName);
            const latency = e.allowed
              ? e.latency_ms === 0
                ? "<1ms"
                : `${e.latency_ms}ms`
              : "—";
            const open = selected === idx;
            return (
              <div key={`${e.ts}-${idx}`}>
                <button
                  class={
                    e.allowed
                      ? "audit-row"
                      : "audit-row audit-row-denied"
                  }
                  onClick={() => setSelected(open ? null : idx)}
                >
                  <div class="audit-time" title={e.ts}>
                    {fmtTimeOfDay(e.ts)}
                    <div class="audit-time-rel">{fmtAge(e.ts)}</div>
                  </div>
                  <div class="audit-status">
                    {e.allowed ? (
                      <span class="status-pip status-pip-ok" />
                    ) : (
                      <span class="status-pip status-pip-error" />
                    )}
                  </div>
                  <div class="audit-agent" title={fullName}>
                    {shortName}
                  </div>
                  <div class="audit-tool">
                    <span class="ev-tool-ns">{e.namespace}__</span>
                    <span class="ev-tool-name">{e.tool}</span>
                  </div>
                  <div class="audit-latency">{latency}</div>
                </button>
                {open && <EventDetail event={e} fullName={fullName} />}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function AuditStat({
  label,
  value,
  tone,
}: {
  label: string;
  value: number | string;
  tone?: "default" | "warn";
}) {
  return (
    <div class="audit-stat">
      <div class="audit-stat-label">{label}</div>
      <div
        class={
          tone === "warn"
            ? "audit-stat-value audit-stat-value-warn"
            : "audit-stat-value"
        }
      >
        {value}
      </div>
    </div>
  );
}

function EventDetail({
  event,
  fullName,
}: {
  event: AuditEvent;
  fullName: string;
}) {
  const trace = event.policy_trace;
  const hasTrace =
    trace && (trace.workspace_id || trace.selector || trace.source);

  return (
    <div class="audit-detail">
      <DetailRow label="timestamp" value={event.ts} mono />
      <DetailRow label="agent" value={fullName} />
      <DetailRow label="client_id" value={event.client_id} mono />
      <DetailRow label="namespace" value={event.namespace} mono />
      <DetailRow label="tool" value={event.tool} mono />
      <DetailRow
        label="status"
        value={event.allowed ? "allowed" : "denied"}
        tone={event.allowed ? "ok" : "error"}
      />
      <DetailRow
        label="latency"
        value={event.allowed ? `${event.latency_ms}ms` : "—"}
      />
      {hasTrace && (
        <>
          {trace.workspace_id && (
            <DetailRow label="workspace" value={trace.workspace_id} mono />
          )}
          {trace.selector && (
            <DetailRow label="selector" value={trace.selector} mono />
          )}
          {trace.source && (
            <DetailRow label="decided by" value={trace.source} />
          )}
          {(trace.layers || []).length > 0 && (
            <div class="audit-detail-row">
              <span class="audit-detail-label">policy stack</span>
              <span class="audit-detail-value">
                {(trace.layers || []).map((l, i) => (
                  <span class="audit-trace-layer" key={`${l.source}-${i}`}>
                    <span class="audit-trace-source">{l.source}</span>
                    {l.selector ? (
                      <code class="audit-trace-selector">{l.selector}</code>
                    ) : (
                      <span class="hint-text">no rule</span>
                    )}
                  </span>
                ))}
              </span>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function DetailRow({
  label,
  value,
  mono,
  tone,
}: {
  label: string;
  value: string;
  mono?: boolean;
  tone?: "ok" | "error";
}) {
  return (
    <div class="audit-detail-row">
      <span class="audit-detail-label">{label}</span>
      <span
        class={[
          "audit-detail-value",
          mono ? "audit-detail-value-mono" : "",
          tone === "ok" ? "audit-detail-value-ok" : "",
          tone === "error" ? "audit-detail-value-error" : "",
        ]
          .filter(Boolean)
          .join(" ")}
      >
        {value}
      </span>
    </div>
  );
}
