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
    .map((b) => b.namespace || b.display_name || b.id)
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
  const agentList = agents.data.value || [];
  // Member count: agents that name this group via id, name, or display_name.
  const memberCount = (g: Group) => {
    const keys = [g.id, g.name, g.display_name].filter((v): v is string => !!v);
    return agentList.filter((a) =>
      (a.policy?.groups || []).some((x) => keys.includes(x)),
    ).length;
  };

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
      <div class="group-list">
        {gr.map((g) => (
          <GroupRow key={g.id || g.name} group={g} members={memberCount(g)} />
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

function GroupRow({ group, members }: { group: Group; members: number }) {
  const [editing, setEditing] = useState(false);
  // Identity routing: ULID is the canonical key — name is the legacy
  // fallback for config-sourced groups that pre-date the dispatcher.
  const routeKey = group.id || group.name;
  const label = group.display_name || group.name;
  const isConfig = group.source === "config";
  const mutate = canMutate() && !isConfig;

  const commit = async (next: string[]) => {
    setEditing(false);
    await withToast(async () => {
      await putJSON(`/groups/${encodeURIComponent(routeKey)}`, {
        scopes: next,
      });
      await groups.refresh();
      await agents.refresh();
    });
  };

  const remove = async (e: MouseEvent) => {
    e.stopPropagation();
    if (!confirm(`Delete group "${label}"?`)) return;
    await withToast(async () => {
      await deleteJSON(`/groups/${encodeURIComponent(routeKey)}`);
      await groups.refresh();
      await agents.refresh();
    });
  };

  const detailHref = `/policy/groups/${encodeURIComponent(routeKey)}`;

  if (editing) {
    return (
      <div class="group-row group-row-editing">
        <div class="group-row-main">
          <div class="group-row-header">
            <span class={`status-pip ${isConfig ? "status-pip-neutral" : "status-pip-ok"}`} />
            <span class="group-row-name">{label}</span>
          </div>
          <div style="margin-top:8px">
            <ScopeEditor
              initial={group.scopes}
              hints={namespaceHints()}
              inputWidth="120px"
              onCommit={commit}
              onCancel={() => setEditing(false)}
            />
          </div>
        </div>
      </div>
    );
  }

  return (
    <a class="group-row" href={detailHref}>
      <div class="group-row-main">
        <div class="group-row-header">
          <span class={`status-pip ${isConfig ? "status-pip-neutral" : "status-pip-ok"}`} />
          <span class="group-row-name">{label}</span>
          {group.id && group.id !== label && (
            <span class="group-row-ns" title={group.id}>
              / {group.id.slice(0, 8)}…
            </span>
          )}
          <span class="group-row-source">
            {isConfig ? "config" : "dynamic"}
          </span>
        </div>
        <div class="group-row-meta">
          {group.scopes.length === 0 ? (
            <span class="hint-text">no scopes</span>
          ) : (
            group.scopes.slice(0, 6).map((s) => (
              <span class="group-row-scope" key={s}>
                {s}
              </span>
            ))
          )}
          {group.scopes.length > 6 && (
            <span class="hint-text">+ {group.scopes.length - 6} more</span>
          )}
        </div>
      </div>
      <div class="group-row-stats">
        <div class="group-row-stat">
          <div class="group-row-stat-value">{group.scopes.length}</div>
          <div class="group-row-stat-label">
            scope{group.scopes.length === 1 ? "" : "s"}
          </div>
        </div>
        <div class="group-row-stat">
          <div class="group-row-stat-value">{members}</div>
          <div class="group-row-stat-label">
            member{members === 1 ? "" : "s"}
          </div>
        </div>
        {mutate && (
          <div class="group-row-actions">
            <button
              class="group-action"
              onClick={(e) => {
                e.preventDefault();
                e.stopPropagation();
                setEditing(true);
              }}
              title="edit scopes"
            >
              edit
            </button>
            <button
              class="group-action delete"
              onClick={remove}
              title="delete group"
            >
              ×
            </button>
          </div>
        )}
      </div>
    </a>
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
        <span class="section-title">backend policy</span>
        <span class="section-sub">
          per-backend workspace selection and rate limits. layers stack agent →
          groups (alphabetical) → defaults → backend static floor.
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
          {editableGroups.map((g) => {
            const routeKey = g.id || g.name;
            const label = g.display_name || g.name;
            return (
              <StoragePolicyLayer
                key={routeKey}
                title={`group: ${label}`}
                policies={g.backend_policies}
                backends={backendList}
                workspaces={workspaces}
                readOnly={!mutate}
                onSave={(next) =>
                  savePolicies(label, () =>
                    putJSON(
                      `/groups/${encodeURIComponent(routeKey)}/backend-policies`,
                      next,
                    ),
                  )
                }
              />
            );
          })}
        </div>
      )}

      <AgentOverridesSubsection />
    </div>
  );
}

function AgentOverridesSubsection() {
  const list = agents.data.value || [];
  const overrides = list.filter((a) => {
    const bp = a.policy?.backend_policies || {};
    return Object.keys(bp).length > 0;
  });

  return (
    <div class="storage-layer">
      <div class="storage-layer-head">
        <span class="storage-layer-title">per-agent overrides</span>
        <span class="storage-layer-sub">
          edit on each agent's detail page. listed here so you can see who
          deviates from group/defaults.
        </span>
      </div>
      {overrides.length === 0 ? (
        <div class="empty-state">
          no agents have per-agent backend overrides.
        </div>
      ) : (
        <div class="storage-layer-rows">
          {overrides.map((a) => {
            const bp = a.policy?.backend_policies || {};
            const ids = Object.keys(bp).sort();
            const name = a.label || a.description || a.client_id;
            const route = a.prism_id || a.client_id;
            const backendLabel = (id: string) => {
              const b = (backends.data.value || []).find((x) => x.id === id);
              return b?.display_name || b?.namespace || id;
            };
            return (
              <div class="storage-layer-row" key={route}>
                <a
                  class="storage-layer-backend link-accent"
                  href={`/agents/${encodeURIComponent(route)}`}
                >
                  {name}
                </a>
                <span class="hint-text">
                  {ids
                    .map((id) => {
                      const rule = bp[id];
                      const parts: string[] = [];
                      if (rule.workspace_selector)
                        parts.push(rule.workspace_selector);
                      if (rule.rate_limit?.rps)
                        parts.push(`${rule.rate_limit.rps} rps`);
                      return `${backendLabel(id)}: ${parts.join(" · ") || "—"}`;
                    })
                    .join("    ")}
                </span>
              </div>
            );
          })}
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
  backends: { id: string; display_name?: string; namespace?: string }[];
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

  const updateRule = (
    backendID: string,
    patch: (prev: BackendPolicy) => BackendPolicy,
  ) => {
    const next = { ...draft };
    const updated = patch(next[backendID] || {});
    if (!updated.workspace_selector && !updated.rate_limit) {
      delete next[backendID];
    } else {
      next[backendID] = updated;
    }
    setDraft(next);
    setDirty(true);
  };

  const setSelector = (backendID: string, kind: Selector, id: string) => {
    updateRule(backendID, (prev) => {
      const selector =
        kind === ""
          ? undefined
          : kind === "id"
            ? id
              ? `id:${id}`
              : ""
            : kind;
      return { ...prev, workspace_selector: selector };
    });
  };

  const setRPS = (backendID: string, raw: string) => {
    updateRule(backendID, (prev) => {
      const n = Number(raw);
      if (!raw.trim() || !Number.isFinite(n) || n <= 0) {
        const next = { ...prev };
        delete next.rate_limit;
        return next;
      }
      return {
        ...prev,
        rate_limit: { rps: n, burst: prev.rate_limit?.burst },
      };
    });
  };

  const setBurst = (backendID: string, raw: string) => {
    updateRule(backendID, (prev) => {
      if (!prev.rate_limit) return prev;
      const n = Number(raw);
      return {
        ...prev,
        rate_limit: {
          rps: prev.rate_limit.rps,
          burst: Number.isFinite(n) && n > 0 ? n : undefined,
        },
      };
    });
  };

  const save = async () => {
    setSaving(true);
    const clean: Record<string, BackendPolicy> = {};
    for (const [id, rule] of Object.entries(draft)) {
      if (rule && (rule.workspace_selector || rule.rate_limit)) {
        clean[id] = rule;
      }
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
            const rule = draft[b.id] || {};
            const parsed = parseSelector(rule.workspace_selector);
            const label = b.display_name || b.namespace || b.id;
            return (
              <div class="storage-layer-row" key={b.id}>
                <span class="storage-layer-backend" title={b.id}>
                  {label}
                </span>
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
                <input
                  type="number"
                  min="0"
                  step="0.1"
                  class="config-input"
                  style="width:90px"
                  value={rule.rate_limit?.rps ?? ""}
                  placeholder="rps"
                  disabled={readOnly || saving}
                  onInput={(e) =>
                    setRPS(b.id, (e.target as HTMLInputElement).value)
                  }
                />
                <input
                  type="number"
                  min="0"
                  step="1"
                  class="config-input"
                  style="width:90px"
                  value={rule.rate_limit?.burst ?? ""}
                  placeholder="burst"
                  disabled={readOnly || saving || !rule.rate_limit}
                  onInput={(e) =>
                    setBurst(b.id, (e.target as HTMLInputElement).value)
                  }
                />
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
