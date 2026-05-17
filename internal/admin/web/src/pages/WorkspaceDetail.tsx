import { useEffect, useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { getJSON } from "../api/client";
import { showError } from "../state/toasts";
import { fmtAge } from "../util/time";
import { fmtBytes, pctOfQuota } from "../util/bytes";
import type {
  WorkspaceDetail as WorkspaceDetailType,
  WorkspaceHealth,
} from "../api/types";

const HEALTH_PILL: Record<WorkspaceHealth, string> = {
  ok: "pill pill-ok",
  quota_warn: "pill pill-warn",
  quota_exceeded: "pill pill-error",
  stale: "pill pill-warn",
};

export function WorkspaceDetail() {
  const { params } = useRoute();
  const loc = useLocation();
  const id = params.id;
  const [data, setData] = useState<WorkspaceDetailType | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    (async () => {
      try {
        const d = await getJSON<WorkspaceDetailType>(
          `/workspaces/${encodeURIComponent(id)}`,
        );
        if (!cancelled) setData(d);
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        if (!cancelled) setError(msg);
        showError(msg);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (error) {
    return (
      <div>
        <div class="detail-breadcrumb">
          <a href="/settings/storage">workspaces</a>
          <span class="breadcrumb-sep">/</span>
          <span class="breadcrumb-current">{id}</span>
        </div>
        <div class="empty-state">{error}</div>
      </div>
    );
  }
  if (!data) {
    return (
      <div>
        <div class="detail-breadcrumb">
          <a href="/settings/storage">workspaces</a>
          <span class="breadcrumb-sep">/</span>
          <span class="breadcrumb-current">{id}</span>
        </div>
        <div class="empty-state">loading…</div>
      </div>
    );
  }

  const ws = data.workspace;
  const pct = pctOfQuota(ws.used_bytes, ws.quota_bytes);

  return (
    <div>
      <div class="detail-breadcrumb">
        <a href="/settings/storage">workspaces</a>
        <span class="breadcrumb-sep">/</span>
        <span class="breadcrumb-current">{ws.id}</span>
      </div>

      <div class="detail-header">
        <div>
          <div class="page-title">{ws.id}</div>
          <div class="page-subtitle">
            {ws.type || "proxied"}
            {ws.owner ? ` · owner ${ws.owner}` : ""}
          </div>
        </div>
        <div class="detail-status">
          {ws.health_status && (
            <span class={HEALTH_PILL[ws.health_status]}>{ws.health_status}</span>
          )}
        </div>
      </div>

      <div class="meta-row">
        <MetaItem label="type" value={ws.type || "proxied"} />
        <MetaItem
          label="used"
          value={
            ws.used_bytes && ws.used_bytes > 0
              ? fmtBytes(ws.used_bytes)
              : "—"
          }
        />
        <MetaItem
          label="quota"
          value={ws.quota_bytes ? fmtBytes(ws.quota_bytes) : "unset"}
        />
        {pct !== null && (
          <MetaItem label="used %" value={`${pct}%`} />
        )}
        {ws.last_seen && (
          <MetaItem label="last seen" value={fmtAge(ws.last_seen)} />
        )}
        {ws.hostname && <MetaItem label="hostname" value={ws.hostname} />}
        {ws.root && <MetaItem label="root" value={ws.root} mono />}
      </div>

      <div class="section">
        <div class="section-header">
          <span class="section-title">selected by policy</span>
          <span class="section-sub">
            policy entries that pin this workspace via id:&lt;workspace-id&gt;. agent-selector
            rules resolve dynamically and aren&#39;t listed here.
          </span>
        </div>
        {data.references.length === 0 ? (
          <div class="empty-state">
            no policy entries pin this workspace. it&#39;s reachable only via
            backend.workspace static config or explicit _prism_workspace
            selectors.
          </div>
        ) : (
          <div class="card">
            <table class="storage-resolution-table">
              <thead>
                <tr>
                  <th>source</th>
                  <th>backend</th>
                  <th>selector</th>
                </tr>
              </thead>
              <tbody>
                {data.references.map((r) => (
                  <tr key={`${r.source}|${r.backend_id}`}>
                    <td class="storage-resolution-source">
                      {r.source.startsWith("agent:") ? (
                        <a
                          href={`/agents/${encodeURIComponent(
                            r.source.slice("agent:".length),
                          )}`}
                        >
                          {r.source}
                        </a>
                      ) : r.source.startsWith("group:") ? (
                        <a
                          href={`/policy/groups/${encodeURIComponent(
                            r.source.slice("group:".length),
                          )}`}
                        >
                          {r.source}
                        </a>
                      ) : (
                        r.source
                      )}
                    </td>
                    <td class="storage-resolution-backend">{r.backend_id}</td>
                    <td>
                      <code>{r.selector}</code>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {(ws.backends || []).length > 0 && (
        <div class="section">
          <div class="section-header">
            <span class="section-title">attached backends</span>
          </div>
          <div class="card">
            {(ws.backends || []).map((b) => (
              <div class="workspace-backend" key={b.id}>
                <span>{b.namespace}</span>
                <span>{b.tools?.length || 0} tools</span>
              </div>
            ))}
          </div>
        </div>
      )}

      <div class="section">
        <button
          class="section-btn"
          onClick={() => loc.route("/settings/storage")}
        >
          ← back to workspaces
        </button>
      </div>
    </div>
  );
}

function MetaItem({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div class="meta-item">
      <div class="meta-label">{label}</div>
      <div class={mono ? "meta-value meta-value-mono" : "meta-value"}>
        {value}
      </div>
    </div>
  );
}
