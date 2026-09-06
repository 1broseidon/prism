import { signal } from "@preact/signals";
import type {
  AgentConfig,
  AuditEntry,
  ConnectSnippet,
  GatewayStatus,
  NativeStatus,
  PendingCall,
  PendingSignIn,
  Rule,
  ServerView,
  UpdateEvent,
  UpdateInfo,
} from "./types";

export const status = signal<GatewayStatus | null>(null);
export const servers = signal<ServerView[]>([]);
export const agents = signal<AgentConfig[]>([]);
export const pending = signal<PendingCall[]>([]);
export const signins = signal<PendingSignIn[]>([]);
export const rules = signal<Rule[]>([]);
export const audit = signal<AuditEntry[]>([]);
export const lastCreatedSnippet = signal<ConnectSnippet | null>(null);
/** Coverage and this week's counts for native actions. Null until loaded. */
export const native = signal<NativeStatus | null>(null);
/** Which rows the Recent feed shows. */
export const feedFilter = signal<"all" | "mcp" | "native">("all");
export const lastCreatedAgentId = signal<string | null>(null);
export const errorMessage = signal<string | null>(null);
/** A newer release, once a check has found one. Drives the dot on the settings button. */
export const update = signal<UpdateInfo | null>(null);
/** Live progress while an update downloads and installs. */
export const updateProgress = signal<UpdateEvent | null>(null);

export type Tab = "now" | "servers" | "agents" | "rules";
const TABS: Tab[] = ["now", "servers", "agents", "rules"];

/** Screens pushed on top of a tab, phone-style. Each owns the whole panel until it is popped. */
export type Screen =
  | { kind: "add-server" }
  | { kind: "connect-agent" }
  | { kind: "agent"; agentId: string }
  | { kind: "agent-server"; agentId: string; serverId: string }
  | { kind: "host"; agentId: string }
  | { kind: "settings" };
export const stack = signal<Screen[]>([]);

export function push(screen: Screen): void {
  stack.value = [...stack.value, screen];
}

export function pop(): void {
  stack.value = stack.value.slice(0, -1);
}

/** Dev affordance: `#servers/add` or `#agents/connect` opens a tab with a screen already pushed. */
const [hashTab, hashScreen] = location.hash.slice(1).split("/");
export const tab = signal<Tab>(TABS.includes(hashTab as Tab) ? (hashTab as Tab) : "now");
if (hashScreen === "add" && tab.value === "servers") stack.value = [{ kind: "add-server" }];
if (hashScreen === "connect" && tab.value === "agents") stack.value = [{ kind: "connect-agent" }];
if (hashScreen === "settings") stack.value = [{ kind: "settings" }];
if (hashScreen === "host") stack.value = [{ kind: "host", agentId: "host:claude-code" }];
if (hashScreen === "host-codex") stack.value = [{ kind: "host", agentId: "host:codex" }];
if (hashScreen && hashScreen.startsWith("a") && tab.value === "agents" && hashScreen !== "connect") {
  const [agentId, serverId] = hashScreen.split(":");
  stack.value = serverId ? [{ kind: "agent", agentId }, { kind: "agent-server", agentId, serverId }] : [{ kind: "agent", agentId }];
}
