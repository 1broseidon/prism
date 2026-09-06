import { useState } from "preact/hooks";
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

const reasonText = (id: string) => id.replace(/_/g, " ");

/** What the row cannot fit: the whole subject, why it was flagged, where and in which session. */
function Details({ entry }: { entry: AuditEntry }) {
  const n = entry.native;
  const lines: [string, string][] = n
    ? [
        ["subject", n.subject],
        ...(n.would_hold ? ([["matched", reasonText(n.would_hold)]] as [string, string][]) : []),
        ...(n.cwd ? ([["in", n.cwd]] as [string, string][]) : []),
        ...(n.session ? ([["session", n.session.slice(0, 8)]] as [string, string][]) : []),
      ]
    : [
        ["tool", entry.tool],
        ["server", entry.server_id],
        ["decided by", sourceText(entry)],
        ["took", `${entry.duration_ms} ms`],
        ...(entry.error ? ([["error", entry.error]] as [string, string][]) : []),
      ];
  return (
    <dl class="row-details">
      {lines.map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}

/** One audit entry. Tap to see everything the row had to cut. */
export function FeedRow({ entry }: { entry: AuditEntry }) {
  const [open, setOpen] = useState(false);
  const n = entry.native;
  const flagged = n ? !!n.would_hold : entry.verdict === "denied" || entry.source.kind === "human" || entry.source.kind === "timeout";
  return (
    <div class={`row ${n ? "native" : ""} ${flagged ? "would-hold" : ""} ${open ? "open" : ""}`}>
      <button type="button" class="row-main" aria-expanded={open} onClick={() => setOpen(!open)}>
        <time dateTime={entry.at}>{clock(entry.at)}</time>
        {n ? (
          <span class="who">
            <span class={`dot ${n.would_hold ? "accent" : ""}`} />
            <b>{nativeTool(entry)}</b>
            <span class="subject">{n.subject}</span>
          </span>
        ) : (
          <span class="who">
            <span class={`dot ${verdictTone(entry)}`} />
            <b>{entry.agent_name}</b>
            <code>{entry.tool}</code>
          </span>
        )}
        <span class="src">{n ? (n.would_hold ? reasonText(n.would_hold) : entry.agent_name.toLowerCase()) : sourceText(entry)}</span>
      </button>
      {open ? <Details entry={entry} /> : null}
    </div>
  );
}
