import { invoke as tauriInvoke } from "@tauri-apps/api/core";
import { mock } from "./mock";

const inTauri = "__TAURI_INTERNALS__" in window;

function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  if (inTauri) return tauriInvoke<T>(cmd, args);
  const fn = (mock as Record<string, (a: unknown) => Promise<unknown>>)[cmd];
  if (!fn) return Promise.reject(new Error(`mock: no handler for ${cmd}`));
  return fn(args ?? {}) as Promise<T>;
}
import type {
  AgentConfig,
  AuditEntry,
  ConnectSnippet,
  Decision,
  GatewayStatus,
  PendingCall,
  Rule,
  ServerView,
} from "./types";

export function getStatus() {
  return invoke<GatewayStatus>("get_status");
}

export function listServers() {
  return invoke<ServerView[]>("list_servers");
}

export function addServer(args: {
  name: string;
  command: string;
  args: string[];
  env: Record<string, string>;
}) {
  return invoke<ServerView>("add_server", { args });
}

export function removeServer(serverId: string) {
  return invoke<void>("remove_server", { serverId });
}

export function restartServer(serverId: string) {
  return invoke<void>("restart_server", { serverId });
}

export function listAgents() {
  return invoke<AgentConfig[]>("list_agents");
}

export function decideAgent(agentId: string, approve: boolean) {
  return invoke<void>("decide_agent", { agentId, approve });
}

export function removeAgent(agentId: string) {
  return invoke<void>("remove_agent", { agentId });
}

export function listPending() {
  return invoke<PendingCall[]>("list_pending");
}

export function decide(id: string, decision: Decision) {
  return invoke<void>("decide", { id, decision });
}

export function listRules() {
  return invoke<Rule[]>("list_rules");
}

export function deleteRule(ruleId: string) {
  return invoke<void>("delete_rule", { ruleId });
}

export function listAudit(limit = 20) {
  return invoke<AuditEntry[]>("list_audit", { limit });
}

export function hidePanel() {
  return invoke<void>("hide_panel");
}

export function getConnectSnippet() {
  return invoke<ConnectSnippet>("get_connect_snippet");
}
