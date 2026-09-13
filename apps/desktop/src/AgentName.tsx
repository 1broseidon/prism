import { signal } from "@preact/signals";
import { useRef, useState } from "preact/hooks";
import * as api from "./api";
import { agents, errorMessage, signins } from "./state";
import type { AgentConfig } from "./types";
import { Button, Label, describeError } from "./ui";

const renaming = signal<Record<string, boolean>>({});

export function AgentName({ agent }: { agent: AgentConfig }) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(agent.name);
  const lastError = useRef<string | null>(null);
  const clearError = () => { if (lastError.current && errorMessage.value === lastError.current) errorMessage.value = null; lastError.current = null; };
  const cancel = () => { clearError(); setEditing(false); };
  const busy = renaming.value[agent.id] ?? false;
  const stored = agents.value.some(item => item.id === agent.id);
  const save = async () => {
    if (renaming.value[agent.id]) return;
    clearError();
    renaming.value = { ...renaming.value, [agent.id]: true };
    try {
      const saved = await api.renameAgent(agent.id, name);
      // Only the label was changed; don't overwrite newer policy or connection state.
      agents.value = agents.value.map(item => item.id === saved.id ? { ...item, name: saved.name } : item);
      signins.value = signins.value.map(item => item.agent_id === saved.id ? { ...item, agent_name: saved.name } : item);
      setName(saved.name); setEditing(false);
    } catch (error) { lastError.current = describeError(error); errorMessage.value = lastError.current; }
    finally { const next = { ...renaming.value }; delete next[agent.id]; renaming.value = next; }
  };
  return <section class="section">
    <Label right={!editing && stored ? <Button variant="quiet" busy={busy} onClick={() => { setName(agent.name); setEditing(true); }}>Rename</Button> : undefined}>Display name</Label>
    {editing ? <form onSubmit={event => { event.preventDefault(); void save(); }} onKeyDown={event => {
      if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); if (!renaming.value[agent.id]) cancel(); }
    }}>
      <label class="field"><input class="input" aria-label="Agent display name" autoFocus required maxLength={160} value={name} disabled={busy} onInput={event => setName(event.currentTarget.value)} /></label>
      <div class="actions"><Button type="submit" variant="primary" busy={busy} disabled={!name.trim() || name.trim() === agent.name}>Save</Button><Button busy={busy} onClick={cancel}>Cancel</Button></div>
    </form> : <div class="setting-title truncate" title={agent.name}>{agent.name}</div>}
    <p class="hint">Client identity: <span class="mono">{agent.client_name}</span></p>
  </section>;
}
