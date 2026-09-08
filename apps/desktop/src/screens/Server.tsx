import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { serverPrimaryAction } from "../server-actions";
import { errorMessage, pop, servers, status } from "../state";
import type { ToolInfo } from "../types";
import { Button, Chip, ConfirmButton, Label, REVEAL, Screen, ShowMore, StatusText, Switch, describeError, useReveal } from "../ui";
import { authenticationGuidance, refreshServers, serverWhere, statusChip } from "./Servers";

/** One server as its own screen: what it is, which of its tools agents get, and the few things you can do to it. */
export function ServerScreen({ serverId }: { serverId: string }) {
  const server = servers.value.find((s) => s.id === serverId);
  const running = server?.status.kind === "running";
  const [tools, setTools] = useState<ToolInfo[] | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!running) {
      setTools(null);
      return;
    }
    api.listServerTools(serverId).then(setTools).catch((err) => {
      errorMessage.value = describeError(err);
    });
  }, [serverId, running]);

  // Removed elsewhere, or removed here: the screen has nothing to show, so it leaves.
  const loaded = status.value !== null;
  useEffect(() => {
    if (loaded && !server) pop();
  }, [loaded, server]);

  const { rows, total, more } = useReveal(tools ?? [], REVEAL, serverId);
  if (!server) return <div class="screen pushed" />;

  const act = async (fn: () => Promise<unknown>) => {
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

  /** Flips at once; the gateway's answer settles it. */
  const expose = async (tool: ToolInfo, next: boolean) => {
    const flip = (list: ToolInfo[] | null, value: boolean) => list?.map((t) => (t.name === tool.name ? { ...t, exposed: value } : t)) ?? null;
    setTools((list) => flip(list, next));
    try {
      await api.setToolExposed(server.id, tool.name, next);
      await refreshServers();
    } catch (err) {
      errorMessage.value = describeError(err);
      setTools((list) => flip(list, !next));
    }
  };

  const primary = serverPrimaryAction(server.auth, server.status.kind);
  const guidance = authenticationGuidance(server);
  const exposed = tools?.filter((t) => t.exposed).length ?? 0;
  const footer = (
    <>
      <ConfirmButton variant="danger" confirm="Remove?" busy={busy} onConfirm={() => void act(() => api.removeServer(server.id))}>
        Remove
      </ConfirmButton>
      {server.auth === "oauth" && running ? (
        <ConfirmButton variant="quiet" confirm="Sign out?" busy={busy} onConfirm={() => void act(() => api.signOutServer(server.id))}>
          Sign out
        </ConfirmButton>
      ) : null}
      {primary === "sign-in" ? (
        <Button variant="primary" busy={busy} onClick={() => void act(() => api.signInServer(server.id))}>Sign in</Button>
      ) : primary === "retry" ? (
        <Button variant="primary" busy={busy} onClick={() => void act(() => api.restartServer(server.id))}>Retry</Button>
      ) : (
        <Button busy={busy} onClick={() => void act(() => api.restartServer(server.id))}>Restart</Button>
      )}
    </>
  );

  return (
    <div class="screen pushed">
      <Screen footer={footer}>
        <div class="server-head">
          {statusChip(server)}
          <StatusText>{server.url ? "remote" : "local"}</StatusText>
          <span class="grow" />
          <span class="sub mono truncate" title={server.url ?? server.command}>{serverWhere(server)}</span>
        </div>
        {server.status.kind === "failed" ? <p class="hint danger">{server.status.error}</p> : null}
        {guidance ? <p class="hint danger">{guidance}</p> : null}

        <section class="section">
          <Label right={tools && tools.length > 0 ? <span>{exposed === tools.length ? "all exposed" : `${exposed} of ${tools.length} exposed`}</span> : null}>Tools</Label>
          {!running ? (
            <p class="hint">Tools appear once the server is running.</p>
          ) : tools === null ? null : tools.length === 0 ? (
            <p class="hint">No tools.</p>
          ) : (
            <div class="list">
              {rows.map((tool) => (
                <div class={`item ${tool.exposed ? "" : "hidden-tool"}`} key={tool.name}>
                  <div class="title">
                    <span class="truncate mono small">{tool.name}</span>
                    {tool.read_only ? <Chip tone="ok">Read</Chip> : null}
                    {tool.destructive ? <Chip tone="warn">Writes</Chip> : null}
                  </div>
                  <div class="side">
                    <Switch checked={tool.exposed} label={`Expose ${tool.name} to agents`} onChange={(next) => void expose(tool, next)} />
                  </div>
                  {tool.description ? <div class="sub truncate" title={tool.description}>{tool.description}</div> : null}
                </div>
              ))}
              <ShowMore shown={rows.length} total={total} size={REVEAL} onMore={more} />
            </div>
          )}
          {running && tools && tools.length > 0 ? <p class="hint">A hidden tool is not listed to any agent and cannot be called.</p> : null}
        </section>
      </Screen>
    </div>
  );
}
