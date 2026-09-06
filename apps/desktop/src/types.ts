export type BackendStatus =
  | { kind: "starting" }
  | { kind: "running"; tool_count: number }
  | { kind: "failed"; error: string }
  | { kind: "stopped" };

export interface GatewayStatus {
  listen_port: number;
  listening: boolean;
  servers_running: number;
  servers_total: number;
  agent_count: number;
  pending_count: number;
  pending_agents: number;
  /** Approved agents whose client is signing in again and waiting for a yes. */
  pending_signins: number;
  auto_open_on_pending: boolean;
  do_not_disturb: boolean;
}

/** What an agent's calls do when no rule covers them. */
export type Posture = "supervised" | "first_use" | "guided" | "trusted";

/** How loudly Prism surfaces a call it resolved without asking. */
export type Attention = "silent" | "badge" | "notify" | "open";

export type TimeoutBehavior = "deny" | "allow_read_only";

export interface Settings {
  on_timeout: TimeoutBehavior;
  do_not_disturb: boolean;
  rate_limit_per_minute: number | null;
  hold_timeout_secs: number;
  auto_open_on_pending: boolean;
}

/** A token an agent holds. Prism keeps only the hash, so this is all there is to show. */
export interface TokenView {
  kind: "access" | "refresh" | "manual";
  created_at: string;
  expires_at: string | null;
}

/** Present only in the create/replace response, never in agent listings. */
export interface ManualToken {
  agent_id: string;
  token: string;
}

export interface ToolInfo {
  name: string;
  description: string | null;
  read_only: boolean;
  destructive: boolean;
}

export interface ServerView {
  id: string;
  name: string;
  command: string;
  args: string[];
  env: Record<string, string>;
  credentials_stored: boolean;
  enabled: boolean;
  status: BackendStatus;
}

export type AgentStatus = "pending" | "approved" | "denied";

export interface AgentConfig {
  id: string;
  name: string;
  client_name: string;
  client_version: string | null;
  status: AgentStatus;
  created_at: string;
  decided_at: string | null;
  posture: Posture;
  attention: Attention;
  /** The OAuth client this agent signs in as; absent for manually configured agents. */
  client_id?: string | null;
  /** Set for an agent host observed through its hooks, e.g. "claude-code". Never holds a token. */
  host?: string | null;
  /** True while at least one MCP session for this agent is open. */
  connected: boolean;
  /** Live tokens, newest last. Empty for agents that never signed in. */
  tokens: TokenView[];
}

/** An OAuth sign-in parked until you answer. Only shown for agents that were already approved. */
export interface PendingSignIn {
  id: string;
  agent_id: string;
  agent_name: string;
  client_name: string;
  requested_at: string;
  needs_consent: boolean;
}

export interface PendingCall {
  id: string;
  agent_id: string;
  agent_name: string;
  server_id: string;
  server_name: string;
  tool: string;
  arguments: unknown;
  requested_at: string;
  /** When the hold times out. */
  deadline: string | null;
  posture: Posture;
  reason: "policy" | "rate_limit";
}

export type DecisionScope = "once" | "session" | "always" | { for: { minutes: number } };

export interface Decision {
  verdict: "allow" | "deny";
  scope: DecisionScope;
  /** How wide the remembered rule reaches. Defaults to this tool. */
  target?: "tool" | "server" | "agent";
}

export type RuleDecision = "allow" | "deny" | "ask";

export interface Rule {
  id: string;
  agent_id: string | null;
  server_id: string | null;
  /** Exact name or a glob with `*`. */
  tool: string | null;
  decision: RuleDecision;
  /** null inherits the agent's attention. */
  attention: Attention | null;
  scope: "session" | "always";
  expires_at: string | null;
  created_at: string;
}

export interface NewRule {
  agent_id: string | null;
  server_id: string | null;
  tool: string | null;
  decision: RuleDecision;
  attention?: Attention | null;
  scope?: "session" | "always";
  minutes?: number | null;
}

export interface AuditEntry {
  id: string;
  at: string;
  agent_id: string;
  agent_name: string;
  server_id: string;
  tool: string;
  verdict: "allowed" | "denied" | "timeout" | "error";
  source:
    | { kind: "rule"; rule_id: string }
    | { kind: "human" }
    | { kind: "timeout" }
    | { kind: "unapproved" }
    | { kind: "posture"; posture: Posture }
    | { kind: "do_not_disturb" }
    | { kind: "observed" };
  duration_ms: number;
  error: string | null;
  attention: Attention;
  /** Present for a native action seen through a host hook. */
  native?: NativeDetail | null;
}

/** What the record keeps about a native action. `subject` is one redacted line, never the raw input. */
export interface NativeDetail {
  host: string;
  session?: string | null;
  cwd?: string | null;
  subject: string;
  /** Shadow deny-list rule id this action would have tripped. Nothing was held. */
  would_hold?: string | null;
  agent_type?: string | null;
  /** An MCP tool Prism serves, seen again through the hook; the gateway already logged the call. */
  via_prism: boolean;
}

export interface ShadowRule {
  id: string;
  summary: string;
}

export interface NativeStatus {
  hook_url: string;
  observe_native: boolean;
  last_event_at: string | null;
  actions_7d: number;
  would_hold_7d: number;
  by_reason: { reason: string; count: number }[];
  rules: ShadowRule[];
  /** Desktop only: where Claude Code's user settings live and whether the current hook URL is in them. */
  settings_path: string;
  hook_installed: boolean;
}

export interface HookInstallResult {
  path: string;
  backup: string | null;
}

export interface ConnectSnippet {
  url: string;
  mcp_json: string;
}

export type GatewayEvent =
  | { type: "pending_call"; data: PendingCall }
  | { type: "call_decided"; data: { id: string; decision: Decision } }
  | { type: "agent_requested"; data: AgentConfig }
  | { type: "sign_in_requested"; data: PendingSignIn }
  | { type: "sign_in_decided"; data: { id: string; approved: boolean } }
  | { type: "agent_decided"; data: { agent_id: string; status: AgentStatus } }
  | { type: "agent_connected"; data: { agent_id: string } }
  | { type: "agent_disconnected"; data: { agent_id: string } }
  | { type: "agent_updated"; data: { agent_id: string } }
  | { type: "settings_changed" }
  | { type: "server_status"; data: { server_id: string; status: BackendStatus } }
  | { type: "audit"; data: AuditEntry }
  | { type: "rules_changed" };

/** A newer release than the running one. `installable` is false for package-manager installs on Linux. */
export interface UpdateInfo {
  version: string;
  current: string;
  notes: string | null;
  date: string | null;
  installable: boolean;
}

export interface UpdateStatus {
  current: string;
  available: UpdateInfo | null;
  checked_at: string | null;
  installable: boolean;
}

export type UpdateEvent =
  | { state: "available"; version: string; current: string; notes: string | null; date: string | null; installable: boolean }
  | { state: "up_to_date" }
  | { state: "downloading"; downloaded: number; total: number | null }
  | { state: "installing" }
  | { state: "error"; message: string };
