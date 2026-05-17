import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { backends, events } from "../state";
import { deleteJSON, getJSON, patchJSON, postJSON } from "../api/client";
import { showError, withToast } from "../state/toasts";
import { canMutate } from "../state/me";
import type {
  AddBackendBody,
  AddBackendResponse,
  AuthStatus,
  Backend,
  BackendUpdateBody,
  BackendTool,
  CredentialInput,
  SandboxConfig,
  SandboxMount,
  WorkspaceApplyResult,
  WorkspaceChangeSet,
} from "../api/types";
import { fmtAge, fmtTimeOfDay, splitLabel } from "../util/time";

type CredType = "none" | "static" | "env" | "command";
const BACKEND_REBUILD_TIMEOUT_MS = 60_000;

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
      {canMutate() && <BackendSettingsSection backend={backend} />}
      {backend.workspace && <WorkspaceChangesSection backend={backend} />}
      {backend.disconnected && canMutate() && (
        <ReconnectSection backend={backend} />
      )}
      <ToolsSection backend={backend} />
      <CredentialSection backend={backend} />
      <ActivitySection backendId={backend.id} backend={backend} />
      {canMutate() && (
        <DangerSection
          backendId={backend.id}
          onRemoved={async () => {
            await backends.refresh();
            loc.route("/servers");
          }}
        />
      )}
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
  if (backend.enabled === false) {
    return <span class="pill pill-neutral">disabled</span>;
  }
  if (backend.disconnected) {
    return <span class="pill pill-error">disconnected</span>;
  }
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
      {backend.bridge_managed && (
        <MetaItem label="bridge" value={backend.runtime || "managed"} />
      )}
      {backend.circuit_breaker && (
        <MetaItem label="breaker" value={backend.circuit_breaker} />
      )}
    </div>
  );
}

function ReconnectSection({ backend }: { backend: Backend }) {
  const [busy, setBusy] = useState(false);
  const [oauth, setOauth] = useState<{
    backendId: string;
    authUrl: string;
  } | null>(null);

  const reconnect = async () => {
    setBusy(true);
    await withToast(async () => {
      await postJSON(`/backends/${encodeURIComponent(backend.id)}/reconnect`, {});
      await backends.refresh();
    });
    setBusy(false);
  };

  const reauthorize = async () => {
    if (!backend.url) return;
    setBusy(true);
    let startedOAuth = false;
    await withToast(async () => {
      const res = await postJSON<AddBackendResponse>(
        `/backends/${encodeURIComponent(backend.id)}`,
        { url: backend.url },
      );
      if (res.status === "auth_required") {
        startedOAuth = true;
        setOauth({ backendId: backend.id, authUrl: res.auth_url });
        return;
      }
      if (res.status === "manual_oauth_required") {
        throw new Error("manual OAuth credentials required");
      }
      await backends.refresh();
    });
    if (!startedOAuth) setBusy(false);
  };

  if (oauth) {
    return (
      <OAuthReconnectFlow
        backendId={oauth.backendId}
        authUrl={oauth.authUrl}
        onConnected={async () => {
          setOauth(null);
          setBusy(false);
          await backends.refresh();
        }}
        onCancel={() => {
          setOauth(null);
          setBusy(false);
        }}
      />
    );
  }

  return (
    <div class="section">
      <div class="card reconnect-card">
        <div>
          <div class="danger-card-title">backend is disconnected</div>
          <div class="danger-card-desc">
            reconnect uses the persisted backend configuration and any stored
            OAuth token without deleting state.
          </div>
        </div>
        <div class="inline-form">
          <button class="save-btn" onClick={reconnect} disabled={busy}>
            {busy ? "reconnecting…" : "reconnect"}
          </button>
          {backend.url && (
            <button class="section-btn" onClick={reauthorize} disabled={busy}>
              reauthorize
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function OAuthReconnectFlow({
  backendId,
  authUrl,
  onConnected,
  onCancel,
}: {
  backendId: string;
  authUrl: string;
  onConnected: () => void | Promise<void>;
  onCancel: () => void;
}) {
  const [status, setStatus] = useState<"idle" | "waiting" | "error" | "timeout">(
    "idle",
  );
  const [message, setMessage] = useState(
    "click authenticate to open the provider in a popup.",
  );
  const pollRef = useRef<number | null>(null);
  const timeoutRef = useRef<number | null>(null);

  const stop = () => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }
  };

  useEffect(() => stop, []);

  const start = () => {
    window.open(authUrl, "prism-auth", "width=600,height=700");
    setStatus("waiting");
    setMessage("authorization in progress…");
    pollRef.current = window.setInterval(async () => {
      try {
        const d = await getJSON<AuthStatus>(
          `/backends/${encodeURIComponent(backendId)}/auth-status`,
        );
        if (d.status === "connected") {
          stop();
          await onConnected();
        } else if (d.status.startsWith("failed")) {
          stop();
          setStatus("error");
          const reason = d.status.replace("failed:", "");
          setMessage("failed: " + reason);
          showError(`auth failed for ${backendId}: ${reason}`);
        }
      } catch {
        // keep polling
      }
    }, 2000);
    timeoutRef.current = window.setTimeout(
      () => {
        stop();
        setStatus("timeout");
        setMessage("authentication timed out.");
      },
      5 * 60 * 1000,
    );
  };

  return (
    <div class="section">
      <div class="card form-card">
        <div class="form-card-title">authenticate with provider</div>
        <div class="oauth-flow">
          <button class="save-btn" onClick={start} disabled={status === "waiting"}>
            {status === "idle"
              ? "authenticate"
              : status === "waiting"
                ? "waiting…"
                : "retry"}
          </button>
          <span
            class={
              status === "error" || status === "timeout"
                ? "oauth-status error"
                : "oauth-status"
            }
          >
            {message}
          </span>
          <button
            class="cancel-btn"
            style="margin-left:auto"
            onClick={() => {
              stop();
              onCancel();
            }}
          >
            cancel
          </button>
        </div>
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

function BackendSettingsSection({ backend }: { backend: Backend }) {
  const [enabled, setEnabled] = useState(backend.enabled !== false);
  const [sandbox, setSandbox] = useState<SandboxConfig>(
    normalizeSandbox(backend.sandbox),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const sandboxApplies = backend.bridge_managed || !backend.url;

  useEffect(() => {
    setEnabled(backend.enabled !== false);
    setSandbox(normalizeSandbox(backend.sandbox));
  }, [backend.id, backend.enabled, JSON.stringify(backend.sandbox || {})]);

  const save = async (nextEnabled = enabled, nextSandbox = sandbox) => {
    setBusy(true);
    setError(null);
    const body: BackendUpdateBody = { enabled: nextEnabled };
    if (sandboxApplies) body.sandbox = nextSandbox;
    const controller = new AbortController();
    const timeout = window.setTimeout(
      () => controller.abort(),
      BACKEND_REBUILD_TIMEOUT_MS,
    );
    try {
      await patchJSON(`/backends/${encodeURIComponent(backend.id)}`, body, {
        signal: controller.signal,
      });
      await backends.refresh();
    } catch (e) {
      const message =
        e instanceof DOMException && e.name === "AbortError"
          ? "backend rebuild timed out after 60 seconds"
          : e instanceof Error
            ? e.message
            : String(e);
      showError(message);
      setError(message);
    } finally {
      window.clearTimeout(timeout);
      setBusy(false);
    }
  };

  const toggleEnabled = async () => {
    const next = !enabled;
    setEnabled(next);
    await save(next, sandbox);
  };

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">runtime</span>
        <div class="runtime-actions">
          {busy && sandboxApplies && (
            <span class="runtime-progress">
              <span class="inline-spinner" aria-hidden="true" />
              rebuilding container
            </span>
          )}
          <button class="save-btn" onClick={() => save()} disabled={busy}>
            {busy ? "saving…" : "save"}
          </button>
        </div>
      </div>
      <div class="card runtime-card">
        <label class="config-toggle runtime-toggle">
          <input
            type="checkbox"
            checked={enabled}
            disabled={busy}
            onChange={toggleEnabled}
          />
          <span class="config-toggle-label">enabled</span>
          <span class="hint-text">
            disabling stops the backend and removes its tools without deleting
            config or tokens
          </span>
        </label>

        {sandboxApplies ? (
          <SandboxControls sandbox={sandbox} onChange={setSandbox} />
        ) : (
          <div class="empty-state runtime-empty">
            sandbox settings apply to stdio Docker backends.
          </div>
        )}
        {error && <div class="error-text">{error}</div>}
      </div>
    </div>
  );
}

function normalizeSandbox(input?: SandboxConfig): SandboxConfig {
  const profile = input?.profile || "default";
  if (profile === "compat") {
    return {
      profile,
      network_profile: input?.network_profile || "standard",
      run_as_root: input?.run_as_root ?? true,
      uid: input?.uid ?? 0,
      gid: input?.gid ?? 0,
      readonly_rootfs: input?.readonly_rootfs ?? false,
      memory: input?.memory || "",
      cpus: input?.cpus ?? 0,
      pids_limit: input?.pids_limit ?? 0,
      mounts: input?.mounts || [],
    };
  }
  return {
    profile,
    network_profile: input?.network_profile || "standard",
    run_as_root: input?.run_as_root ?? false,
    uid: input?.uid ?? 65532,
    gid: input?.gid ?? 65532,
    readonly_rootfs: input?.readonly_rootfs ?? true,
    memory: input?.memory || "512m",
    cpus: input?.cpus ?? 1,
    pids_limit: input?.pids_limit ?? 128,
    mounts: input?.mounts || [],
  };
}

function SandboxControls({
  sandbox,
  onChange,
}: {
  sandbox: SandboxConfig;
  onChange: (next: SandboxConfig) => void;
}) {
  const set = (patch: Partial<SandboxConfig>) =>
    onChange({ ...sandbox, ...patch });

  const setProfile = (profile: "default" | "compat") => {
    if (profile === "compat") {
      onChange({
        ...sandbox,
        profile,
        run_as_root: true,
        readonly_rootfs: false,
        memory: "",
        cpus: 0,
        pids_limit: 0,
      });
      return;
    }
    onChange(normalizeSandbox({ ...sandbox, profile }));
  };

  const mounts = sandbox.mounts || [];
  const updateMount = (index: number, patch: Partial<SandboxMount>) => {
    const next = mounts.map((m, i) => (i === index ? { ...m, ...patch } : m));
    set({ mounts: next });
  };
  const removeMount = (index: number) => {
    set({ mounts: mounts.filter((_, i) => i !== index) });
  };

  return (
    <div class="sandbox-controls">
      <div class="sandbox-grid">
        <label>
          <span>profile</span>
          <select
            value={sandbox.profile || "default"}
            onChange={(e) =>
              setProfile((e.target as HTMLSelectElement).value as "default" | "compat")
            }
          >
            <option value="default">recommended</option>
            <option value="compat">compatibility</option>
          </select>
        </label>
        <label>
          <span>network</span>
          <select value={sandbox.network_profile || "standard"} disabled>
            <option value="standard">standard</option>
          </select>
        </label>
        <label>
          <span>memory</span>
          <input
            type="text"
            value={sandbox.memory || ""}
            placeholder="512m"
            onInput={(e) => set({ memory: (e.target as HTMLInputElement).value })}
          />
        </label>
        <label>
          <span>cpus</span>
          <input
            type="number"
            min="0"
            step="0.25"
            value={String(sandbox.cpus || 0)}
            onInput={(e) =>
              set({ cpus: Number((e.target as HTMLInputElement).value) })
            }
          />
        </label>
        <label>
          <span>pids</span>
          <input
            type="number"
            min="0"
            step="1"
            value={String(sandbox.pids_limit || 0)}
            onInput={(e) =>
              set({ pids_limit: Number((e.target as HTMLInputElement).value) })
            }
          />
        </label>
        <label>
          <span>uid</span>
          <input
            type="number"
            min="0"
            value={String(sandbox.uid || 0)}
            disabled={sandbox.run_as_root}
            onInput={(e) => set({ uid: Number((e.target as HTMLInputElement).value) })}
          />
        </label>
        <label>
          <span>gid</span>
          <input
            type="number"
            min="0"
            value={String(sandbox.gid || 0)}
            disabled={sandbox.run_as_root}
            onInput={(e) => set({ gid: Number((e.target as HTMLInputElement).value) })}
          />
        </label>
      </div>

      <div class="sandbox-toggles">
        <label class="config-toggle">
          <input
            type="checkbox"
            checked={!sandbox.run_as_root}
            onChange={(e) => {
              const checked = (e.target as HTMLInputElement).checked;
              set({
                run_as_root: !checked,
                uid: checked && !sandbox.uid ? 65532 : sandbox.uid,
                gid: checked && !sandbox.gid ? 65532 : sandbox.gid,
              });
            }}
          />
          <span class="config-toggle-label">run as non-root</span>
        </label>
        <label class="config-toggle">
          <input
            type="checkbox"
            checked={sandbox.readonly_rootfs !== false}
            onChange={(e) =>
              set({ readonly_rootfs: (e.target as HTMLInputElement).checked })
            }
          />
          <span class="config-toggle-label">read-only root filesystem</span>
        </label>
      </div>

      <div class="mounts-block">
        <div class="mounts-header">
          <span>mounts</span>
          <button
            type="button"
            class="section-btn"
            onClick={() =>
              set({
                mounts: [
                  ...mounts,
                  { source: "", target: "/workspace", readonly: true },
                ],
              })
            }
          >
            add mount
          </button>
        </div>
        {mounts.length === 0 ? (
          <div class="empty-state runtime-empty">no host paths mounted.</div>
        ) : (
          <div class="mount-list">
            {mounts.map((m, index) => (
              <div class="mount-row" key={index}>
                <input
                  type="text"
                  placeholder="/host/path"
                  value={m.source}
                  onInput={(e) =>
                    updateMount(index, {
                      source: (e.target as HTMLInputElement).value,
                    })
                  }
                />
                <input
                  type="text"
                  placeholder="/container/path"
                  value={m.target}
                  onInput={(e) =>
                    updateMount(index, {
                      target: (e.target as HTMLInputElement).value,
                    })
                  }
                />
                <label class="mount-readonly">
                  <input
                    type="checkbox"
                    checked={m.readonly !== false}
                    onChange={(e) =>
                      updateMount(index, {
                        readonly: (e.target as HTMLInputElement).checked,
                      })
                    }
                  />
                  ro
                </label>
                <button
                  type="button"
                  class="cancel-btn"
                  onClick={() => removeMount(index)}
                >
                  remove
                </button>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function WorkspaceChangesSection({ backend }: { backend: Backend }) {
  const [changes, setChanges] = useState<WorkspaceChangeSet | null>(null);
  const [busy, setBusy] = useState(false);
  const files = changes?.files || [];

  const load = async (refresh = false) => {
    setBusy(true);
    try {
      const path = `/backends/${encodeURIComponent(backend.id)}/workspace-changes`;
      const next = refresh
        ? await postJSON<WorkspaceChangeSet>(`${path}/refresh`, {})
        : await getJSON<WorkspaceChangeSet>(path);
      setChanges(next);
    } catch (e) {
      showError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    load(false);
  }, [backend.id]);

  const apply = async () => {
    setBusy(true);
    try {
      const result = await postJSON<WorkspaceApplyResult>(
        `/backends/${encodeURIComponent(backend.id)}/workspace-changes/apply`,
        {},
      );
      if (result.conflicts?.length) {
        showError(`workspace apply had conflicts: ${result.conflicts.join(", ")}`);
      }
      await load(true);
    } catch (e) {
      showError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const discard = async () => {
    setBusy(true);
    try {
      await postJSON(
        `/backends/${encodeURIComponent(backend.id)}/workspace-changes/discard`,
        {},
      );
      await load(false);
    } catch (e) {
      showError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">workspace changes</span>
        <div class="section-actions">
          {busy && (
            <span class="runtime-progress">
              <span class="inline-spinner" aria-hidden="true" />
              syncing
            </span>
          )}
          <button class="section-btn" disabled={busy} onClick={() => load(true)}>
            refresh
          </button>
          {canMutate() && files.length > 0 && (
            <>
              <button class="save-btn" disabled={busy} onClick={apply}>
                apply
              </button>
              <button class="cancel-btn" disabled={busy} onClick={discard}>
                discard
              </button>
            </>
          )}
        </div>
      </div>
      <div class="card runtime-card">
        <div class="hint-text">
          {backend.workspace?.id
            ? `workspace ${backend.workspace.id} · ${backend.workspace.write_mode || "stage"}`
            : "no workspace attached"}
        </div>
        {files.length === 0 ? (
          <div class="empty-state runtime-empty">no staged workspace changes.</div>
        ) : (
          <div class="workspace-change-list">
            {files.map((file) => (
              <div class="workspace-change-row" key={file.path}>
                <div class="workspace-change-head">
                  <span class={`workspace-change-kind workspace-change-${file.type}`}>
                    {file.type}
                  </span>
                  <span class="meta-value-mono">{file.path}</span>
                  {file.binary && <span class="hint-text">binary</span>}
                </div>
                {file.preview && (
                  <pre class="workspace-change-preview">{file.preview}</pre>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function ToolsSection({ backend }: { backend: Backend }) {
  const [query, setQuery] = useState("");
  const tools = backend.tools || [];
  const ev = events.data.value || [];

  // Per-tool call counts from the recent events buffer.
  const counts = useMemo(() => {
    const m = new Map<string, number>();
    for (const e of ev) {
      const namespaced = `${e.namespace}__${e.tool}`;
      m.set(namespaced, (m.get(namespaced) ?? 0) + 1);
    }
    return m;
  }, [ev]);

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
              callCount={counts.get(t.name) ?? 0}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function ToolRow({
  tool,
  callCount,
}: {
  tool: BackendTool;
  callCount: number;
}) {
  return (
    <div class="tool-row">
      <div class="tool-row-header">
        <div class="tool-name">{tool.name}</div>
        {callCount > 0 && (
          <span class="tool-count-value">
            {callCount} call{callCount === 1 ? "" : "s"}
          </span>
        )}
      </div>
      {tool.description ? (
        <div class="tool-desc">{tool.description}</div>
      ) : null}
    </div>
  );
}

function CredentialSection({ backend }: { backend: Backend }) {
  const [editing, setEditing] = useState(false);
  const [cred, setCred] = useState<CredFormState>(credFromBackend(backend));
  const [error, setError] = useState<string | null>(null);

  const save = async () => {
    setError(null);
    const result = await withToast(async () => {
      const body: AddBackendBody = { url: backend.url || "" };
      const credInput = buildCredInput(cred);
      body.credential = credInput ?? null;
      await postJSON(`/backends/${encodeURIComponent(backend.id)}`, body);
      await backends.refresh();
    });
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
        {!editing && canMutate() && (
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
    await withToast(async () => {
      await deleteJSON(`/backends/${encodeURIComponent(backendId)}`);
      await onRemoved();
    });
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
