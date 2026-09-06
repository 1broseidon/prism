import { clock } from "./time";
import type { AuditEntry } from "./types";

function verdictTone(entry: AuditEntry): string {
  switch (entry.verdict) {
    case "allowed":
      return "ok";
    case "denied":
      return "danger";
    case "timeout":
      return "warn";
    default:
      return "danger";
  }
}

function sourceText(entry: AuditEntry): string {
  switch (entry.source.kind) {
    case "human":
      return "you";
    case "rule":
      return "rule";
    case "unapproved":
      return "unapproved";
    case "posture":
      return entry.source.posture.replace("_", " ");
    case "do_not_disturb":
      return "dnd";
    case "observed":
      return "seen";
    default:
      return "timeout";
  }
}

/** The tool as a short label. Host tool names are already short; MCP tools drop the server. */
function nativeTool(entry: AuditEntry): string {
  const t = entry.tool;
  if (t.startsWith("mcp__")) return t.split("__").slice(2).join("__") || t;
  return t;
}

function NativeRow({ entry }: { entry: AuditEntry }) {
  const n = entry.native!;
  const reason = n.would_hold ? `Would have asked: ${n.would_hold.replace(/_/g, " ")}` : undefined;
  return (
    <div class={`row native ${n.would_hold ? "would-hold" : ""}`} title={reason}>
      <time dateTime={entry.at}>{clock(entry.at)}</time>
      <span class="who">
        <span class={`dot ${n.would_hold ? "accent" : ""}`} />
        <b>{nativeTool(entry)}</b>
        <span class="subject">{n.subject}</span>
      </span>
      <span class="src">{n.would_hold ? "would ask" : entry.agent_name.toLowerCase()}</span>
    </div>
  );
}

/** One audit entry as a feed row: a native action seen through a hook, or an MCP call and its verdict. */
export function FeedRow({ entry }: { entry: AuditEntry }) {
  if (entry.native) return <NativeRow entry={entry} />;
  return (
    <div class="row">
      <time dateTime={entry.at}>{clock(entry.at)}</time>
      <span class="who">
        <span class={`dot ${verdictTone(entry)}`} />
        <b>{entry.agent_name}</b>
        <code>{entry.tool}</code>
      </span>
      <span class="src">{sourceText(entry)}</span>
      {entry.error ? <span class="err">{entry.error}</span> : null}
    </div>
  );
}
