import { useEffect, useRef, useState, useMemo } from "preact/hooks";
import { useLocation } from "preact-iso";
import { backends, events } from "../state";
import { getJSON, postJSON } from "../api/client";
import { showError } from "../state/toasts";
import type {
  AddBackendBody,
  AddBackendResponse,
  AuthStatus,
  Backend,
  CredentialInput,
} from "../api/types";
import { fmtAge } from "../util/time";

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
          <button
            class="section-btn section-btn-primary"
            onClick={() => setAddingOpen((v) => !v)}
          >
            + connect
          </button>
        </div>
      </div>

      {addingOpen && (
        <AddBackend
          onDone={() => {
            setAddingOpen(false);
            backends.refresh();
          }}
          onCancel={() => setAddingOpen(false)}
        />
      )}

      {list.length === 0 && !addingOpen ? (
        <EmptyServers onConnect={() => setAddingOpen(true)} />
      ) : filtered.length === 0 ? (
        <div class="empty-state">no backends match “{query}”.</div>
      ) : (
        <div class="server-list">
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
          <span class="server-row-url">{backend.url || "stdio process"}</span>
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
  onDone,
  onCancel,
}: {
  onDone: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState("");
  const [cmd, setCmd] = useState("");
  const [cred, setCred] = useState<CredFormState>(emptyCred());
  const [error, setError] = useState<string | null>(null);
  const [oauth, setOauth] = useState<{
    backendId: string;
    authUrl: string;
  } | null>(null);

  const submit = async () => {
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
    const credInput = buildCredInput(cred);
    if (credInput) body.credential = credInput;
    try {
      const res = await postJSON<AddBackendResponse>(
        `/backends/${encodeURIComponent(id)}`,
        body,
      );
      if (res.status === "auth_required") {
        setOauth({ backendId: id, authUrl: res.auth_url });
        return;
      }
      onDone();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setError(msg);
    }
  };

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
