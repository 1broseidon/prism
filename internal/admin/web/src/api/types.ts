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

export interface BackendTool {
  name: string;
  description?: string;
}

export interface SandboxMount {
  source: string;
  target: string;
  readonly?: boolean;
}

export interface SandboxConfig {
  profile?: "default" | "compat";
  network_profile?: "standard";
  run_as_root?: boolean;
  uid?: number;
  gid?: number;
  readonly_rootfs?: boolean;
  memory?: string;
  cpus?: number;
  pids_limit?: number;
  mounts?: SandboxMount[];
}

export interface WorkspaceConfig {
  id?: string;
  type?: "proxied" | "virtual" | "ephemeral";
  mode?: "snapshot";
  write_mode?: "sandbox_only" | "stage" | "auto_apply";
  include?: string[];
  exclude?: string[];
  max_bytes?: number;
}

export interface Backend {
  id: string;
  namespace?: string;
  url?: string;
  enabled: boolean;
  credential?: BackendCredentialInfo;
  tools?: BackendTool[];
  circuit_breaker?: string;
  bridge_managed?: boolean;
  runtime?: string;
  sandbox?: SandboxConfig;
  workspace?: WorkspaceConfig;
  disconnected?: boolean;
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
  enabled?: boolean;
  sandbox?: SandboxConfig;
  workspace?: WorkspaceConfig;
  credential?: CredentialInput | null;
  // Optional manual OAuth client credentials. Skips DCR when supplied —
  // required for providers without DCR (GitHub, most IdPs).
  oauth_client_id?: string;
  oauth_client_secret?: string;
}

export interface BackendUpdateBody {
  enabled?: boolean;
  sandbox?: SandboxConfig;
  workspace?: WorkspaceConfig;
}

export interface WorkspaceChange {
  path: string;
  type: "add" | "modify" | "delete";
  old_sha256?: string;
  new_sha256?: string;
  binary?: boolean;
  preview?: string;
}

export interface WorkspaceChangeSet {
  base_id?: string;
  generated_at?: string;
  files?: WorkspaceChange[];
}

export interface WorkspaceApplyResult {
  applied: number;
  conflicts?: string[];
}

export type AddBackendResponse =
  | { status: "ok"; id: string }
  | { status: "connecting"; id: string }
  | { status: "auth_required"; auth_url: string; state: string; backend_id: string }
  | {
      status: "manual_oauth_required";
      auth_server: string;
      callback_url: string;
      backend_id: string;
    };

export interface AuthStatus {
  status: string;
}

export interface AdminAuthRule {
  role: "admin" | "viewer";
  emails?: string[];
  domains?: string[];
  groups?: string[];
}

export interface AdminAuthConfigView {
  issuer: string;
  client_id: string;
  redirect_url: string;
  scopes?: string[];
  groups_claim?: string;
  session_ttl?: string;
  cookie_domain?: string;
  cookie_secure?: boolean;
  rules: AdminAuthRule[];
}

export interface AdminAuthView {
  config: AdminAuthConfigView | null;
  client_secret_set: boolean;
  enabled: boolean;
  active: boolean;
  active_issuer?: string;
}

export interface AdminAuthPutPayload {
  issuer: string;
  client_id: string;
  client_secret: string | null;
  redirect_url: string;
  scopes?: string[];
  groups_claim?: string;
  session_ttl?: string;
  cookie_domain?: string;
  cookie_secure?: boolean;
  rules: AdminAuthRule[];
}

export type AdminAuthTestResponse =
  | { ok: true; issuer: string; authorize_url: string; token_url: string }
  | { ok: false; error: string };

export interface NetworkSettings {
  public_url?: string;
  admin_public_url?: string;
  trust_proxy_headers?: boolean;
}

export interface WorkspaceBridgeConfig {
  enabled: boolean;
  token_set: boolean;
}

export interface WorkspaceBridgeUpdate {
  enabled: boolean;
  token?: string;
}

export interface WorkspaceTool {
  name: string;
  description?: string;
}

export interface WorkspaceBackend {
  id: string;
  namespace: string;
  tools?: WorkspaceTool[];
}

export interface Workspace {
  id: string;
  type?: "proxied" | "virtual" | "ephemeral";
  hostname?: string;
  root?: string;
  version?: string;
  created_at?: string;
  last_seen?: string;
  connected: boolean;
  backends?: WorkspaceBackend[];
}
