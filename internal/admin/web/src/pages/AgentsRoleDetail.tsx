// AgentsRoleDetail — per-role detail page on the Agents > Roles tab.
//
// Symmetric with AgentsGroupDetail:
//   - Member roster (add/remove agent from role via the role:<name> marker
//     in AgentPolicy.Grant)
//   - Capabilities preview (read-only mirror of /policy/roles/:name)
//   - Cross-link to Policy Builder for capability authoring
//   - "Local" badge per row: today every assignment is locally managed.
//     The column is here so OIDC-claim-asserted ("External") roles slot in
//     later without restructuring.
//   - Delete-role action gated on member count == 0. Roles have no
//     dedicated DELETE endpoint server-side (they're derived from agent
//     state); deletion just means "no agent holds the marker and no
//     binding references it" — when both conditions hold the row drops out
//     of every listing automatically. The button is therefore disabled
//     whenever any member or binding still references the role.

import { useEffect, useMemo, useState } from "preact/hooks";
import { useRoute } from "preact-iso";
import { agents } from "../state";
import { canMutate } from "../state/me";
import { ApiError } from "../api/client";
import { withToast } from "../state/toasts";
import {
  addAgentToRole,
  agentsInRole,
  listRoles,
  removeAgentFromRole,
} from "../api/identity";
import {
  listCapabilities,
  type CapabilityView,
} from "../api/policy";
import { listGrantBindings, type GrantBinding } from "../api/grants";
import { agentDetailHref } from "../util/agentRoute";
import { splitLabel } from "../util/time";
import { AgentsShell } from "./Agents";
import { RenamePencil } from "../components/identity/RenamePencil";
import type { Agent } from "../api/types";

export function AgentsRoleDetail() {
  const { params } = useRoute();
  const raw = (params as Record<string, string | undefined>).name || "";
  const slug = decodeURIComponent(raw);
  const [roleID, setRoleID] = useState<string | undefined>(undefined);
  const [roleDisplayName, setRoleDisplayName] = useState<string | null>(null);

  const agentList = agents.data.value || [];
  const name = roleDisplayName || slug;
  const members = useMemo(() => agentsInRole(agentList, name), [agentList, name]);
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
    let cancelled = false;
    setRoleID(undefined);
    setRoleDisplayName(null);
    (async () => {
      try {
        const rows = await listRoles();
        if (cancelled) return;
        const role = rows.find(
          (r) => r.id === slug || r.name === slug || r.display_name === slug,
        );
        setRoleID(role?.id);
        setRoleDisplayName(role?.display_name || role?.name || null);
      } catch {
        if (!cancelled) {
          setRoleID(undefined);
          setRoleDisplayName(null);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [slug]);

  return (
    <AgentsShell
      subtitle={`role · ${members.length} member${
        members.length === 1 ? "" : "s"
      }`}
    >
      <div class="agents-detail-back">
        <a href="/agents/roles">← back to roles</a>
      </div>

      <header class="agents-detail-head">
        <div>
          <h1 class="agents-detail-title">
            {roleID ? (
              <RenamePencil
                currentName={name}
                entityId={roleID}
                onSuccess={(newDisplayName) => {
                  setRoleDisplayName(newDisplayName);
                  void agents.refresh();
                }}
              />
            ) : (
              name
            )}
          </h1>
          <p class="agents-detail-sub">
            label on agent identity — locally assigned in v1
          </p>
        </div>
        <div class="agents-detail-actions">
          <a
            class="policy-summary-action"
            href={`/policy/roles/${encodeURIComponent(name)}`}
          >
            Edit policy →
          </a>
        </div>
      </header>

      <MembersSection name={name} members={members} nonMembers={nonMembers} />

      <CapabilitiesPreview subjectID={name} />

      <DeleteSection name={name} memberCount={members.length} />
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
      await addAgentToRole(target, name);
      await agents.refresh();
    });
    setAdding(false);
    setSelectID("");
  };

  const removeMember = async (agent: Agent) => {
    await withToast(async () => {
      await removeAgentFromRole(agent, name);
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
        <div class="empty-state">no members yet.</div>
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
        <span class="role-source-badge" title="Locally assigned in the admin console.">
          Local
        </span>
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
        const list = await listCapabilities("roles", subjectID);
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
            href={`/policy/roles/${encodeURIComponent(subjectID)}`}
          >
            Edit in Policy →
          </a>
        </div>
      </div>
      <p class="tab-explainer">
        Read-only mirror of what this role can do. Edit permissions in
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
}: {
  name: string;
  memberCount: number;
}) {
  if (!canMutate()) return null;
  // Roles have no first-class DELETE endpoint server-side — see the file
  // header comment. We surface a disabled action with the explanation so
  // the operator knows the answer is "remove every member; the role drops
  // out of every listing once nothing references it."
  const [bindingRefs, setBindingRefs] = useState<number | null>(null);
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const bindings = await listGrantBindings();
        const count = (bindings as GrantBinding[]).filter((b) => {
          const subj = b.subjects;
          if (!subj) return false;
          if (subj.role_required === name) return true;
          return (subj.roles ?? []).includes(name);
        }).length;
        if (!cancelled) setBindingRefs(count);
      } catch {
        if (!cancelled) setBindingRefs(0);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [name]);

  const disabledReason =
    memberCount > 0
      ? "remove all members before deleting this role."
      : bindingRefs && bindingRefs > 0
        ? "policy bindings still reference this role. Remove the bindings in Policy first."
        : "";
  // Roles are derived state — when no agent holds the marker and no binding
  // references the role, it drops out of every listing on the next refresh.
  // Rather than render a button that promises an action the server can't
  // honor (there's no DELETE endpoint), show an informational hint that
  // matches reality.
  const isEmpty = memberCount === 0 && bindingRefs !== null && bindingRefs === 0;

  return (
    <section class="section danger-zone">
      <div class="section-header">
        <span class="section-title">delete role</span>
      </div>
      <p class="tab-explainer">
        Roles disappear once no agent holds them and no policy binding
        references them. There is nothing else to delete.
      </p>
      {isEmpty ? (
        <div class="empty-role-hint">
          This role is empty and will disappear from listings on next refresh.{" "}
          <a href="/agents/roles">← back to all roles</a>
        </div>
      ) : (
        <button
          class="section-btn danger"
          disabled
          title={disabledReason || "this role is already empty"}
        >
          delete role
        </button>
      )}
    </section>
  );
}
