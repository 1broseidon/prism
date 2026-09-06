import type { AgentConfig, NativeStatus } from "./types";

/** The agent hosts Prism knows how to observe. The id is the agent record id the gateway creates. */
export const HOSTS = [
  { id: "host:claude-code", host: "claude-code", name: "Claude Code" },
  { id: "host:codex", host: "codex", name: "Codex" },
] as const;

export type HostId = (typeof HOSTS)[number]["host"];

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
  };
}

export function hostStatus(st: NativeStatus | null, host: string) {
  return st?.hosts.find((h) => h.host === host) ?? null;
}

export function hostSetup(st: NativeStatus | null, host: string) {
  return st?.setup.find((h) => h.host === host) ?? null;
}
