import { useState, useMemo } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { agents, groups, backends } from "../state";
import { deleteJSON, putJSON } from "../api/client";
import { withToast } from "../state/toasts";
import { ScopeEditor } from "../components/ScopeEditor";
import { ScopeList } from "../components/ScopeList";
import { StatusCell } from "../components/StatusCell";
import { splitLabel } from "../util/time";
import type { Agent } from "../api/types";

function namespaceHints(): string[] {
  const ns = (backends.data.value || [])
    .map((b) => b.namespace || b.id)
    .filter(Boolean);
  return ns.map((n) => `${n}:*`).sort();
}

export function GroupDetail() {
  const { params } = useRoute();
  const loc = useLocation();
  const name = decodeURIComponent(params.name);
  const list = groups.data.value || [];
  const group = list.find((g) => g.name === name);
  const ag = agents.data.value || [];

  const members = useMemo<Agent[]>(() => {
    return ag.filter((a) => (a.policy?.groups || []).includes(name));
  }, [ag, name]);

  const [editing, setEditing] = useState(false);

  if (groups.data.value === null) {
    return <Shell title={name}>loading…</Shell>;
  }
  if (!group) {
    return (
      <Shell title={name}>
        <div class="empty-state">
          group not found.{" "}
          <a href="/identity" class="link-accent">
            back to identity
          </a>
        </div>
      </Shell>
    );
  }

  const isConfig = group.source === "config";

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
    if (
      !confirm(
        `Delete group "${group.name}"? Agents currently in this group will lose its scopes.`,
      )
    )
      return;
    const ok = await withToast(async () => {
      await deleteJSON(`/groups/${encodeURIComponent(group.name)}`);
      await groups.refresh();
      await agents.refresh();
    });
    if (ok !== undefined) loc.route("/identity");
  };

  return (
    <div>
      <div class="detail-breadcrumb">
        <a href="/identity">identity</a>
        <span class="breadcrumb-sep">/</span>
        <span>groups</span>
        <span class="breadcrumb-sep">/</span>
        <span class="breadcrumb-current">{group.name}</span>
      </div>

      <div class="detail-header">
        <div>
          <div class="page-title">{group.name}</div>
          <div class="page-subtitle">
            {isConfig
              ? "defined in config · read-only from the console"
              : "dynamic · editable"}
          </div>
        </div>
        <div class="detail-status">
          <span
            class={isConfig ? "pill pill-neutral" : "pill pill-ok"}
          >
            {group.source}
          </span>
        </div>
      </div>

      <div class="meta-row">
        <MetaItem label="source" value={group.source} />
        <MetaItem label="scopes" value={String(group.scopes.length)} />
        <MetaItem label="members" value={String(members.length)} />
      </div>

      <div class="section">
        <div class="section-header">
          <span class="section-title">scopes</span>
          {!isConfig && !editing && (
            <button class="section-btn" onClick={() => setEditing(true)}>
              edit
            </button>
          )}
        </div>
        <div class="card">
          {editing ? (
            <ScopeEditor
              initial={group.scopes}
              hints={namespaceHints()}
              onCommit={commit}
              onCancel={() => setEditing(false)}
            />
          ) : group.scopes.length === 0 ? (
            <div class="empty-state" style="padding:0">
              no scopes assigned. members of this group inherit only the
              default scopes.
            </div>
          ) : (
            <ScopeList scopes={group.scopes} />
          )}
        </div>
      </div>

      <div class="section">
        <div class="section-header">
          <span class="section-title">
            members ({members.length})
          </span>
        </div>
        {members.length === 0 ? (
          <div class="empty-state">
            no agents in this group. assign one from the agent's policy view.
          </div>
        ) : (
          <div class="agent-list">
            {members.map((a) => (
              <MemberRow key={a.client_id} agent={a} groupName={name} />
            ))}
          </div>
        )}
      </div>

      {!isConfig && (
        <div class="section section-danger">
          <div class="section-header">
            <span class="section-title section-title-danger">
              danger zone
            </span>
          </div>
          <div class="card card-danger">
            <div>
              <div class="danger-card-title">delete this group</div>
              <div class="danger-card-desc">
                members lose this group's scopes immediately. their other
                grants and group memberships are preserved.
              </div>
            </div>
            <button class="danger-btn" onClick={remove}>
              delete group
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function Shell({
  title,
  children,
}: {
  title: string;
  children: preact.ComponentChildren;
}) {
  return (
    <div>
      <div class="detail-breadcrumb">
        <a href="/identity">identity</a>
        <span class="breadcrumb-sep">/</span>
        <span>groups</span>
        <span class="breadcrumb-sep">/</span>
        <span class="breadcrumb-current">{title}</span>
      </div>
      <div class="page-header">
        <div>
          <div class="page-title">{title}</div>
        </div>
      </div>
      {children}
    </div>
  );
}

function MetaItem({ label, value }: { label: string; value: string }) {
  return (
    <div class="meta-item">
      <div class="meta-label">{label}</div>
      <div class="meta-value">{value}</div>
    </div>
  );
}

function MemberRow({ agent, groupName }: { agent: Agent; groupName: string }) {
  const loc = useLocation();
  const display = agent.label || agent.description || agent.client_id;
  const [name, ctx] = splitLabel(display);
  const canOpen = agent.dynamic && !!agent.prism_id;

  const onClick = () => {
    if (canOpen) {
      loc.route(`/identity/agents/${encodeURIComponent(agent.prism_id!)}`);
    }
  };

  const removeFromGroup = async (e: MouseEvent) => {
    e.stopPropagation();
    const policy = agent.policy || { groups: [], grant: [], deny: [] };
    await withToast(async () => {
      await putJSON(
        `/agents/${encodeURIComponent(agent.prism_id!)}/policy`,
        {
          groups: (policy.groups || []).filter((g) => g !== groupName),
          grant: policy.grant || [],
          deny: policy.deny || [],
        },
      );
      await agents.refresh();
    });
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
        </div>
        <div class="agent-row-meta">
          {agent.dynamic ? "dynamic agent" : "static agent · config"}
        </div>
      </div>
      <div class="agent-row-right">
        <StatusCell agent={agent} />
        {canOpen && (
          <span
            class="remove-btn"
            role="button"
            onClick={removeFromGroup}
            title="remove from this group"
          >
            remove
          </span>
        )}
      </div>
    </button>
  );
}
