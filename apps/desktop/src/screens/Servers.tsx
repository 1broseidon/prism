import * as api from "../api";
import { errorMessage, push, servers, status } from "../state";
import type { BackendStatus, ServerView } from "../types";
import { Button, Chip, ConfirmButton, Empty, Label, Screen, describeError } from "../ui";

function statusChip(s: BackendStatus) {
  switch (s.kind) {
    case "running":
      return <Chip tone="ok">{s.tool_count} tools</Chip>;
    case "failed":
      return <Chip tone="danger">failed</Chip>;
    case "starting":
      return <Chip tone="warn">starting</Chip>;
    case "sign_in_required":
      return <Chip tone="warn">needs sign-in</Chip>;
    default:
      return <Chip>stopped</Chip>;
  }
}

/** The URL without its scheme: the host is what tells servers apart, the scheme is always https. */
function shortUrl(url: string): string {
  return url.replace(/^https?:\/\//, "");
}

async function refresh() {
  servers.value = await api.listServers();
  status.value = await api.getStatus();
}

function ServerRow({ server, act }: { server: ServerView; act: (fn: () => Promise<unknown>) => Promise<void> }) {
  const oauth = server.auth === "oauth";
  const running = server.status.kind === "running";
  return (
    <div class="item">
      <div class="title">
        <span class="truncate">{server.name}</span>
        {statusChip(server.status)}
      </div>
      <div class="side">
        {oauth && !running ? (
          <Button variant="quiet" onClick={() => void act(() => api.signInServer(server.id))}>
            Sign in
          </Button>
        ) : null}
        {oauth && running ? (
          <ConfirmButton variant="quiet" confirm="Sign out?" onConfirm={() => void act(() => api.signOutServer(server.id))}>
            Sign out
          </ConfirmButton>
        ) : null}
        {oauth && !running ? null : (
          <Button variant="quiet" onClick={() => void act(() => api.restartServer(server.id))}>
            Restart
          </Button>
        )}
        <ConfirmButton variant="quiet" class="danger" confirm="Remove?" onConfirm={() => void act(() => api.removeServer(server.id))}>
          Remove
        </ConfirmButton>
      </div>
      <div class="sub truncate" title={server.url ?? server.command}>
        {server.url ? shortUrl(server.url) : server.command}
        {server.auth === "header" ? " · API key" : server.auth === "oauth" ? " · OAuth" : ""}
      </div>
      {server.status.kind === "failed" ? <div class="sub danger">{server.status.error}</div> : null}
    </div>
  );
}

export function ServersScreen() {
  const list = servers.value;

  const act = async (fn: () => Promise<unknown>) => {
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
          <Empty title="No servers yet." />
        ) : (
          <div class="list">
            {list.map((server) => (
              <ServerRow key={server.id} server={server} act={act} />
            ))}
          </div>
        )}
      </Screen>
    </div>
  );
}
