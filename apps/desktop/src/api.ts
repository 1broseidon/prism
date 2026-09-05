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
  Attention,
  GatewayStatus,
  NewRule,
  PendingCall,
  PendingSignIn,
  Posture,
  Rule,
  ServerView,
  Settings,
  ToolInfo,
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

export function listSignins() {
  return invoke<PendingSignIn[]>("list_signins");
}

export function decideSignin(id: string, approve: boolean) {
  return invoke<void>("decide_signin", { id, approve });
}

/** Sign an agent out everywhere: every token it holds stops working at once. */
export function revokeAgentTokens(agentId: string) {
  return invoke<void>("revoke_agent_tokens", { agentId });
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

export function addRule(rule: NewRule) {
  return invoke<Rule>("add_rule", { rule });
}

export function setAgentPolicy(agentId: string, policy: { posture?: Posture; attention?: Attention }) {
  return invoke<AgentConfig>("set_agent_policy", { agentId, posture: policy.posture ?? null, attention: policy.attention ?? null });
}

export function getSettings() {
  return invoke<Settings>("get_settings");
}

export function setSettings(settings: Settings) {
  return invoke<void>("set_settings", { settings });
}

export function listServerTools(serverId: string) {
  return invoke<ToolInfo[]>("list_server_tools", { serverId });
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

export function createManualAgent(name: string) {
  return invoke<import("./types").ManualToken>("create_manual_agent", { name });
}

export function replaceManualToken(agentId: string) {
  return invoke<import("./types").ManualToken>("replace_manual_token", { agentId });
}
