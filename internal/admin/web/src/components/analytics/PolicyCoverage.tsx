import { useEffect, useState } from "preact/hooks";
import { listTemplateAggregates, type TemplateAggregate } from "../../api/analytics";

export function PolicyCoverage() {
  const [templates, setTemplates] = useState<TemplateAggregate[]>([]);
  const [selected, setSelected] = useState<TemplateAggregate | null>(null);
  useEffect(() => {
    listTemplateAggregates("24h").then(setTemplates);
  }, []);
  return (
    <section class="panel">
      <div class="panel-header"><h2>Policy coverage</h2></div>
      <div class="audit-stats">
        {templates.map((t) => (
          <button class="audit-stat" type="button" key={t.template_hash} onClick={() => setSelected(t)}>
            <div class="audit-stat-label">{t.template_id || "template"}</div>
            <div class="audit-stat-value">{t.allow_24h}/{t.deny_24h}</div>
            <code>{t.template_hash}</code>
          </button>
        ))}
      </div>
      {selected && (
        <div class="audit-detail">
          <div class="audit-detail-row"><span class="audit-detail-label">active tokens</span><span>{selected.active_token_count}</span></div>
          <div class="audit-detail-row"><span class="audit-detail-label">drift</span><span>{selected.drift_events_24h}</span></div>
          <pre>{JSON.stringify(selected.top_deny_dims || [], null, 2)}</pre>
        </div>
      )}
    </section>
  );
}
