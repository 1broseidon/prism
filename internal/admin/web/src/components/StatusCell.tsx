import type { Agent } from "../api/types";
import { fmtAge } from "../util/time";

interface Props {
  agent: Agent;
}

export function StatusCell({ agent }: Props) {
  const ts = agent.last_used_at || agent.created_at;
  if (!ts) {
    return (
      <div class="status-cell">
        <div class="dot faded" />
        <span class="time">new</span>
      </div>
    );
  }
  const age = Date.now() - new Date(ts).getTime();
  const days = Math.floor(age / 86400000);
  const label = fmtAge(ts);
  if (days > 7) {
    return (
      <div class="status-cell">
        <div class="dot stale" />
        <span class="time stale">{label}</span>
      </div>
    );
  }
  if (Math.floor(age / 60000) < 5) {
    return (
      <div class="status-cell">
        <div class="dot" />
        <span class="time fresh">{label}</span>
      </div>
    );
  }
  return (
    <div class="status-cell">
      <div class="dot faded" />
      <span class="time">{label}</span>
    </div>
  );
}
