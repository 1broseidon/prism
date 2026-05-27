// SubjectHeader — the trimmed summary card at the top of every per-subject
// policy page. Spec §5.1 originally listed six rows; task-46 dropped the
// "Allowed on" / "Denied on" rows because they duplicated the information
// rendered in the Permissions card below (every allowed/denied backend is
// already visible row-by-row with effect colour and chips). Carrying both
// representations forced operators to triangulate two surfaces — bad SecOps
// presentation.
//
// Remaining rows:
//   1. Identity                 — name, type, member count, role breakdown
//   2. Created                  — audit timestamp (or "—" for groups/roles)
//   3. Rate limit               — composed from BackendPolicies (read-only)
//   4. Activity (24h)           — calls / denials / drift counts
//
// Data sources & fallbacks:
//
//   - Identity & member count come from the polled `groups` / `agents`
//     state for groups/agents respectively. Role subjects derive their
//     member count by counting bindings that reference them (or simply 0).
//   - Audit (created at / by) — `Agent.created_at` exists; groups don't
//     have a creation timestamp on the wire today, so we render "—" for
//     groups & roles rather than fabricate a value.
//   - Rate limit — pulled from `backend_policies` on the subject record.
//   - Activity 24h — counted from the polled `events` signal. Filtered by
//     client_id for agents; for groups/roles we filter to agents that
//     belong to the group/role. This is a best-effort UI synthesis; a
//     dedicated /admin/policy/subjects/:type/:id/summary endpoint is
//     deferred per spec §5.1 / open-questions.

import { useMemo } from "preact/hooks";
import { agents, groups, events } from "../../state";
import { fmtAge } from "../../util/time";
import type {
  Agent,
  AuditEvent,
  BackendPolicy,
  Group,
} from "../../api/types";
import type { CapabilityView, SubjectType } from "../../api/policy";

interface Props {
  subjectType: SubjectType;
  subjectID: string;
  // capabilities is still accepted (callers pass it) but no longer consumed
  // here — task-46 moved the per-backend allow/deny split into the
  // Permissions card. Kept as a prop so the SubjectDetail signature is
  // stable across the rename.
  capabilities: CapabilityView[];
}

export function SubjectHeader({
  subjectType,
  subjectID,
  capabilities: _capabilities,
}: Props) {
  const groupList = groups.data.value || [];
  const agentList = agents.data.value || [];
  const eventList = events.data.value || [];

  const identity = useIdentity(subjectType, subjectID, groupList, agentList);
  const audit = useAudit(subjectType, subjectID, groupList, agentList);
  const rateLimit = useRateLimit(subjectType, subjectID, groupList, agentList);
  const activity = useActivity24h(
    subjectType,
    subjectID,
    eventList,
    agentList,
  );

  const auditHref =
    subjectType === "agents"
      ? `/agents/${encodeURIComponent(subjectID)}`
      : `/policy/${subjectType}/${encodeURIComponent(subjectID)}?audit=1`;
  const activityHref = `/analytics?subject=${encodeURIComponent(
    `${subjectType}:${subjectID}`,
  )}`;
  // Cross-link back to the identity-management surface for groups/roles
  // (Members | Groups | Roles tabs on /agents). This is the only deliberate
  // conflation point between Policy Builder (authorization) and the Agents
  // tabs (identity) per the task-40 contract — one click each way, no
  // editing across surfaces.
  const manageHref =
    subjectType === "groups"
      ? `/agents/groups/${encodeURIComponent(subjectID)}`
      : subjectType === "roles"
        ? `/agents/roles/${encodeURIComponent(subjectID)}`
        : null;

  return (
    <section class="policy-summary-card" aria-label="Subject summary">
      <div class="policy-summary-row identity">
        <span class="policy-summary-name">{identity.name}</span>
        <span class="policy-summary-meta">{identity.subline}</span>
      </div>

      <div class="policy-summary-row">
        <span class="policy-summary-label">Created</span>
        <span class="policy-summary-value">{audit}</span>
      </div>

      <div class="policy-summary-row">
        <span class="policy-summary-label">Rate limit</span>
        <span class="policy-summary-value">
          {rateLimit ? (
            rateLimit
          ) : (
            <span class="policy-summary-faint">default</span>
          )}
        </span>
      </div>

      <div class="policy-summary-row">
        <span class="policy-summary-label">Activity</span>
        <span class="policy-summary-value">
          {activity.calls} calls 24h · {activity.denials} denial
          {activity.denials === 1 ? "" : "s"} · {activity.drift} drift
        </span>
      </div>

      <div class="policy-summary-actions">
        <a class="policy-summary-action" href={auditHref}>
          Audit resolution →
        </a>
        <a class="policy-summary-action" href={activityHref}>
          View activity →
        </a>
        {manageHref && (
          <a class="policy-summary-action" href={manageHref}>
            Manage members →
          </a>
        )}
      </div>
    </section>
  );
}

// ── Line composers ──────────────────────────────────────────────────────────

interface Identity {
  name: string;
  subline: string;
}

function useIdentity(
  type: SubjectType,
  id: string,
  groupList: Group[],
  agentList: Agent[],
): Identity {
  return useMemo<Identity>(() => {
    if (type === "groups") {
      const g = groupList.find((x) => x.name === id);
      const members = agentList.filter((a) =>
        (a.policy?.groups || []).includes(id),
      );
      return {
        name: id,
        subline: `group · ${members.length} agent${
          members.length === 1 ? "" : "s"
        }${g ? "" : " (not found)"}`,
      };
    }
    if (type === "roles") {
      return { name: id, subline: "role · binding-defined" };
    }
    const a =
      agentList.find((x) => x.prism_id === id) ||
      agentList.find((x) => x.client_id === id);
    if (!a) return { name: id, subline: "agent · not found" };
    const display = a.label || a.description || a.client_id;
    const groupsCount = (a.policy?.groups || []).length;
    return {
      name: display,
      subline: `agent · ${groupsCount} group${groupsCount === 1 ? "" : "s"}`,
    };
  }, [type, id, groupList, agentList]);
}

function useAudit(
  type: SubjectType,
  id: string,
  _groupList: Group[],
  agentList: Agent[],
): string {
  return useMemo(() => {
    if (type === "agents") {
      const a =
        agentList.find((x) => x.prism_id === id) ||
        agentList.find((x) => x.client_id === id);
      if (a?.created_at) return fmtAge(a.created_at);
    }
    return "—";
  }, [type, id, agentList]);
}

function describeRule(rule: BackendPolicy): string {
  if (!rule.rate_limit?.rps) return "";
  const parts = [`${rule.rate_limit.rps} req/s`];
  if (rule.rate_limit.burst) parts.push(`burst ${rule.rate_limit.burst}`);
  return parts.join(" ");
}

function useRateLimit(
  type: SubjectType,
  id: string,
  groupList: Group[],
  agentList: Agent[],
): string {
  return useMemo(() => {
    let bp: Record<string, BackendPolicy> | undefined;
    let label = "";
    if (type === "groups") {
      bp = groupList.find((g) => g.name === id)?.backend_policies;
      label = "group default";
    } else if (type === "agents") {
      const a =
        agentList.find((x) => x.prism_id === id) ||
        agentList.find((x) => x.client_id === id);
      bp = a?.policy?.backend_policies;
      label = "agent override";
    }
    if (!bp) return "";
    // Pick the strictest rule as the headline (lowest rps).
    let chosen: { id: string; desc: string; rps: number } | null = null;
    for (const [bid, rule] of Object.entries(bp)) {
      const desc = describeRule(rule);
      if (!desc) continue;
      const rps = rule.rate_limit?.rps ?? Infinity;
      if (!chosen || rps < chosen.rps) chosen = { id: bid, desc, rps };
    }
    if (!chosen) return "";
    return `${chosen.desc} (${chosen.id} · ${label})`;
  }, [type, id, groupList, agentList]);
}

interface Activity {
  calls: number;
  denials: number;
  drift: number;
}

function useActivity24h(
  type: SubjectType,
  id: string,
  eventList: AuditEvent[],
  agentList: Agent[],
): Activity {
  return useMemo<Activity>(() => {
    const cutoff = Date.now() - 24 * 60 * 60 * 1000;
    let predicate: (e: AuditEvent) => boolean;
    if (type === "agents") {
      const a =
        agentList.find((x) => x.prism_id === id) ||
        agentList.find((x) => x.client_id === id);
      const cid = a?.client_id || id;
      predicate = (e) => e.client_id === cid;
    } else if (type === "groups") {
      const memberCIDs = new Set(
        agentList
          .filter((a) => (a.policy?.groups || []).includes(id))
          .map((a) => a.client_id),
      );
      predicate = (e) => memberCIDs.has(e.client_id);
    } else {
      // Roles: no client_id linkage at the event layer in v1; report zeros.
      predicate = () => false;
    }
    let calls = 0;
    let denials = 0;
    for (const e of eventList) {
      if (new Date(e.ts).getTime() < cutoff) continue;
      if (!predicate(e)) continue;
      calls += 1;
      if (!e.allowed) denials += 1;
    }
    // Drift counts are exposed via the per-agent grant-resolution endpoint
    // (drift_count_24h) rather than the audit log; not loaded here to keep
    // the summary header cheap. Report 0 as a stable placeholder; the
    // capability rows task (task-34) will surface drift inline.
    return { calls, denials, drift: 0 };
  }, [type, id, eventList, agentList]);
}
