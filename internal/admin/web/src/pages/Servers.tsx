import { useEffect, useRef, useState } from "preact/hooks";
import { backends } from "../state";
import {
  deleteJSON,
  getJSON,
  postJSON,
} from "../api/client";
import type {
  AddBackendBody,
  AddBackendResponse,
  AuthStatus,
  Backend,
  CredentialInput,
} from "../api/types";

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

export function Servers() {
  const list = (backends.data.value || []).slice().sort((a, b) =>
    a.id.localeCompare(b.id),
  );
  const [addingOpen, setAddingOpen] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">MCP Servers</div>
          <div class="page-subtitle">
            {list.length} backend{list.length === 1 ? "" : "s"} connected
          </div>
        </div>
        <button
          class="section-btn"
          onClick={() => setAddingOpen((v) => !v)}
        >
          + Connect
        </button>
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
        <div class="empty-state">
          No backends connected. Use “+ Connect” to add one.
        </div>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th style="width:50%">Transport</th>
              <th style="width:10%" class="right">
                {busy ? <span style="color:var(--muted)">…</span> : null}
              </th>
            </tr>
          </thead>
          <tbody>
            {list.map((b) => (
              <>
                <tr
                  key={b.id}
                  style="cursor:pointer"
                  onClick={() =>
                    setEditingId(editingId === b.id ? null : b.id)
                  }
                >
                  <td>
                    <span class="backend-name">{b.id}</span>
                    {b.namespace && b.namespace !== b.id && (
                      <span class="backend-ns-suffix">({b.namespace})</span>
                    )}
                  </td>
                  <td>
                    <span class="backend-transport">
                      {b.url || "stdio"}
                    </span>
                    <CredBadge b={b} />
                  </td>
                  <td class="right">
                    <span style="font-size:10px;color:var(--muted)">
                      {editingId === b.id ? "▾" : "▸"}
                    </span>
                  </td>
                </tr>
                {editingId === b.id && (
                  <tr class="backend-edit-row" key={`${b.id}-edit`}>
                    <td colspan={3} style="padding:0">
                      <BackendEdit
                        backend={b}
                        onDone={() => {
                          setEditingId(null);
                          backends.refresh();
                        }}
                        onCancel={() => setEditingId(null)}
                        onBusy={setBusy}
                      />
                    </td>
                  </tr>
                )}
              </>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function CredBadge({ b }: { b: Backend }) {
  const c = b.credential;
  if (!c || !c.configured) return null;
  if (c.type === "static") {
    return <span class="cred-badge">api-key ••••</span>;
  }
  if (c.type === "env") {
    return <span class="cred-badge">env: {c.env || ""}</span>;
  }
  if (c.type === "command") {
    const cmd = c.command || "";
    const short = cmd.length > 30 ? cmd.slice(0, 30) + "…" : cmd;
    return <span class="cred-badge">cmd: {short}</span>;
  }
  return null;
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
        style="width:100px"
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
            style="width:200px"
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
      setError(e instanceof Error ? e.message : String(e));
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
    <div
      class="inline-form"
      style="border:1px solid var(--line);padding:12px;margin-bottom:16px;background:var(--bg-elev)"
    >
      <input
        type="text"
        placeholder="name"
        value={name}
        autoFocus
        spellcheck={false}
        style="width:140px"
        onInput={(e) => setName((e.target as HTMLInputElement).value)}
      />
      <input
        type="text"
        placeholder="command or http(s) URL"
        value={cmd}
        spellcheck={false}
        style="flex:1;min-width:240px"
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
        <option value="static">API Key</option>
        <option value="env">Env Var</option>
        <option value="command">Command</option>
      </select>
      <CredFields state={cred} onChange={setCred} />
      <div style="display:flex;gap:6px;align-items:center;width:100%;margin-top:4px">
        <button class="save-btn" onClick={submit}>
          connect
        </button>
        <button class="cancel-btn" onClick={onCancel}>
          cancel
        </button>
        {error && (
          <span style="font-size:11px;color:var(--denied)">{error}</span>
        )}
      </div>
    </div>
  );
}

function BackendEdit({
  backend,
  onDone,
  onCancel,
  onBusy,
}: {
  backend: Backend;
  onDone: () => void;
  onCancel: () => void;
  onBusy: (id: string | null) => void;
}) {
  const [cred, setCred] = useState<CredFormState>(credFromBackend(backend));
  const [error, setError] = useState<string | null>(null);

  const save = async () => {
    setError(null);
    onBusy(backend.id);
    try {
      const body: AddBackendBody = { url: backend.url || "" };
      const credInput = buildCredInput(cred);
      body.credential = credInput ?? null;
      await postJSON(`/backends/${encodeURIComponent(backend.id)}`, body);
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      onBusy(null);
    }
  };

  const remove = async () => {
    if (!confirm(`Remove backend "${backend.id}"?`)) return;
    onBusy(backend.id);
    try {
      await deleteJSON(`/backends/${encodeURIComponent(backend.id)}`);
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      onBusy(null);
    }
  };

  return (
    <div class="backend-edit">
      <div class="inline-form" style="padding:0 0 0 24px">
        <span class="policy-label" style="width:auto">
          credential
        </span>
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
          <option value="static">API Key</option>
          <option value="env">Env Var</option>
          <option value="command">Command</option>
        </select>
        <CredFields state={cred} onChange={setCred} />
        <div style="display:flex;gap:6px;margin-left:auto;padding-right:24px">
          <button class="save-btn" onClick={save}>
            save
          </button>
          <button class="danger-btn" onClick={remove}>
            remove
          </button>
          <button class="cancel-btn" onClick={onCancel}>
            cancel
          </button>
        </div>
        {error && (
          <span style="font-size:11px;color:var(--denied);width:100%">
            {error}
          </span>
        )}
      </div>
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
    "Click Authenticate to open the provider in a popup.",
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
    setMessage("Authorization in progress…");
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
          setMessage("Failed: " + d.status.replace("failed:", ""));
        }
      } catch {
        // keep polling
      }
    }, 2000);
    timeoutRef.current = window.setTimeout(
      () => {
        stop();
        setStatus("timeout");
        setMessage("Authentication timed out.");
      },
      5 * 60 * 1000,
    );
  };

  return (
    <div
      class="inline-form"
      style="border:1px solid var(--line);padding:12px;margin-bottom:16px;background:var(--bg-elev)"
    >
      <div class="oauth-flow">
        <button class="save-btn" onClick={start} disabled={status === "waiting"}>
          {status === "idle"
            ? "Authenticate"
            : status === "waiting"
              ? "Waiting…"
              : "Retry"}
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
