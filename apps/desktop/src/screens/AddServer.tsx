import { useEffect, useRef, useState } from "preact/hooks";
import * as api from "../api";
import { ServerAuthFields } from "../ServerAuthFields";
import type { RemoteProbe } from "../types";
import {
  addServerDraft,
  applyAddServerProbe,
  clearAddServerDraft,
  completeScreen,
  errorMessage,
  servers,
  status,
  updateAddServerDraft,
} from "../state";
import { Button, Screen, Segmented, describeError } from "../ui";

/** Adding a server: a command Prism runs, or a URL it connects to. Submitting starts it. */
export function AddServerScreen() {
  const [busy, setBusy] = useState(false);
  const draft = addServerDraft.value;
  const { kind, auth } = draft;

  const urlInput = useRef<HTMLInputElement>(null);
  const keyInput = useRef<HTMLInputElement>(null);
  const request = useRef(0);
  const pendingUrl = useRef<string | null>(null);
  const [probe, setProbe] = useState<{ url: string; result: RemoteProbe | "checking" } | null>(null);

  const checkUrl = async () => {
    const current = addServerDraft.value;
    const url = current.url;
    if (busy || current.kind !== "url" || !url || !urlInput.current?.validity.valid
      || pendingUrl.current === url || current.probedUrl === url) return;
    const version = ++request.current;
    pendingUrl.current = url;
    setProbe({ url, result: "checking" });
    try {
      const result = await api.probeServerUrl(url.trim());
      if (request.current !== version || addServerDraft.value.url !== url) return;
      const focusKey = applyAddServerProbe(url, result);
      setProbe({ url, result });
      if (focusKey) window.setTimeout(() => {
        if (request.current === version && !addServerDraft.value.authTouched) keyInput.current?.focus();
      }, 0);
    } catch {
      if (request.current === version) setProbe(null);
    } finally {
      if (request.current === version) pendingUrl.current = null;
    }
  };

  useEffect(() => {
    const timer = window.setTimeout(() => void checkUrl(), 600);
    return () => {
      window.clearTimeout(timer);
      request.current += 1;
      pendingUrl.current = null;
    };
  }, [kind, draft.url, busy]);

  const result = probe?.url === draft.url && kind === "url" ? probe.result : null;
  const probeText = result === "checking" ? "Checking…"
    : result?.kind === "open" ? "No sign-in needed."
    : result?.kind === "oauth_ready" ? "Signs you in through your browser."
    : result?.kind === "bearer_likely" && result.reason === "oauth_broken" ? "Couldn't verify browser sign-in. Check the server, or choose auth yourself."
    : result?.kind === "bearer_likely" ? "No supported browser sign-in found. Check whether this server needs an API key."
    : result?.kind === "unreachable" ? "Couldn't reach this URL. Check it, or pick the auth method yourself." : null;

  const onSubmit = async (event: Event) => {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const data = new FormData(form);
    const field = (name: string) => String(data.get(name) ?? "").trim();
    const name = field("name");
    setBusy(true);
    try {
      let added;
      if (kind === "url") {
        const headers: Record<string, string> = {};
        if (auth === "header") {
          const header = field("header") || "Authorization";
          const key = field("key");
          headers[header] = header.toLowerCase() === "authorization" && !/^\S+\s/.test(key) ? `Bearer ${key}` : key;
        }
        added = await api.addServer({ name, url: field("url"), auth, headers });
      } else {
        const env: Record<string, string> = {};
        for (const line of String(data.get("env") ?? "").split("\n")) {
          const eq = line.indexOf("=");
          if (eq > 0) env[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
        }
        const argsLine = field("args");
        added = await api.addServer({ name, command: field("command"), args: argsLine ? argsLine.split(/\s+/) : [], env });
      }
      servers.value = await api.listServers();
      status.value = await api.getStatus();
      if (kind === "url" && auth === "oauth") await api.signInServer(added.id);
      clearAddServerDraft();
      completeScreen({ kind: "add-server" });
    } catch (err) {
      errorMessage.value = describeError(err);
    } finally {
      setBusy(false);
    }
  };

  const submitLabel = busy ? "Starting…" : kind === "url" && auth === "oauth" ? "Add and sign in" : "Add and start";

  return (
    <form id="add-server" class="screen pushed" onSubmit={onSubmit}>
      <Screen
        footer={
          <Button variant="primary" type="submit" busy={busy}>
            {submitLabel}
          </Button>
        }
      >
        <Segmented
          label="Server kind"
          value={kind}
          options={[
            { value: "command", label: "Command" },
            { value: "url", label: "URL" },
          ]}
          onChange={(next) => updateAddServerDraft({ kind: next })}
        />
        <div class="fields">
          <label class="field">
            <span>Name</span>
            <input class="input" name="name" required autoFocus value={draft.name} onInput={(event) => updateAddServerDraft({ name: event.currentTarget.value })} placeholder={kind === "url" ? "linear" : "filesystem"} />
          </label>
          {kind === "url" ? (
            <>
              <label class="field">
                <span>URL</span>
                <input ref={urlInput} onBlur={() => void checkUrl()} class="input mono" name="url" type="url" required value={draft.url} onInput={(event) => updateAddServerDraft({ url: event.currentTarget.value })} placeholder="https://mcp.example.com/mcp" />
                <small>https, or http on this machine.</small>
                {/* A blur starts the probe: keep its message from moving the auth button being clicked. */}
                <small role="status" style={{ minHeight: "2lh" }}>{probeText ?? "\u00a0"}</small>
              </label>
              <ServerAuthFields auth={auth} header={draft.header} secret={draft.key} keyRef={keyInput}
                onAuth={(next) => updateAddServerDraft({ auth: next, authTouched: true })}
                onHeader={(header) => updateAddServerDraft({ header })}
                onSecret={(key) => updateAddServerDraft({ key })} />
            </>
          ) : (
            <>
              <label class="field">
                <span>Command</span>
                <input class="input mono" name="command" required value={draft.command} onInput={(event) => updateAddServerDraft({ command: event.currentTarget.value })} placeholder="npx" />
              </label>
              <label class="field">
                <span>Arguments</span>
                <input class="input mono" name="args" value={draft.args} onInput={(event) => updateAddServerDraft({ args: event.currentTarget.value })} placeholder="-y @modelcontextprotocol/server-filesystem ~/Projects" />
                <small>Space-separated.</small>
              </label>
              <label class="field">
                <span>Environment</span>
                <textarea class="input mono" name="env" value={draft.env} onInput={(event) => updateAddServerDraft({ env: event.currentTarget.value })} placeholder={"API_KEY=…\nONE_PER_LINE=true"} />
                <small>KEY=value per line. Stored in your keyring.</small>
              </label>
            </>
          )}
        </div>
      </Screen>
    </form>
  );
}
