import { useEffect, useMemo, useState } from "preact/hooks";
import { agents, groups, backends, defaults } from "../state";
import { deleteJSON, getJSON, putJSON } from "../api/client";
import { withToast } from "../state/toasts";
import { canMutate } from "../state/me";
import { ScopeEditor } from "../components/ScopeEditor";
import type {
  BackendPolicy,
  Group,
  Workspace,
} from "../api/types";

const SYSTEM_SCOPE = "mcp:connect";

function namespaceHints(): string[] {
  const ns = (backends.data.value || [])
    .map((b) => b.namespace || b.id)
    .filter(Boolean);
  return ns.map((n) => `${n}:*`).sort();
}

function visibleScopes(scopes: string[] | undefined): string[] {
  return (scopes || []).filter((s) => s !== SYSTEM_SCOPE);
}

export function Policy() {
  const gr = (groups.data.value || []).slice().sort((a, b) =>
    a.name.localeCompare(b.name),
  );

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">policy</div>
          <div class="page-subtitle">
            {gr.length} group{gr.length === 1 ? "" : "s"} · default scopes
            applied to every agent unless overridden
          </div>
        </div>
      </div>

      <DefaultsSection />
      <GroupsSection groups={gr} />
      <StoragePolicySection groups={gr} />
    </div>
  );
}

function DefaultsSection() {
  const data = defaults.data.value;
  const scopes = visibleScopes(data?.default_scopes);
  const [editing, setEditing] = useState(false);

  const commit = async (next: string[]) => {
    setEditing(false);
    await withToast(async () => {
      await putJSON("/defaults", { default_scopes: next });
      await defaults.refresh();
      await agents.refresh();
    });
  };

  const mutate = canMutate();

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">default scopes</span>
        <span class="section-sub">
          applied to every agent unless overridden
        </span>
      </div>
      <div class="card">
        {editing && mutate ? (
          <ScopeEditor
            initial={scopes}
            hints={namespaceHints()}
            onCommit={commit}
            onCancel={() => setEditing(false)}
          />
        ) : scopes.length === 0 ? (
          mutate ? (
            <span class="scope-add-cta" onClick={() => setEditing(true)}>
              + add default scopes
            </span>
          ) : (
            <span class="hint-text">no defaults</span>
          )
        ) : (
          <div
            class={mutate ? "scope-list editable" : "scope-list"}
            onClick={mutate ? () => setEditing(true) : undefined}
            title={mutate ? "click to edit" : undefined}
          >
            {scopes.map((s) => (
              <span class="scope-tag" key={s}>
                {s}
              </span>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function GroupsSection({ groups: gr }: { groups: Group[] }) {
  const [adding, setAdding] = useState(false);

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">groups ({gr.length})</span>
        {canMutate() && (
          <button class="section-btn" onClick={() => setAdding(true)}>
            + group
          </button>
        )}
      </div>
      <div class="groups-list">
        {gr.map((g) => (
          <GroupCard key={g.name} group={g} />
        ))}
        {adding && (
          <AddGroupForm
            onDone={async () => {
              setAdding(false);
              await groups.refresh();
            }}
            onCancel={() => setAdding(false)}
          />
        )}
      </div>
      {gr.length === 0 && !adding && (
        <div class="empty-state" style="padding-top:8px">
          no groups defined. groups bundle scopes so agents can be assigned
          by role rather than individual permissions.
        </div>
      )}
    </div>
  );
}

function AddGroupForm({
  onDone,
  onCancel,
}: {
  onDone: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState("");
  const submit = async () => {
    const n = name.trim();
    if (!n) {
      onCancel();
      return;
    }
    const ok = await withToast(async () => {
      await putJSON(`/groups/${encodeURIComponent(n)}`, { scopes: [] });
    });
    if (ok !== undefined) onDone();
    else onCancel();
  };
  return (
    <div class="inline-form">
      <input
        type="text"
        placeholder="group name"
        value={name}
        autoFocus
        spellcheck={false}
        style="width:160px"
        onInput={(e) => setName((e.target as HTMLInputElement).value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") submit();
          if (e.key === "Escape") onCancel();
        }}
      />
      <button class="save-btn" onClick={submit}>
        create
      </button>
      <button class="cancel-btn" onClick={onCancel}>
        cancel
      </button>
    </div>
  );
}

function GroupCard({ group }: { group: Group }) {
  const [editing, setEditing] = useState(false);

  const commit = async (next: string[]) => {
    setEditing(false);
    await withToast(async () => {
      await putJSON(`/groups/${encodeURIComponent(group.name)}`, {
        scopes: next,
      });
      await groups.refresh();
      await agents.refresh();
    });
  };

  const remove = async () => {
    if (!confirm(`Delete group "${group.name}"?`)) return;
    await withToast(async () => {
      await deleteJSON(`/groups/${encodeURIComponent(group.name)}`);
      await groups.refresh();
      await agents.refresh();
    });
  };

  const detailHref = `/policy/groups/${encodeURIComponent(group.name)}`;

  if (group.source === "config") {
    return (
      <div class="group-card">
        <a href={detailHref} class="group-name group-name-link">
          {group.name}
        </a>
        {group.scopes.map((s) => (
          <span class="group-scope" key={s}>
            {s}
          </span>
        ))}
        <span class="group-source">config</span>
      </div>
    );
  }

  if (editing) {
    return (
      <div class="group-card">
        <span class="group-name">{group.name}</span>
        <ScopeEditor
          initial={group.scopes}
          hints={namespaceHints()}
          inputWidth="100px"
          onCommit={commit}
          onCancel={() => setEditing(false)}
        />
      </div>
    );
  }

  return (
    <div class="group-card">
      <a href={detailHref} class="group-name group-name-link">
        {group.name}
      </a>
      {group.scopes.length === 0 ? (
        <span class="hint-text">no scopes</span>
      ) : (
        group.scopes.map((s) => (
          <span class="group-scope" key={s}>
            {s}
          </span>
        ))
      )}
      {canMutate() && (
        <>
          <button class="group-action" onClick={() => setEditing(true)}>
            edit
          </button>
          <button class="group-action delete" onClick={remove}>
            ×
          </button>
        </>
      )}
    </div>
  );
}

// ───────────────────────── Storage policy section ─────────────────────────

interface DefaultsView {
  default_scopes?: string[];
  backend_policies?: Record<string, BackendPolicy>;
}

function StoragePolicySection({ groups: gr }: { groups: Group[] }) {
  const [defaultsView, setDefaultsView] = useState<DefaultsView | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const backendList = backends.data.value || [];
  const mutate = canMutate();

  const load = async () => {
    try {
      const [d, ws] = await Promise.all([
        getJSON<DefaultsView>("/defaults"),
        getJSON<Workspace[]>("/workspaces"),
      ]);
      setDefaultsView(d);
      setWorkspaces(ws);
    } catch {
      // Silent — Policy page is read-mostly; surface errors only on edit.
    }
  };

  useEffect(() => {
    load();
  }, []);

  const editableGroups = useMemo(
    () => gr.filter((g) => g.source !== "config"),
    [gr],
  );

  const savePolicies = async (
    layerLabel: string,
    saver: () => Promise<void>,
  ) => {
    await withToast(async () => {
      await saver();
      await load();
      await groups.refresh();
    });
    return layerLabel;
  };

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">storage policy</span>
        <span class="section-sub">
          per-backend workspace selection. layers stack agent → groups
          (alphabetical) → defaults → backend static floor.
        </span>
      </div>

      <StoragePolicyLayer
        title="defaults"
        sub="applied to every agent unless a group or agent rule overrides"
        policies={defaultsView?.backend_policies}
        backends={backendList}
        workspaces={workspaces}
        readOnly={!mutate}
        onSave={(next) =>
          savePolicies("defaults", () =>
            putJSON("/defaults/backend-policies", next),
          )
        }
      />

      {editableGroups.length === 0 ? null : (
        <div class="storage-group-layers">
          {editableGroups.map((g) => (
            <StoragePolicyLayer
              key={g.name}
              title={`group: ${g.name}`}
              policies={g.backend_policies}
              backends={backendList}
              workspaces={workspaces}
              readOnly={!mutate}
              onSave={(next) =>
                savePolicies(g.name, () =>
                  putJSON(
                    `/groups/${encodeURIComponent(g.name)}/backend-policies`,
                    next,
                  ),
                )
              }
            />
          ))}
        </div>
      )}
    </div>
  );
}

type Selector = "" | "static" | "agent" | "id";

function parseSelector(raw: string | undefined): {
  kind: Selector;
  id: string;
} {
  if (!raw) return { kind: "", id: "" };
  if (raw === "static" || raw === "agent") return { kind: raw, id: "" };
  if (raw.startsWith("id:"))
    return { kind: "id", id: raw.slice("id:".length) };
  return { kind: "", id: "" };
}

function StoragePolicyLayer({
  title,
  sub,
  policies,
  backends: backendList,
  workspaces,
  readOnly,
  onSave,
}: {
  title: string;
  sub?: string;
  policies: Record<string, BackendPolicy> | undefined;
  backends: { id: string }[];
  workspaces: Workspace[];
  readOnly: boolean;
  onSave: (next: Record<string, BackendPolicy>) => Promise<unknown>;
}) {
  const [draft, setDraft] = useState<Record<string, BackendPolicy>>(
    policies || {},
  );
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setDraft(policies || {});
    setDirty(false);
  }, [JSON.stringify(policies || {})]);

  const virtualOptions = workspaces.filter((w) => w.type === "virtual");

  const setSelector = (backendID: string, kind: Selector, id: string) => {
    const next = { ...draft };
    if (kind === "") {
      delete next[backendID];
    } else if (kind === "id") {
      next[backendID] = { workspace_selector: id ? `id:${id}` : "" };
    } else {
      next[backendID] = { workspace_selector: kind };
    }
    setDraft(next);
    setDirty(true);
  };

  const save = async () => {
    setSaving(true);
    // Drop empty / cleared entries.
    const clean: Record<string, BackendPolicy> = {};
    for (const [id, rule] of Object.entries(draft)) {
      if (rule && rule.workspace_selector) clean[id] = rule;
    }
    await onSave(clean);
    setSaving(false);
    setDirty(false);
  };

  return (
    <div class="storage-layer">
      <div class="storage-layer-head">
        <span class="storage-layer-title">{title}</span>
        {sub && <span class="storage-layer-sub">{sub}</span>}
      </div>
      {backendList.length === 0 ? (
        <div class="empty-state">no backends registered yet.</div>
      ) : (
        <div class="storage-layer-rows">
          {backendList.map((b) => {
            const parsed = parseSelector(draft[b.id]?.workspace_selector);
            return (
              <div class="storage-layer-row" key={b.id}>
                <span class="storage-layer-backend">{b.id}</span>
                <select
                  class="config-input"
                  value={parsed.kind}
                  disabled={readOnly || saving}
                  onChange={(e) => {
                    const kind = (e.target as HTMLSelectElement).value as Selector;
                    setSelector(b.id, kind, parsed.id);
                  }}
                >
                  <option value="">inherit</option>
                  <option value="static">static (backend floor)</option>
                  <option value="agent">agent (bring your own)</option>
                  <option value="id">id (pin workspace)</option>
                </select>
                {parsed.kind === "id" && (
                  <select
                    class="config-input"
                    value={parsed.id}
                    disabled={readOnly || saving}
                    onChange={(e) =>
                      setSelector(b.id, "id", (e.target as HTMLSelectElement).value)
                    }
                  >
                    <option value="">— select workspace —</option>
                    {virtualOptions.map((w) => (
                      <option value={w.id} key={w.id}>
                        {w.id}
                      </option>
                    ))}
                  </select>
                )}
              </div>
            );
          })}
        </div>
      )}
      {!readOnly && (
        <div class="config-actions">
          <button class="save-btn" onClick={save} disabled={!dirty || saving}>
            {saving ? "saving…" : "save"}
          </button>
          {dirty && !saving && (
            <span class="config-dirty-marker">unsaved changes</span>
          )}
        </div>
      )}
    </div>
  );
}
