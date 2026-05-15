import { info, agents, backends, groups, events } from "../state";
import { fmtUptime, fmtTimeOfDay, splitLabel } from "../util/time";

export function Overview() {
  const i = info.data.value;
  const ag = agents.data.value || [];
  const be = backends.data.value || [];
  const gr = groups.data.value || [];
  const ev = events.data.value || [];

  const recent = ev.slice(0, 8);
  const nameCache = new Map<string, string>();
  ag.forEach((a) => {
    nameCache.set(a.client_id, a.label || a.description || a.client_id);
  });

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">Overview</div>
          <div class="page-subtitle">
            {i ? `${i.name} · v${i.version} · ${i.go_version}` : "loading…"}
          </div>
        </div>
      </div>

      <div class="tile-grid">
        <div class="tile">
          <div class="tile-label">Status</div>
          <div class="tile-value" style="color:var(--active)">
            {info.error.value ? "down" : "running"}
          </div>
          <div class="tile-sub">{i ? `up ${fmtUptime(i.uptime)}` : ""}</div>
        </div>
        <div class="tile">
          <div class="tile-label">Backends</div>
          <div class="tile-value">{be.length}</div>
          <div class="tile-sub">{be.map((b) => b.id).join(" · ") || "none"}</div>
        </div>
        <div class="tile">
          <div class="tile-label">Agents</div>
          <div class="tile-value">{ag.length}</div>
          <div class="tile-sub">
            {ag.filter((a) => a.dynamic).length} dynamic ·{" "}
            {ag.filter((a) => !a.dynamic).length} static
          </div>
        </div>
        <div class="tile">
          <div class="tile-label">Groups</div>
          <div class="tile-value">{gr.length}</div>
          <div class="tile-sub">{gr.length === 0 ? "no groups" : ""}</div>
        </div>
        <div class="tile">
          <div class="tile-label">Goroutines</div>
          <div class="tile-value">{i?.goroutines ?? "—"}</div>
          <div class="tile-sub">runtime</div>
        </div>
      </div>

      <div class="section">
        <div class="section-header">
          <span class="section-title">Recent Events ({ev.length})</span>
          <a class="section-btn" href="/audit">
            View all
          </a>
        </div>
        {recent.length === 0 ? (
          <div class="empty-state">Waiting for tool calls…</div>
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
              {recent.map((e, idx) => {
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
