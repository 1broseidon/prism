import { useEffect, useRef, useState } from "preact/hooks";
import * as api from "../api";
import { ServerAuthFields } from "../ServerAuthFields";
import { persistExposure, serverPrimaryAction, toolExposureActions } from "../server-actions";
import { beginServerEdit, discardServerEdit, errorMessage, pop, saveServerEdit, savingServerEdits, serverEditDrafts, servers, status, toolRevisions, updateServerEdit } from "../state";
import { changedServerOrigin, serverEditArgs } from "../server-edit";
import type { ServerView, ToolInfo } from "../types";
import { Button, Chip, ConfirmButton, Label, REVEAL, Screen, ShowMore, StatusText, Switch, describeError, useReveal } from "../ui";
import { authenticationGuidance, refreshServers, serverWhere, statusChip } from "./Servers";

/** One server as its own screen: what it is, which of its tools agents get, and the few things you can do to it. */
export function ServerScreen({ serverId }: { serverId: string }) {
  const server = servers.value.find((s) => s.id === serverId);
  const running = server?.status.kind === "running";
  const [tools, setTools] = useState<ToolInfo[] | null>(null);
  const [busy, setBusy] = useState(false);
  const editing = serverEditDrafts.value[serverId] !== undefined;
  const [updating, setUpdating] = useState<Set<string>>(new Set());
  const toolsRequest = useRef(0);
  const revision = toolRevisions.value[serverId] ?? 0;

  useEffect(() => {
    let current = true;
    const request = ++toolsRequest.current;
    if (!running) {
      setTools(null);
      return;
    }
    // Don't let an older list overwrite an optimistic mutation.
    if (updating.size > 0) return;
    api.listServerTools(serverId).then((result) => {
      if (current && request === toolsRequest.current) setTools(result);
    }).catch((err) => {
      if (current && request === toolsRequest.current) errorMessage.value = describeError(err);
    });
    return () => { current = false; };
  }, [serverId, running, revision, updating]);

  // Removed elsewhere, or removed here: the screen has nothing to show, so it leaves.
  const loaded = status.value !== null;
  useEffect(() => {
    if (loaded && !server) { discardServerEdit(serverId); pop(); }
  }, [loaded, server]);

  const { rows, total, more } = useReveal(tools ?? [], REVEAL, serverId);
  if (!server) return <div class="screen pushed" />;

  const act = async (fn: () => Promise<unknown>) => {
    if (busy) return;
    setBusy(true);
    try {
      await fn();
    } catch (err) {
      errorMessage.value = describeError(err);
    } finally {
      try { await refreshServers(); } catch (err) { errorMessage.value ??= describeError(err); }
      setBusy(false);
    }
  };

  /** Only one write per target; a refresh failure cannot undo a confirmed save. */
  const expose = async (tool: ToolInfo, next: boolean) => {
    await toolExposureActions.run(JSON.stringify([serverId, tool.name]), async () => {
      const flip = (list: ToolInfo[] | null, value: boolean) => list?.map((t) => (t.name === tool.name ? { ...t, exposed: value } : t)) ?? null;
      // Invalidate reads immediately, before the disabled control re-renders.
      toolsRequest.current += 1;
      setUpdating((old) => new Set(old).add(tool.name));
      setTools((list) => flip(list, next));
      try {
        await persistExposure(
          () => api.setToolExposed(serverId, tool.name, next),
          refreshServers,
          () => setTools((list) => flip(list, tool.exposed)),
        );
      } catch (err) {
        errorMessage.value = describeError(err);
      } finally {
        setUpdating((old) => {
          const remaining = new Set(old);
          remaining.delete(tool.name);
          return remaining;
        });
      }
    });
  };

  const primary = serverPrimaryAction(server.auth, server.status);
  const guidance = authenticationGuidance(server);
  const exposed = tools?.filter((t) => t.exposed).length ?? 0;
  const footer = (
    <>
      <ConfirmButton variant="danger" confirm="Remove?" busy={busy} onConfirm={() => void act(() => api.removeServer(server.id))}>
        Remove
      </ConfirmButton>
      <Button busy={busy} disabled={editing} onClick={() => beginServerEdit(server)}>Edit</Button>
      {server.auth === "oauth" && running ? (
        <ConfirmButton variant="quiet" confirm="Sign out?" busy={busy} onConfirm={() => void act(() => api.signOutServer(server.id))}>
          Sign out
        </ConfirmButton>
      ) : null}
      {primary === "sign-in" ? (
        <Button variant="primary" busy={busy} onClick={() => void act(() => api.signInServer(server.id))}>Sign in</Button>
      ) : primary === "retry" ? (
        <Button variant="primary" busy={busy} onClick={() => void act(() => api.restartServer(server.id))}>Retry</Button>
      ) : primary === "edit" ? null : (
        <Button busy={busy} onClick={() => void act(() => api.restartServer(server.id))}>Restart</Button>
      )}
    </>
  );

  if (editing) return <ServerEditor server={server} />;

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
                    <Switch checked={tool.exposed} disabled={busy || updating.has(tool.name)} label={`Expose ${tool.name} to agents`} onChange={(next) => void expose(tool, next)} />
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

/** Secret values are deliberately blank; omission preserves the stored launch settings. */
function ServerEditor({ server }: { server: ServerView }) {
  const draft = serverEditDrafts.value[server.id];
  const busy = savingServerEdits.value[server.id] ?? false;
  if (!draft) return null;
  const patch = (value: Partial<typeof draft>) => updateServerEdit(server.id, value);
  const requireKey = server.auth !== "header" || changedServerOrigin(server, draft);
  const save = (event: Event) => {
    event.preventDefault();
    if (busy) return;
    try {
      const args = serverEditArgs(server, draft);
      void saveServerEdit(server.id, () => api.updateServer(server.id, args), refreshServers)
        .catch(err => { errorMessage.value = describeError(err); });
    } catch (err) { errorMessage.value = describeError(err); }
  };
  return <form class="screen pushed" onSubmit={save}>
    <Screen footer={<>
      <Button busy={busy} onClick={() => discardServerEdit(server.id)}>Cancel</Button>
      <Button type="submit" variant="primary" busy={busy}>{busy ? "Saving…" : "Save"}</Button>
    </>}>
      <Label>Edit server</Label>
      <fieldset class="fields" disabled={busy} style={{ border: 0, padding: 0, minWidth: 0 }}>
        <label class="field"><span>Name</span><input class="input" required autoFocus value={draft.name} onInput={event => patch({ name: event.currentTarget.value })} /></label>
        {server.url !== null ? <>
          <label class="field"><span>URL</span><input class="input mono" type="url" required value={draft.url} onInput={event => patch({ url: event.currentTarget.value })} />
            {changedServerOrigin(server, draft) && draft.auth === "header" ? <small>A different origin needs the API key entered again.</small> : null}
            {draft.auth === "oauth" && draft.url.trim() !== server.url ? <small>Changing this URL requires signing in again.</small> : null}
          </label>
          <ServerAuthFields editing requireKey={requireKey} auth={draft.auth} header={draft.header} secret={draft.secret}
            onAuth={auth => patch({ auth })} onHeader={header => patch({ header })} onSecret={secret => patch({ secret })} />
        </> : <>
          <label class="field"><span>Command</span><input class="input mono" required value={draft.command} onInput={event => patch({ command: event.currentTarget.value })} /></label>
          <label class="field"><span>Arguments</span><input class="input mono" value={draft.args} placeholder="unchanged" onInput={event => patch({ args: event.currentTarget.value, argsTouched: true })} /><small>Space-separated. Leave untouched to keep stored arguments; clear to remove.</small></label>
          <label class="field"><span>Environment</span><textarea class="input mono" disabled={draft.clearEnv} value={draft.env} placeholder="unchanged" onInput={event => patch({ env: event.currentTarget.value })} /><small>KEY=value per line replaces the stored environment.</small></label>
          <label class="field"><span><input type="checkbox" checked={draft.clearEnv} onChange={event => patch({ clearEnv: event.currentTarget.checked })} /> Clear stored environment</span></label>
        </>}
      </fieldset>
    </Screen>
  </form>;
}
