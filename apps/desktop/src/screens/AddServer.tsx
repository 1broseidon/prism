import { useState } from "preact/hooks";
import * as api from "../api";
import { errorMessage, pop, servers, status } from "../state";
import type { HttpAuth } from "../types";
import { Button, Screen, Segmented, describeError } from "../ui";

type Kind = "command" | "url";

/** Adding a server: a command Prism runs, or a URL it connects to. Submitting starts it. */
export function AddServerScreen() {
  const [busy, setBusy] = useState(false);
  const [kind, setKind] = useState<Kind>("command");
  const [auth, setAuth] = useState<HttpAuth>("none");

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
      pop();
      if (kind === "url" && auth === "oauth") await api.signInServer(added.id);
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
          onChange={setKind}
        />
        <div class="fields">
          <label class="field">
            <span>Name</span>
            <input class="input" name="name" required autoFocus placeholder={kind === "url" ? "linear" : "filesystem"} />
          </label>
          {kind === "url" ? (
            <>
              <label class="field">
                <span>URL</span>
                <input class="input mono" name="url" type="url" required placeholder="https://mcp.example.com/mcp" />
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
                  onChange={setAuth}
                />
                {auth === "oauth" ? <small>Signs in through your browser. Tokens stay in your keyring.</small> : null}
              </div>
              {auth === "header" ? (
                <>
                  <label class="field">
                    <span>Header</span>
                    <input class="input mono" name="header" placeholder="Authorization" />
                  </label>
                  <label class="field">
                    <span>Key</span>
                    <input class="input mono" name="key" type="password" required autoComplete="off" placeholder="ghp_…" />
                    <small>Sent as Bearer unless you give a prefix. Stored in your keyring.</small>
                  </label>
                </>
              ) : null}
            </>
          ) : (
            <>
              <label class="field">
                <span>Command</span>
                <input class="input mono" name="command" required placeholder="npx" />
              </label>
              <label class="field">
                <span>Arguments</span>
                <input class="input mono" name="args" placeholder="-y @modelcontextprotocol/server-filesystem ~/Projects" />
                <small>Space-separated.</small>
              </label>
              <label class="field">
                <span>Environment</span>
                <textarea class="input mono" name="env" placeholder={"API_KEY=…\nONE_PER_LINE=true"} />
                <small>KEY=value per line. Stored in your keyring.</small>
              </label>
            </>
          )}
        </div>
      </Screen>
    </form>
  );
}
