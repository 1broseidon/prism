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
  backend_policies?: Record<string, BackendPolicy>;
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
  disabled?: boolean;
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
  quota_bytes?: number;
  retention_seconds?: number;
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
  // Transport is "openapi" for OpenAPI-spec-backed servers, empty/"stdio"/"http"
  // otherwise. The admin layer doesn't surface a separate source URL on the
  // backend list, so the UI re-prompts for a URL or file on re-import.
  transport?: string;
}

export interface BackendRateLimit {
  rps: number;
  burst?: number;
}

export interface BackendPolicy {
  workspace_selector?: string;
  rate_limit?: BackendRateLimit;
}

export interface Group {
  name: string;
  scopes: string[];
  source: "config" | "dynamic";
  backend_policies?: Record<string, BackendPolicy>;
}

export interface DefaultsResponse {
  default_scopes: string[];
  backend_policies?: Record<string, BackendPolicy>;
}

export interface AgentPolicyResolutionLayer {
  source: string;
  selector?: string;
}

export interface AgentWorkspaceResolution {
  workspace_id?: string;
  selector: string;
  source: string;
  layers?: AgentPolicyResolutionLayer[];
  deny_reason?: string;
}

export interface AgentRateLimitResolution {
  rps?: number;
  burst?: number;
  source?: string;
  layers?: AgentPolicyResolutionLayer[];
}

export interface AgentPolicyResolution {
  backend_id: string;
  workspace?: AgentWorkspaceResolution;
  rate_limit?: AgentRateLimitResolution;
}

export interface PolicyTraceLayer {
  source: string;
  selector?: string;
}

export interface PolicyTrace {
  workspace_id?: string;
  selector?: string;
  source?: string;
  layers?: PolicyTraceLayer[];
}

export interface AuditEvent {
  ts: string;
  client_id: string;
  namespace: string;
  tool: string;
  allowed: boolean;
  latency_ms: number;
  policy_trace?: PolicyTrace;
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
  // disabled_tools is the list of bare (un-namespaced) tool names switched
  // off on this backend. Omit to leave the toggle state unchanged; pass an
  // empty array to re-enable every tool.
  disabled_tools?: string[];
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

export interface WorkspacePolicyReference {
  source: string;
  backend_id: string;
  selector: string;
}

export interface WorkspaceDetail {
  workspace: Workspace;
  references: WorkspacePolicyReference[];
}

export type WorkspaceHealth =
  | "ok"
  | "quota_warn"
  | "quota_exceeded"
  | "stale";

export interface Workspace {
  id: string;
  type?: "proxied" | "virtual" | "ephemeral";
  owner?: string;
  allowed_agents?: string[];
  allowed_templates?: string[];
  quota_bytes?: number;
  used_bytes?: number;
  retention_seconds?: number;
  hostname?: string;
  root?: string;
  version?: string;
  created_at?: string;
  last_seen?: string;
  connected: boolean;
  health_status?: WorkspaceHealth;
  backends?: WorkspaceBackend[];
}

// ---------------------------------------------------------------------------
// OpenAPI preview / save / diff / reimport.
// ---------------------------------------------------------------------------

// Polymorphic input accepted on every OpenAPI endpoint that needs to
// materialize a spec. Exactly one of file, url, or inline must be supplied;
// file is base64-encoded raw spec bytes, inline is the raw YAML/JSON text.
export type OpenAPISpecSource =
  | { file: string; url?: undefined; inline?: undefined }
  | { url: string; file?: undefined; inline?: undefined }
  | { inline: string; file?: undefined; url?: undefined };

// Request/response for POST /openapi/scaffold-from-curl. The server parses
// the curl command and returns an OpenAPI 3.1 YAML stub that the operator
// reviews/edits in the inline editor before saving.
export interface OpenAPIScaffoldRequest {
  curl: string;
}

export interface OpenAPIScaffoldResponse {
  spec: string;
  warnings?: string[];
}

// Bearer schemes surface as type:"bearer" so the UI can pick the right
// credential form without parsing scheme + type both; named header schemes
// come through as type:"apiKey" with the header name attached.
export interface OpenAPISecurityScheme {
  name: string;
  type: "bearer" | "apiKey" | string;
  header?: string;
}

export interface OpenAPIOperationView {
  name: string;
  method: string;
  path: string;
  summary?: string;
  tags?: string[];
  deprecated?: boolean;
  security?: string[];
  fingerprint: string;
}

export interface OpenAPISkippedOperation {
  name: string;
  method?: string;
  path?: string;
  reason: string;
  detail?: string;
}

export interface OpenAPIPreviewResponse {
  title: string;
  version: string;
  base_url: string;
  security_schemes: OpenAPISecurityScheme[];
  operations: OpenAPIOperationView[];
  skipped: OpenAPISkippedOperation[];
  spec_warnings?: string[];
}

export interface OpenAPISaveBody {
  type: "openapi";
  source: OpenAPISpecSource;
  base_url_override?: string;
  security_scheme?: string;
  credential?: CredentialInput | null;
  // disabled_tools is the bare list of operation names switched off at save
  // time. Empty array = every operation enabled; omitted = same as empty.
  disabled_tools?: string[];
}

export interface OpenAPIDiffEntry {
  name: string;
  method?: string;
  path?: string;
}

export interface OpenAPIRenameEntry {
  from: string;
  to: string;
}

export interface OpenAPISignatureChange {
  name: string;
  old_fingerprint: string;
  new_fingerprint: string;
}

export interface OpenAPIDiffResponse {
  added: OpenAPIDiffEntry[];
  removed: OpenAPIDiffEntry[];
  renamed: OpenAPIRenameEntry[];
  signature_changed: OpenAPISignatureChange[];
  unchanged_count: number;
  newly_skipped: OpenAPISkippedOperation[];
}

export interface OpenAPIReimportBody {
  source: OpenAPISpecSource;
  disabled_tools_resolution: "preserve" | "default_enabled";
}

export interface OpenAPISaveResponse {
  status: "ok";
  id: string;
  operations: number;
  skipped: number;
}

// Persisted spec source for an existing OpenAPI backend. Returned by
// GET /backends/{id}/openapi-source. Spec is the raw YAML/JSON the operator
// imported (UTF-8); source_url is empty for file- and inline-sourced specs.
export interface OpenAPISourceResponse {
  source_url: string;
  spec: string;
}
