import * as api from "./api";
import { rules } from "./state";
import type { Attention, Posture, Rule, RuleDecision } from "./types";

export const POSTURES: { value: Posture; label: string; hint: string }[] = [
  { value: "supervised", label: "Supervised", hint: "Every call asks you." },
  { value: "first_use", label: "First use", hint: "Asks once per tool, then remembers your answer." },
  { value: "guided", label: "Guided", hint: "Read-only tools pass. Anything that writes asks." },
  { value: "trusted", label: "Trusted", hint: "Everything passes and is logged." },
];

export const ATTENTIONS: { value: Attention; label: string; hint: string }[] = [
  { value: "silent", label: "Silent", hint: "Nothing. The call still shows under Recent." },
  { value: "badge", label: "Badge", hint: "The tray icon lights up until you open the panel." },
  { value: "notify", label: "Notify", hint: "A notification, plus the badge." },
  { value: "open", label: "Open", hint: "Opens the panel every time." },
];

export const ACCESS: { value: RuleDecision; label: string }[] = [
  { value: "allow", label: "Allow" },
  { value: "ask", label: "Ask" },
  { value: "deny", label: "Deny" },
];

export function postureLabel(p: Posture): string {
  return POSTURES.find((x) => x.value === p)?.label ?? p;
}

/** The rule that sits exactly on this agent, server, and tool. `null` fields must match `null`. */
export function findRule(list: Rule[], agentId: string | null, serverId: string | null, tool: string | null): Rule | undefined {
  return list.find((r) => r.agent_id === agentId && r.server_id === serverId && r.tool === tool);
}

/** Set or clear the rule on one triple. Clearing means "inherit from the level above". */
export async function setAccess(agentId: string, serverId: string | null, tool: string | null, access: RuleDecision | null): Promise<void> {
  const existing = findRule(rules.value, agentId, serverId, tool);
  if (access === null) {
    if (existing) await api.deleteRule(existing.id);
  } else {
    await api.addRule({ agent_id: agentId, server_id: serverId, tool, decision: access });
  }
  rules.value = await api.listRules();
}
