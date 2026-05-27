import { useEffect, useState } from "preact/hooks";
import { getAnalyticsStatus, type AnalyticsStatus } from "../api/analytics";
import { DecisionLog } from "../components/analytics/DecisionLog";
import { PolicyCoverage } from "../components/analytics/PolicyCoverage";

type Tab = "log" | "coverage";

export function Analytics() {
  const [tab, setTab] = useState<Tab>("log");
  const [status, setStatus] = useState<AnalyticsStatus | null>(null);
  useEffect(() => {
    getAnalyticsStatus().then(setStatus).catch(() => setStatus(null));
  }, []);
  return (
    <>
      <div class="page-header">
        <div>
          <div class="page-title">Grant analytics</div>
          <div class="page-subtitle">
            Capability decisions, drift, and template coverage
            {status ? ` / retention ${status.retention_days}d / ${status.store?.event_count ?? 0} events / ${formatBytes(status.store?.size_bytes ?? 0)}` : ""}
          </div>
        </div>
      </div>
      <div class="tabs">
        <button class={tab === "log" ? "tab active" : "tab"} onClick={() => setTab("log")}>Decision log</button>
        <button class={tab === "coverage" ? "tab active" : "tab"} onClick={() => setTab("coverage")}>Coverage</button>
      </div>
      {tab === "log" ? <DecisionLog /> : <PolicyCoverage />}
    </>
  );
}

function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value >= 10 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`;
}
