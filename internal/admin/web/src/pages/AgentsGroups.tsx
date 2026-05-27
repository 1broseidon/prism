// AgentsGroups — the Groups tab on /agents. Lists every group with its
// member count, lets operators add new dynamic groups, and links into the
// per-group detail page for membership management.
//
// Identity, not authorization: this page never edits capabilities. The
// per-group detail page (AgentsGroupDetail) has a "Edit policy →" cross-
// link to /policy/groups/:name for that.

import { useMemo, useState } from "preact/hooks";
import { groups, agents } from "../state";
import { putJSON } from "../api/client";
import { withToast } from "../state/toasts";
import { canMutate } from "../state/me";
import { AgentsShell } from "./Agents";
import type { Group } from "../api/types";

export function AgentsGroups() {
  const groupList = (groups.data.value || []).slice().sort((a, b) =>
    a.name.localeCompare(b.name),
  );
  const agentList = agents.data.value || [];
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState(false);
  const [newName, setNewName] = useState("");

  const memberCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const a of agentList) {
      for (const g of a.policy?.groups ?? []) {
        counts.set(g, (counts.get(g) ?? 0) + 1);
      }
    }
    return counts;
  }, [agentList]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return groupList;
    return groupList.filter((g) => g.name.toLowerCase().includes(q));
  }, [groupList, query]);

  const submit = async () => {
    const name = newName.trim();
    if (!name) {
      setAdding(false);
      return;
    }
    await withToast(async () => {
      await putJSON(`/groups/${encodeURIComponent(name)}`, { scopes: [] });
      await groups.refresh();
    });
    setAdding(false);
    setNewName("");
  };

  const subtitle = `${groupList.length} group${
    groupList.length === 1 ? "" : "s"
  } · membership lives here, permissions live in Policy`;

  return (
    <AgentsShell subtitle={subtitle}>
      <p class="tab-explainer">
        Local teams of agents you manage here. Add agents directly.
      </p>

      <div class="section">
        <div class="section-header">
          <span class="section-title">groups ({groupList.length})</span>
          <div class="section-actions">
            {groupList.length > 0 && (
              <input
                type="search"
                class="search-input"
                placeholder="search groups…"
                value={query}
                onInput={(e) =>
                  setQuery((e.target as HTMLInputElement).value)
                }
              />
            )}
            {canMutate() && !adding && (
              <button
                class="section-btn"
                onClick={() => {
                  setAdding(true);
                  setNewName("");
                }}
              >
                + add group
              </button>
            )}
          </div>
        </div>

        {adding && (
          <div class="agents-add-row">
            <input
              type="text"
              class="search-input"
              placeholder="group name"
              autofocus
              value={newName}
              onInput={(e) =>
                setNewName((e.target as HTMLInputElement).value)
              }
              onKeyDown={(e) => {
                if (e.key === "Enter") submit();
                if (e.key === "Escape") {
                  setAdding(false);
                  setNewName("");
                }
              }}
            />
            <button class="section-btn" onClick={submit}>
              save
            </button>
            <button
              class="section-btn"
              onClick={() => {
                setAdding(false);
                setNewName("");
              }}
            >
              cancel
            </button>
          </div>
        )}

        {groupList.length === 0 ? (
          <div class="empty-callout">
            <div class="empty-callout-title">no groups yet</div>
            <div class="empty-callout-body">
              groups are local teams of agents. Create one above, then open
              its detail page to add members. Permissions for the group live
              in Policy.
            </div>
          </div>
        ) : filtered.length === 0 ? (
          <div class="empty-state">no groups match “{query}”.</div>
        ) : (
          <ul class="agents-tab-list" role="list">
            {filtered.map((g) => (
              <GroupRow
                key={g.id || g.name}
                group={g}
                memberCount={memberCounts.get(g.id || g.name) ?? memberCounts.get(g.name) ?? 0}
              />
            ))}
          </ul>
        )}
      </div>
    </AgentsShell>
  );
}

function GroupRow({
  group,
  memberCount,
}: {
  group: Group;
  memberCount: number;
}) {
  const slug = group.id || group.name;
  const label = group.display_name || group.name;
  return (
    <li class="agents-tab-row">
      <a
        class="agents-tab-row-link"
        href={`/agents/groups/${encodeURIComponent(slug)}`}
      >
        <div class="agents-tab-row-main">
          <span class="agents-tab-row-name">{label}</span>
          <span class="agents-tab-row-meta">
            {memberCount} member{memberCount === 1 ? "" : "s"}
            <span class="agents-tab-row-sep">·</span>
            <span class="agents-tab-row-source">{group.source}</span>
          </span>
        </div>
        <span class="agents-tab-row-chevron">›</span>
      </a>
    </li>
  );
}
