import type { AgentGrantResolution, GrantEvent } from "../../api/analytics";

interface Props {
  grant?: AgentGrantResolution;
}

export function PerAgent({ grant }: Props) {
  const bindings = grant?.bindings || [];
  const tokens = grant?.live_tokens || [];
  const decisions = grant?.recent_decisions || [];
  return (
    <section class="panel">
      <div class="panel-header">
        <div>
          <h2>Grant posture</h2>
          <p>{bindings.length} bindings · {tokens.length} recent token keys · {grant?.drift_count_24h || 0} drift denials in 24h</p>
        </div>
      </div>
      <div class="table-wrap">
        <table class="events-table">
          <thead>
            <tr><th>template</th><th>binding</th><th>via</th><th>24h deny</th></tr>
          </thead>
          <tbody>
            {bindings.length === 0 ? (
              <tr><td colSpan={4}>no grant bindings</td></tr>
            ) : bindings.map((b) => (
              <tr key={b.id}>
                <td><code>{b.template_hash}</code></td>
                <td>{b.template_id}</td>
                <td>{b.via || "direct"}</td>
                <td>{grant?.top_deny_dim_24h || "none"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div class="activity-list">
        {decisions.slice(0, 50).map((event) => <DecisionRow event={event} key={event.RequestID || `${event.TemplateHash}-${event.TokenJTI}`} />)}
      </div>
    </section>
  );
}

function DecisionRow({ event }: { event: GrantEvent }) {
  return (
    <details class="audit-detail">
      <summary>
        <span class={event.Outcome === "allowed" ? "audit-detail-value-ok" : "audit-detail-value-error"}>
          {event.Outcome || "unknown"}
        </span>{" "}
        {event.Backend}/{event.Tool} <code>{event.TemplateHash}</code>
      </summary>
      <pre>{JSON.stringify(event.Trace || {}, null, 2)}</pre>
    </details>
  );
}
