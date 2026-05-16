import { useState, useMemo } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { backends, events } from "../state";
import { deleteJSON, postJSON } from "../api/client";
import { withToast } from "../state/toasts";
import type {
  AddBackendBody,
  Backend,
  BackendTool,
  CredentialInput,
} from "../api/types";
import { fmtAge, fmtTimeOfDay, splitLabel } from "../util/time";

type CredType = "none" | "static" | "env" | "command";

interface CredFormState {
  type: CredType;
  header: string;
  value: string;
  env: string;
  command: string;
}

function emptyCred(): CredFormState {
  return {
    type: "none",
    header: "Authorization",
    value: "",
    env: "",
    command: "",
  };
}

function credFromBackend(b: Backend): CredFormState {
  const c = b.credential;
  if (!c || !c.configured) return emptyCred();
  return {
    type: c.type,
    header: c.header || "Authorization",
    value: "",
    env: c.env || "",
    command: c.command || "",
  };
}

function buildCredInput(s: CredFormState): CredentialInput | undefined {
  if (s.type === "none") return undefined;
  const c: CredentialInput = { type: s.type, header: s.header };
  if (s.type === "static") c.value = s.value;
  if (s.type === "env") c.env = s.env;
  if (s.type === "command") c.command = s.command;
  return c;
}

export function ServerDetail() {
  const { params } = useRoute();
  const loc = useLocation();
  const id = params.id;
  const list = backends.data.value || [];
  const backend = list.find((b) => b.id === id);

  if (backends.data.value === null) {
    return <PageShell title={id}>loading…</PageShell>;
  }
  if (!backend) {
    return (
      <PageShell title={id}>
        <div class="empty-state">
          backend not found.{" "}
          <a href="/servers" class="link-accent">
            back to servers
          </a>
        </div>
      </PageShell>
    );
  }

  return (
    <div>
      <div class="detail-breadcrumb">
        <a href="/servers">servers</a>
        <span class="breadcrumb-sep">/</span>
        <span class="breadcrumb-current">{backend.id}</span>
      </div>

      <div class="detail-header">
        <div>
          <div class="page-title">{backend.id}</div>
          <div class="page-subtitle">
            {backend.namespace && backend.namespace !== backend.id
              ? `namespace: ${backend.namespace}`
              : `namespace inherits id`}
          </div>
        </div>
        <div class="detail-status">
          <StatusPill backend={backend} />
        </div>
      </div>

      <MetaRow backend={backend} />
      <ToolsSection backend={backend} />
      <CredentialSection backend={backend} />
      <ActivitySection backendId={backend.id} backend={backend} />
      <DangerSection
        backendId={backend.id}
        onRemoved={async () => {
          await backends.refresh();
          loc.route("/servers");
        }}
      />
    </div>
  );
}

function PageShell({
  title,
  children,
}: {
  title: string;
  children: preact.ComponentChildren;
}) {
  return (
    <div>
      <div class="detail-breadcrumb">
        <a href="/servers">servers</a>
        <span class="breadcrumb-sep">/</span>
        <span class="breadcrumb-current">{title}</span>
      </div>
      <div class="page-header">
        <div>
          <div class="page-title">{title}</div>
        </div>
      </div>
      {children}
    </div>
  );
}

function StatusPill({ backend }: { backend: Backend }) {
  const cb = backend.circuit_breaker;
  if (cb === "open") {
    return <span class="pill pill-error">circuit open</span>;
  }
  if (cb === "half_open" || cb === "half-open") {
    return <span class="pill pill-warn">recovering</span>;
  }
  if ((backend.tools?.length ?? 0) > 0) {
    return <span class="pill pill-ok">connected</span>;
  }
  return <span class="pill pill-neutral">idle</span>;
}

function MetaRow({ backend }: { backend: Backend }) {
  const transport = backend.url ? "http" : "stdio";
  const toolCount = backend.tools?.length ?? 0;
  const credType = backend.credential?.configured
    ? backend.credential.type
    : "none";

  return (
    <div class="meta-row">
      <MetaItem label="transport" value={transport} />
      <MetaItem label="endpoint" value={backend.url || "stdio"} mono />
      <MetaItem label="tools" value={String(toolCount)} />
      <MetaItem label="credential" value={credType} />
      {backend.circuit_breaker && (
        <MetaItem label="breaker" value={backend.circuit_breaker} />
      )}
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

function ToolsSection({ backend }: { backend: Backend }) {
  const [query, setQuery] = useState("");
  const [expanded, setExpanded] = useState<string | null>(null);
  const tools = backend.tools || [];
  const ev = events.data.value || [];

  // Per-tool call statistics computed from the events buffer.
  const stats = useMemo(() => {
    const m = new Map<string, { count: number; denied: number; lastTs?: string }>();
    for (const t of tools) m.set(t.name, { count: 0, denied: 0 });
    for (const e of ev) {
      const namespaced = `${e.namespace}__${e.tool}`;
      const s = m.get(namespaced);
      if (!s) continue;
      s.count++;
      if (!e.allowed) s.denied++;
      if (!s.lastTs) s.lastTs = e.ts;
    }
    return m;
  }, [tools, ev]);

  const filtered = useMemo(() => {
    const q = query.toLowerCase().trim();
    if (!q) return tools;
    return tools.filter(
      (t) =>
        t.name.toLowerCase().includes(q) ||
        (t.description || "").toLowerCase().includes(q),
    );
  }, [tools, query]);

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">tools ({tools.length})</span>
        {tools.length > 0 && (
          <input
            type="search"
            placeholder="search tools…"
            class="search-input"
            value={query}
            onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
          />
        )}
      </div>
      {tools.length === 0 ? (
        <div class="empty-state">
          no tools registered. the backend either hasn't connected or exposes
          none.
        </div>
      ) : filtered.length === 0 ? (
        <div class="empty-state">no tools match “{query}”.</div>
      ) : (
        <div class="tools-list">
          {filtered.map((t) => (
            <ToolRow
              key={t.name}
              tool={t}
              stats={stats.get(t.name)}
              expanded={expanded === t.name}
              onToggle={() => setExpanded(expanded === t.name ? null : t.name)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function ToolRow({
  tool,
  stats,
  expanded,
  onToggle,
}: {
  tool: BackendTool;
  stats?: { count: number; denied: number; lastTs?: string };
  expanded: boolean;
  onToggle: () => void;
}) {
  const desc = tool.description || "";
  const short =
    !expanded && desc.length > 240 ? desc.slice(0, 240) + "…" : desc;

  return (
    <div
      class={expanded ? "tool-row tool-row-expanded" : "tool-row"}
      onClick={onToggle}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onToggle();
        }
      }}
    >
      <div class="tool-row-header">
        <div class="tool-name">{tool.name}</div>
        {stats && stats.count > 0 && (
          <div class="tool-count">
            <span class="tool-count-value">{stats.count}</span>
            <span class="tool-count-label">
              call{stats.count === 1 ? "" : "s"}
              {stats.denied > 0 && ` · ${stats.denied} denied`}
            </span>
          </div>
        )}
      </div>
      {desc ? (
        <div class="tool-desc">{short}</div>
      ) : (
        <div class="tool-desc tool-desc-empty">no description provided</div>
      )}
      {expanded && stats && (
        <div class="tool-stats">
          <div class="tool-stat">
            <div class="tool-stat-label">calls (recent)</div>
            <div class="tool-stat-value">{stats.count}</div>
          </div>
          <div class="tool-stat">
            <div class="tool-stat-label">denied</div>
            <div class="tool-stat-value">{stats.denied}</div>
          </div>
          <div class="tool-stat">
            <div class="tool-stat-label">last call</div>
            <div class="tool-stat-value">
              {stats.lastTs
                ? new Date(stats.lastTs).toLocaleTimeString()
                : "—"}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function CredentialSection({ backend }: { backend: Backend }) {
  const [editing, setEditing] = useState(false);
  const [cred, setCred] = useState<CredFormState>(credFromBackend(backend));
  const [error, setError] = useState<string | null>(null);

  const save = async () => {
    setError(null);
    const result = await withToast(
      async () => {
        const body: AddBackendBody = { url: backend.url || "" };
        const credInput = buildCredInput(cred);
        body.credential = credInput ?? null;
        await postJSON(
          `/backends/${encodeURIComponent(backend.id)}`,
          body,
        );
        await backends.refresh();
      },
      { success: `credential saved for ${backend.id}` },
    );
    if (result !== undefined) setEditing(false);
  };

  const c = backend.credential;
  const summary = c?.configured
    ? c.type === "static"
      ? `api key (header: ${c.header || "Authorization"})`
      : c.type === "env"
        ? `env var: ${c.env}`
        : c.type === "command"
          ? `command: ${(c.command || "").slice(0, 60)}${(c.command || "").length > 60 ? "…" : ""}`
          : "configured"
    : "none";

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">credential</span>
        {!editing && (
          <button class="section-btn" onClick={() => {
            setCred(credFromBackend(backend));
            setEditing(true);
          }}>
            edit
          </button>
        )}
      </div>
      {!editing ? (
        <div class="card">
          <div class="card-row">
            <span class="meta-label">type</span>
            <span class="meta-value-mono">{summary}</span>
          </div>
        </div>
      ) : (
        <div class="card">
          <div class="inline-form">
            <select
              value={cred.type}
              onChange={(e) =>
                setCred({
                  ...cred,
                  type: (e.target as HTMLSelectElement).value as CredType,
                })
              }
            >
              <option value="none">none</option>
              <option value="static">api key</option>
              <option value="env">env var</option>
              <option value="command">command</option>
            </select>
            <CredFields state={cred} onChange={setCred} />
          </div>
          <div class="form-actions">
            <button class="save-btn" onClick={save}>
              save
            </button>
            <button class="cancel-btn" onClick={() => setEditing(false)}>
              cancel
            </button>
            {error && <span class="error-text">{error}</span>}
          </div>
        </div>
      )}
    </div>
  );
}

function CredFields({
  state,
  onChange,
}: {
  state: CredFormState;
  onChange: (next: CredFormState) => void;
}) {
  if (state.type === "none") return null;
  return (
    <div class="cred-fields">
      <input
        type="text"
        placeholder="header"
        value={state.header}
        style="width:140px"
        onInput={(e) =>
          onChange({ ...state, header: (e.target as HTMLInputElement).value })
        }
      />
      {state.type === "static" && (
        <>
          <input
            type="password"
            placeholder="secret value (write-only)"
            value={state.value}
            style="width:220px"
            onInput={(e) =>
              onChange({
                ...state,
                value: (e.target as HTMLInputElement).value,
              })
            }
          />
          <span class="cred-hint">write-only</span>
        </>
      )}
      {state.type === "env" && (
        <input
          type="text"
          placeholder="ENV_VAR_NAME"
          value={state.env}
          style="width:180px"
          onInput={(e) =>
            onChange({ ...state, env: (e.target as HTMLInputElement).value })
          }
        />
      )}
      {state.type === "command" && (
        <input
          type="text"
          placeholder="vault kv get -field=token …"
          value={state.command}
          style="flex:1;min-width:260px"
          onInput={(e) =>
            onChange({
              ...state,
              command: (e.target as HTMLInputElement).value,
            })
          }
        />
      )}
    </div>
  );
}

function ActivitySection({
  backendId,
  backend,
}: {
  backendId: string;
  backend: Backend;
}) {
  const ev = events.data.value || [];
  // Events use namespace as the routing key; match against either id or namespace
  const ns = backend.namespace || backendId;
  const scoped = ev.filter((e) => e.namespace === ns).slice(0, 10);
  const lastCall = scoped[0];

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">
          recent activity ({scoped.length})
          {lastCall && (
            <span class="section-sub">· last call {fmtAge(lastCall.ts)}</span>
          )}
        </span>
        <a class="section-btn" href={`/audit?namespace=${encodeURIComponent(ns)}`}>
          view in audit
        </a>
      </div>
      {scoped.length === 0 ? (
        <div class="empty-state">no recent calls for this backend.</div>
      ) : (
        <table class="events-table">
          <thead>
            <tr>
              <th style="width:8%">time</th>
              <th style="width:20%">agent</th>
              <th>tool</th>
              <th style="width:8%">status</th>
              <th style="width:8%" class="right">
                latency
              </th>
            </tr>
          </thead>
          <tbody>
            {scoped.map((e, idx) => {
              const [shortName] = splitLabel(e.client_id);
              const latency = e.allowed
                ? e.latency_ms === 0
                  ? "<1ms"
                  : `${e.latency_ms}ms`
                : "-";
              return (
                <tr key={`${e.ts}-${idx}`}>
                  <td class="ev-ts">{fmtTimeOfDay(e.ts)}</td>
                  <td class="ev-agent" title={e.client_id}>
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
  );
}

function DangerSection({
  backendId,
  onRemoved,
}: {
  backendId: string;
  onRemoved: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);

  const remove = async () => {
    if (
      !confirm(
        `Remove backend "${backendId}"? This will disconnect it and remove all its tools from the gateway.`,
      )
    )
      return;
    setBusy(true);
    await withToast(
      async () => {
        await deleteJSON(`/backends/${encodeURIComponent(backendId)}`);
        await onRemoved();
      },
      { success: `backend ${backendId} removed` },
    );
    setBusy(false);
  };

  return (
    <div class="section section-danger">
      <div class="section-header">
        <span class="section-title section-title-danger">danger zone</span>
      </div>
      <div class="card card-danger">
        <div>
          <div class="danger-card-title">remove this backend</div>
          <div class="danger-card-desc">
            disconnects from the gateway and unregisters every tool it
            exposes. tokens and audit history are retained.
          </div>
        </div>
        <button class="danger-btn" onClick={remove} disabled={busy}>
          {busy ? "removing…" : "remove"}
        </button>
      </div>
    </div>
  );
}
