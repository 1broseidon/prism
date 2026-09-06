import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { FeedRow } from "../feed";
import { audit, errorMessage, feedFilter } from "../state";
import type { AuditEntry } from "../types";
import { Label, Screen, Segmented, describeError } from "../ui";

/** The full list behind the summary: every recorded action, or one agent's. Newest first. */
export function ActivityScreen({ agentId }: { agentId?: string }) {
  const [rows, setRows] = useState<AuditEntry[] | null>(null);
  const filter = feedFilter.value;
  // A new entry anywhere means this list is stale too.
  const latest = audit.value[0]?.id;

  useEffect(() => {
    api
      .listAudit(200, agentId)
      .then(setRows)
      .catch((err) => {
        errorMessage.value = describeError(err);
      });
  }, [agentId, latest]);

  const visible = (rows ?? [])
    .filter((e) => !e.native?.via_prism)
    .filter((e) => (agentId || filter === "all" ? true : filter === "native" ? !!e.native : !e.native));

  return (
    <div class="screen pushed">
      <Screen log>
        <div class="section feed">
          <Label right={rows ? <span>{visible.length} shown</span> : null}>{agentId ? "Actions" : "Every action"}</Label>
          {!agentId && rows?.some((e) => e.native) ? (
            <Segmented
              small
              label="Feed filter"
              value={filter}
              options={[
                { value: "all", label: "All" },
                { value: "mcp", label: "MCP" },
                { value: "native", label: "Native" },
              ]}
              onChange={(v) => (feedFilter.value = v)}
            />
          ) : null}
          {rows === null ? (
            <div class="muted small">Loading…</div>
          ) : visible.length === 0 ? (
            <div class="muted small">No actions recorded yet.</div>
          ) : (
            visible.map((entry) => <FeedRow key={entry.id} entry={entry} />)
          )}
        </div>
      </Screen>
    </div>
  );
}
