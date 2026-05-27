import { deleteJSON, getJSON, postJSON, putJSON } from "./client";

export interface GrantPredicate {
  equals?: unknown;
  prefix?: string;
  oneOf?: unknown[];
  pattern?: string;
  size_max?: number;
  range?: { min?: number; max?: number };
  // tool_in_set entries are "backend:tool" pairs. Added by the policy-backend
  // verb compile path (task-32). Mirrors auth.Predicate.ToolInSet on the Go
  // side; see internal/auth/grants_dsl.go.
  tool_in_set?: string[];
}

export interface GrantSpec {
  type?: string;
  tool: string;
  backend: string;
  args?: Record<string, GrantPredicate>;
  workspace?: {
    id?: GrantPredicate;
    type?: GrantPredicate;
    write_mode?: GrantPredicate;
  };
  hours?: string;
  not_before?: number;
  expires_at?: number;
  auth_freshness_max?: number;
  cnf_required?: boolean;
  acr_required?: string;
}

export interface GrantTemplate {
  id: string;
  version?: number;
  hash?: string;
  supersedes?: string;
  spec: GrantSpec;
  created_at?: string;
  created_by?: string;
}

export interface GrantBinding {
  id: string;
  template_id: string;
  template_hash?: string;
  subjects: {
    groups?: string[];
    roles?: string[];
    agent_ids?: string[];
    role_required?: string;
  };
  created_at?: string;
  created_by?: string;
}

export function listGrantTemplates(filters: { tool?: string; backend?: string } = {}) {
  const q = new URLSearchParams();
  if (filters.tool) q.set("tool", filters.tool);
  if (filters.backend) q.set("backend", filters.backend);
  const qs = q.toString();
  return getJSON<GrantTemplate[]>(`/grant-templates${qs ? `?${qs}` : ""}`);
}

export function getGrantTemplate(id: string, version?: number) {
  const suffix = version ? `/${version}` : "";
  return getJSON<GrantTemplate | GrantTemplate[]>(`/grant-templates/${encodeURIComponent(id)}${suffix}`);
}

export function getGrantTemplateByHash(hash: string) {
  return getJSON<GrantTemplate>(`/grant-templates/by-hash/${encodeURIComponent(hash)}`);
}

export function createGrantTemplate(template: GrantTemplate) {
  return postJSON<GrantTemplate>("/grant-templates", template);
}

export function updateGrantTemplate(id: string, template: GrantTemplate) {
  return putJSON<GrantTemplate>(`/grant-templates/${encodeURIComponent(id)}`, template);
}

export function deleteGrantTemplate(id: string, version: number) {
  return deleteJSON<void>(`/grant-templates/${encodeURIComponent(id)}/${version}`);
}

export function listGrantBindings(filters: { template?: string; group?: string; agent?: string } = {}) {
  const q = new URLSearchParams();
  if (filters.template) q.set("template", filters.template);
  if (filters.group) q.set("group", filters.group);
  if (filters.agent) q.set("agent", filters.agent);
  const qs = q.toString();
  return getJSON<GrantBinding[]>(`/grant-bindings${qs ? `?${qs}` : ""}`);
}

export function getGrantBinding(id: string) {
  return getJSON<GrantBinding>(`/grant-bindings/${encodeURIComponent(id)}`);
}

export function createGrantBinding(binding: GrantBinding) {
  return postJSON<GrantBinding>("/grant-bindings", binding);
}

export function updateGrantBinding(id: string, binding: GrantBinding) {
  return putJSON<GrantBinding>(`/grant-bindings/${encodeURIComponent(id)}`, binding);
}

export function deleteGrantBinding(id: string) {
  return deleteJSON<void>(`/grant-bindings/${encodeURIComponent(id)}`);
}
