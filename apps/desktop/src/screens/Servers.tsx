import { useState } from "preact/hooks";
import * as api from "../api";
import { serverPrimaryAction } from "../server-actions";
import { errorMessage, push, servers, status } from "../state";
import type { ServerView } from "../types";
import { Button, ChevronIcon, Chip, Empty, Label, REVEAL, Screen, ShowMore, StatusText, describeError, useReveal } from "../ui";

export function statusChip(server: ServerView) {
  const s = server.status;
  switch (s.kind) {
    case "running":
      return <StatusText>{s.tool_count} tools</StatusText>;
    case "failed":
      return <Chip tone="danger">Failed</Chip>;
    case "starting":
      return <Chip tone="warn">Starting</Chip>;
    case "sign_in_required":
      return server.auth === "oauth" ? <Chip tone="warn">Needs sign-in</Chip> : <Chip tone="danger">Authentication failed</Chip>;
    default:
      return <StatusText>Stopped</StatusText>;
  }
}

export function authenticationGuidance(server: ServerView): string | null {
  if (server.status.kind !== "sign_in_required" || server.auth === "oauth") return null;
  return server.auth === "header" ? "Check the API key, then retry." : "The server returned 401. Check its setup, then retry.";
}

/** The URL without its scheme: the host is what tells servers apart, the scheme is always https. */
export function shortUrl(url: string): string {
  return url.replace(/^https?:\/\//, "");
}

/** Where it runs and how it authenticates, on one mono line. */
export function serverWhere(server: ServerView): string {
  const where = server.url ? shortUrl(server.url) : server.command;
  return server.auth === "header" ? `${where} · API key` : server.auth === "oauth" ? `${where} · OAuth` : where;
}

export async function refreshServers() {
  servers.value = await api.listServers();
  status.value = await api.getStatus();
}

/** One row per server. The row is the door; only recovery stays on it, because that is the one thing worth a tap here. */
function ServerRow({ server }: { server: ServerView }) {
  const [busy, setBusy] = useState(false);
  const primary = serverPrimaryAction(server.auth, server.status.kind);
  const guidance = authenticationGuidance(server);
  const hidden = server.hidden_tools.length;
  const run = async (fn: () => Promise<unknown>) => {
    if (busy) return;
    setBusy(true);
    try {
      await fn();
      await refreshServers();
    } catch (err) {
      errorMessage.value = describeError(err);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div class="item">
      <button type="button" class="title row-btn" onClick={() => push({ kind: "server", serverId: server.id })}>
        <span class="truncate">{server.name}</span>
        {statusChip(server)}
        {hidden > 0 ? <StatusText>{hidden} hidden</StatusText> : null}
        <span class="chev"><ChevronIcon /></span>
      </button>
      {primary === "sign-in" ? (
        <div class="side">
          <Button variant="quiet" busy={busy} onClick={() => void run(() => api.signInServer(server.id))}>Sign in</Button>
        </div>
      ) : primary === "retry" ? (
        <div class="side">
          <Button variant="quiet" busy={busy} onClick={() => void run(() => api.restartServer(server.id))}>Retry</Button>
        </div>
      ) : null}
      <div class="sub truncate" title={server.url ?? server.command}>{serverWhere(server)}</div>
      {server.status.kind === "failed" ? <div class="sub danger">{server.status.error}</div> : null}
      {guidance ? <div class="sub danger">{guidance}</div> : null}
    </div>
  );
}

export function ServersScreen() {
  const list = servers.value;
  const { rows, total, more } = useReveal(list, REVEAL);

  return (
    <div class="screen">
      <Screen
        footer={
          <>
            <Button class="add-server-action" onClick={() => push({ kind: "add-server" })}>Add server</Button>
          </>
        }
      >
        <Label right={<span>{list.length}</span>}>MCP servers</Label>
        {list.length === 0 ? (
          <Empty title="No servers yet." />
        ) : (
          <div class="list">
            {rows.map((server) => <ServerRow key={server.id} server={server} />)}
            <ShowMore shown={rows.length} total={total} size={REVEAL} onMore={more} />
          </div>
        )}
      </Screen>
    </div>
  );
}
