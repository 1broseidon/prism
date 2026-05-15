export interface Info {
  name: string;
  version: string;
  go_version: string;
  uptime: string;
  goroutines: number;
}

export interface AgentPolicy {
  groups?: string[];
  grant?: string[];
  deny?: string[];
}

export interface AgentBreakdown {
  defaults?: string[];
  effective?: string[];
  grants?: string[];
  denies?: string[];
  groups?: Record<string, string[]>;
}

export interface Agent {
  client_id: string;
  prism_id?: string;
  label?: string;
  description?: string;
  dynamic: boolean;
  scopes?: string[];
  policy?: AgentPolicy;
  breakdown?: AgentBreakdown;
  created_at?: string;
  last_used_at?: string;
}

export interface BackendCredentialInfo {
  type: "static" | "env" | "command" | "none";
  header?: string;
  env?: string;
  command?: string;
  configured: boolean;
}

export interface Backend {
  id: string;
  namespace?: string;
  url?: string;
  credential?: BackendCredentialInfo;
}

export interface Group {
  name: string;
  scopes: string[];
  source: "config" | "dynamic";
}

export interface DefaultsResponse {
  default_scopes: string[];
}

export interface AuditEvent {
  ts: string;
  client_id: string;
  namespace: string;
  tool: string;
  allowed: boolean;
  latency_ms: number;
}

export interface CredentialInput {
  type: "static" | "env" | "command" | "none";
  header?: string;
  value?: string;
  env?: string;
  command?: string;
}

export interface AddBackendBody {
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  credential?: CredentialInput | null;
}

export type AddBackendResponse =
  | { status: "ok"; id: string }
  | { status: "auth_required"; auth_url: string; state: string; backend_id: string };

export interface AuthStatus {
  status: string;
}
