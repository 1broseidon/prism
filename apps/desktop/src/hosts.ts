import type { AgentConfig, NativeStatus } from "./types";

/** The agent hosts Prism knows how to observe. The id is the agent record id the gateway creates. */
export const HOSTS = [
  { id: "host:claude-code", host: "claude-code", name: "Claude Code", scope: "Local sessions", login: "/mcp", loginHint: "Run inside Claude Code.", observation: "Run a tool in a new session to verify." },
  { id: "host:codex", host: "codex", name: "Codex", scope: "Local sessions", login: "codex mcp login prism", loginHint: "Run in your terminal.", observation: "Review the hook in Codex: /hooks. Then run a tool." },
  { id: "host:cursor", host: "cursor", name: "Cursor", scope: "Local Agent sessions", login: "cursor-agent mcp login prism", loginHint: "Or connect Prism in Cursor’s MCP settings.", observation: "Run a tool in a local Agent session." },
  { id: "host:opencode", host: "opencode", name: "OpenCode", scope: "V1 · local sessions", login: "opencode mcp auth prism", loginHint: "Run in your terminal.", observation: "Restart OpenCode V1, then run a tool." },
  { id: "host:goose", host: "goose", name: "Goose", scope: "1.49+ · local sessions", login: "", loginHint: "Restart Goose and connect the Prism extension.", observation: "Goose 1.49+ reports tool decisions. Restart, then run a tool." },
  { id: "host:antigravity", host: "antigravity", name: "Antigravity", scope: "CLI sessions", login: "", loginHint: "Connect Prism from Antigravity’s MCP settings.", observation: "Reports completed tool calls. Start a new CLI session to verify." },
] as const;

export type HostId = (typeof HOSTS)[number]["host"];

export function harness(host: string) { return HOSTS.find(h => h.host === host); }

export function hostOf(agentId: string) {
  return HOSTS.find((h) => h.id === agentId) ?? null;
}

export function hostName(agentId: string): string {
  return hostOf(agentId)?.name ?? "Agent host";
}

/** A placeholder record for a host that has not reported yet, so the list is stable. */
export function placeholderHost(h: (typeof HOSTS)[number]): AgentConfig {
  return {
    id: h.id,
    name: h.name,
    client_name: h.host,
    client_version: null,
    status: "approved",
    created_at: new Date(0).toISOString(),
    decided_at: null,
    posture: "trusted",
    attention: "silent",
    host: h.host,
    connected: false,
    tokens: [],
    clients: [],
  };
}

export function hostStatus(st: NativeStatus | null, host: string) {
  return st?.hosts.find((h) => h.host === host) ?? null;
}

export function hostSetup(st: NativeStatus | null, host: string) {
  return st?.setup.find((h) => h.host === host) ?? null;
}

/** A known harness belongs on the Agents list once it has a gateway record or a global setup on disk.
 *  Nothing is detected from installed software: an installed but unconfigured client is not an agent. */
export function hostPresent(st: NativeStatus | null, all: AgentConfig[], h: (typeof HOSTS)[number]): boolean {
  return all.some((a) => a.id === h.id) || !!hostSetup(st, h.host)?.setup_present;
}
