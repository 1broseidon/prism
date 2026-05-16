import { useMemo, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { agents, groups, backends, defaults } from "../state";
import { deleteJSON, putJSON } from "../api/client";
import { withToast } from "../state/toasts";
import { canMutate } from "../state/me";
import { ScopeEditor } from "../components/ScopeEditor";
import { StatusCell } from "../components/StatusCell";
import { CopyId } from "../components/CopyId";
import { splitLabel } from "../util/time";
import type { Agent, Group } from "../api/types";

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

function policyLabel(a: Agent): string {
  if (!a.policy) return "defaults";
  const parts: string[] = [];
  const g = a.policy.groups || [];
  const gr = a.policy.grant || [];
  const de = a.policy.deny || [];
  if (g.length) parts.push(g.join(", "));
  if (gr.length) parts.push(`+${gr.length} grant${gr.length > 1 ? "s" : ""}`);
  if (de.length) parts.push(`−${de.length} deny`);
  return parts.join("  ") || "defaults";
}

export function Identity() {
  const ag = (agents.data.value || []).slice().sort((a, b) =>
    (a.label || a.description || a.client_id).localeCompare(
      b.label || b.description || b.client_id,
    ),
  );
  const gr = (groups.data.value || []).slice().sort((a, b) =>
    a.name.localeCompare(b.name),
  );

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">identity</div>
          <div class="page-subtitle">
            {ag.length} agent{ag.length === 1 ? "" : "s"} · {gr.length} group
            {gr.length === 1 ? "" : "s"}
          </div>
        </div>
      </div>

      <DefaultsSection />
      <GroupsSection groups={gr} />
      <AgentsSection agents={ag} />
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

  const detailHref = `/identity/groups/${encodeURIComponent(group.name)}`;

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

function AgentsSection({ agents: ag }: { agents: Agent[] }) {
  const [query, setQuery] = useState("");

  const filtered = useMemo(() => {
    const q = query.toLowerCase().trim();
    if (!q) return ag;
    return ag.filter((a) => {
      const name = (a.label || a.description || a.client_id).toLowerCase();
      return (
        name.includes(q) ||
        a.client_id.toLowerCase().includes(q) ||
        (a.prism_id || "").toLowerCase().includes(q)
      );
    });
  }, [ag, query]);

  const cleanStale = async () => {
    await withToast(async () => {
      await deleteJSON("/agents/stale");
      await agents.refresh();
    });
  };

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">agents ({ag.length})</span>
        <div class="section-actions">
          {ag.length > 0 && (
            <input
              type="search"
              class="search-input"
              placeholder="search agents…"
              value={query}
              onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
            />
          )}
          {canMutate() && (
            <button class="section-btn" onClick={cleanStale}>
              clean stale
            </button>
          )}
        </div>
      </div>
      {ag.length === 0 ? (
        <div class="empty-callout">
          <div class="empty-callout-title">no agents yet</div>
          <div class="empty-callout-body">
            agents authenticate to prism via oauth client-credentials. they
            either register dynamically (dcr) or are defined in config under
            policy.agents.
          </div>
        </div>
      ) : filtered.length === 0 ? (
        <div class="empty-state">no agents match “{query}”.</div>
      ) : (
        <div class="agent-list">
          {filtered.map((a) => (
            <AgentRow key={a.client_id} agent={a} />
          ))}
        </div>
      )}
    </div>
  );
}

function AgentRow({ agent: a }: { agent: Agent }) {
  const loc = useLocation();
  const display = a.label || a.description || a.client_id;
  const [name, ctx] = splitLabel(display);
  const canOpen = a.dynamic && !!a.prism_id;
  const copyVal = a.prism_id || a.client_id;
  const copyLabel = a.prism_id ? "id" : "cid";

  const onClick = () => {
    if (canOpen) {
      loc.route(`/identity/agents/${encodeURIComponent(a.prism_id!)}`);
    }
  };

  return (
    <button
      class={canOpen ? "agent-row" : "agent-row agent-row-static"}
      onClick={onClick}
      disabled={!canOpen}
    >
      <div class="agent-row-main">
        <div class="agent-name-row">
          <span class="agent-label">{name}</span>
          {ctx && <span class="agent-ctx">{ctx}</span>}
          <CopyId value={copyVal} label={copyLabel} />
        </div>
        <div class="agent-row-meta">
          {a.dynamic ? (
            <span class="policy-summary">{policyLabel(a)}</span>
          ) : (
            <span class="scope-lock">config-managed</span>
          )}
        </div>
      </div>
      <div class="agent-row-right">
        <StatusCell agent={a} />
        {canOpen && <span class="server-row-chevron">›</span>}
      </div>
    </button>
  );
}
