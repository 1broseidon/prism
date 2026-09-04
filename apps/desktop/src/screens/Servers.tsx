import { useState } from "preact/hooks";
import * as api from "../api";
import { errorMessage, servers, status } from "../state";
import type { BackendStatus } from "../types";
import { Button, Chip, Empty, Label, describeError } from "../ui";

function statusChip(s: BackendStatus) {
  switch (s.kind) {
    case "running":
      return <Chip tone="ok">{s.tool_count} tools</Chip>;
    case "failed":
      return <Chip tone="danger">failed</Chip>;
    case "starting":
      return <Chip tone="warn">starting</Chip>;
    default:
      return <Chip>stopped</Chip>;
  }
}

async function refresh() {
  servers.value = await api.listServers();
  status.value = await api.getStatus();
}

function AddServerForm({ onDone }: { onDone: () => void }) {
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
      await refresh();
      form.reset();
      onDone();
    } catch (err) {
      errorMessage.value = describeError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <form class="form" onSubmit={onSubmit}>
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
        <small>Split on whitespace.</small>
      </label>
      <label class="field">
        <span>Environment</span>
        <textarea class="input mono" name="env" placeholder={"API_KEY=…\nONE_PER_LINE=true"} />
      </label>
      <div class="form-foot">
        <Button variant="quiet" onClick={onDone}>
          Cancel
        </Button>
        <Button variant="primary" type="submit" busy={busy}>
          {busy ? "Starting…" : "Add and start"}
        </Button>
      </div>
    </form>
  );
}

export function ServersScreen() {
  const [adding, setAdding] = useState(false);
  const list = servers.value;

  const act = async (fn: () => Promise<void>) => {
    try {
      await fn();
      await refresh();
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };

  return (
    <div>
      <Label right={<span>{list.length}</span>}>MCP servers</Label>
      {list.length === 0 ? (
        <Empty title="No servers yet.">Add the MCP servers you already use. Prism starts them and hands their tools to every agent.</Empty>
      ) : (
        <div class="list">
          {list.map((server) => (
            <div class="item" key={server.id}>
              <div class="title">
                <span class="truncate">{server.name}</span>
                {statusChip(server.status)}
              </div>
              <div class="side">
                <Button variant="quiet" onClick={() => void act(() => api.restartServer(server.id))}>
                  Restart
                </Button>
                <Button variant="quiet" class="danger" onClick={() => void act(() => api.removeServer(server.id))}>
                  Remove
                </Button>
              </div>
              <div class="sub truncate" title={`${server.command} ${server.args.join(" ")}`}>
                {server.command} {server.args.join(" ")}
              </div>
              {server.status.kind === "failed" ? <div class="sub" style={{ color: "var(--color-danger)" }}>{server.status.error}</div> : null}
            </div>
          ))}
        </div>
      )}
      {adding ? (
        <AddServerForm onDone={() => setAdding(false)} />
      ) : (
        <div class="actions" style={{ marginTop: "var(--space-3)" }}>
          <Button onClick={() => setAdding(true)}>Add server</Button>
        </div>
      )}
    </div>
  );
}
