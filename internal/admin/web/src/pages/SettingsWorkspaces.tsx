import { useEffect, useMemo, useState } from "preact/hooks";
import { deleteJSON, getJSON, postJSON, putJSON } from "../api/client";
import { showError, withToast } from "../state/toasts";
import { canMutate } from "../state/me";
import { fmtAge } from "../util/time";
import { fmtBytes, pctOfQuota } from "../util/bytes";
import { Field } from "../components/Field";
import type {
  Workspace,
  WorkspaceBridgeConfig,
  WorkspaceHealth,
} from "../api/types";

const SECRET_PLACEHOLDER = "•••••••• (kept)";

function commaList(value: string): string[] {
  return value
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

const HEALTH_LABELS: Record<WorkspaceHealth, { label: string; pill: string }> = {
  ok: { label: "ok", pill: "pill pill-ok" },
  quota_warn: { label: "warn", pill: "pill pill-warn" },
  quota_exceeded: { label: "over quota", pill: "pill pill-error" },
  stale: { label: "stale", pill: "pill pill-warn" },
};

function HealthPill({ health }: { health: WorkspaceHealth | undefined }) {
  if (!health) return null;
  const def = HEALTH_LABELS[health];
  return <span class={def.pill}>{def.label}</span>;
}

function UsageBar({ used, quota }: { used?: number; quota?: number }) {
  const pct = pctOfQuota(used, quota);
  if (pct === null) {
    return (
      <span class="hint-text">{fmtBytes(used)} used · no quota</span>
    );
  }
  return (
    <span class="workspace-usage">
      <span class="workspace-usage-text">
        {fmtBytes(used)} / {fmtBytes(quota)} ({pct}%)
      </span>
      <span class="workspace-usage-bar">
        <span
          class={
            pct >= 100
              ? "workspace-usage-fill workspace-usage-fill-error"
              : pct >= 90
                ? "workspace-usage-fill workspace-usage-fill-warn"
                : "workspace-usage-fill"
          }
          style={`width:${Math.max(2, pct)}%`}
        />
      </span>
    </span>
  );
}

export function SettingsWorkspaces() {
  const mutate = canMutate();
  const [config, setConfig] = useState<WorkspaceBridgeConfig | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);

  const load = async () => {
    try {
      const [cfg, ws] = await Promise.all([
        getJSON<WorkspaceBridgeConfig>("/config/workspace-bridge"),
        getJSON<Workspace[]>("/workspaces"),
      ]);
      setConfig(cfg);
      setWorkspaces(ws);
    } catch (e) {
      showError(e instanceof Error ? e.message : String(e));
    }
  };

  useEffect(() => {
    load();
    const timer = window.setInterval(load, 10000);
    return () => window.clearInterval(timer);
  }, []);

  const { proxied, virtual, ephemeral, totals } = useMemo(() => {
    const byType = { proxied: [] as Workspace[], virtual: [] as Workspace[], ephemeral: [] as Workspace[] };
    const totals = { used: 0, quota: 0, byType: { proxied: 0, virtual: 0, ephemeral: 0 } };
    for (const w of workspaces) {
      const t = (w.type || "proxied") as keyof typeof byType;
      byType[t].push(w);
      totals.used += w.used_bytes || 0;
      totals.quota += w.quota_bytes || 0;
      totals.byType[t] += w.used_bytes || 0;
    }
    return { ...byType, totals };
  }, [workspaces]);

  if (config === null) {
    return (
      <div>
        <div class="page-header">
          <div>
            <div class="page-title">workspaces</div>
          </div>
        </div>
        <div class="empty-state">loading…</div>
      </div>
    );
  }

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">workspaces</div>
          <div class="page-subtitle">
            storage attached to mcp servers. virtual workspaces persist on the
            gateway; proxied bridges sync from local repos; ephemeral storage
            is auto-managed.
          </div>
        </div>
      </div>

      <StorageSummary totals={totals} workspaces={workspaces} />

      <LocalBridgesSection
        config={config}
        mutate={mutate}
        proxied={proxied}
        onChange={load}
      />

      <VirtualWorkspacesSection
        virtual={virtual}
        mutate={mutate}
        onChange={load}
      />

      {ephemeral.length > 0 && (
        <EphemeralListSection ephemeral={ephemeral} mutate={mutate} onChange={load} />
      )}
    </div>
  );
}

function StorageSummary({
  totals,
  workspaces,
}: {
  totals: { used: number; quota: number; byType: { proxied: number; virtual: number; ephemeral: number } };
  workspaces: Workspace[];
}) {
  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">storage usage</span>
      </div>
      <div class="card storage-summary">
        <div class="storage-summary-total">
          <div class="storage-summary-label">total used</div>
          <div class="storage-summary-value">{fmtBytes(totals.used)}</div>
          {totals.quota > 0 && (
            <div class="hint-text">
              of {fmtBytes(totals.quota)} configured quota
            </div>
          )}
        </div>
        <div class="storage-summary-breakdown">
          <SummaryRow label="proxied" bytes={totals.byType.proxied} count={workspaces.filter((w) => (w.type || "proxied") === "proxied").length} />
          <SummaryRow label="virtual" bytes={totals.byType.virtual} count={workspaces.filter((w) => w.type === "virtual").length} />
          <SummaryRow label="ephemeral" bytes={totals.byType.ephemeral} count={workspaces.filter((w) => w.type === "ephemeral").length} />
        </div>
      </div>
    </div>
  );
}

function SummaryRow({ label, bytes, count }: { label: string; bytes: number; count: number }) {
  return (
    <div class="storage-summary-row">
      <span class="storage-summary-row-label">{label}</span>
      <span class="storage-summary-row-count">
        {count} workspace{count === 1 ? "" : "s"}
      </span>
      <span class="storage-summary-row-bytes">{fmtBytes(bytes)}</span>
    </div>
  );
}

function LocalBridgesSection({
  config,
  mutate,
  proxied,
  onChange,
}: {
  config: WorkspaceBridgeConfig;
  mutate: boolean;
  proxied: Workspace[];
  onChange: () => Promise<void>;
}) {
  const [enabled, setEnabled] = useState(config.enabled);
  const [token, setToken] = useState("");
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);

  // Keep local "enabled" in sync when config refreshes from outside.
  useEffect(() => {
    setEnabled(config.enabled);
    setToken("");
    setDirty(false);
  }, [config.enabled, config.token_set]);

  const save = async () => {
    setSaving(true);
    await withToast(async () => {
      await putJSON<WorkspaceBridgeConfig>("/config/workspace-bridge", {
        enabled,
        token: token.trim() || undefined,
      });
      await onChange();
    });
    setSaving(false);
  };

  const disconnect = async (id: string) => {
    if (!confirm(`Disconnect local bridge "${id}"?`)) return;
    await withToast(async () => {
      await deleteJSON(`/workspaces/${encodeURIComponent(id)}`);
      await onChange();
    });
  };

  const gatewayURL = window.location.origin;
  const installCommand =
    `prism-bridge workspace install --gateway ${gatewayURL} ` +
    `--token <workspace-token> --root "$PWD" --files-only`;

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">local bridges</span>
        <span class="section-sub">
          proxied workspaces. install prism-bridge on a developer machine to
          sync that repo to a gateway sandbox.
        </span>
      </div>
      <div class="card workspace-bridge-card">
        <div class="config-status-row">
          <span class={enabled ? "pill pill-ok" : "pill pill-neutral"}>
            {enabled ? "enabled" : "disabled"}
          </span>
          <div class="config-status-text">
            <span>
              {config.token_set
                ? "token configured. rotate it below when needed."
                : "set a token before enabling workspace bridges."}
            </span>
          </div>
        </div>

        <label class="config-toggle workspace-toggle">
          <input
            type="checkbox"
            checked={enabled}
            disabled={!mutate}
            onChange={(e) => {
              setEnabled((e.target as HTMLInputElement).checked);
              setDirty(true);
            }}
          />
          <span class="config-toggle-label">allow workspace bridge connections</span>
        </label>

        <Field label="workspace token">
          <input
            type="password"
            class="config-input"
            value={token}
            placeholder={config.token_set ? SECRET_PLACEHOLDER : "minimum 24 characters"}
            disabled={!mutate}
            autoComplete="new-password"
            onInput={(e) => {
              setToken((e.target as HTMLInputElement).value);
              setDirty(true);
            }}
          />
          <div class="hint-text" style="margin-top:4px">
            write-only shared secret used by local prism-bridge services.
          </div>
        </Field>

        <div class="workspace-install">
          <div class="workspace-install-label">install command</div>
          <code>{installCommand}</code>
        </div>

        {mutate && (
          <div class="config-actions">
            <button
              class="save-btn"
              onClick={save}
              disabled={saving || !dirty || (enabled && !config.token_set && !token.trim())}
            >
              {saving ? "saving…" : "save"}
            </button>
            {saving && (
              <span class="runtime-progress">
                <span class="inline-spinner" />
                applying
              </span>
            )}
            {dirty && !saving && (
              <span class="config-dirty-marker">unsaved changes</span>
            )}
          </div>
        )}

        <div class="workspace-list">
          {proxied.length === 0 ? (
            <div class="empty-state">no local bridges connected.</div>
          ) : (
            proxied.map((ws) => (
              <WorkspaceRow
                key={ws.id}
                workspace={ws}
                mutate={mutate}
                onDelete={() => disconnect(ws.id)}
                deleteLabel="disconnect"
              />
            ))
          )}
        </div>
      </div>
    </div>
  );
}

function VirtualWorkspacesSection({
  virtual,
  mutate,
  onChange,
}: {
  virtual: Workspace[];
  mutate: boolean;
  onChange: () => Promise<void>;
}) {
  const [adding, setAdding] = useState(false);

  const remove = async (id: string) => {
    if (!confirm(`Delete virtual workspace "${id}"? Any data stored on the gateway will be lost.`)) return;
    await withToast(async () => {
      await deleteJSON(`/workspaces/${encodeURIComponent(id)}`);
      await onChange();
    });
  };

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">virtual workspaces</span>
        <span class="section-sub">
          persistent storage on the gateway. attach to one or more servers.
        </span>
        {mutate && !adding && (
          <button class="section-btn" onClick={() => setAdding(true)}>
            + workspace
          </button>
        )}
      </div>
      <div class="workspace-list">
        {adding && mutate && (
          <CreateVirtualWorkspaceForm
            onDone={async () => {
              setAdding(false);
              await onChange();
            }}
            onCancel={() => setAdding(false)}
          />
        )}
        {virtual.length === 0 && !adding ? (
          <div class="empty-state">
            no virtual workspaces. create one to give servers persistent
            gateway-side storage.
          </div>
        ) : (
          virtual.map((ws) => (
            <WorkspaceRow
              key={ws.id}
              workspace={ws}
              mutate={mutate}
              onDelete={() => remove(ws.id)}
              deleteLabel="delete"
            />
          ))
        )}
      </div>
    </div>
  );
}

function CreateVirtualWorkspaceForm({
  onDone,
  onCancel,
}: {
  onDone: () => Promise<void>;
  onCancel: () => void;
}) {
  const [id, setId] = useState("");
  const [owner, setOwner] = useState("");
  const [allowedAgents, setAllowedAgents] = useState("");
  const [allowedTemplates, setAllowedTemplates] = useState("");
  const [quotaMB, setQuotaMB] = useState("");
  const [retentionDays, setRetentionDays] = useState("");

  const submit = async () => {
    const trimmedID = id.trim();
    if (!trimmedID) return;
    const quotaN = Number(quotaMB);
    const retentionN = Number(retentionDays);
    const ok = await withToast(async () => {
      await postJSON<Workspace>("/workspaces", {
        id: trimmedID,
        type: "virtual",
        owner: owner.trim() || undefined,
        allowed_agents: commaList(allowedAgents),
        allowed_templates: commaList(allowedTemplates),
        quota_bytes:
          Number.isFinite(quotaN) && quotaN > 0
            ? Math.round(quotaN * 1024 * 1024)
            : undefined,
        retention_seconds:
          Number.isFinite(retentionN) && retentionN > 0
            ? Math.round(retentionN * 24 * 60 * 60)
            : undefined,
      });
    });
    if (ok !== undefined) await onDone();
  };

  return (
    <div class="card workspace-create-card">
      <div class="inline-form server-workspace-form">
        <input
          type="text"
          class="config-input"
          value={id}
          placeholder="workspace id"
          spellcheck={false}
          autoFocus
          onInput={(e) => setId((e.target as HTMLInputElement).value)}
        />
        <input
          type="text"
          class="config-input"
          value={owner}
          placeholder="owner email"
          spellcheck={false}
          onInput={(e) => setOwner((e.target as HTMLInputElement).value)}
        />
        <input
          type="text"
          class="config-input"
          value={allowedAgents}
          placeholder="allowed agents (csv, * for any)"
          spellcheck={false}
          onInput={(e) => setAllowedAgents((e.target as HTMLInputElement).value)}
        />
        <input
          type="text"
          class="config-input"
          value={allowedTemplates}
          placeholder="allowed servers (csv)"
          spellcheck={false}
          onInput={(e) => setAllowedTemplates((e.target as HTMLInputElement).value)}
        />
        <input
          type="number"
          min="0"
          class="config-input"
          value={quotaMB}
          placeholder="quota mb"
          onInput={(e) => setQuotaMB((e.target as HTMLInputElement).value)}
        />
        <input
          type="number"
          min="0"
          class="config-input"
          value={retentionDays}
          placeholder="retention days"
          onInput={(e) => setRetentionDays((e.target as HTMLInputElement).value)}
        />
        <button class="save-btn" disabled={!id.trim()} onClick={submit}>
          create
        </button>
        <button class="cancel-btn" onClick={onCancel}>
          cancel
        </button>
      </div>
    </div>
  );
}

function EphemeralListSection({
  ephemeral,
  mutate,
  onChange,
}: {
  ephemeral: Workspace[];
  mutate: boolean;
  onChange: () => Promise<void>;
}) {
  const remove = async (id: string) => {
    await withToast(async () => {
      await deleteJSON(`/workspaces/${encodeURIComponent(id)}`);
      await onChange();
    });
  };
  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">ephemeral workspaces</span>
        <span class="section-sub">
          auto-allocated scratch storage. discarded when no server is attached.
        </span>
      </div>
      <div class="workspace-list">
        {ephemeral.map((ws) => (
          <WorkspaceRow
            key={ws.id}
            workspace={ws}
            mutate={mutate}
            onDelete={() => remove(ws.id)}
            deleteLabel="remove"
          />
        ))}
      </div>
    </div>
  );
}

function WorkspaceRow({
  workspace: ws,
  mutate,
  onDelete,
  deleteLabel,
}: {
  workspace: Workspace;
  mutate: boolean;
  onDelete: () => void | Promise<void>;
  deleteLabel: string;
}) {
  return (
    <div class="workspace-row">
      <div class="workspace-row-main">
        <div class="workspace-title">
          <span>{ws.id}</span>
          <span class="pill pill-neutral">{ws.type || "proxied"}</span>
          <HealthPill health={ws.health_status} />
        </div>
        <div class="workspace-usage-row">
          <UsageBar used={ws.used_bytes} quota={ws.quota_bytes} />
        </div>
        <div class="workspace-meta">
          {[
            ws.owner ? `owner ${ws.owner}` : "",
            ws.allowed_agents?.length
              ? `agents ${ws.allowed_agents.join(", ")}`
              : "",
            ws.allowed_templates?.length
              ? `servers ${ws.allowed_templates.join(", ")}`
              : "",
            ws.retention_seconds
              ? `retention ${Math.round(ws.retention_seconds / 86400)}d`
              : "",
            ws.hostname,
            ws.root,
            ws.last_seen ? fmtAge(ws.last_seen) : "",
          ]
            .filter(Boolean)
            .join(" · ")}
        </div>
        {(ws.backends || []).map((backend) => (
          <div class="workspace-backend" key={backend.id}>
            <span>{backend.namespace}</span>
            <span>{backend.tools?.length || 0} tools</span>
          </div>
        ))}
      </div>
      {mutate && (
        <button class="danger-btn" onClick={onDelete}>
          {deleteLabel}
        </button>
      )}
    </div>
  );
}
