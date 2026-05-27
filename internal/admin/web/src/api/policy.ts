// Typed wrapper for the /admin/policy/* surface (spec §9). All callers in
// the policy-builder UI go through this module so we have a single place to
// keep request shapes aligned with internal/admin/policy_builder.go.
import type { GrantPredicate } from "./grants";
import { deleteJSON, getJSON, postJSON, putJSON } from "./client";

// ── CapabilitySpec mirror (matches admin.CapabilitySpec) ────────────────────

export type SubjectType = "groups" | "roles" | "agents";

export interface ActionSpec {
  mode: "verb" | "tool" | "backend_wildcard";
  verb_slug?: string;
  backend?: string;
  tool?: string;
}

export interface WhereSpec {
  mode: "anywhere" | "path_prefix" | "agent_home" | "shared" | "ephemeral";
  path_prefix?: string;
}

export interface WhenSpec {
  mode: "anytime" | "business" | "window";
  hours?: string;
  timezone?: string;
}

export interface HowSecureSpec {
  mode: "token" | "mfa" | "mfa_dpop";
  mfa_freshness_sec?: number;
  acr_override?: string;
  require_dpop?: boolean;
}

export interface WorkspaceConstraint {
  id?: GrantPredicate;
  type?: GrantPredicate;
  write_mode?: GrantPredicate;
}

export interface AdvancedSpec {
  args?: Record<string, GrantPredicate>;
  workspace?: WorkspaceConstraint;
  acr_required?: string;
  role_required?: string;
}

export interface CapabilitySpec {
  action: ActionSpec;
  where?: WhereSpec;
  when?: WhenSpec;
  how_secure?: HowSecureSpec;
  advanced?: AdvancedSpec;
}

// ── Read shapes (server-side rendered, see spec §5.2 / §10.1) ───────────────

export interface Chip {
  kind: string;
  label: string;
  value?: string;
}

export type CapabilitySource = "scope" | "grant";

// Effect: "allow" (default) or "deny" — task-46 added the field so the UI
// can split the row list into ALLOWED / DENIED sections without re-deriving
// the effect from storage shape. Deny rows are always Source="scope" and
// carry ids prefixed with "scope-deny-" so DELETE routes to AgentPolicy.Deny.
export type CapabilityEffect = "allow" | "deny";

// InheritanceSource names an upstream subject that contributes a binding to
// an agent. Only present on /policy/subjects/agents/{id}/capabilities reads.
export interface InheritanceSource {
  type: "group" | "role" | "direct";
  name?: string;
}

export interface CapabilityView {
  id: string;
  source: CapabilitySource;
  /** allow | deny. Optional for backwards-compat with older responses. */
  effect?: CapabilityEffect;
  spec: CapabilitySpec;
  display_summary: string;
  chips?: Chip[];
  shared_with?: string[];
  inherited_from?: InheritanceSource[];
}

// ── Verb vocabulary ─────────────────────────────────────────────────────────

export interface ToolPattern {
  backend: string;
  tools: string[];
}

export interface Verb {
  slug: string;
  label: string;
  patterns: ToolPattern[];
}

export interface ResolvedTool {
  backend: string;
  tool: string;
}

// ── Endpoints ───────────────────────────────────────────────────────────────

export function listVerbs(): Promise<Verb[]> {
  return getJSON<Verb[]>("/policy/verbs");
}

export function resolveVerb(
  slug: string,
  enabledBackends?: string[],
): Promise<ResolvedTool[]> {
  const q = enabledBackends && enabledBackends.length
    ? `?enabled_backends=${encodeURIComponent(enabledBackends.join(","))}`
    : "";
  return getJSON<ResolvedTool[]>(
    `/policy/verbs/${encodeURIComponent(slug)}/resolve${q}`,
  );
}

// ── Policy Health (task-41) ─────────────────────────────────────────────────

// PolicyHealth mirrors admin.PolicyHealth in internal/admin/policy_health.go.
// All six tile numbers come back in one shot so the strip never displays a
// row of numbers computed at different points in time. The frontend refreshes
// the whole struct every 30s via the PolicyHealthStrip polling pattern.
//
// `median_freshness_seconds` carries -1 when no events in the window have a
// non-zero auth_time — the strip renders that as "—" rather than a number.
export interface PolicyHealth {
  window_seconds: number;
  generated_at: string;
  calls_24h: number;
  drift_events_24h: number;
  denials_24h: number;
  denial_rate_24h: number;
  // SecOps-aligned tiles (task-46).
  permissions_in_force: number;
  calls_7d_avg: number;
  top_deny_dim?: string;
  top_deny_dim_count?: number;
  // Deprecated for UI rendering, retained on the wire for backwards-compat
  // with external consumers of GET /policy/health.
  median_freshness_seconds: number;
  dpop_bound_agents: number;
  active_templates: number;
}

export function getPolicyHealth(): Promise<PolicyHealth> {
  return getJSON<PolicyHealth>("/policy/health");
}

// ── Reverse-policy: who can use this backend? (task-43) ────────────────────

// PolicyAccessEntry mirrors admin.PolicyAccessEntry in internal/admin/policy_access.go.
//
// One entry per (subject, capability) pair that grants access to the
// requested backend (optionally filtered by tool). `template_hash` is empty
// for scope-shape entries; `capability_id` always matches what
// /policy/subjects/{type}/{id}/capabilities surfaces so the "Edit policy →"
// link can route the operator straight to the row in Policy Builder.
//
// Counts are 24h aggregates constrained to the backend in question, computed
// from one analytics Query (see backend doc for the join). Both default to 0
// when analytics is disabled.
export type PolicyAccessSource = "scope" | "grant";

export interface PolicyAccessEntry {
  subject_type: SubjectType;
  subject_id: string;
  source: PolicyAccessSource;
  summary: string;
  template_hash?: string;
  capability_id: string;
  calls_24h: number;
  denials_24h: number;
}

// PolicyAccessResponse mirrors admin.PolicyAccessResponse. `empty=true` is the
// explicit signal for the UI to render the "No policy grants access" empty
// state with a deep-link to Policy Builder — different from "no analytics
// data yet" (entries non-empty, calls all 0), which is a separate UX path.
export interface PolicyAccessResponse {
  backend: string;
  tool?: string;
  window_seconds: number;
  generated_at: string;
  entries: PolicyAccessEntry[];
  empty: boolean;
}

export function getPolicyAccess(
  backend: string,
  tool?: string,
): Promise<PolicyAccessResponse> {
  const params = new URLSearchParams({ backend });
  if (tool) params.set("tool", tool);
  return getJSON<PolicyAccessResponse>(`/policy/access?${params.toString()}`);
}

function capabilitiesPath(type: SubjectType, id: string): string {
  return `/policy/subjects/${type}/${encodeURIComponent(id)}/capabilities`;
}

export function listCapabilities(
  type: SubjectType,
  id: string,
): Promise<CapabilityView[]> {
  return getJSON<CapabilityView[]>(capabilitiesPath(type, id));
}

export function createCapability(
  type: SubjectType,
  id: string,
  spec: CapabilitySpec,
): Promise<CapabilityView> {
  return postJSON<CapabilityView>(capabilitiesPath(type, id), spec);
}

export function updateCapability(
  type: SubjectType,
  id: string,
  capID: string,
  spec: CapabilitySpec,
): Promise<CapabilityView> {
  return putJSON<CapabilityView>(
    `${capabilitiesPath(type, id)}/${encodeURIComponent(capID)}`,
    spec,
  );
}

export function deleteCapability(
  type: SubjectType,
  id: string,
  capID: string,
): Promise<void> {
  return deleteJSON<void>(
    `${capabilitiesPath(type, id)}/${encodeURIComponent(capID)}`,
  );
}
