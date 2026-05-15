import { useState } from "preact/hooks";
import {
  agents,
  groups,
  backends,
  defaults,
} from "../state";
import { deleteJSON, putJSON } from "../api/client";
import { fmtAge } from "../util/time";
import { ScopeEditor } from "../components/ScopeEditor";
import { ScopeList } from "../components/ScopeList";
import { StatusCell } from "../components/StatusCell";
import { CopyId } from "../components/CopyId";
import { splitLabel } from "../util/time";
import type { Agent, AgentPolicy, Group } from "../api/types";

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

async function setPolicy(prismID: string, p: AgentPolicy) {
  await putJSON(`/agents/${encodeURIComponent(prismID)}/policy`, p);
  await agents.refresh();
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
          <div class="page-title">Identity</div>
          <div class="page-subtitle">
            {ag.length} agent{ag.length === 1 ? "" : "s"} · {gr.length} group
            {gr.length === 1 ? "" : "s"}
          </div>
        </div>
      </div>

      <DefaultsSection />
      <GroupsSection groups={gr} />
      <AgentsSection agents={ag} groups={gr} />
    </div>
  );
}

function DefaultsSection() {
  const data = defaults.data.value;
  const scopes = visibleScopes(data?.default_scopes);
  const [editing, setEditing] = useState(false);

  const commit = async (next: string[]) => {
    setEditing(false);
    try {
      await putJSON("/defaults", { default_scopes: next });
      await defaults.refresh();
      await agents.refresh();
    } catch {
      await defaults.refresh();
    }
  };

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">Default Scopes</span>
      </div>
      {editing ? (
        <ScopeEditor
          initial={scopes}
          hints={namespaceHints()}
          onCommit={commit}
          onCancel={() => setEditing(false)}
        />
      ) : scopes.length === 0 ? (
        <span class="scope-add-cta" onClick={() => setEditing(true)}>
          + add default scopes
        </span>
      ) : (
        <div
          class="scope-list editable"
          onClick={() => setEditing(true)}
          title="Click to edit"
        >
          {scopes.map((s) => (
            <span class="scope-tag" key={s}>
              {s}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function GroupsSection({ groups: gr }: { groups: Group[] }) {
  const [adding, setAdding] = useState(false);

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">Groups ({gr.length})</span>
        <button class="section-btn" onClick={() => setAdding(true)}>
          + Group
        </button>
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
          No groups defined. Use “+ Group” to create one.
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
    try {
      await putJSON(`/groups/${encodeURIComponent(n)}`, { scopes: [] });
      onDone();
    } catch {
      onCancel();
    }
  };
  return (
    <div class="inline-form">
      <input
        type="text"
        placeholder="group name"
        value={name}
        autoFocus
        spellcheck={false}
        style="width:140px"
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
    try {
      await putJSON(`/groups/${encodeURIComponent(group.name)}`, {
        scopes: next,
      });
      await groups.refresh();
      await agents.refresh();
    } catch {
      await groups.refresh();
    }
  };

  const remove = async () => {
    if (!confirm(`Delete group "${group.name}"?`)) return;
    try {
      await deleteJSON(`/groups/${encodeURIComponent(group.name)}`);
      await groups.refresh();
      await agents.refresh();
    } catch {
      // ignore
    }
  };

  if (group.source === "config") {
    return (
      <div class="group-card">
        <span class="group-name">{group.name}</span>
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
      <span class="group-name">{group.name}</span>
      {group.scopes.map((s) => (
        <span class="group-scope" key={s}>
          {s}
        </span>
      ))}
      <button class="group-action" onClick={() => setEditing(true)}>
        edit
      </button>
      <button class="group-action delete" onClick={remove}>
        ×
      </button>
    </div>
  );
}

function AgentsSection({
  agents: ag,
  groups: gr,
}: {
  agents: Agent[];
  groups: Group[];
}) {
  const cleanStale = async () => {
    try {
      await deleteJSON("/agents/stale");
      await agents.refresh();
    } catch {
      // ignore
    }
  };

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">Agents ({ag.length})</span>
        <button class="section-btn" onClick={cleanStale}>
          Clean stale
        </button>
      </div>
      {ag.length === 0 ? (
        <div class="empty-state">
          No agents registered. Agents self-register via OAuth DCR.
        </div>
      ) : (
        <table>
          <thead>
            <tr>
              <th style="width:40%">Agent</th>
              <th style="width:14%">Last seen</th>
              <th>Policy</th>
              <th style="width:6%"></th>
            </tr>
          </thead>
          <tbody>
            {ag.map((a) => (
              <AgentRow key={a.client_id} agent={a} groups={gr} />
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function AgentRow({ agent: a, groups: gr }: { agent: Agent; groups: Group[] }) {
  const [expanded, setExpanded] = useState(false);
  const display = a.label || a.description || a.client_id;
  const [name, ctx] = splitLabel(display);
  const canExpand = a.dynamic && !!a.prism_id && !!a.breakdown;
  const copyVal = a.prism_id || a.client_id;
  const copyLabel = a.prism_id ? "id" : "cid";

  const removeAgent = async (e: MouseEvent) => {
    e.stopPropagation();
    try {
      await deleteJSON(`/agents/${encodeURIComponent(a.client_id)}`);
      await agents.refresh();
    } catch {
      // ignore
    }
  };

  return (
    <>
      <tr
        style={canExpand ? "cursor:pointer" : undefined}
        onClick={() => canExpand && setExpanded((v) => !v)}
      >
        <td>
          <div class="agent-name-row">
            {canExpand && (
              <span class="expand-indicator">
                {expanded ? "▾" : "▸"}
              </span>
            )}
            <span class="agent-label">{name}</span>
            {ctx && <span class="agent-ctx">{ctx}</span>}
            <CopyId value={copyVal} label={copyLabel} />
          </div>
        </td>
        <td>
          <StatusCell agent={a} />
        </td>
        <td>
          {a.dynamic ? (
            <span class="policy-summary">{policyLabel(a)}</span>
          ) : (
            <span class="scope-lock">config · {fmtAge(a.created_at)}</span>
          )}
        </td>
        <td class="right">
          {a.dynamic && (
            <button class="remove-btn" onClick={removeAgent}>
              remove
            </button>
          )}
        </td>
      </tr>
      {expanded && a.breakdown && (
        <tr>
          <td colspan={4} style="padding:0">
            <AgentBreakdown agent={a} groups={gr} />
          </td>
        </tr>
      )}
    </>
  );
}

function AgentBreakdown({
  agent: a,
  groups: gr,
}: {
  agent: Agent;
  groups: Group[];
}) {
  const bd = a.breakdown!;
  const defaultsList = visibleScopes(bd.defaults).sort();
  const effective = visibleScopes(bd.effective).sort();
  const grants = bd.grants || [];
  const denies = bd.denies || [];
  const groupNames = Object.keys(bd.groups || {});
  const policy: AgentPolicy = a.policy || { groups: [], grant: [], deny: [] };
  const prismID = a.prism_id!;

  const removeGroup = (name: string) =>
    setPolicy(prismID, {
      groups: (policy.groups || []).filter((g) => g !== name),
      grant: policy.grant || [],
      deny: policy.deny || [],
    });

  const addGroup = (name: string) =>
    setPolicy(prismID, {
      groups: [...(policy.groups || []), name],
      grant: policy.grant || [],
      deny: policy.deny || [],
    });

  const removeGrant = (s: string) =>
    setPolicy(prismID, {
      groups: policy.groups || [],
      grant: (policy.grant || []).filter((x) => x !== s),
      deny: policy.deny || [],
    });

  const removeDeny = (s: string) =>
    setPolicy(prismID, {
      groups: policy.groups || [],
      grant: policy.grant || [],
      deny: (policy.deny || []).filter((x) => x !== s),
    });

  const addGrant = (s: string) =>
    setPolicy(prismID, {
      groups: policy.groups || [],
      grant: [...(policy.grant || []), s],
      deny: policy.deny || [],
    });

  const addDeny = (s: string) =>
    setPolicy(prismID, {
      groups: policy.groups || [],
      grant: policy.grant || [],
      deny: [...(policy.deny || []), s],
    });

  const [groupDropdown, setGroupDropdown] = useState(false);
  const [grantInput, setGrantInput] = useState(false);
  const [denyInput, setDenyInput] = useState(false);

  const availableGroups = gr
    .map((g) => g.name)
    .filter((n) => !groupNames.includes(n));

  return (
    <div class="policy-breakdown">
      <div class="policy-row">
        <span class="policy-label">defaults</span>
        <div class="policy-scopes">
          <ScopeList scopes={defaultsList} empty="none" />
        </div>
      </div>

      <div class="policy-row">
        <span class="policy-label">groups</span>
        <div class="policy-scopes">
          {groupNames.map((g) => (
            <span
              class="group-chip"
              key={g}
              title="Click to remove"
              onClick={() => removeGroup(g)}
            >
              {g}
            </span>
          ))}
          {groupDropdown ? (
            availableGroups.length === 0 ? (
              <span
                style="font-size:10px;color:var(--muted);font-style:italic"
                onClick={() => setGroupDropdown(false)}
              >
                no available groups
              </span>
            ) : (
              <div style="position:relative">
                <div class="group-dropdown">
                  {availableGroups.map((g) => (
                    <div
                      class="group-dropdown-item"
                      key={g}
                      onMouseDown={(e) => {
                        e.preventDefault();
                        setGroupDropdown(false);
                        addGroup(g);
                      }}
                    >
                      {g}
                    </div>
                  ))}
                </div>
              </div>
            )
          ) : (
            <button
              class="add-btn"
              onClick={() => setGroupDropdown(true)}
            >
              + group
            </button>
          )}
        </div>
      </div>

      <div class="policy-row">
        <span class="policy-label">grants</span>
        <div class="policy-scopes">
          {grants.map((s) => (
            <span
              class="scope-tag removable"
              key={s}
              title="Click to remove"
              onClick={() => removeGrant(s)}
            >
              {s} <span class="x">x</span>
            </span>
          ))}
          {grantInput ? (
            <ScopeAddInput
              hints={namespaceHints()}
              onSubmit={(s) => {
                setGrantInput(false);
                addGrant(s);
              }}
              onCancel={() => setGrantInput(false)}
            />
          ) : (
            <button class="add-btn" onClick={() => setGrantInput(true)}>
              + grant
            </button>
          )}
        </div>
      </div>

      <div class="policy-row">
        <span class="policy-label">denies</span>
        <div class="policy-scopes">
          {denies.map((s) => (
            <span
              class="scope-tag removable denied"
              key={s}
              title="Click to remove"
              onClick={() => removeDeny(s)}
            >
              {s} <span class="x">x</span>
            </span>
          ))}
          {denyInput ? (
            <ScopeAddInput
              hints={namespaceHints()}
              onSubmit={(s) => {
                setDenyInput(false);
                addDeny(s);
              }}
              onCancel={() => setDenyInput(false)}
            />
          ) : (
            <button class="add-btn" onClick={() => setDenyInput(true)}>
              + deny
            </button>
          )}
        </div>
      </div>

      <div class="policy-row policy-effective">
        <span class="policy-label">effective</span>
        <div class="policy-scopes">
          <ScopeList scopes={effective} empty="none" />
        </div>
      </div>
    </div>
  );
}

function ScopeAddInput({
  hints,
  onSubmit,
  onCancel,
}: {
  hints: string[];
  onSubmit: (s: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState("");
  const matches = (() => {
    const v = value.toLowerCase();
    if (!v) return [];
    return hints.filter((h) => h.toLowerCase().includes(v)).slice(0, 6);
  })();
  return (
    <span class="inline-form" style="display:inline-flex;position:relative">
      <input
        type="text"
        placeholder="scope"
        autoFocus
        style="width:140px"
        value={value}
        spellcheck={false}
        onInput={(e) => setValue((e.target as HTMLInputElement).value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            const v = value.trim();
            if (v) onSubmit(v);
            else onCancel();
          }
          if (e.key === "Escape") onCancel();
        }}
        onBlur={() => setTimeout(onCancel, 150)}
      />
      {matches.length > 0 && (
        <div class="scope-suggest">
          {matches.map((m) => (
            <div
              key={m}
              class="scope-suggest-item"
              onMouseDown={(e) => {
                e.preventDefault();
                onSubmit(m);
              }}
            >
              {m}
            </div>
          ))}
        </div>
      )}
    </span>
  );
}
