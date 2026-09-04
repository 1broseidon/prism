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
  auto_open_on_pending: boolean;
}

export interface ServerView {
  id: string;
  name: string;
  command: string;
  args: string[];
  env: Record<string, string>;
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
  /** True while at least one MCP session for this agent is open. */
  connected: boolean;
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
}

export interface Decision {
  verdict: "allow" | "deny";
  scope: "once" | "session" | "always";
}

export interface Rule {
  id: string;
  agent_id: string | null;
  server_id: string | null;
  tool: string | null;
  decision: "allow" | "deny";
  scope: "session" | "always";
  created_at: string;
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
    | { kind: "unapproved" };
  duration_ms: number;
  error: string | null;
}

export interface ConnectSnippet {
  url: string;
  mcp_json: string;
}

export type GatewayEvent =
  | { type: "pending_call"; data: PendingCall }
  | { type: "call_decided"; data: { id: string; decision: Decision } }
  | { type: "agent_requested"; data: AgentConfig }
  | { type: "agent_decided"; data: { agent_id: string; status: AgentStatus } }
  | { type: "agent_connected"; data: { agent_id: string } }
  | { type: "agent_disconnected"; data: { agent_id: string } }
  | { type: "server_status"; data: { server_id: string; status: BackendStatus } }
  | { type: "audit"; data: AuditEntry }
  | { type: "rules_changed" };
