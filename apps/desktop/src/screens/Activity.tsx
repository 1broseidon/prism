import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { FeedRow } from "../feed";
import { hostName } from "../hosts";
import { agents, audit, errorMessage, replace } from "../state";
import type { ActivityFilter } from "../state";
import type { AuditPage } from "../types";
import { Button, Chip, Label, Screen, Segmented, describeError } from "../ui";

function dayText(day: string): string {
  return new Date(`${day}T12:00:00`).toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

/** A stable view of the retained rows behind a count; incoming events never shift its pages. */
export function ActivityScreen({ filter }: { filter: ActivityFilter }) {
  const [page, setPage] = useState<AuditPage | null>(null);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const latest = audit.value[0];
  const key = JSON.stringify(filter);
  useEffect(() => {
    let active = true;
    setFailed(false);
    api.listAuditPage(filter).then(p => { if (active) setPage(p); }).catch(e => {
      if (active) { setFailed(true); errorMessage.value = describeError(e); }
    });
    return () => { active = false; };
  }, [key]);
  const more = async () => {
    if (!page || busy) return;
    setBusy(true);
    try {
      const next = await api.listAuditPage({...filter, at: page.window.snapshot_at}, page.entries.length);
      if (next.total !== page.total) { setFailed(true); errorMessage.value = "Retained history changed. Refresh the log."; return; }
      setPage({...next, entries: [...page.entries, ...next.entries]});
    } catch (e) { errorMessage.value = describeError(e); }
    finally { setBusy(false); }
  };
  const narrow = (patch: Partial<ActivityFilter>) => replace({ kind: "activity", ...filter, ...patch });
  const refresh = () => narrow({at: new Date().toISOString()});
  const agentName = filter.agentId ? (agents.value.find(a => a.id === filter.agentId)?.name ?? hostName(filter.agentId)) : null;
  const chips: { key: keyof ActivityFilter; text: string }[] = [
    ...(agentName ? [{ key: "agentId" as const, text: agentName }] : []),
    ...(filter.day ? [{ key: "day" as const, text: dayText(filter.day) }] : []),
    ...(filter.reason ? [{ key: "reason" as const, text: filter.reason.replace(/_/g, " ") }] : []),
    ...(filter.nativeOnly ? [{ key: "nativeOnly" as const, text: "Observed" }] : []),
  ];
  return <div class="screen pushed"><Screen log>
    <div class="section feed">
      <Label right={page ? <span>{page.entries.length < page.total ? `${page.entries.length} / ${page.total}` : page.total}</span> : null}>
        <Segmented small label="Which actions" value={filter.attention ? "attention" : "all"}
          options={[{value:"all", label:"All"}, {value:"attention", label:"Needed attention"}]}
          onChange={v => narrow({attention: v === "attention" ? true : undefined})} />
      </Label>
      <div class="history-controls">
        <select aria-label="History period" value={filter.days ?? 7} onChange={e => narrow({days: Number(e.currentTarget.value), day:undefined})}>
          <option value={7}>Last 7 days</option><option value={30}>Last 30 days</option>
        </select>
        <button type="button" class="link" onClick={refresh}>{latest && page && latest.at > page.window.snapshot_at ? "New actions · Refresh" : "Refresh"}</button>
      </div>
      {chips.length ? <div class="filters">{chips.map(c => <button type="button" class="filter" key={c.key} onClick={() => narrow({[c.key]: undefined})} title="Remove this filter"><Chip>{c.text}</Chip><span aria-hidden="true">×</span></button>)}</div> : null}
      <p class="hint">Retained events only · up to 30 days / 20 MiB.</p>
      {failed ? <Button variant="quiet" onClick={refresh}>Retry history</Button> : page === null ? <div class="muted small">Loading…</div> : page.entries.length === 0 ? <div class="muted small">Nothing here.</div> : page.entries.map(entry => <FeedRow key={entry.id} entry={entry} />)}
      {page?.has_more && !failed ? <Button variant="quiet" busy={busy} onClick={() => void more()}>Load more</Button> : null}
    </div>
  </Screen></div>;
}
