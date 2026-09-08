import { signal } from "@preact/signals";
import { discardVolatile, preserveRecoverable, rememberVolatile } from "./lifecycle";
import type {
  ActivitySummary,
  AgentConfig,
  AuditEntry,
  ConnectSnippet,
  GatewayStatus,
  HttpAuth,
  ManualToken,
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
/** The week at a glance for the Now tab. Null until loaded. */
export const activityError = signal<string | null>(null);
export const activity = signal<ActivitySummary | null>(null);
export const lastCreatedSnippet = signal<ConnectSnippet | null>(null);
/** Coverage and this week's counts for native actions. Null until loaded. */
export const native = signal<NativeStatus | null>(null);
export const lastCreatedAgentId = signal<string | null>(null);
export const errorMessage = signal<string | null>(null);
/** A newer release, once a check has found one. Drives the dot on the settings button. */
export const update = signal<UpdateInfo | null>(null);
/** Live progress while an update downloads and installs. */
export const updateProgress = signal<UpdateEvent | null>(null);

export interface AddServerDraft {
  kind: "command" | "url";
  auth: HttpAuth;
  name: string;
  url: string;
  header: string;
  key: string;
  command: string;
  args: string;
  env: string;
}

const emptyAddServerDraft = (): AddServerDraft => ({
  kind: "command",
  auth: "none",
  name: "",
  url: "",
  header: "",
  key: "",
  command: "",
  args: "",
  env: "",
});
export const addServerDraft = signal<AddServerDraft>(emptyAddServerDraft());
export function updateAddServerDraft(patch: Partial<AddServerDraft>): void {
  addServerDraft.value = { ...addServerDraft.value, ...patch };
}
export function clearAddServerDraft(): void {
  addServerDraft.value = emptyAddServerDraft();
}

export interface ConnectAgentDraft {
  custom: boolean;
  mode: "oauth" | "manual";
  name: string;
  snippet: ConnectSnippet | null;
  issuedAgentId: string | null;
}

const emptyConnectAgentDraft = (): ConnectAgentDraft => ({ custom: false, mode: "oauth", name: "", snippet: null, issuedAgentId: null });
export const connectAgentDraft = signal<ConnectAgentDraft>(emptyConnectAgentDraft());
export function updateConnectAgentDraft(patch: Partial<ConnectAgentDraft>): void {
  connectAgentDraft.value = { ...connectAgentDraft.value, ...patch };
}
export function clearConnectAgentDraft(): void {
  connectAgentDraft.value = emptyConnectAgentDraft();
}

export interface HarnessSetupDraft {
  message: string;
  details: boolean;
  snippet: string;
}
export const harnessSetupDrafts = signal<Record<string, HarnessSetupDraft>>({});
export function harnessSetupDraft(host: string): HarnessSetupDraft {
  return harnessSetupDrafts.value[host] ?? { message: "", details: false, snippet: "" };
}
export function updateHarnessSetupDraft(host: string, patch: Partial<HarnessSetupDraft>): void {
  harnessSetupDrafts.value = { ...harnessSetupDrafts.value, [host]: { ...harnessSetupDraft(host), ...patch } };
}

/** One-time credentials exist only in this webview's memory. */
export const manualTokens = signal<Record<string, ManualToken>>({});
export const issuingManualTokens = signal<Record<string, boolean>>({});

/** Register recovery before the request can outlive its screen. */
export async function issueManualToken(agentId: string, issue: () => Promise<ManualToken>): Promise<void> {
  if (issuingManualTokens.value[agentId]) return;
  issuingManualTokens.value = { ...issuingManualTokens.value, [agentId]: true };
  preserveNavigation();
  try {
    rememberManualToken(await issue());
  } catch (error) {
    if (!manualTokens.value[agentId]) clearResumableNavigation({ kind: "agent-connections", agentId });
    throw error;
  } finally {
    issuingManualTokens.value = discardVolatile(issuingManualTokens.value, agentId);
  }
}
export function rememberManualToken(token: ManualToken): void {
  manualTokens.value = rememberVolatile(manualTokens.value, token.agent_id, token);
}
export function discardManualToken(agentId: string): void {
  manualTokens.value = discardVolatile(manualTokens.value, agentId);
}

export type Tab = "now" | "servers" | "agents" | "rules";
const TABS: Tab[] = ["now", "servers", "agents", "rules"];

/** What the Actions list is narrowed to. Every field is a chip the reader can drop. */
export interface ActivityFilter {
  agentId?: string;
  days?: number;
  at?: string;
  nativeOnly?: boolean;
  attention?: boolean;
  /** Local calendar day, YYYY-MM-DD. */
  day?: string;
  /** A would-have-asked rule id. */
  reason?: string;
}

/** Screens pushed on top of a tab, phone-style. Each owns the whole panel until it is popped. */
export type Screen =
  | { kind: "add-server" }
  | { kind: "connect-agent" }
  | { kind: "harness-setup"; host: string }
  | { kind: "harness-files"; host: string }
  | { kind: "agent"; agentId: string }
  | { kind: "agent-connections"; agentId: string }
  | { kind: "agent-harness"; agentId: string }
  | { kind: "agent-servers"; agentId: string }
  | { kind: "agent-grants"; agentId: string }
  | { kind: "agent-server"; agentId: string; serverId: string }
  | { kind: "host"; agentId: string }
  | { kind: "inspect-call"; callId: string }
  | ({ kind: "activity" } & ActivityFilter)
  | { kind: "settings" }
  | { kind: "settings-observe" }
  | { kind: "settings-updates" };
export const stack = signal<Screen[]>([]);
export const queueCursor = signal<string | null>(null);
export const queuePosition = signal(0);

export interface ResumableNavigation {
  tab: Tab;
  stack: Screen[];
}
export const resumableNavigation = signal<ResumableNavigation | null>(null);

/** Screens that hold an interaction worth keeping across a hide: a form, or a one-time token on display. */
export function isSetupScreen(screen: Screen): boolean {
  return screen.kind === "add-server" || screen.kind === "connect-agent" || screen.kind === "harness-setup";
}

function isRecoverableScreen(screen: Screen): boolean {
  return isSetupScreen(screen) || (screen.kind === "agent-connections" &&
    (manualTokens.value[screen.agentId] !== undefined || issuingManualTokens.value[screen.agentId]));
}

/** Park an in-progress setup or one-time token without putting it on disk. */
export function preserveNavigation(): void {
  const top = stack.value[stack.value.length - 1];
  const saved = preserveRecoverable(
    stack.value,
    resumableNavigation.value?.stack ?? null,
    top !== undefined && isRecoverableScreen(top),
  );
  if (saved !== resumableNavigation.value?.stack && saved !== null) {
    resumableNavigation.value = { tab: tab.value, stack: saved };
  }
}

/** Back to the resting place while retaining recoverable setup separately. */
export function resetNavigation(): void {
  preserveNavigation();
  stack.value = [];
  tab.value = "now";
  errorMessage.value = null;
}

export function resumeNavigation(): void {
  const saved = resumableNavigation.value;
  if (!saved) return;
  tab.value = saved.tab;
  stack.value = saved.stack.slice();
}

function sameScreen(left: Screen, right: Screen): boolean {
  if (left.kind !== right.kind) return false;
  if (left.kind === "add-server" || left.kind === "connect-agent") return true;
  if (left.kind === "agent-connections" && right.kind === "agent-connections") return left.agentId === right.agentId;
  return false;
}

export function clearResumableNavigation(screen: Screen): void {
  if (resumableNavigation.value?.stack.some((saved) => sameScreen(saved, screen))) {
    resumableNavigation.value = null;
  }
}

/** The close control beside Resume is the explicit way to forget recoverable state. */
export function discardResumableNavigation(): void {
  const saved = resumableNavigation.value;
  if (!saved) return;
  if (saved.stack.some(screen => screen.kind === "agent-connections" && issuingManualTokens.value[screen.agentId])) return;
  for (const screen of saved.stack) {
    if (screen.kind === "add-server") clearAddServerDraft();
    if (screen.kind === "connect-agent") {
      if (connectAgentDraft.value.issuedAgentId) discardManualToken(connectAgentDraft.value.issuedAgentId);
      clearConnectAgentDraft();
    }
    if (screen.kind === "harness-setup") {
      const next = { ...harnessSetupDrafts.value };
      delete next[screen.host];
      harnessSetupDrafts.value = next;
    }
    if (screen.kind === "agent-connections") discardManualToken(screen.agentId);
  }
  resumableNavigation.value = null;
}

export function push(screen: Screen): void {
  stack.value = [...stack.value, screen];
}

export function pop(): void {
  preserveNavigation();
  stack.value = stack.value.slice(0, -1);
}

/** Leave a finished flow without turning it back into a resumable draft. */
export function completeScreen(screen: Screen): void {
  const top = stack.value[stack.value.length - 1];
  if (top && sameScreen(top, screen)) stack.value = stack.value.slice(0, -1);
  clearResumableNavigation(screen);
}

/** Swap the top screen in place, for a screen that re-narrows itself. */
export function replace(screen: Screen): void {
  stack.value = [...stack.value.slice(0, -1), screen];
}

/** Dev affordance: `#servers/add` or `#agents/connect` opens a tab with a screen already pushed. */
const [hashTab, hashScreen, hashSub] = location.hash.slice(1).split("/");
export const tab = signal<Tab>(TABS.includes(hashTab as Tab) ? (hashTab as Tab) : "now");
if (hashScreen === "add" && tab.value === "servers") stack.value = [{ kind: "add-server" }];
if (hashScreen === "connect" && tab.value === "agents") stack.value = [{ kind: "connect-agent" }];
if (hashScreen === "settings") stack.value = [{ kind: "settings" }];
if (hashScreen === "activity") stack.value = [{ kind: "activity" }];
if (hashScreen === "host") stack.value = [{ kind: "host", agentId: "host:claude-code" }];
if (hashScreen === "host-codex") stack.value = [{ kind: "host", agentId: "host:codex" }];
if (hashScreen && hashScreen.startsWith("a") && tab.value === "agents" && hashScreen !== "connect") {
  const [agentId, serverId] = hashScreen.split(":");
  stack.value = serverId ? [{ kind: "agent", agentId }, { kind: "agent-server", agentId, serverId }] : [{ kind: "agent", agentId }];
}

if (hashScreen === "setup-codex") stack.value = [{ kind: "harness-setup", host: "codex" }];
if (["cursor", "opencode", "goose", "antigravity"].some(h => hashScreen === `setup-${h}`)) stack.value = [{ kind: "harness-setup", host: hashScreen.slice(6) }];
if (hashScreen === "setup-claude") stack.value = [{ kind: "harness-setup", host: "claude-code" }];
/** A third segment opens a hub's subscreen: `#agents/host/grants`, `#now/settings/updates`. */
const hubTop = stack.value[stack.value.length - 1];
if (hashSub && hubTop && (hubTop.kind === "agent" || hubTop.kind === "host")) {
  const sub = { connections: "agent-connections", harness: "agent-harness", servers: "agent-servers", grants: "agent-grants" } as const;
  const kind = sub[hashSub as keyof typeof sub];
  if (kind) stack.value = [...stack.value, { kind, agentId: hubTop.agentId }];
}
if (hashSub && hubTop?.kind === "settings" && (hashSub === "observe" || hashSub === "updates")) {
  stack.value = [...stack.value, { kind: hashSub === "observe" ? "settings-observe" : "settings-updates" }];
}

/** `#now/inspect/p1` opens a pending call's full payload. */
if (hashScreen === "inspect" && hashSub) stack.value = [{ kind: "inspect-call", callId: hashSub }];
