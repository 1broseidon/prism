// Identity (membership) API — used by the Members | Groups | Roles tabs on
// /agents to add/remove an agent's membership in a group or role.
//
// Membership lives on the agent. The admin surface exposes one mutation
// path (PUT /agents/{prism_id}/policy) that we re-use here — no new backend
// endpoints were added (groups and roles are both derived from
// AgentPolicy.Groups and AgentPolicy.Grant["role:<name>"] respectively).
// This keeps the contract simple: identity work is the same data plane the
// Direct Grants page already mutates, just sliced by membership.
//
// Authorization (what a group/role can DO) stays in Policy Builder. There
// are no capability mutations in this module — by design.

import { getJSON, putJSON } from "./client";
import type { Agent, AgentPolicy } from "./types";

const ROLE_PREFIX = "role:";

// Render the policy body we PUT back to /agents/{id}/policy. The backend
// replaces the stored policy with what we send, so callers always start
// from the agent's current policy and only mutate the field they own.
function policyBody(p: AgentPolicy | undefined): {
  groups: string[];
  grant: string[];
  deny: string[];
} {
  return {
    groups: [...(p?.groups ?? [])],
    grant: [...(p?.grant ?? [])],
    deny: [...(p?.deny ?? [])],
  };
}

async function putAgentPolicy(
  prismID: string,
  body: { groups: string[]; grant: string[]; deny: string[] },
): Promise<void> {
  await putJSON(`/agents/${encodeURIComponent(prismID)}/policy`, body);
}

// ── Group membership ────────────────────────────────────────────────────────

export async function addAgentToGroup(
  agent: Agent,
  group: string,
): Promise<void> {
  if (!agent.prism_id) throw new Error("agent has no prism_id");
  const body = policyBody(agent.policy);
  if (body.groups.includes(group)) return;
  body.groups = [...body.groups, group].sort();
  await putAgentPolicy(agent.prism_id, body);
}

export async function removeAgentFromGroup(
  agent: Agent,
  group: string,
): Promise<void> {
  if (!agent.prism_id) throw new Error("agent has no prism_id");
  const body = policyBody(agent.policy);
  if (!body.groups.includes(group)) return;
  body.groups = body.groups.filter((g) => g !== group);
  await putAgentPolicy(agent.prism_id, body);
}

// ── Role membership ─────────────────────────────────────────────────────────
//
// Roles are stored on the agent as a `role:<name>` entry in AgentPolicy.Grant
// — the same convention authserver.subjectIdentity reads at authorization
// time. listAgentRoles inverts that encoding so the UI can render plain
// names; addAgentToRole / removeAgentFromRole re-encode on write.

export function listAgentRoles(agent: Agent): string[] {
  return (agent.policy?.grant ?? [])
    .filter((g) => g.startsWith(ROLE_PREFIX))
    .map((g) => g.slice(ROLE_PREFIX.length));
}

export async function addAgentToRole(
  agent: Agent,
  role: string,
): Promise<void> {
  if (!agent.prism_id) throw new Error("agent has no prism_id");
  const body = policyBody(agent.policy);
  const marker = ROLE_PREFIX + role;
  if (body.grant.includes(marker)) return;
  body.grant = [...body.grant, marker];
  await putAgentPolicy(agent.prism_id, body);
}

export async function removeAgentFromRole(
  agent: Agent,
  role: string,
): Promise<void> {
  if (!agent.prism_id) throw new Error("agent has no prism_id");
  const body = policyBody(agent.policy);
  const marker = ROLE_PREFIX + role;
  if (!body.grant.includes(marker)) return;
  body.grant = body.grant.filter((g) => g !== marker);
  await putAgentPolicy(agent.prism_id, body);
}

// ── Aggregations across the agents list ─────────────────────────────────────
//
// The /agents endpoint already returns every agent's policy, so we derive
// group and role summaries by folding that snapshot — no separate listing
// endpoint is needed for v1 (the same pattern the SubjectSidebar uses for
// roles). When the agents signal refreshes, these recompute for free.

export interface GroupSummary {
  // Source mirrors GroupInfo.source so the UI can disable mutations on
  // config-defined groups when needed; the listing for the Groups tab
  // augments these summaries with the group manager's view (which carries
  // the source field) — agent-derived summaries default to "dynamic".
  name: string;
  id?: string;
  display_name?: string;
  memberCount: number;
}

export function summarizeGroups(agents: Agent[]): GroupSummary[] {
  const counts = new Map<string, number>();
  for (const a of agents) {
    for (const g of a.policy?.groups ?? []) {
      counts.set(g, (counts.get(g) ?? 0) + 1);
    }
  }
  return Array.from(counts.entries())
    .map(([name, memberCount]) => ({ name, memberCount }))
    .sort((a, b) => a.name.localeCompare(b.name));
}

export interface RoleSummary {
  name: string;
  id?: string;
  display_name?: string;
  memberCount: number;
}

interface RoleListItem {
  name: string;
  id?: string;
  display_name?: string;
  member_count?: number;
  memberCount?: number;
}

export async function listRoles(): Promise<RoleSummary[]> {
  const rows = await getJSON<RoleListItem[]>("/agents/roles");
  return rows.map((r) => ({
    name: r.name,
    id: r.id,
    display_name: r.display_name,
    memberCount: r.memberCount ?? r.member_count ?? 0,
  }));
}

export function summarizeRoles(agents: Agent[]): RoleSummary[] {
  const counts = new Map<string, number>();
  for (const a of agents) {
    for (const r of listAgentRoles(a)) {
      counts.set(r, (counts.get(r) ?? 0) + 1);
    }
  }
  return Array.from(counts.entries())
    .map(([name, memberCount]) => ({ name, memberCount }))
    .sort((a, b) => a.name.localeCompare(b.name));
}

export function agentsInGroup(agents: Agent[], group: string): Agent[] {
  return agents.filter((a) => (a.policy?.groups ?? []).includes(group));
}

export function agentsInRole(agents: Agent[], role: string): Agent[] {
  const marker = ROLE_PREFIX + role;
  return agents.filter((a) => (a.policy?.grant ?? []).includes(marker));
}

export async function identityRename(
  id: string,
  displayName: string,
): Promise<void> {
  await putJSON(`/identity/${encodeURIComponent(id)}/display-name`, {
    display_name: displayName,
  });
}
