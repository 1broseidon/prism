import { useEffect, useRef, useState, useMemo } from "preact/hooks";
import { useLocation } from "preact-iso";
import { backends, events } from "../state";
import { getJSON, postJSON } from "../api/client";
import { showError } from "../state/toasts";
import { canMutate } from "../state/me";
import type {
  AddBackendBody,
  AddBackendResponse,
  AuthStatus,
  Backend,
  CredentialInput,
  Workspace,
  WorkspaceConfig,
} from "../api/types";
import { fmtAge } from "../util/time";

type CredType = "none" | "static" | "env" | "command";

interface PendingBackend {
  id: string;
  command: string;
  startedAt: number;
  stale: boolean;
}

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

function buildCredInput(s: CredFormState): CredentialInput | undefined {
  if (s.type === "none") return undefined;
  const c: CredentialInput = { type: s.type, header: s.header };
  if (s.type === "static") c.value = s.value;
  if (s.type === "env") c.env = s.env;
  if (s.type === "command") c.command = s.command;
  return c;
}

export function Servers() {
  const list = (backends.data.value || []).slice().sort((a, b) =>
    a.id.localeCompare(b.id),
  );
  const [addingOpen, setAddingOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [pendingAdds, setPendingAdds] = useState<PendingBackend[]>([]);
  const [now, setNow] = useState(Date.now());
  const loc = useLocation();

  const ev = events.data.value || [];
  const lastCallByNs = useMemo(() => {
    const m = new Map<string, string>();
    for (const e of ev) {
      if (!m.has(e.namespace)) m.set(e.namespace, e.ts);
    }
    return m;
  }, [ev]);

  const totalTools = list.reduce(
    (acc, b) => acc + (b.tools?.length ?? 0),
    0,
  );

  const filtered = useMemo(() => {
    const q = query.toLowerCase().trim();
    if (!q) return list;
    return list.filter(
      (b) =>
        b.id.toLowerCase().includes(q) ||
        (b.namespace || "").toLowerCase().includes(q) ||
        (b.url || "").toLowerCase().includes(q) ||
        (b.tools || []).some((t) => t.name.toLowerCase().includes(q)),
    );
  }, [list, query]);

  const backendIDsKey = useMemo(() => list.map((b) => b.id).join("\0"), [list]);

  const visiblePendingAdds = useMemo(() => {
    const connected = new Set(list.map((b) => b.id));
    const q = query.toLowerCase().trim();
    return pendingAdds.filter((p) => {
      if (connected.has(p.id)) return false;
      if (!q) return true;
      return (
        p.id.toLowerCase().includes(q) ||
        p.command.toLowerCase().includes(q)
      );
    });
  }, [list, pendingAdds, query]);

  useEffect(() => {
    if (pendingAdds.length === 0) return;
    const connected = new Set(list.map((b) => b.id));
    setPendingAdds((items) => {
      if (!items.some((p) => connected.has(p.id))) return items;
      return items.filter((p) => !connected.has(p.id));
    });
  }, [backendIDsKey, pendingAdds.length]);

  useEffect(() => {
    if (pendingAdds.length === 0) return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [pendingAdds.length]);

  const addPendingBackend = (id: string, command: string) => {
    setPendingAdds((items) => [
      ...items.filter((p) => p.id !== id),
      { id, command, startedAt: Date.now(), stale: false },
    ]);
    window.setTimeout(() => {
      setPendingAdds((items) =>
        items.map((p) => (p.id === id ? { ...p, stale: true } : p)),
      );
    }, 45000);
    window.setTimeout(() => {
      setPendingAdds((items) => items.filter((p) => p.id !== id));
    }, 2 * 60 * 1000);
  };

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">mcp servers</div>
          <div class="page-subtitle">
            {list.length} backend{list.length === 1 ? "" : "s"} ·{" "}
            {totalTools} tool{totalTools === 1 ? "" : "s"}
          </div>
        </div>
        <div class="page-header-actions">
          <input
            type="search"
            class="search-input"
            placeholder="search backends or tools…"
            value={query}
            onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
          />
          {canMutate() && (
            <button
              class="section-btn section-btn-primary"
              onClick={() => setAddingOpen((v) => !v)}
            >
              + connect
            </button>
          )}
        </div>
      </div>

      {addingOpen && (
        <AddBackend
          onConnecting={addPendingBackend}
          onDone={() => {
            setAddingOpen(false);
            backends.refresh();
          }}
          onCancel={() => setAddingOpen(false)}
        />
      )}

      {list.length === 0 && pendingAdds.length === 0 && !addingOpen ? (
        <EmptyServers onConnect={() => setAddingOpen(true)} />
      ) : filtered.length === 0 && visiblePendingAdds.length === 0 ? (
        <div class="empty-state">no backends match “{query}”.</div>
      ) : (
        <div class="server-list">
          {visiblePendingAdds.map((p) => (
            <PendingServerRow key={p.id} pending={p} now={now} />
          ))}
          {filtered.map((b) => (
            <ServerRow
              key={b.id}
              backend={b}
              lastCall={lastCallByNs.get(b.namespace || b.id)}
              onClick={() => loc.route(`/servers/${encodeURIComponent(b.id)}`)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function PendingServerRow({
  pending,
  now,
}: {
  pending: PendingBackend;
  now: number;
}) {
  const seconds = Math.max(
    1,
    Math.floor((now - pending.startedAt) / 1000),
  );
  return (
    <div class="server-row server-row-pending" aria-live="polite">
      <div class="server-row-main">
        <div class="server-row-header">
          <span class="status-pip status-pip-pending" />
          <span class="server-row-name">{pending.id}</span>
          <span class="server-row-transport">stdio</span>
          <span class="server-row-pending-label">
            {pending.stale ? "taking longer" : "connecting"}
          </span>
        </div>
        <div class="server-row-meta">
          <span class="server-row-url">{pending.command}</span>
        </div>
      </div>
      <div class="server-row-stats">
        <div class="server-row-stat">
          <div class="server-row-stat-value">{seconds}s</div>
          <div class="server-row-stat-label">elapsed</div>
        </div>
        <div class="server-row-stat server-row-skeleton-stat">
          <span class="server-row-skeleton" />
          <span class="server-row-skeleton server-row-skeleton-short" />
        </div>
      </div>
    </div>
  );
}

function EmptyServers({ onConnect }: { onConnect: () => void }) {
  return (
    <div class="empty-callout">
      <div class="empty-callout-title">no backends connected yet</div>
      <div class="empty-callout-body">
        backends are the MCP servers behind the gateway. connect a stdio
        process (e.g. <code>npx @modelcontextprotocol/server-github</code>)
        or an http endpoint to start routing tool calls through prism.
      </div>
      <button class="save-btn" onClick={onConnect}>
        connect a backend
      </button>
    </div>
  );
}

function ServerRow({
  backend,
  lastCall,
  onClick,
}: {
  backend: Backend;
  lastCall: string | undefined;
  onClick: () => void;
}) {
  const transport = backend.url ? "http" : "stdio";
  const toolCount = backend.tools?.length ?? 0;
  const statusKind: "ok" | "warn" | "error" | "neutral" = (() => {
    const cb = backend.circuit_breaker;
    if (backend.enabled === false) return "neutral";
    if (backend.disconnected) return "error";
    if (cb === "open") return "error";
    if (cb === "half_open" || cb === "half-open") return "warn";
    if (toolCount > 0) return "ok";
    return "neutral";
  })();

  return (
    <button class="server-row" onClick={onClick}>
      <div class="server-row-main">
        <div class="server-row-header">
          <span class={`status-pip status-pip-${statusKind}`} />
          <span class="server-row-name">{backend.id}</span>
          {backend.namespace && backend.namespace !== backend.id && (
            <span class="server-row-ns">/ {backend.namespace}</span>
          )}
          <span class="server-row-transport">{transport}</span>
        </div>
        <div class="server-row-meta">
          <span class="server-row-url">
            {backend.enabled === false
              ? "disabled · "
              : backend.disconnected
                ? "disconnected · "
                : ""}
            {backend.url || "stdio process"}
          </span>
        </div>
      </div>
      <div class="server-row-stats">
        <div class="server-row-stat">
          <div class="server-row-stat-value">{toolCount}</div>
          <div class="server-row-stat-label">tool{toolCount === 1 ? "" : "s"}</div>
        </div>
        <div class="server-row-stat">
          <div class="server-row-stat-value">
            {lastCall ? fmtAge(lastCall) : "—"}
          </div>
          <div class="server-row-stat-label">last call</div>
        </div>
        <span class="server-row-chevron">›</span>
      </div>
    </button>
  );
}

function AddBackend({
  onConnecting,
  onDone,
  onCancel,
}: {
  onConnecting: (id: string, command: string) => void;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState("");
  const [cmd, setCmd] = useState("");
  const [cred, setCred] = useState<CredFormState>(emptyCred());
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [workspace, setWorkspace] = useState<WorkspaceConfig>({ write_mode: "stage" });
  const [error, setError] = useState<string | null>(null);
  const [oauth, setOauth] = useState<{
    backendId: string;
    authUrl: string;
  } | null>(null);
  const [manualOAuth, setManualOAuth] = useState<{
    backendId: string;
    authServer: string;
    callbackUrl: string;
  } | null>(null);

  const scheduleRefresh = () => {
    [1000, 2500, 5000, 9000, 15000].forEach((delay) => {
      window.setTimeout(() => backends.refresh(), delay);
    });
  };

  useEffect(() => {
    getJSON<Workspace[]>("/workspaces")
      .then(setWorkspaces)
      .catch(() => setWorkspaces([]));
  }, []);

  const waitForBackend = async (id: string): Promise<boolean> => {
    for (const delay of [800, 1600, 3000, 5000, 8000]) {
      await new Promise((resolve) => window.setTimeout(resolve, delay));
      try {
        const list = await getJSON<Backend[]>("/backends");
        if (list.some((b) => b.id === id)) {
          await backends.refresh();
          return true;
        }
      } catch {
        // Network may still be settling after Docker attached the sandbox.
      }
    }
    return false;
  };

  const submitWith = async (extra?: Partial<AddBackendBody>) => {
    setError(null);
    const id = name.trim();
    const target = cmd.trim();
    if (!id || !target) {
      setError("name and command/URL are required");
      return;
    }
    const body: AddBackendBody = target.startsWith("http")
      ? { url: target }
      : { command: target };
    if (!target.startsWith("http") && workspace.id) {
      body.workspace = {
        ...workspace,
        mode: "snapshot",
        max_bytes: workspace.max_bytes || 33554432,
      };
    }
    const credInput = buildCredInput(cred);
    if (credInput) body.credential = credInput;
    Object.assign(body, extra || {});
    try {
      const res = await postJSON<AddBackendResponse>(
        `/backends/${encodeURIComponent(id)}`,
        body,
      );
      if (res.status === "auth_required") {
        setOauth({ backendId: id, authUrl: res.auth_url });
        return;
      }
      if (res.status === "manual_oauth_required") {
        setManualOAuth({
          backendId: id,
          authServer: res.auth_server,
          callbackUrl: res.callback_url,
        });
        return;
      }
      if (res.status === "connecting") {
        onConnecting(id, target);
        scheduleRefresh();
        onDone();
        return;
      }
      onDone();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (
        !target.startsWith("http") &&
        /failed to fetch|networkerror|err_network_changed/i.test(msg)
      ) {
        onConnecting(id, target);
        setError("connection interrupted; checking backend status…");
        if (await waitForBackend(id)) {
          onDone();
          return;
        }
      }
      setError(msg);
    }
  };

  const submit = () => submitWith();

  if (oauth) {
    return (
      <OAuthFlow
        backendId={oauth.backendId}
        authUrl={oauth.authUrl}
        onConnected={onDone}
        onCancel={onCancel}
      />
    );
  }

  if (manualOAuth) {
    return (
      <ManualOAuthForm
        backendId={manualOAuth.backendId}
        authServer={manualOAuth.authServer}
        callbackUrl={manualOAuth.callbackUrl}
        onSubmit={(clientId, clientSecret) =>
          submitWith({
            oauth_client_id: clientId,
            oauth_client_secret: clientSecret,
          })
        }
        onCancel={() => setManualOAuth(null)}
      />
    );
  }

  return (
    <div class="card form-card">
      <div class="form-card-title">connect a backend</div>
      <div class="inline-form">
        <input
          type="text"
          placeholder="name"
          value={name}
          autoFocus
          spellcheck={false}
          style="width:160px"
          onInput={(e) => setName((e.target as HTMLInputElement).value)}
        />
        <input
          type="text"
          placeholder="command or http(s) URL"
          value={cmd}
          spellcheck={false}
          style="flex:1;min-width:260px"
          onInput={(e) => setCmd((e.target as HTMLInputElement).value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") submit();
            if (e.key === "Escape") onCancel();
          }}
        />
        <select
          value={cred.type}
          onChange={(e) =>
            setCred({
              ...cred,
              type: (e.target as HTMLSelectElement).value as CredType,
            })
          }
        >
          <option value="none">no credential</option>
          <option value="static">api key</option>
          <option value="env">env var</option>
          <option value="command">command</option>
        </select>
        <CredFields state={cred} onChange={setCred} />
      </div>
      {!cmd.trim().startsWith("http") && workspaces.length > 0 && (
        <div class="inline-form server-workspace-form">
          <select
            value={workspace.id || ""}
            onChange={(e) =>
              setWorkspace({
                ...workspace,
                id: (e.target as HTMLSelectElement).value || undefined,
              })
            }
          >
            <option value="">no workspace snapshot</option>
            {workspaces.map((ws) => (
              <option value={ws.id} key={ws.id}>
                workspace: {ws.id}
              </option>
            ))}
          </select>
          <select
            value={workspace.write_mode || "stage"}
            disabled={!workspace.id}
            onChange={(e) =>
              setWorkspace({
                ...workspace,
                write_mode: (e.target as HTMLSelectElement)
                  .value as WorkspaceConfig["write_mode"],
              })
            }
          >
            <option value="sandbox_only">sandbox only</option>
            <option value="stage">stage changes</option>
            <option value="auto_apply">auto-apply allowed paths</option>
          </select>
          <span class="hint-text">
            sandbox gets a copy at /workspace; local writes go through policy
          </span>
        </div>
      )}
      <div class="form-actions">
        <button class="save-btn" onClick={submit}>
          connect
        </button>
        <button class="cancel-btn" onClick={onCancel}>
          cancel
        </button>
        {error && <span class="error-text">{error}</span>}
      </div>
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
        style="width:120px"
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
          style="width:160px"
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
          style="flex:1;min-width:240px"
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

function OAuthFlow({
  backendId,
  authUrl,
  onConnected,
  onCancel,
}: {
  backendId: string;
  authUrl: string;
  onConnected: () => void;
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
          onConnected();
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
  );
}

function ManualOAuthForm({
  backendId,
  authServer,
  callbackUrl,
  onSubmit,
  onCancel,
}: {
  backendId: string;
  authServer: string;
  callbackUrl: string;
  onSubmit: (clientId: string, clientSecret: string) => void | Promise<void>;
  onCancel: () => void;
}) {
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [copied, setCopied] = useState(false);

  const submit = async () => {
    if (!clientId.trim()) return;
    setSubmitting(true);
    try {
      await onSubmit(clientId.trim(), clientSecret.trim());
    } finally {
      setSubmitting(false);
    }
  };

  const copyCallback = async () => {
    try {
      await navigator.clipboard.writeText(callbackUrl);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // ignore; user can still copy manually
    }
  };

  return (
    <div class="card form-card">
      <div class="form-card-title">manual app registration required</div>
      <div class="manual-oauth-body">
        <p class="manual-oauth-lead">
          <code>{authServer}</code> doesn't support dynamic client
          registration. Register prism as an OAuth app with the provider, then
          paste the credentials below.
        </p>
        <div class="manual-oauth-step">
          <div class="manual-oauth-label">backend</div>
          <div class="manual-oauth-value">
            <code>{backendId}</code>
          </div>
        </div>
        <div class="manual-oauth-step">
          <div class="manual-oauth-label">callback URL to register</div>
          <div class="manual-oauth-value manual-oauth-callback">
            <code>{callbackUrl}</code>
            <button
              type="button"
              class="section-btn"
              onClick={copyCallback}
            >
              {copied ? "copied" : "copy"}
            </button>
          </div>
        </div>
        <div class="inline-form" style="flex-wrap:wrap;gap:8px">
          <input
            type="text"
            placeholder="client id"
            value={clientId}
            autoFocus
            spellcheck={false}
            style="flex:1;min-width:220px"
            onInput={(e) => setClientId((e.target as HTMLInputElement).value)}
          />
          <input
            type="password"
            placeholder="client secret (optional for public clients)"
            value={clientSecret}
            spellcheck={false}
            autoComplete="new-password"
            style="flex:1;min-width:220px"
            onInput={(e) =>
              setClientSecret((e.target as HTMLInputElement).value)
            }
          />
        </div>
        <div class="inline-form" style="margin-top:4px">
          <button
            class="save-btn"
            onClick={submit}
            disabled={submitting || !clientId.trim()}
          >
            {submitting ? "connecting…" : "continue"}
          </button>
          <button class="cancel-btn" onClick={onCancel}>
            cancel
          </button>
        </div>
      </div>
    </div>
  );
}
