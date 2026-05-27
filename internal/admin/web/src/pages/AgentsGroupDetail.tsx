// AgentsGroupDetail — per-group detail page on the Agents > Groups tab.
//
// Owns:
//   - Member roster (add/remove agent from group)
//   - Capabilities preview (read-only mirror of /policy/groups/:name)
//   - Cross-link to Policy Builder for capability authoring
//   - Delete-group action, gated on member count == 0
//
// Does NOT own capability authoring. The "Edit policy →" link is the one
// allowed conflation point between identity and authorization surfaces.

import { useEffect, useMemo, useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { agents, groups } from "../state";
import { canMutate } from "../state/me";
import { ApiError, deleteJSON } from "../api/client";
import { withToast } from "../state/toasts";
import {
  addAgentToGroup,
  agentsInGroup,
  removeAgentFromGroup,
} from "../api/identity";
import {
  listCapabilities,
  type CapabilityView,
} from "../api/policy";
import { agentDetailHref } from "../util/agentRoute";
import { splitLabel } from "../util/time";
import { AgentsShell } from "./Agents";
import { RenamePencil } from "../components/identity/RenamePencil";
import type { Agent, Group } from "../api/types";

export function AgentsGroupDetail() {
  const { params } = useRoute();
  const raw = (params as Record<string, string | undefined>).name || "";
  const slug = decodeURIComponent(raw);
  const loc = useLocation();
  const [renamedDisplayName, setRenamedDisplayName] = useState<string | null>(null);

  const groupList = groups.data.value || [];
  const agentList = agents.data.value || [];
  const group = groupList.find(
    (g) => g.id === slug || g.name === slug || g.display_name === slug,
  );
  const name = group?.name || slug;
  const displayName = renamedDisplayName || group?.display_name || name;
  const members = useMemo(() => agentsInGroup(agentList, name), [agentList, name]);
  const nonMembers = useMemo(
    () =>
      agentList
        .filter((a) => !!a.prism_id && !members.some((m) => m.prism_id === a.prism_id))
        .sort((a, b) =>
          (a.label || a.description || a.client_id).localeCompare(
            b.label || b.description || b.client_id,
          ),
        ),
    [agentList, members],
  );

  useEffect(() => {
    setRenamedDisplayName(null);
  }, [group?.id, group?.display_name]);

  return (
    <AgentsShell
      subtitle={`group · ${members.length} member${
        members.length === 1 ? "" : "s"
      }`}
    >
      <div class="agents-detail-back">
        <a href="/agents/groups">← back to groups</a>
      </div>

      <header class="agents-detail-head">
        <div>
          <h1 class="agents-detail-title">
            {group?.id ? (
              <RenamePencil
                currentName={displayName}
                entityId={group.id}
                onSuccess={(newDisplayName) => {
                  setRenamedDisplayName(newDisplayName);
                  void groups.refresh();
                }}
              />
            ) : (
              displayName
            )}
          </h1>
          <p class="agents-detail-sub">
            {group ? `source: ${group.source}` : "not found in group list"}
          </p>
        </div>
        <div class="agents-detail-actions">
          <a
            class="policy-summary-action"
            href={`/policy/groups/${encodeURIComponent(name)}`}
          >
            Edit policy →
          </a>
        </div>
      </header>

      <MembersSection name={name} members={members} nonMembers={nonMembers} />

      <CapabilitiesPreview subjectID={name} />

      <DeleteSection name={name} memberCount={members.length} group={group} loc={loc} />
    </AgentsShell>
  );
}

function MembersSection({
  name,
  members,
  nonMembers,
}: {
  name: string;
  members: Agent[];
  nonMembers: Agent[];
}) {
  const [adding, setAdding] = useState(false);
  const [selectID, setSelectID] = useState("");

  const submit = async () => {
    if (!selectID) {
      setAdding(false);
      return;
    }
    const target = nonMembers.find((a) => a.prism_id === selectID);
    if (!target) {
      setAdding(false);
      return;
    }
    await withToast(async () => {
      await addAgentToGroup(target, name);
      await agents.refresh();
    });
    setAdding(false);
    setSelectID("");
  };

  const removeMember = async (agent: Agent) => {
    await withToast(async () => {
      await removeAgentFromGroup(agent, name);
      await agents.refresh();
    });
  };

  return (
    <section class="section">
      <div class="section-header">
        <span class="section-title">members ({members.length})</span>
        <div class="section-actions">
          {canMutate() && !adding && nonMembers.length > 0 && (
            <button
              class="section-btn"
              onClick={() => {
                setAdding(true);
                setSelectID(nonMembers[0]?.prism_id || "");
              }}
            >
              + add agent
            </button>
          )}
        </div>
      </div>

      {adding && (
        <div class="agents-add-row">
          <select
            class="search-input"
            value={selectID}
            onChange={(e) =>
              setSelectID((e.target as HTMLSelectElement).value)
            }
          >
            {nonMembers.map((a) => (
              <option key={a.prism_id} value={a.prism_id}>
                {a.label || a.description || a.client_id}
              </option>
            ))}
          </select>
          <button class="section-btn" onClick={submit}>
            add
          </button>
          <button
            class="section-btn"
            onClick={() => {
              setAdding(false);
              setSelectID("");
            }}
          >
            cancel
          </button>
        </div>
      )}

      {members.length === 0 ? (
        <div class="empty-state">
          no members yet.{" "}
          {canMutate() && nonMembers.length > 0
            ? "use “+ add agent” above."
            : nonMembers.length === 0
              ? "no agents available to add."
              : null}
        </div>
      ) : (
        <ul class="agents-tab-list" role="list">
          {members.map((a) => (
            <MemberRow key={a.client_id} agent={a} onRemove={removeMember} />
          ))}
        </ul>
      )}
    </section>
  );
}

function MemberRow({
  agent,
  onRemove,
}: {
  agent: Agent;
  onRemove: (a: Agent) => Promise<void>;
}) {
  const display = agent.label || agent.description || agent.client_id;
  const [name, ctx] = splitLabel(display);
  const detailHref = agentDetailHref(agent);
  return (
    <li class="agents-tab-row member-row">
      <div class="agents-tab-row-main">
        {detailHref ? (
          <a class="agents-tab-row-name link" href={detailHref}>
            {name}
          </a>
        ) : (
          <span class="agents-tab-row-name">{name}</span>
        )}
        {ctx && <span class="agent-ctx">{ctx}</span>}
      </div>
      {canMutate() && (
        <button
          class="section-btn agents-tab-row-action"
          onClick={() => void onRemove(agent)}
        >
          remove
        </button>
      )}
    </li>
  );
}

function CapabilitiesPreview({ subjectID }: { subjectID: string }) {
  const [caps, setCaps] = useState<CapabilityView[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setCaps(null);
    setError(null);
    (async () => {
      try {
        const list = await listCapabilities("groups", subjectID);
        if (!cancelled) setCaps(list);
      } catch (e) {
        if (cancelled) return;
        if (e instanceof ApiError && e.status === 404) {
          setCaps([]);
          return;
        }
        setError(e instanceof Error ? e.message : String(e));
        setCaps([]);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [subjectID]);

  return (
    <section class="section">
      <div class="section-header">
        <span class="section-title">permissions preview</span>
        <div class="section-actions">
          <a
            class="policy-summary-action"
            href={`/policy/groups/${encodeURIComponent(subjectID)}`}
          >
            Edit in Policy →
          </a>
        </div>
      </div>
      <p class="tab-explainer">
        Read-only mirror of what this group can do. Edit permissions in
        Policy.
      </p>
      {caps === null ? (
        <div class="empty-state">loading…</div>
      ) : error ? (
        <div class="empty-state">failed to load: {error}</div>
      ) : caps.length === 0 ? (
        <div class="empty-state">no permissions yet.</div>
      ) : (
        <ul class="agents-cap-list" role="list">
          {caps.map((c) => (
            <li class="agents-cap-row" key={c.id}>
              <span class="agents-cap-summary">{c.display_summary}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function DeleteSection({
  name,
  memberCount,
  group,
  loc,
}: {
  name: string;
  memberCount: number;
  group: Group | undefined;
  loc: ReturnType<typeof useLocation>;
}) {
  if (!canMutate()) return null;
  const disabledReason =
    memberCount > 0
      ? "remove all members before deleting this group."
      : group?.source === "config"
        ? "config-defined groups cannot be deleted from the admin UI."
        : "";
  const disabled = disabledReason !== "";

  const doDelete = async () => {
    if (
      !window.confirm(
        `Delete group “${name}”? This cannot be undone. Permissions bound to the group remain in Policy.`,
      )
    ) {
      return;
    }
    await withToast(async () => {
      await deleteJSON(`/groups/${encodeURIComponent(name)}`);
      await groups.refresh();
      loc.route("/agents/groups");
    });
  };

  return (
    <section class="section danger-zone">
      <div class="section-header">
        <span class="section-title">delete group</span>
      </div>
      <p class="tab-explainer">
        Deleting a group does not remove permissions bound to it — clean
        those up in Policy first if they are stale.
      </p>
      <button
        class="section-btn danger"
        disabled={disabled}
        title={disabledReason || undefined}
        onClick={doDelete}
      >
        delete group
      </button>
    </section>
  );
}
