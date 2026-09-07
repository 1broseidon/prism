import { useState } from "preact/hooks";
import * as api from "../api";
import {
  addServerDraft,
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
                <input class="input mono" name="url" type="url" required value={draft.url} onInput={(event) => updateAddServerDraft({ url: event.currentTarget.value })} placeholder="https://mcp.example.com/mcp" />
                <small>https, or http on this machine.</small>
              </label>
              <div class="field">
                <span>Auth</span>
                <Segmented
                  small
                  label="Authentication"
                  value={auth}
                  options={[
                    { value: "none", label: "None" },
                    { value: "header", label: "API key" },
                    { value: "oauth", label: "OAuth" },
                  ]}
                  onChange={(next) => updateAddServerDraft({ auth: next })}
                />
                {auth === "oauth" ? <small>Signs in through your browser. Tokens stay in your keyring.</small> : null}
              </div>
              {auth === "header" ? (
                <>
                  <label class="field">
                    <span>Header</span>
                    <input class="input mono" name="header" value={draft.header} onInput={(event) => updateAddServerDraft({ header: event.currentTarget.value })} placeholder="Authorization" />
                  </label>
                  <label class="field">
                    <span>Key</span>
                    <input class="input mono" name="key" type="password" required autoComplete="off" value={draft.key} onInput={(event) => updateAddServerDraft({ key: event.currentTarget.value })} placeholder="ghp_…" />
                    <small>Sent as Bearer unless you give a prefix. Stored in your keyring.</small>
                  </label>
                </>
              ) : null}
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
