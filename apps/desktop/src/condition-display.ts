import type { AuditEntry, JsonValue, Offer, Rule } from "./types";

export function shortPath(path: string): string {
  const home = path.replace(/^(?:\/home\/[^/]+|\/Users\/[^/]+|[A-Za-z]:[\\/]Users[\\/][^\\/]+)/, "~").replaceAll("\\", "/");
  const parts = home.split("/").filter(Boolean);
  return parts.length > (home.startsWith("~") ? 3 : 2) ? `${home.startsWith("~") ? "~/" : ""}…/${parts.slice(-2).join("/")}` : home;
}

function object(value: JsonValue | undefined): Record<string, JsonValue> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? value : undefined;
}

/** Only condition structure is described; arbitrary argument values stay out of the UI copy. */
export function conditionClause(value: JsonValue | undefined, depth = 0): string {
  const c = object(value);
  if (!c || depth > 16) return "condition";
  if (typeof c.tag === "string") return `when ${c.tag}`;
  const path = object(c.path);
  if (path) {
    const where = typeof path.under === "string" ? `under ${shortPath(path.under)}` : path.outside_cwd === true ? "outside cwd" : path.outside_cwd === false ? "inside cwd" : "path";
    return `${where}${Array.isArray(path.access) ? ` (${path.access.join(", ")})` : ""}`;
  }
  const host = object(c.host);
  if (host) return `host ${[...(Array.isArray(host.in) ? host.in : []), ...(Array.isArray(host.scope) ? host.scope : [])].join(", ")}`;
  if (Array.isArray(c.all)) return c.all.map((v) => conditionClause(v, depth + 1)).join(" and ");
  if (Array.isArray(c.any)) return c.any.map((v) => conditionClause(v, depth + 1)).join(" or ");
  if (c.not) return `not (${conditionClause(c.not, depth + 1)})`;
  const arg = object(c.arg);
  if (arg && typeof arg.pointer === "string") return `when argument ${arg.pointer}`;
  const command = object(c.command);
  if (command && Array.isArray(command.program_in)) return `program ${command.program_in.join(", ")}`;
  return "condition";
}

export function offerCondition(offer: Offer): JsonValue {
  return offer.kind === "path_under" ? { path: { under: offer.value } } : { host: { in: [offer.value] } };
}

export function explainAudit(entry: AuditEntry, rules: Rule[], servers: { id: string; name: string }[] = []): string {
  switch (entry.source.kind) {
    case "rule": {
      const id = entry.source.rule_id;
      const rule = rules.find((r) => r.id === id);
      const decision = rule?.decision ?? (entry.verdict === "allowed" ? "allow" : entry.verdict === "denied" ? "deny" : entry.verdict);
      const serverId = rule ? rule.server_id : entry.server_id;
      const server = serverId ? servers.find((s) => s.id === serverId)?.name ?? serverId : "any server";
      const tool = rule ? rule.tool ?? "any tool" : entry.tool;
      return `Rule: ${entry.agent_name} · ${server} · ${tool}${rule?.condition ? ` · ${conditionClause(rule.condition)}` : ""} · ${decision[0].toUpperCase()}${decision.slice(1)}`;
    }
    case "posture": {
      const posture = entry.source.posture.replace("_", "-");
      return `${posture[0].toUpperCase()}${posture.slice(1)} posture`;
    }
    case "tripwire": return "Rate tripwire";
    case "do_not_disturb": return "Do not disturb";
    case "timeout": return "Nobody answered in time";
    case "human": return "You decided";
    case "unapproved": return "Agent awaiting approval";
    case "cancelled": return "Caller cancelled";
    case "observed": return "Native action observed";
  }
}
