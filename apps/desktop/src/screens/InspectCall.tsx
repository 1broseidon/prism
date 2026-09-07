import { useLayoutEffect } from "preact/hooks";
import { pending, pop, replace, resetNavigation, stack } from "../state";
import { mmss } from "../time";
import { Button, Screen } from "../ui";
import { callSecondsLeft, decide, decisionBusy, primaryScope, useDecisionKeys, useRequestQueue } from "./Now";

export function InspectCallScreen({ callId }: { callId: string }) {
  const { current } = useRequestQueue();
  const call = pending.value.find((p) => p.id === callId);
  useLayoutEffect(() => {
    if (call) return;
    const top = stack.value[stack.value.length - 1];
    if (top?.kind !== "inspect-call" || top.callId !== callId) return;
    if (current?.kind === "call") replace({ kind: "inspect-call", callId: current.call.id });
    else resetNavigation();
  }, [call, callId, current?.key]);

  const answer = async (approve: boolean) => {
    if (!call) return;
    await decide(call, approve ? "allow" : "deny", approve ? primaryScope(call) : "once");
  };
  useDecisionKeys((approve) => void answer(approve));

  return (
    <div class="screen pushed">
    <Screen log footer={call ? (
      <>
        <Button variant="danger" busy={decisionBusy.value} hint="D" onClick={() => void answer(false)}>Deny</Button>
        <Button variant="primary" busy={decisionBusy.value} hint="A" title={primaryScope(call) === "always" ? "Remembered for this tool" : "This call only"} onClick={() => void answer(true)}>Allow</Button>
      </>
    ) : <Button onClick={pop}>Back</Button>}>
      {call ? (
        <>
          <div class="muted small">
            {call.agent_name} → <code>{call.tool}</code> on {call.server_name} · {mmss(callSecondsLeft(call))} left
          </div>
          <pre class="code full">{JSON.stringify(call.arguments, null, 2)}</pre>
        </>
      ) : <div class="muted">This request is no longer waiting.</div>}
    </Screen>
    </div>
  );
}
