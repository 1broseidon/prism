import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { loadNativeStatus } from "../events";
import { hostOf, hostSetup, hostStatus } from "../hosts";
import { agents, errorMessage, native, push, status } from "../state";
import { relative } from "../time";
import { Button, Chip, CodeBlock, ConfirmButton, Label, Screen, describeError, useCopy } from "../ui";

/** One agent host: whether its hook is talking, how to set it up, and what would have been asked. */
export function HostScreen({ agentId }: { agentId: string }) {
  const agent = agents.value.find((a) => a.id === agentId);
  const known = hostOf(agentId);
  const host = known?.host ?? agent?.host ?? "";
  const st = native.value;
  const hs = hostStatus(st, host);
  const setup = hostSetup(st, host);
  const [snippet, setSnippet] = useState<string>("");
  const [showSnippet, setShowSnippet] = useState(false);
  const [wrote, setWrote] = useState(false);
  const [busy, setBusy] = useState(false);
  const [copyState, copy] = useCopy();

  useEffect(() => {
    if (!host) return;
    api
      .getHostHookSnippet(host)
      .then(setSnippet)
      .catch(() => setSnippet(""));
    loadNativeStatus();
  }, [host, hs?.hook_url]);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    try {
      await fn();
      loadNativeStatus();
    } catch (err) {
      errorMessage.value = describeError(err);
    } finally {
      setBusy(false);
    }
  };

  const install = () =>
    run(async () => {
      await api.installHostHook(host);
      setWrote(true);
    });
  const rotate = () =>
    run(async () => {
      await api.rotateHookToken();
      setWrote(false);
    });
  const revoke = (deny: boolean) =>
    run(async () => {
      await api.decideAgent(agentId, !deny);
      agents.value = await api.listAgents();
      status.value = await api.getStatus();
    });

  const revoked = agent?.status === "denied";
  const installed = setup?.hook_installed ?? false;
  const needsReview = installed && setup?.hook_trusted === false;
  const isCodex = host === "codex";
  const path = setup?.settings_path ?? (isCodex ? "~/.codex/hooks.json" : "~/.claude/settings.json");
  const coverage = revoked ? (
    <Chip tone="danger">refused</Chip>
  ) : st?.observe_native && hs?.last_event_at ? (
    <Chip tone="ok">observed</Chip>
  ) : needsReview ? (
    <Chip tone="warn">review in Codex</Chip>
  ) : installed ? (
    <Chip tone="ok">hooked</Chip>
  ) : (
    <Chip>not hooked up</Chip>
  );
  const asked = st ? st.by_reason.reduce((n, r) => n + r.count, 0) : 0;

  return (
    <div class="screen pushed">
      <Screen>
        <div class="agent-head">
          {coverage}
          <span class="grow" />
          {hs?.last_event_at ? (
            <button type="button" class="link" onClick={() => push({ kind: "activity", agentId })}>
              {hs.actions_7d} this week · {relative(hs.last_event_at)} ›
            </button>
          ) : (
            <span class="sub">
              {revoked ? "events dropped" : needsReview ? "Codex skips it until trusted: /hooks" : installed ? "nothing yet" : ""}
            </span>
          )}
        </div>

        <section class="section">
          <Label right={installed ? <Chip tone="ok">installed</Chip> : null}>Hook</Label>
          <p class="hint">Posts each action to Prism before it runs. One redacted line is kept.</p>
          <div class="actions update-actions">
            <Button variant="primary" busy={busy} onClick={() => void install()}>
              {installed ? "Rewrite the hook" : "Write it for me"}
            </Button>
            <Button variant="quiet" busy={busy} onClick={() => void rotate()} title="New token; rewrite the hook afterwards">
              Rotate token
            </Button>
          </div>
          <p class="hint">{wrote ? (isCodex ? "Written. Trust it in Codex: /hooks." : "Written.") : `Writes ${path}, backup kept.`}</p>
          <div class="snippet-row">
            <button type="button" class="link" aria-expanded={showSnippet} onClick={() => setShowSnippet(!showSnippet)}>
              {showSnippet ? "Hide snippet" : "Show snippet"}
            </button>
            {showSnippet ? null : (
              <Button variant="quiet" state={copyState} disabled={!snippet} onClick={() => void copy(snippet)}>
                {copyState === "success" ? "Copied" : copyState === "error" ? "Copy failed" : "Copy"}
              </Button>
            )}
          </div>
          {showSnippet ? <CodeBlock text={snippet} copyable emptyText="Loading…" /> : null}
        </section>

        <section class="section">
          <Label right={<span class={asked ? "accent" : ""}>{asked}</span>}>Would have asked</Label>
          <ul class="shadow-rules">
            {(st?.rules ?? []).map((rule) => {
              const count = st?.by_reason.find((r) => r.reason === rule.id)?.count ?? 0;
              const body = (
                <>
                  <span class="mono">{rule.id.replace(/_/g, " ")}</span>
                  <span class={`shadow-count ${count ? "hit" : ""}`}>{count}</span>
                </>
              );
              return (
                <li key={rule.id} title={rule.summary}>
                  {count ? (
                    <button type="button" class="row-btn" onClick={() => push({ kind: "activity", agentId, reason: rule.id })}>
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

        <section class="section">
          <Label>Access</Label>
          <div class="actions update-actions">
            {revoked ? (
              <Button busy={busy} onClick={() => void revoke(false)}>
                Restore
              </Button>
            ) : (
              <ConfirmButton variant="quiet" class="danger" busy={busy} confirm="Refuse?" onConfirm={() => void revoke(true)}>
                Refuse
              </ConfirmButton>
            )}
          </div>
        </section>
      </Screen>
    </div>
  );
}
