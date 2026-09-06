import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { loadNativeStatus } from "../events";
import { agents, errorMessage, native, status } from "../state";
import { relative } from "../time";
import { Button, Chip, CodeBlock, Label, Screen, describeError } from "../ui";

/** One agent host: how to connect its hooks, whether they are talking, and what is being recorded. */
export function HostScreen({ agentId }: { agentId: string }) {
  const agent = agents.value.find((a) => a.id === agentId);
  const st = native.value;
  const [snippet, setSnippet] = useState<string>("");
  const [wrote, setWrote] = useState<{ path: string; backup: string | null } | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.getClaudeHookSnippet().then(setSnippet).catch(() => setSnippet(""));
    loadNativeStatus();
  }, [st?.hook_url]);

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
      setWrote(await api.installClaudeHook());
    });
  const rotate = () =>
    run(async () => {
      await api.rotateHookToken();
      setWrote(null);
    });
  const revoke = (deny: boolean) =>
    run(async () => {
      await api.decideAgent(agentId, !deny);
      agents.value = await api.listAgents();
      status.value = await api.getStatus();
    });

  const revoked = agent?.status === "denied";
  const coverage = revoked ? (
    <Chip tone="danger">revoked</Chip>
  ) : st?.observe_native && st.last_event_at ? (
    <Chip tone="ok">observed</Chip>
  ) : (
    <Chip>MCP only</Chip>
  );

  return (
    <div class="screen pushed">
      <Screen>
        <section class="section">
          <Label right={coverage}>Coverage</Label>
          <p class="hint">
            {revoked
              ? "Prism refuses this host's hook events. Restore it to start recording again."
              : st?.last_event_at
                ? `Recording. Last action ${relative(st.last_event_at)}, ${st.actions_7d} this week. Nothing is held or changed; Claude Code's own permissions still apply.`
                : st?.hook_installed
                  ? "The hook is in place. The first action Claude Code takes will show up in the Now feed."
                  : "Only MCP calls through Prism are visible. Add the hook below and every command, file edit and fetch is recorded too."}
          </p>
        </section>

        <section class="section">
          <Label right={st?.hook_installed ? <Chip tone="ok">installed</Chip> : null}>Hook</Label>
          <p class="hint">
            Claude Code posts each action to Prism before it runs and carries on regardless of the answer. Prism
            keeps one line per action, redacted; never the raw input.
          </p>
          <div class="actions update-actions">
            <Button variant="primary" busy={busy} onClick={() => void install()}>
              {st?.hook_installed ? "Rewrite the hook" : "Write it for me"}
            </Button>
            <Button variant="quiet" busy={busy} onClick={() => void rotate()}>
              Rotate token
            </Button>
          </div>
          {wrote ? (
            <p class="hint">
              Written to {wrote.path}
              {wrote.backup ? `, previous file kept as ${wrote.backup}` : ""}. Running Claude Code sessions pick
              it up on their next action.
            </p>
          ) : (
            <p class="hint">Merges into {st?.settings_path ?? "~/.claude/settings.json"} and keeps a backup. Or paste this yourself:</p>
          )}
          <CodeBlock text={snippet} copyable emptyText="Loading…" />
        </section>

        <section class="section">
          <Label>Would have asked</Label>
          <p class="hint">
            A short list runs in shadow to measure how often a real gate would interrupt. It marks entries in the
            feed and counts them in Settings; it never stops anything.
          </p>
          <ul class="shadow-rules">
            {(st?.rules ?? []).map((rule) => {
              const count = st?.by_reason.find((r) => r.reason === rule.id)?.count ?? 0;
              return (
                <li key={rule.id}>
                  <span class="mono">{rule.id.replace(/_/g, " ")}</span>
                  <span class="hint">{rule.summary}</span>
                  <span class={`shadow-count ${count ? "hit" : ""}`}>{count}</span>
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
              <Button variant="quiet" class="danger" busy={busy} onClick={() => void revoke(true)}>
                Refuse this host
              </Button>
            )}
          </div>
          <p class="hint">
            Coverage depends on Claude Code honouring its own hooks. Prism shows what it can see and never claims
            more; it does not sandbox anything.
          </p>
        </section>
      </Screen>
    </div>
  );
}
