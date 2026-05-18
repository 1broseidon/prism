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
  BinaryFetchRequest,
  BinaryFetchResponse,
  BinaryUploadResponse,
  CredentialInput,
  OpenAPIPreviewResponse,
  OpenAPISaveBody,
  OpenAPISaveResponse,
  OpenAPIScaffoldRequest,
  OpenAPIScaffoldResponse,
  OpenAPISecurityScheme,
  OpenAPISpecSource,
  Workspace,
  WorkspaceConfig,
} from "../api/types";
import { fmtAge } from "../util/time";
import { OperationPicker } from "../components/OperationPicker";

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
    (acc, b) => acc + (b.tools?.filter((t) => !t.disabled).length ?? 0),
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
  // Bridge-managed backends are conceptually stdio even though the gateway
  // talks to them over HTTP internally — the user sees a command, not a URL.
  const transport =
    backend.bridge_managed || !backend.url ? "stdio" : "http";
  const toolCount = backend.tools?.filter((t) => !t.disabled).length ?? 0;
  const statusKind: "ok" | "warn" | "error" | "neutral" = (() => {
    const cb = backend.circuit_breaker;
    if (backend.enabled === false) return "neutral";
    if (backend.disconnected) return "error";
    if (cb === "open") return "error";
    if (cb === "half_open" || cb === "half-open") return "warn";
    if (toolCount > 0) return "ok";
    return "neutral";
  })();
  const stateLabel =
    backend.enabled === false
      ? "disabled"
      : backend.disconnected
        ? "disconnected"
        : null;
  const detail =
    transport === "http"
      ? backend.url
      : backend.runtime
        ? `stdio · ${backend.runtime}`
        : "stdio process";

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
            {stateLabel ? `${stateLabel} · ` : ""}
            {detail}
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

type AddMode = "choose" | "command" | "openapi" | "binary";

function AddBackend({
  onConnecting,
  onDone,
  onCancel,
}: {
  onConnecting: (id: string, command: string) => void;
  onDone: () => void;
  onCancel: () => void;
}) {
  // The connect flow lets the operator pick between the existing
  // command/URL path and the new OpenAPI-spec path. We default to the
  // "choose" tile picker so neither path is privileged; the existing keyboard
  // muscle memory still works because pressing Enter on the command field is
  // unchanged once they pick "command".
  const [mode, setMode] = useState<AddMode>("choose");
  const [name, setName] = useState("");
  const [cmd, setCmd] = useState("");
  const [cred, setCred] = useState<CredFormState>(emptyCred());
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [workspace, setWorkspace] = useState<WorkspaceConfig>({
    write_mode: "stage",
  });
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
  const proxiedWorkspaces = workspaces.filter(
    (ws) => (ws.type || "proxied") === "proxied",
  );

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
    if (!target.startsWith("http") && workspace.id && workspace.type) {
      const workspaceType = workspace.type;
      body.workspace = {
        ...workspace,
        type: workspaceType,
        mode: "snapshot",
        max_bytes: workspace.max_bytes || 33554432,
      };
      if (workspaceType !== "proxied") {
        body.workspace.write_mode = "sandbox_only";
      }
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

  if (mode === "choose") {
    return (
      <AddBackendChooser
        onPick={(next) => setMode(next)}
        onCancel={onCancel}
      />
    );
  }

  if (mode === "openapi") {
    return (
      <AddOpenAPI
        onConnecting={onConnecting}
        onDone={onDone}
        onCancel={onCancel}
        onBack={() => setMode("choose")}
      />
    );
  }

  if (mode === "binary") {
    return (
      <AddBinary
        onConnecting={onConnecting}
        onDone={onDone}
        onCancel={onCancel}
        onBack={() => setMode("choose")}
      />
    );
  }

  return (
    <div class="card form-card">
      <div class="form-card-title">
        <span>connect a backend</span>
        <button
          type="button"
          class="section-btn"
          style="margin-left:auto"
          onClick={() => setMode("choose")}
        >
          ← back
        </button>
      </div>
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
      {!cmd.trim().startsWith("http") && (
        <div class="inline-form server-workspace-form">
          <select
            value={workspace.type || ""}
            onChange={(e) => {
              const nextType = (e.target as HTMLSelectElement)
                .value as WorkspaceConfig["type"] | "";
              setWorkspace(
                nextType
                  ? {
                      type: nextType,
                      write_mode: workspace.write_mode || "stage",
                      id: undefined,
                    }
                  : { write_mode: workspace.write_mode || "stage" },
              );
            }}
          >
            <option value="">no workspace</option>
            <option value="proxied">local synced workspace</option>
            <option value="virtual">remote persistent workspace</option>
            <option value="ephemeral">temporary scratch workspace</option>
          </select>
          {workspace.type === "proxied" ? (
            <select
              value={workspace.id || ""}
              disabled={proxiedWorkspaces.length === 0}
              onChange={(e) =>
                setWorkspace({
                  ...workspace,
                  type: "proxied",
                  id: (e.target as HTMLSelectElement).value || undefined,
                })
              }
            >
              <option value="">
                {proxiedWorkspaces.length === 0
                  ? "no local bridges connected"
                  : "select workspace"}
              </option>
              {proxiedWorkspaces.map((ws) => (
                <option value={ws.id} key={ws.id}>
                  {ws.id}
                </option>
              ))}
            </select>
          ) : workspace.type ? (
            <input
              type="text"
              class="config-input"
              value={workspace.id || ""}
              placeholder="workspace id"
              spellcheck={false}
              onInput={(e) =>
                setWorkspace({
                  ...workspace,
                  id: (e.target as HTMLInputElement).value || undefined,
                })
              }
            />
          ) : null}
          <select
            value={workspace.write_mode || "stage"}
            disabled={!workspace.id || workspace.type !== "proxied"}
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
            {workspace.type === "proxied"
              ? "sandbox gets a copy at /workspace; local writes go through policy"
              : workspace.type
                ? "workspace lives on Prism server storage and does not sync locally"
                : "run without an attached workspace"}
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

function AddBackendChooser({
  onPick,
  onCancel,
}: {
  onPick: (mode: "command" | "openapi" | "binary") => void;
  onCancel: () => void;
}) {
  return (
    <div class="card form-card">
      <div class="form-card-title">connect a backend</div>
      <div class="connect-tiles">
        <button
          type="button"
          class="connect-tile"
          onClick={() => onPick("command")}
        >
          <div class="connect-tile-title">command or http url</div>
          <div class="connect-tile-desc">
            stdio process (e.g. <code>npx server-github</code>) or
            an existing MCP-over-HTTP endpoint.
          </div>
        </button>
        <button
          type="button"
          class="connect-tile"
          onClick={() => onPick("openapi")}
        >
          <div class="connect-tile-title">openapi spec</div>
          <div class="connect-tile-desc">
            paste a spec URL or upload a file. prism imports operations
            as MCP tools — pick which ones go live before saving.
          </div>
        </button>
        <button
          type="button"
          class="connect-tile"
          onClick={() => onPick("binary")}
        >
          <div class="connect-tile-title">prism-managed binary</div>
          <div class="connect-tile-desc">
            upload a Linux x86_64 binary or fetch one from a URL. prism
            owns the artifact and runs it inside the existing sandbox —
            no host install required.
          </div>
        </button>
      </div>
      <div class="form-actions">
        <button class="cancel-btn" onClick={onCancel}>
          cancel
        </button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// OpenAPI add flow
// ---------------------------------------------------------------------------

type OpenAPICredType = "none" | "bearer" | "apiKey";

interface OpenAPICredState {
  type: OpenAPICredType;
  header: string;
  value: string;
}

async function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () =>
      reject(reader.error ?? new Error("file read failed"));
    reader.onload = () => {
      const result = reader.result;
      if (typeof result !== "string") {
        reject(new Error("unexpected file reader result"));
        return;
      }
      const comma = result.indexOf(",");
      resolve(comma >= 0 ? result.slice(comma + 1) : result);
    };
    reader.readAsDataURL(file);
  });
}

type OpenAPISourceMode = "url" | "file" | "inline";

function AddOpenAPI({
  onConnecting,
  onDone,
  onCancel,
  onBack,
}: {
  onConnecting: (id: string, command: string) => void;
  onDone: () => void;
  onCancel: () => void;
  onBack: () => void;
}) {
  const [id, setId] = useState("");
  const [sourceMode, setSourceMode] = useState<OpenAPISourceMode>("url");
  const [url, setUrl] = useState("");
  const [fileBase64, setFileBase64] = useState<string | null>(null);
  const [fileName, setFileName] = useState("");
  const [inlineText, setInlineText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [preview, setPreview] = useState<OpenAPIPreviewResponse | null>(
    null,
  );
  const [previewSource, setPreviewSource] = useState<OpenAPISpecSource | null>(
    null,
  );
  const [baseURLOverride, setBaseURLOverride] = useState("");
  const [securityScheme, setSecurityScheme] = useState<string>("");
  const [cred, setCred] = useState<OpenAPICredState>({
    type: "none",
    header: "Authorization",
    value: "",
  });
  // Curl scaffold sub-flow state — only visible when sourceMode === "inline".
  const [showCurlScaffold, setShowCurlScaffold] = useState(false);
  const [curlInput, setCurlInput] = useState("");
  const [scaffoldBusy, setScaffoldBusy] = useState(false);
  const [scaffoldWarnings, setScaffoldWarnings] = useState<string[]>([]);
  // enabled is the curated set of operation names. Defaults to "all enabled"
  // every time a fresh preview lands — the operator opts OUT of operations
  // they don't want.
  const [enabled, setEnabled] = useState<Set<string>>(new Set());

  const onFile = async (e: Event) => {
    const input = e.target as HTMLInputElement;
    const f = input.files && input.files[0];
    if (!f) return;
    setError(null);
    try {
      const data = await readFileAsBase64(f);
      setFileBase64(data);
      setFileName(f.name);
      setUrl("");
      setInlineText("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const buildSource = (): OpenAPISpecSource | null => {
    if (sourceMode === "url") {
      const trimmed = url.trim();
      return trimmed ? { url: trimmed } : null;
    }
    if (sourceMode === "file") {
      return fileBase64 ? { file: fileBase64 } : null;
    }
    // inline mode — accept the spec as-is (no trimming so leading/trailing
    // bytes are preserved verbatim).
    return inlineText ? { inline: inlineText } : null;
  };

  const generateFromCurl = async () => {
    if (!curlInput.trim()) {
      setError("paste a curl command first");
      return;
    }
    setError(null);
    setScaffoldBusy(true);
    setScaffoldWarnings([]);
    try {
      const req: OpenAPIScaffoldRequest = { curl: curlInput };
      const resp = await postJSON<OpenAPIScaffoldResponse>(
        "/openapi/scaffold-from-curl",
        req,
      );
      setInlineText(resp.spec);
      setScaffoldWarnings(resp.warnings || []);
      // Collapse the curl panel once the spec lands; warnings stay visible
      // alongside the inline editor so the operator can review and edit.
      setShowCurlScaffold(false);
      setCurlInput("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setScaffoldBusy(false);
    }
  };

  const runPreview = async () => {
    setError(null);
    const source = buildSource();
    if (!source) {
      setError("provide a spec URL or upload a file");
      return;
    }
    setBusy(true);
    try {
      const resp = await postJSON<OpenAPIPreviewResponse>(
        "/backends/preview-openapi",
        { source },
      );
      setPreview(resp);
      setPreviewSource(source);
      setBaseURLOverride("");
      // Auto-select the only scheme if there's exactly one; otherwise force
      // the operator to pick before save.
      setSecurityScheme(
        resp.security_schemes.length === 1
          ? resp.security_schemes[0].name
          : "",
      );
      setEnabled(new Set(resp.operations.map((op) => op.name)));
      setCred({ type: "none", header: "Authorization", value: "" });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    if (!preview || !previewSource) {
      setError("run a preview first");
      return;
    }
    const trimmedID = id.trim();
    if (!trimmedID) {
      setError("backend id is required");
      return;
    }
    if (preview.security_schemes.length > 0 && !securityScheme) {
      setError("pick a security scheme");
      return;
    }
    setBusy(true);
    setError(null);
    const disabled = preview.operations
      .map((op) => op.name)
      .filter((name) => !enabled.has(name));
    const body: OpenAPISaveBody = {
      type: "openapi",
      source: previewSource,
      // empty string for base_url_override matches "leave it alone"; the
      // gateway only applies it when non-empty.
      base_url_override: baseURLOverride.trim() || undefined,
      security_scheme: securityScheme || undefined,
      credential: buildOpenAPICredential(cred, preview, securityScheme),
      // Always send disabled_tools explicitly. Empty array means "enable
      // every operation" per the API contract; omitting it would mean "leave
      // the current curation alone", which has no meaning on a fresh create.
      disabled_tools: disabled,
    };
    try {
      const res = await postJSON<OpenAPISaveResponse>(
        `/backends/${encodeURIComponent(trimmedID)}`,
        body,
      );
      const sourceLabel =
        sourceMode === "url"
          ? url.trim()
          : sourceMode === "file"
            ? `openapi: ${fileName}`
            : `openapi: inline spec`;
      onConnecting(res.id, sourceLabel);
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  if (!preview) {
    return (
      <div class="card form-card">
        <div class="form-card-title">
          <span>connect an openapi server</span>
          <button
            type="button"
            class="section-btn"
            style="margin-left:auto"
            onClick={onBack}
          >
            ← back
          </button>
        </div>
        <SourceModeTabs
          mode={sourceMode}
          onChange={(next) => {
            setSourceMode(next);
            setError(null);
          }}
        />
        {sourceMode === "url" && (
          <div class="modal-section">
            <label class="config-label">spec url</label>
            <input
              type="text"
              class="config-input"
              value={url}
              spellcheck={false}
              placeholder="https://petstore3.swagger.io/api/v3/openapi.json"
              onInput={(e) =>
                setUrl((e.target as HTMLInputElement).value)
              }
            />
          </div>
        )}
        {sourceMode === "file" && (
          <div class="modal-section">
            <label class="config-label">upload file</label>
            <div class="inline-form">
              <input
                type="file"
                accept=".json,.yaml,.yml,application/json,application/x-yaml,text/yaml,text/x-yaml"
                onChange={onFile}
              />
              {fileName && (
                <span class="hint-text">selected: {fileName}</span>
              )}
            </div>
          </div>
        )}
        {sourceMode === "inline" && (
          <div class="modal-section openapi-inline-editor">
            <div class="openapi-inline-head">
              <label class="config-label">paste yaml or json spec</label>
              {!showCurlScaffold && (
                <button
                  type="button"
                  class="section-btn"
                  onClick={() => {
                    setShowCurlScaffold(true);
                    setError(null);
                  }}
                >
                  generate from curl
                </button>
              )}
            </div>
            {showCurlScaffold && (
              <CurlScaffoldPanel
                value={curlInput}
                onChange={setCurlInput}
                busy={scaffoldBusy}
                onGenerate={generateFromCurl}
                onCancel={() => {
                  setShowCurlScaffold(false);
                  setCurlInput("");
                }}
              />
            )}
            {scaffoldWarnings.length > 0 && (
              <div class="openapi-scaffold-warnings">
                <div class="openapi-scaffold-warnings-title">
                  scaffold warnings
                </div>
                <ul>
                  {scaffoldWarnings.map((w, i) => (
                    <li key={i}>{w}</li>
                  ))}
                </ul>
              </div>
            )}
            <textarea
              class="openapi-inline-textarea"
              value={inlineText}
              spellcheck={false}
              autoComplete="off"
              autoCorrect="off"
              autoCapitalize="off"
              placeholder={
                "openapi: 3.1.0\ninfo:\n  title: My API\n  version: \"1.0\"\n…"
              }
              onInput={(e) =>
                setInlineText((e.target as HTMLTextAreaElement).value)
              }
            />
          </div>
        )}
        <div class="form-actions">
          <button class="save-btn" onClick={runPreview} disabled={busy}>
            {busy ? "fetching…" : "preview"}
          </button>
          <button class="cancel-btn" onClick={onCancel} disabled={busy}>
            cancel
          </button>
          {error && <span class="error-text">{error}</span>}
        </div>
      </div>
    );
  }

  const scheme =
    preview.security_schemes.find((s) => s.name === securityScheme) ||
    null;

  return (
    <div class="card form-card openapi-preview-card">
      <div class="form-card-title">
        <span>preview · {preview.title}</span>
        <button
          type="button"
          class="section-btn"
          style="margin-left:auto"
          onClick={() => {
            setPreview(null);
            setPreviewSource(null);
          }}
        >
          ← change source
        </button>
      </div>

      <div class="openapi-preview-meta">
        <MetaPair label="title" value={preview.title} />
        <MetaPair label="version" value={preview.version} />
        <MetaPair label="base url" value={preview.base_url || "—"} mono />
      </div>

      <div class="modal-section">
        <label class="config-label">backend id</label>
        <input
          type="text"
          class="config-input"
          value={id}
          spellcheck={false}
          placeholder="e.g. petstore"
          onInput={(e) => setId((e.target as HTMLInputElement).value)}
        />
      </div>

      <div class="modal-section">
        <label class="config-label">
          base url override <span class="hint-text">(optional)</span>
        </label>
        <input
          type="text"
          class="config-input"
          value={baseURLOverride}
          spellcheck={false}
          placeholder={preview.base_url || "https://api.example.com"}
          onInput={(e) =>
            setBaseURLOverride((e.target as HTMLInputElement).value)
          }
        />
      </div>

      {preview.security_schemes.length > 0 && (
        <div class="modal-section">
          <label class="config-label">security scheme</label>
          {preview.security_schemes.length === 1 ? (
            <div class="hint-text">
              auto-selected:{" "}
              <code>{preview.security_schemes[0].name}</code> (
              {preview.security_schemes[0].type})
            </div>
          ) : (
            <div class="openapi-scheme-radios">
              {preview.security_schemes.map((s) => (
                <label class="openapi-scheme-radio" key={s.name}>
                  <input
                    type="radio"
                    name="openapi-scheme"
                    checked={securityScheme === s.name}
                    onChange={() => setSecurityScheme(s.name)}
                  />
                  <span class="openapi-scheme-name">{s.name}</span>
                  <span class="hint-text">({s.type})</span>
                </label>
              ))}
            </div>
          )}
        </div>
      )}

      {scheme && (
        <OpenAPICredFields
          scheme={scheme}
          cred={cred}
          onChange={setCred}
        />
      )}

      <div class="openapi-preview-picker">
        <OperationPicker
          operations={preview.operations}
          enabled={enabled}
          onChange={setEnabled}
          skipped={preview.skipped}
          specWarnings={preview.spec_warnings}
          title="operations"
          description="every checked operation becomes an MCP tool. uncheck the ones you want hidden — bulk-toggle per tag for big specs."
        />
      </div>

      <div class="form-actions">
        <button class="save-btn" onClick={save} disabled={busy}>
          {busy ? "saving…" : "save backend"}
        </button>
        <button class="cancel-btn" onClick={onCancel} disabled={busy}>
          cancel
        </button>
        {error && <span class="error-text">{error}</span>}
      </div>
    </div>
  );
}

function MetaPair({
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
        {value || "—"}
      </div>
    </div>
  );
}

function OpenAPICredFields({
  scheme,
  cred,
  onChange,
}: {
  scheme: OpenAPISecurityScheme;
  cred: OpenAPICredState;
  onChange: (next: OpenAPICredState) => void;
}) {
  // bearer schemes: just a token; the gateway always uses Authorization. For
  // apiKey we surface both the header name (pre-filled from the spec) and the
  // value so the operator can override if their gateway has been reverse-
  // proxied behind something with a different header.
  const isBearer = scheme.type === "bearer";
  const isAPIKey = scheme.type === "apiKey";
  return (
    <div class="modal-section">
      <label class="config-label">credential</label>
      <div class="inline-form" style="flex-wrap:wrap;gap:8px">
        {isBearer && (
          <>
            <span class="hint-text">bearer token →</span>
            <input
              type="password"
              class="config-input"
              placeholder="token (write-only)"
              autoComplete="new-password"
              style="flex:1;min-width:240px"
              value={cred.value}
              onInput={(e) =>
                onChange({
                  ...cred,
                  type: "bearer",
                  header: "Authorization",
                  value: (e.target as HTMLInputElement).value,
                })
              }
            />
          </>
        )}
        {isAPIKey && (
          <>
            <input
              type="text"
              class="config-input"
              placeholder="header"
              style="width:160px"
              value={cred.header || scheme.header || ""}
              onInput={(e) =>
                onChange({
                  ...cred,
                  type: "apiKey",
                  header: (e.target as HTMLInputElement).value,
                })
              }
            />
            <input
              type="password"
              class="config-input"
              placeholder="value (write-only)"
              autoComplete="new-password"
              style="flex:1;min-width:240px"
              value={cred.value}
              onInput={(e) =>
                onChange({
                  ...cred,
                  type: "apiKey",
                  header: cred.header || scheme.header || "",
                  value: (e.target as HTMLInputElement).value,
                })
              }
            />
          </>
        )}
        {!isBearer && !isAPIKey && (
          <span class="hint-text">
            scheme <code>{scheme.type}</code> is not credential-eligible.
          </span>
        )}
      </div>
    </div>
  );
}

function SourceModeTabs({
  mode,
  onChange,
}: {
  mode: OpenAPISourceMode;
  onChange: (next: OpenAPISourceMode) => void;
}) {
  const tabs: { id: OpenAPISourceMode; label: string }[] = [
    { id: "url", label: "url" },
    { id: "file", label: "file" },
    { id: "inline", label: "inline" },
  ];
  return (
    <div class="openapi-source-tabs" role="tablist">
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          role="tab"
          aria-selected={mode === t.id}
          class={
            mode === t.id
              ? "openapi-source-tab openapi-source-tab-active"
              : "openapi-source-tab"
          }
          onClick={() => onChange(t.id)}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

function CurlScaffoldPanel({
  value,
  onChange,
  busy,
  onGenerate,
  onCancel,
}: {
  value: string;
  onChange: (v: string) => void;
  busy: boolean;
  onGenerate: () => void;
  onCancel: () => void;
}) {
  return (
    <div class="openapi-curl-panel">
      <div class="openapi-curl-head">
        <span class="hint-text">
          paste a curl command — we'll convert it to a starter spec.
        </span>
        <button
          type="button"
          class="section-btn"
          onClick={onCancel}
          disabled={busy}
        >
          dismiss
        </button>
      </div>
      <textarea
        class="openapi-curl-textarea"
        value={value}
        spellcheck={false}
        autoComplete="off"
        autoCorrect="off"
        autoCapitalize="off"
        placeholder={
          "curl -X POST \\\n  -H 'Authorization: Bearer xyz' \\\n  -d '{\"name\":\"alice\"}' \\\n  https://api.example.com/v1/users"
        }
        onInput={(e) => onChange((e.target as HTMLTextAreaElement).value)}
      />
      <div class="inline-form">
        <button
          type="button"
          class="save-btn"
          onClick={onGenerate}
          disabled={busy || !value.trim()}
        >
          {busy ? "generating…" : "generate"}
        </button>
      </div>
    </div>
  );
}

function buildOpenAPICredential(
  state: OpenAPICredState,
  preview: OpenAPIPreviewResponse,
  schemeName: string,
): CredentialInput | undefined {
  const scheme = preview.security_schemes.find((s) => s.name === schemeName);
  if (!scheme) return undefined;
  if (scheme.type === "bearer") {
    if (!state.value) return undefined;
    return { type: "static", header: "Authorization", value: state.value };
  }
  if (scheme.type === "apiKey") {
    if (!state.value) return undefined;
    return {
      type: "static",
      header: state.header || scheme.header || "X-API-Key",
      value: state.value,
    };
  }
  return undefined;
}

// ---------------------------------------------------------------------------
// Binary add flow
// ---------------------------------------------------------------------------

type BinarySourceMode = "upload" | "url";

// uploadBinary posts the file to /binaries/upload as multipart. We bypass
// postJSON because the helper sets Content-Type: application/json which would
// break multipart form encoding.
async function uploadBinary(
  file: File,
  archiveBinaryPath: string,
): Promise<BinaryUploadResponse> {
  const form = new FormData();
  form.append("file", file);
  if (archiveBinaryPath.trim()) {
    form.append("archive_binary_path", archiveBinaryPath.trim());
  }
  const res = await fetch("/api/v1/binaries/upload", {
    method: "POST",
    body: form,
  });
  const text = await res.text();
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const parsed = JSON.parse(text);
      if (parsed && typeof parsed === "object" && "error" in parsed) {
        msg = String((parsed as { error: unknown }).error);
      }
    } catch {
      if (text.trim()) msg = text.trim();
    }
    throw new Error(msg);
  }
  return JSON.parse(text) as BinaryUploadResponse;
}

function AddBinary({
  onConnecting,
  onDone,
  onCancel,
  onBack,
}: {
  onConnecting: (id: string, command: string) => void;
  onDone: () => void;
  onCancel: () => void;
  onBack: () => void;
}) {
  const [sourceMode, setSourceMode] = useState<BinarySourceMode>("upload");
  const [id, setId] = useState("");
  const [command, setCommand] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [url, setUrl] = useState("");
  const [archivePath, setArchivePath] = useState("");
  const [staged, setStaged] = useState<BinaryUploadResponse | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setStaged(null);
    setError(null);
  };

  const stage = async () => {
    setError(null);
    setBusy(true);
    try {
      if (sourceMode === "upload") {
        if (!file) {
          setError("pick a binary or archive file");
          return;
        }
        const resp = await uploadBinary(file, archivePath);
        setStaged(resp);
      } else {
        const trimmed = url.trim();
        if (!trimmed) {
          setError("paste a download URL");
          return;
        }
        const req: BinaryFetchRequest = { url: trimmed };
        if (archivePath.trim()) {
          req.archive_binary_path = archivePath.trim();
        }
        const resp = await postJSON<BinaryFetchResponse>(
          "/binaries/fetch",
          req,
        );
        setStaged(resp);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    if (!staged) {
      setError("upload or fetch a binary first");
      return;
    }
    const trimmedID = id.trim();
    if (!trimmedID) {
      setError("backend id is required");
      return;
    }
    setError(null);
    setBusy(true);
    try {
      // The "MCP command" field is parsed shell-style on save; first token
      // is the display/name (informational only — the gateway always runs
      // /opt/prism/bin/<hash>/<name>) and the rest become argv.
      const tokens = splitCommandTokens(command);
      const args = tokens.length > 1 ? tokens.slice(1) : tokens.length === 1 ? tokens : [];
      const body: AddBackendBody = {
        binary_hash: staged.hash,
        binary_args: args,
        binary_name: staged.name,
        binary_source: staged.source,
      };
      if (staged.source_url) {
        body.binary_source_url = staged.source_url;
      }
      const res = await postJSON<AddBackendResponse>(
        `/backends/${encodeURIComponent(trimmedID)}`,
        body,
      );
      const displayCmd = `binary: ${staged.name} (${staged.hash.slice(0, 12)}…)`;
      if (res.status === "connecting") {
        onConnecting(trimmedID, displayCmd);
        onDone();
        return;
      }
      onConnecting(trimmedID, displayCmd);
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  if (!staged) {
    return (
      <div class="card form-card">
        <div class="form-card-title">
          <span>connect a prism-managed binary</span>
          <button
            type="button"
            class="section-btn"
            style="margin-left:auto"
            onClick={onBack}
          >
            ← back
          </button>
        </div>
        <BinarySourceTabs
          mode={sourceMode}
          onChange={(next) => {
            setSourceMode(next);
            setError(null);
            setFile(null);
            setUrl("");
          }}
        />
        <div class="modal-section">
          <span class="hint-text">
            Linux x86_64 binary required. Static builds recommended
            (CGO_ENABLED=0). Archives: .zip, .tar.gz.
          </span>
        </div>
        {sourceMode === "upload" && (
          <div class="modal-section">
            <label class="config-label">upload binary or archive</label>
            <div class="inline-form">
              <input
                type="file"
                accept=".zip,.tar,.tar.gz,.tgz,application/zip,application/gzip,application/x-tar,application/octet-stream"
                onChange={(e) => {
                  const target = e.target as HTMLInputElement;
                  setFile(target.files && target.files[0] ? target.files[0] : null);
                }}
              />
              {file && <span class="hint-text">selected: {file.name}</span>}
            </div>
          </div>
        )}
        {sourceMode === "url" && (
          <div class="modal-section">
            <label class="config-label">download URL</label>
            <input
              type="text"
              class="config-input"
              value={url}
              spellcheck={false}
              placeholder="https://github.com/owner/repo/releases/download/v1.0/cymbal-linux-amd64.tar.gz"
              onInput={(e) => setUrl((e.target as HTMLInputElement).value)}
            />
          </div>
        )}
        <div class="modal-section">
          <label class="config-label">
            archive binary path <span class="hint-text">(optional)</span>
          </label>
          <input
            type="text"
            class="config-input"
            value={archivePath}
            spellcheck={false}
            placeholder="bin/cymbal — required only for archives with multiple ELF binaries"
            onInput={(e) => setArchivePath((e.target as HTMLInputElement).value)}
          />
        </div>
        <div class="form-actions">
          <button class="save-btn" onClick={stage} disabled={busy}>
            {busy
              ? sourceMode === "upload"
                ? "uploading…"
                : "fetching…"
              : sourceMode === "upload"
                ? "upload"
                : "fetch"}
          </button>
          <button class="cancel-btn" onClick={onCancel} disabled={busy}>
            cancel
          </button>
          {error && <span class="error-text">{error}</span>}
        </div>
      </div>
    );
  }

  return (
    <div class="card form-card">
      <div class="form-card-title">
        <span>review binary · {staged.name}</span>
        <button
          type="button"
          class="section-btn"
          style="margin-left:auto"
          onClick={reset}
        >
          ← change source
        </button>
      </div>
      <div class="openapi-preview-meta">
        <MetaPair label="name" value={staged.name} mono />
        <MetaPair label="sha256" value={staged.hash.slice(0, 16) + "…"} mono />
        <MetaPair label="size" value={formatBytes(staged.size)} />
        <MetaPair
          label="source"
          value={staged.source === "url" ? "url" : "upload"}
        />
        {staged.detected_binary_path && (
          <MetaPair
            label="archive entry"
            value={staged.detected_binary_path}
            mono
          />
        )}
      </div>
      <div class="modal-section">
        <label class="config-label">backend id</label>
        <input
          type="text"
          class="config-input"
          value={id}
          spellcheck={false}
          placeholder="e.g. cymbal"
          onInput={(e) => setId((e.target as HTMLInputElement).value)}
        />
      </div>
      <div class="modal-section">
        <label class="config-label">
          MCP command <span class="hint-text">(optional)</span>
        </label>
        <input
          type="text"
          class="config-input"
          value={command}
          spellcheck={false}
          placeholder="recoil mcp"
          onInput={(e) => setCommand((e.target as HTMLInputElement).value)}
        />
        <span class="hint-text">
          Parsed shell-style. First token (when 2+ supplied) is treated as the
          informational binary name; the rest become argv. The actual
          executable is always{" "}
          <code>/opt/prism/bin/{staged.hash.slice(0, 8)}…/{staged.name}</code>.
        </span>
      </div>
      <div class="form-actions">
        <button class="save-btn" onClick={save} disabled={busy}>
          {busy ? "saving…" : "save backend"}
        </button>
        <button class="cancel-btn" onClick={onCancel} disabled={busy}>
          cancel
        </button>
        {error && <span class="error-text">{error}</span>}
      </div>
    </div>
  );
}

function BinarySourceTabs({
  mode,
  onChange,
}: {
  mode: BinarySourceMode;
  onChange: (next: BinarySourceMode) => void;
}) {
  const tabs: { id: BinarySourceMode; label: string }[] = [
    { id: "upload", label: "upload" },
    { id: "url", label: "url" },
  ];
  return (
    <div class="openapi-source-tabs" role="tablist">
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          role="tab"
          aria-selected={mode === t.id}
          class={
            mode === t.id
              ? "openapi-source-tab openapi-source-tab-active"
              : "openapi-source-tab"
          }
          onClick={() => onChange(t.id)}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

// splitCommandTokens is a small shell-style tokenizer mirroring the Go
// admin.ParseBinaryCommand helper. It handles whitespace splitting and basic
// single/double-quote literals; backslash escapes within double quotes are
// supported. The server re-tokenizes the same input as a defense-in-depth
// check, so the client-side split is just for UX (preview/display).
function splitCommandTokens(input: string): string[] {
  const trimmed = input.trim();
  if (!trimmed) return [];
  const out: string[] = [];
  let cur = "";
  let inSingle = false;
  let inDouble = false;
  let escape = false;
  let started = false;
  const flush = () => {
    if (started) {
      out.push(cur);
      cur = "";
      started = false;
    }
  };
  for (const ch of trimmed) {
    if (escape) {
      cur += ch;
      escape = false;
      started = true;
      continue;
    }
    if (inSingle) {
      if (ch === "'") {
        inSingle = false;
      } else {
        cur += ch;
      }
      continue;
    }
    if (inDouble) {
      if (ch === "\\") {
        escape = true;
      } else if (ch === '"') {
        inDouble = false;
      } else {
        cur += ch;
      }
      continue;
    }
    if (ch === "'") {
      inSingle = true;
      started = true;
    } else if (ch === '"') {
      inDouble = true;
      started = true;
    } else if (ch === "\\") {
      escape = true;
      started = true;
    } else if (ch === " " || ch === "\t") {
      flush();
    } else {
      cur += ch;
      started = true;
    }
  }
  flush();
  return out;
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}
