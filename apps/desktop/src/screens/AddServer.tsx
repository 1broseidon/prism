import { useState } from "preact/hooks";
import * as api from "../api";
import { errorMessage, pop, servers, status } from "../state";
import { Button, Screen, describeError } from "../ui";

/** A full screen for adding a stdio MCP server. Submitting starts it and returns to the Servers tab. */
export function AddServerScreen() {
  const [busy, setBusy] = useState(false);

  const onSubmit = async (event: Event) => {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const data = new FormData(form);
    const name = String(data.get("name") ?? "").trim();
    const command = String(data.get("command") ?? "").trim();
    const argsLine = String(data.get("args") ?? "").trim();
    const env: Record<string, string> = {};
    for (const line of String(data.get("env") ?? "").split("\n")) {
      const eq = line.indexOf("=");
      if (eq > 0) env[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
    }
    setBusy(true);
    try {
      await api.addServer({ name, command, args: argsLine ? argsLine.split(/\s+/) : [], env });
      servers.value = await api.listServers();
      status.value = await api.getStatus();
      pop();
    } catch (err) {
      errorMessage.value = describeError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <form id="add-server" class="screen pushed" onSubmit={onSubmit}>
      <Screen
        footer={
          <Button variant="primary" type="submit" busy={busy}>
            {busy ? "Starting…" : "Add and start"}
          </Button>
        }
      >
        <p class="lede">Prism starts the server for you and hands its tools to every approved agent.</p>
        <div class="fields">
          <label class="field">
            <span>Name</span>
            <input class="input" name="name" required autoFocus placeholder="filesystem" />
          </label>
          <label class="field">
            <span>Command</span>
            <input class="input mono" name="command" required placeholder="npx" />
          </label>
          <label class="field">
            <span>Arguments</span>
            <input class="input mono" name="args" placeholder="-y @modelcontextprotocol/server-filesystem ~/Projects" />
            <small>Split on whitespace. Values are kept in your OS credential store.</small>
          </label>
          <label class="field">
            <span>Environment</span>
            <textarea class="input mono" name="env" placeholder={"API_KEY=…\nONE_PER_LINE=true"} />
            <small>One KEY=value per line. Values are kept in your OS credential store, never shown to agents.</small>
          </label>
        </div>
      </Screen>
    </form>
  );
}
