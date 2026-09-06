import { useEffect } from "preact/hooks";
import { loadNativeStatus } from "../events";
import { hostSetup, hostStatus } from "../hosts";
import { native, push } from "../state";
import { Button, Chip, Label } from "../ui";

/** Connection setup and observed patterns for this harness. */
export function HarnessSections({ agentId, host }: { agentId: string; host: string }) {
  const st = native.value;
  const hs = hostStatus(st, host);
  const setup = hostSetup(st, host);
  useEffect(() => { loadNativeStatus(); }, [host]);
  const reasons = hs?.by_reason ?? [];
  const asked = reasons.reduce((n, r) => n + r.count, 0);
  return <>
    <section class="section">
      <Label right={<Chip tone={setup?.events_received && st?.observe_native ? "ok" : undefined}>{!st?.observe_native || setup?.hooks_disabled ? "observation off" : setup?.events_received ? "observed" : setup?.hook_installed ? "configured" : "not configured"}</Chip>}>Setup</Label>
      <Button variant="quiet" onClick={() => push({ kind: "harness-setup", host })}>{setup?.hook_installed || setup?.mcp_configured ? "Manage setup" : "Set up MCP + observation"}</Button>
    </section>
      <section class="section">
        <Label right={<span class={asked ? "accent" : ""}>{asked}</span>}>Watch list</Label>
        <ul class="shadow-rules">
          {(st?.rules ?? []).map((rule) => {
            const count = reasons.find((r) => r.reason === rule.id)?.count ?? 0;
            const body = (
              <>
                <span class="mono">{rule.id.replace(/_/g, " ")}</span>
                <span class={`shadow-count ${count ? "hit" : ""}`}>{count}</span>
              </>
            );
            return (
              <li key={rule.id} title={rule.summary}>
                {count ? (
                  <button type="button" class="row-btn" onClick={() => push({ kind: "activity", agentId, reason: rule.id, nativeOnly: true, days: 7, at: st?.window.snapshot_at })}>
                    {body}
                  </button>
                ) : (
                  body
                )}
              </li>
            );
          })}
        </ul>
      </section>
  </>;
}
