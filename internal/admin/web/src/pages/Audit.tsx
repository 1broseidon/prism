import { useMemo, useState } from "preact/hooks";
import { events, agents } from "../state";
import { fmtTimeOfDay, splitLabel } from "../util/time";

export function Audit() {
  const ev = events.data.value || [];
  const ag = agents.data.value || [];

  const [filterAgent, setFilterAgent] = useState("");
  const [filterNamespace, setFilterNamespace] = useState("");
  const [showDenied, setShowDenied] = useState<"all" | "allowed" | "denied">(
    "all",
  );

  const nameCache = useMemo(() => {
    const m = new Map<string, string>();
    ag.forEach((a) =>
      m.set(a.client_id, a.label || a.description || a.client_id),
    );
    return m;
  }, [ag]);

  const namespaces = useMemo(() => {
    return Array.from(new Set(ev.map((e) => e.namespace).filter(Boolean)));
  }, [ev]);

  const filtered = useMemo(() => {
    return ev.filter((e) => {
      if (filterAgent && e.client_id !== filterAgent) return false;
      if (filterNamespace && e.namespace !== filterNamespace) return false;
      if (showDenied === "allowed" && !e.allowed) return false;
      if (showDenied === "denied" && e.allowed) return false;
      return true;
    });
  }, [ev, filterAgent, filterNamespace, showDenied]);

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">Audit</div>
          <div class="page-subtitle">
            {filtered.length} of {ev.length} events
          </div>
        </div>
      </div>

      <div class="section">
        <div
          class="inline-form"
          style="border-bottom:1px solid var(--line);padding-bottom:12px;margin-bottom:0"
        >
          <select
            value={filterAgent}
            onChange={(e) =>
              setFilterAgent((e.target as HTMLSelectElement).value)
            }
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
          >
            <option value="">all namespaces</option>
            {namespaces.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
          <select
            value={showDenied}
            onChange={(e) =>
              setShowDenied(
                (e.target as HTMLSelectElement).value as
                  | "all"
                  | "allowed"
                  | "denied",
              )
            }
          >
            <option value="all">all</option>
            <option value="allowed">allowed</option>
            <option value="denied">denied only</option>
          </select>
          {(filterAgent || filterNamespace || showDenied !== "all") && (
            <button
              class="cancel-btn"
              onClick={() => {
                setFilterAgent("");
                setFilterNamespace("");
                setShowDenied("all");
              }}
            >
              clear
            </button>
          )}
        </div>

        {filtered.length === 0 ? (
          <div class="empty-state">
            {ev.length === 0 ? "Waiting for tool calls…" : "No matching events."}
          </div>
        ) : (
          <table class="events-table">
            <thead>
              <tr>
                <th style="width:8%">Time</th>
                <th style="width:18%">Agent</th>
                <th>Tool</th>
                <th style="width:7%">Status</th>
                <th style="width:8%" class="right">
                  Latency
                </th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((e, idx) => {
                const full =
                  nameCache.get(e.client_id) || e.client_id || "anonymous";
                const [shortName] = splitLabel(full);
                const latency = e.allowed
                  ? e.latency_ms === 0
                    ? "<1ms"
                    : `${e.latency_ms}ms`
                  : "-";
                return (
                  <tr key={`${e.ts}-${idx}`}>
                    <td class="ev-ts">{fmtTimeOfDay(e.ts)}</td>
                    <td class="ev-agent" title={full}>
                      {shortName}
                    </td>
                    <td>
                      <span class="ev-tool-ns">{e.namespace}__</span>
                      <span class="ev-tool-name">{e.tool}</span>
                    </td>
                    <td>
                      {e.allowed ? (
                        <span class="ev-status">
                          <span class="dot" />
                        </span>
                      ) : (
                        <span class="ev-status">
                          <span class="denied-text">denied</span>
                        </span>
                      )}
                    </td>
                    <td class="ev-latency">{latency}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
