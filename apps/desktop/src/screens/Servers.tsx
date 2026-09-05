import * as api from "../api";
import { errorMessage, push, servers, status } from "../state";
import type { BackendStatus } from "../types";
import { Button, Chip, Empty, Label, Screen, describeError } from "../ui";

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

export function ServersScreen() {
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
    <div class="screen">
      <Screen footer={<Button onClick={() => push({ kind: "add-server" })}>Add server</Button>}>
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
                <div class="sub truncate" title={server.command}>
                  {server.command}{server.credentials_stored ? " · launch settings secured" : ""}
                </div>
                {server.status.kind === "failed" ? <div class="sub danger">{server.status.error}</div> : null}
              </div>
            ))}
          </div>
        )}
      </Screen>
    </div>
  );
}
