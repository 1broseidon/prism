import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { FeedRow } from "../feed";
import { hostName } from "../hosts";
import { agents, audit, errorMessage, replace } from "../state";
import type { ActivityFilter } from "../state";
import type { AuditEntry } from "../types";
import { Chip, Label, Screen, Segmented, describeError } from "../ui";

function dayText(day: string): string {
  return new Date(`${day}T12:00:00`).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" });
}

/** The rows behind a number. Every filter is a chip; drop one and the list widens. */
export function ActivityScreen({ filter }: { filter: ActivityFilter }) {
  const [rows, setRows] = useState<AuditEntry[] | null>(null);
  // A new entry anywhere means this list is stale too.
  const latest = audit.value[0]?.id;
  const key = JSON.stringify(filter);

  useEffect(() => {
    api
      .listAudit(300, filter)
      .then(setRows)
      .catch((err) => {
        errorMessage.value = describeError(err);
      });
  }, [key, latest]);

  const visible = (rows ?? []).filter((e) => !e.native?.via_prism);
  const narrow = (patch: Partial<ActivityFilter>) => replace({ kind: "activity", ...filter, ...patch });
  const agentName = filter.agentId ? (agents.value.find((a) => a.id === filter.agentId)?.name ?? hostName(filter.agentId)) : null;
  const chips: { key: keyof ActivityFilter; text: string }[] = [
    ...(agentName ? [{ key: "agentId" as const, text: agentName }] : []),
    ...(filter.day ? [{ key: "day" as const, text: dayText(filter.day) }] : []),
    ...(filter.reason ? [{ key: "reason" as const, text: filter.reason.replace(/_/g, " ") }] : []),
  ];

  return (
    <div class="screen pushed">
      <Screen log>
        <div class="section feed">
          <Label right={rows ? <span>{visible.length}</span> : null}>
            <Segmented
              small
              label="Which actions"
              value={filter.attention ? "attention" : "all"}
              options={[
                { value: "all", label: "All" },
                { value: "attention", label: "Needed attention" },
              ]}
              onChange={(v) => narrow({ attention: v === "attention" ? true : undefined })}
            />
          </Label>
          {chips.length ? (
            <div class="filters">
              {chips.map((c) => (
                <button type="button" class="filter" key={c.key} onClick={() => narrow({ [c.key]: undefined })} title="Remove this filter">
                  <Chip>{c.text}</Chip>
                  <span aria-hidden="true">×</span>
                </button>
              ))}
            </div>
          ) : null}
          {rows === null ? (
            <div class="muted small">Loading…</div>
          ) : visible.length === 0 ? (
            <div class="muted small">Nothing here.</div>
          ) : (
            visible.map((entry) => <FeedRow key={entry.id} entry={entry} />)
          )}
        </div>
      </Screen>
    </div>
  );
}
