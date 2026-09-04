import { signal } from "@preact/signals";
import type {
  AgentConfig,
  AuditEntry,
  ConnectSnippet,
  GatewayStatus,
  PendingCall,
  Rule,
  ServerView,
} from "./types";

export const status = signal<GatewayStatus | null>(null);
export const servers = signal<ServerView[]>([]);
export const agents = signal<AgentConfig[]>([]);
export const pending = signal<PendingCall[]>([]);
export const rules = signal<Rule[]>([]);
export const audit = signal<AuditEntry[]>([]);
export const lastCreatedSnippet = signal<ConnectSnippet | null>(null);
export const lastCreatedAgentId = signal<string | null>(null);
export const errorMessage = signal<string | null>(null);
export type Tab = "now" | "servers" | "agents" | "rules";
const TABS: Tab[] = ["now", "servers", "agents", "rules"];
const fromHash = location.hash.slice(1) as Tab;
export const tab = signal<Tab>(TABS.includes(fromHash) ? fromHash : "now");
