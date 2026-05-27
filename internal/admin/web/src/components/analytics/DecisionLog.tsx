import { useEffect, useState } from "preact/hooks";
import { listGrantEvents, type GrantEvent } from "../../api/analytics";

export function DecisionLog() {
  const [events, setEvents] = useState<GrantEvent[]>([]);
  const [agent, setAgent] = useState("");
  const [outcome, setOutcome] = useState("");
  const [live, setLive] = useState(false);

  const load = () => listGrantEvents({ agent_id: agent, outcome, limit: 50 }).then(setEvents);
  useEffect(() => { load(); }, []);
  useEffect(() => {
    if (!live) return;
    const es = new EventSource("/api/v1/analytics/events/tail");
    es.addEventListener("grant", (msg) => {
      const event = JSON.parse((msg as MessageEvent).data) as GrantEvent;
      setEvents((prev) => [event, ...prev].slice(0, 50));
    });
    return () => es.close();
  }, [live]);

  return (
    <section class="panel">
      <div class="audit-controls">
        <input class="search-input audit-search" placeholder="agent id" value={agent} onInput={(e) => setAgent((e.currentTarget as HTMLInputElement).value)} />
        <select class="audit-select" value={outcome} onChange={(e) => setOutcome((e.currentTarget as HTMLSelectElement).value)}>
          <option value="">all outcomes</option>
          <option value="allowed">allowed</option>
          <option value="denied">denied</option>
          <option value="challenged">challenged</option>
        </select>
        <button class="audit-export" type="button" onClick={load}>apply</button>
        <label><input type="checkbox" checked={live} onChange={(e) => setLive((e.currentTarget as HTMLInputElement).checked)} /> live</label>
      </div>
      <div class="audit-table">
        <div class="audit-table-header"><span>decision log</span><span>{events.length} rows</span></div>
        {events.map((e) => (
          <div class={e.Outcome === "allowed" ? "audit-row" : "audit-row audit-row-denied"} key={e.RequestID || `${e.TemplateHash}-${e.TokenJTI}`}>
            <div class="audit-status">{e.Outcome}</div>
            <div class="audit-agent">{e.AgentID || e.ClientID}</div>
            <div class="audit-tool">{e.Backend}/{e.Tool}</div>
            <div class="audit-latency">{e.Trace?.deny_dim || ""}</div>
          </div>
        ))}
      </div>
    </section>
  );
}
