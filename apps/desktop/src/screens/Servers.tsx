import { useEffect, useRef, useState } from "preact/hooks";
import * as api from "../api";
import { serverFocusIndexAfterRemoval, serverPrimaryAction, toggleServerDisclosure } from "../server-actions";
import { errorMessage, push, servers, status } from "../state";
import type { ServerView } from "../types";
import { Button, ChevronIcon, Chip, ConfirmButton, Empty, Label, Pager, Screen, StatusText, describeError, usePage } from "../ui";

function statusChip(server: ServerView) {
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

function authenticationGuidance(server: ServerView): string | null {
  if (server.status.kind !== "sign_in_required" || server.auth === "oauth") return null;
  return server.auth === "header" ? "Check the API key, then retry." : "The server returned 401. Check its setup, then retry.";
}

/** The URL without its scheme: the host is what tells servers apart, the scheme is always https. */
function shortUrl(url: string): string {
  return url.replace(/^https?:\/\//, "");
}

async function refresh() {
  servers.value = await api.listServers();
  status.value = await api.getStatus();
}

function ServerRow({
  server,
  open,
  act,
  onToggle,
  onClose,
  onRemoved,
}: {
  server: ServerView;
  open: boolean;
  act: (fn: () => Promise<unknown>) => Promise<boolean>;
  onToggle: () => void;
  onClose: () => void;
  onRemoved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const running = server.status.kind === "running";
  const oauth = server.auth === "oauth";
  const primary = serverPrimaryAction(server.auth, server.status.kind);
  const guidance = authenticationGuidance(server);
  const row = useRef<HTMLDivElement>(null);
  const maintenance = useRef<HTMLDivElement>(null);
  const panelId = `server-maintenance-${server.id}`;

  useEffect(() => {
    if (open) maintenance.current?.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus();
  }, [open]);

  const close = (returnFocus = false) => {
    onClose();
    if (returnFocus) requestAnimationFrame(() => row.current?.querySelector<HTMLButtonElement>(`[aria-controls="${panelId}"]`)?.focus());
  };
  const run = async (fn: () => Promise<unknown>, removesRow = false) => {
    if (busy) return;
    setBusy(true);
    try {
      if (await act(fn)) {
        if (removesRow) onRemoved();
        else close(true);
      }
    } finally {
      setBusy(false);
    }
  };
  const onMaintenanceKeyDown = (event: KeyboardEvent) => {
    if (event.key !== "Escape") return;
    event.preventDefault();
    event.stopPropagation();
    close(true);
  };

  return (
    <div ref={row} class={`item server-item ${open ? "maintenance-open" : ""}`}>
      <div class="title">
        <span class="truncate">{server.name}</span>
        {statusChip(server)}
      </div>
      <div class="side">
        {primary === "sign-in" ? (
          <Button variant="quiet" busy={busy} onClick={() => void run(() => api.signInServer(server.id))}>
            Sign in
          </Button>
        ) : null}
        {primary === "retry" ? (
          <Button variant="quiet" busy={busy} onClick={() => void run(() => api.restartServer(server.id))}>
            Retry
          </Button>
        ) : null}
        <Button
          variant="quiet"
          class="disclosure-trigger"
          aria-expanded={open}
          aria-controls={panelId}
          data-server-manage={server.id}
          busy={busy}
          onClick={onToggle}
        >
          Manage <ChevronIcon direction={open ? "up" : "down"} />
        </Button>
      </div>
      <div class="sub truncate" title={server.url ?? server.command}>
        {server.url ? shortUrl(server.url) : server.command}
        {server.auth === "header" ? " · API key" : server.auth === "oauth" ? " · OAuth" : ""}
      </div>
      {server.status.kind === "failed" ? <div class="sub danger">{server.status.error}</div> : null}
      {guidance ? <div class="sub danger">{guidance}</div> : null}
      {open ? (
        <div
          ref={maintenance}
          id={panelId}
          class="server-maintenance"
          role="group"
          aria-label={`Maintenance for ${server.name}`}
          onKeyDown={onMaintenanceKeyDown}
        >
          <span>Maintenance</span>
          {running || server.status.kind === "starting" ? (
            <Button variant="quiet" busy={busy} onClick={() => void run(() => api.restartServer(server.id))}>
              Restart
            </Button>
          ) : null}
          {oauth && running ? (
            <ConfirmButton variant="quiet" confirm="Sign out?" busy={busy} onConfirm={() => void run(() => api.signOutServer(server.id))}>
              Sign out
            </ConfirmButton>
          ) : null}
          <ConfirmButton variant="quiet" class="danger" confirm="Remove?" busy={busy} onConfirm={() => void run(() => api.removeServer(server.id), true)}>
            Remove
          </ConfirmButton>
        </div>
      ) : null}
    </div>
  );
}

export function ServersScreen() {
  const list = servers.value;
  const { rows, offset, setOffset, total } = usePage(list, 5);
  const [openServerId, setOpenServerId] = useState<string | null>(null);

  const act = async (fn: () => Promise<unknown>): Promise<boolean> => {
    try {
      await fn();
      await refresh();
      return true;
    } catch (err) {
      errorMessage.value = describeError(err);
      return false;
    }
  };

  const focusAfterRemoval = (removedIndex: number) => {
    setOpenServerId(null);
    requestAnimationFrame(() => {
      const manage = [...document.querySelectorAll<HTMLButtonElement>("[data-server-manage]")];
      const focusIndex = serverFocusIndexAfterRemoval(removedIndex, manage.length);
      if (focusIndex === null) document.querySelector<HTMLButtonElement>(".add-server-action")?.focus();
      else manage[focusIndex]?.focus();
    });
  };

  return (
    <div class="screen">
      <Screen
        footer={
          <>
            {total > 5 ? <Pager offset={offset} size={5} total={total} onOffset={(next) => { setOpenServerId(null); setOffset(next); }} /> : undefined}
            <Button class="add-server-action" onClick={() => push({ kind: "add-server" })}>Add server</Button>
          </>
        }
      >
        <Label right={<span>{list.length}</span>}>MCP servers</Label>
        {list.length === 0 ? (
          <Empty title="No servers yet." />
        ) : (
          <div class="list">
            {rows.map((server, index) => (
              <ServerRow
                key={server.id}
                server={server}
                open={openServerId === server.id}
                act={act}
                onToggle={() => setOpenServerId(toggleServerDisclosure(openServerId, server.id))}
                onClose={() => setOpenServerId(null)}
                onRemoved={() => focusAfterRemoval(index)}
              />
            ))}
          </div>
        )}
      </Screen>
    </div>
  );
}
