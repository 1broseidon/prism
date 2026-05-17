import { useState } from "preact/hooks";
import { agents, groups, backends, defaults } from "../state";
import { deleteJSON, putJSON } from "../api/client";
import { withToast } from "../state/toasts";
import { canMutate } from "../state/me";
import { ScopeEditor } from "../components/ScopeEditor";
import type { Group } from "../api/types";

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
