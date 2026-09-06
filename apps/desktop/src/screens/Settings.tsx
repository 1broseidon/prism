import type * as preact from "preact";
import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { errorMessage, status, update, updateProgress } from "../state";
import type { Settings, UpdateStatus } from "../types";
import { Button, Chip, Label, Screen, Segmented, Switch, describeError } from "../ui";
import { native } from "../state";
import { loadNativeStatus } from "../events";

const RELEASES_URL = "https://github.com/1broseidon/prism/releases/latest";
const releaseUrl = (version: string) => `https://github.com/1broseidon/prism/releases/tag/v${version}`;

/** Inline markdown from the changelog: code spans, bold, links reduced to their text. */
function inline(text: string) {
  const out: preact.ComponentChildren[] = [];
  const re = /`([^`]+)`|\*\*([^*]+)\*\*|\[([^\]]+)\]\([^)]*\)/g;
  let last = 0;
  for (const m of text.matchAll(re)) {
    if (m.index! > last) out.push(text.slice(last, m.index));
    if (m[1] !== undefined) out.push(<code>{m[1]}</code>);
    else if (m[2] !== undefined) out.push(<strong>{m[2]}</strong>);
    else out.push(m[3]);
    last = m.index! + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

/** Release notes are the changelog section: `### Added` headings and `- ` bullets, nothing fancier. */
function Notes({ text }: { text: string }) {
  const blocks: preact.ComponentChildren[] = [];
  let items: string[] = [];
  const flush = () => {
    if (items.length) blocks.push(<ul>{items.map((item) => <li>{inline(item)}</li>)}</ul>);
    items = [];
  };
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const heading = /^#{1,6}\s+(.*)$/.exec(line);
    const bullet = /^[-*]\s+(.*)$/.exec(line);
    if (heading) {
      flush();
      blocks.push(<div class="notes-heading">{heading[1]}</div>);
    } else if (bullet) {
      items.push(bullet[1]);
    } else if (items.length) {
      items[items.length - 1] += ` ${line}`;
    } else {
      blocks.push(<p>{inline(line)}</p>);
    }
  }
  flush();
  return <div class="update-notes">{blocks}</div>;
}

function mb(bytes: number): string {
  return `${(bytes / 1_000_000).toFixed(1)} MB`;
}

/** Version, a manual check, and the one-button install when something newer exists. */
function UpdatesSection() {
  const [info, setInfo] = useState<UpdateStatus | null>(null);
  const [checking, setChecking] = useState(false);
  const [checkedNow, setCheckedNow] = useState<"none" | "latest" | "found">("none");
  const progress = updateProgress.value;
  const available = update.value;

  useEffect(() => {
    api.getUpdateStatus().then(setInfo).catch(() => setInfo(null));
  }, []);

  const check = async () => {
    setChecking(true);
    setCheckedNow("none");
    try {
      const found = await api.checkUpdate();
      update.value = found;
      setCheckedNow(found ? "found" : "latest");
      setInfo(await api.getUpdateStatus());
    } catch (err) {
      errorMessage.value = describeError(err);
    } finally {
      setChecking(false);
    }
  };

  const install = async () => {
    try {
      await api.installUpdate();
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };

  const busy = progress?.state === "downloading" || progress?.state === "installing";
  const installable = available?.installable ?? info?.installable ?? true;

  return (
    <section class="section">
      <Label right={<span class="mono">v{info?.current ?? "…"}</span>}>Updates</Label>
      {available ? (
        <div class="update">
          <div class="update-title">Prism {available.version} is ready</div>
          {available.notes ? <Notes text={available.notes} /> : null}
          <a class="update-link" href={releaseUrl(available.version)} target="_blank" rel="noreferrer">
            Full release notes
          </a>
          {progress?.state === "downloading" ? (
            <div class="update-progress" role="progressbar" aria-valuemin={0} aria-valuemax={progress.total ?? undefined} aria-valuenow={progress.downloaded}>
              <span style={{ width: progress.total ? `${Math.min(100, (progress.downloaded / progress.total) * 100)}%` : "30%" }} />
            </div>
          ) : null}
          {progress?.state === "downloading" ? (
            <p class="hint">
              Downloading {mb(progress.downloaded)}{progress.total ? ` of ${mb(progress.total)}` : ""}
            </p>
          ) : progress?.state === "installing" ? (
            <p class="hint">Installing…</p>
          ) : progress?.state === "error" ? (
            <p class="hint danger">{progress.message}</p>
          ) : installable ? (
            <p class="hint">Installs and restarts Prism.</p>
          ) : (
            <p class="hint">Packaged install: update from the release page.</p>
          )}
          <div class="actions update-actions">
            {installable ? (
              <Button variant="primary" busy={busy} onClick={() => void install()}>
                {busy ? "Updating" : "Install and restart"}
              </Button>
            ) : (
              <a class="btn primary" href={RELEASES_URL} target="_blank" rel="noreferrer">
                Open release page
              </a>
            )}
          </div>
        </div>
      ) : (
        <div class="list">
          <div class="setting">
            <div>
              <div class="setting-title">{checkedNow === "latest" ? "Up to date" : "Checks every 6 hours"}</div>
              <div class="hint">
                {info?.checked_at ? `Last checked ${new Date(info.checked_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}.` : "Not checked yet."}
              </div>
            </div>
            <Button busy={checking} onClick={() => void check()}>
              Check now
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}

/** Operator-level knobs. Every control saves as soon as it changes. */
function NativeSection() {
  const st = native.value;
  const [exported, setExported] = useState<string | null>(null);
  const toggle = async (on: boolean) => {
    try {
      await api.setObserveNative(on);
      loadNativeStatus();
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };
  const exportReport = async () => {
    try {
      setExported(await api.exportNativeReport());
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };
  return (
    <section class="section">
      <Label right={st?.last_event_at ? <Chip tone="ok">observed</Chip> : <Chip>none yet</Chip>}>Native actions</Label>
      <div class="list">
        <div class="setting">
          <div>
            <div class="setting-title">Observe native actions</div>
            <div class="hint">Commands, files, fetches from hooked hosts. Logged only.</div>
          </div>
          <Switch label="Observe native actions" checked={st?.observe_native ?? true} onChange={(v) => void toggle(v)} />
        </div>
      </div>
      <div class="actions update-actions">
        <Button variant="quiet" onClick={() => void exportReport()}>
          Export needed attention, 30 days
        </Button>
      </div>
      {exported ? <p class="hint">Saved to {exported}</p> : null}
    </section>
  );
}

export function SettingsScreen() {
  const [settings, setSettings] = useState<Settings | null>(null);

  useEffect(() => {
    api.getSettings().then(setSettings).catch((err) => {
      errorMessage.value = describeError(err);
    });
  }, []);

  if (!settings) return <div class="screen pushed" />;

  const save = async (patch: Partial<Settings>) => {
    const next = { ...settings, ...patch };
    setSettings(next);
    try {
      await api.setSettings(next);
      status.value = await api.getStatus();
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };

  const number = (value: string, fallback: number) => {
    const n = Number.parseInt(value, 10);
    return Number.isFinite(n) ? n : fallback;
  };

  return (
    <div class="screen pushed">
      <Screen>
        <section class="section">
          <Label>Interruptions</Label>
          <div class="list">
            <div class="setting">
              <div>
                <div class="setting-title">Do not disturb</div>
                <div class="hint">Held calls use the rule below. New agents still ask.</div>
              </div>
              <Switch label="Do not disturb" checked={settings.do_not_disturb} onChange={(v) => void save({ do_not_disturb: v })} />
            </div>
            <div class="setting">
              <div>
                <div class="setting-title">Open on hold</div>
                <div class="hint">Off: notification and badge only.</div>
              </div>
              <Switch label="Open the panel on request" checked={settings.auto_open_on_pending} onChange={(v) => void save({ auto_open_on_pending: v })} />
            </div>
          </div>
        </section>

        <section class="section">
          <Label>When nobody answers</Label>
          <Segmented
            label="Timeout behaviour"
            value={settings.on_timeout}
            options={[
              { value: "deny", label: "Deny the call" },
              { value: "allow_read_only", label: "Allow if read-only" },
            ]}
            onChange={(on_timeout) => void save({ on_timeout })}
          />
          <p class="hint">
            {settings.on_timeout === "deny"
              ? "Refused. The agent can retry."
              : "Read-only tools pass. Writes are refused."}
          </p>
        </section>

        <section class="section">
          <Label>Limits</Label>
          <div class="list">
            <label class="setting">
              <div>
                <div class="setting-title">Hold a call for</div>
                <div class="hint">Before the rule above applies.</div>
              </div>
              <span class="num">
                <input
                  class="input mono"
                  type="number"
                  min={10}
                  max={3600}
                  value={settings.hold_timeout_secs}
                  onChange={(e) => void save({ hold_timeout_secs: Math.max(10, number((e.currentTarget as HTMLInputElement).value, 120)) })}
                />
                <span>s</span>
              </span>
            </label>
            <label class="setting">
              <div>
                <div class="setting-title">Rate tripwire</div>
                <div class="hint">Above this, allowed calls ask. Empty: off.</div>
              </div>
              <span class="num">
                <input
                  class="input mono"
                  type="number"
                  min={0}
                  max={10000}
                  placeholder="off"
                  value={settings.rate_limit_per_minute ?? ""}
                  onChange={(e) => {
                    const raw = (e.currentTarget as HTMLInputElement).value.trim();
                    void save({ rate_limit_per_minute: raw === "" ? null : Math.max(0, number(raw, 0)) || null });
                  }}
                />
                <span>/min</span>
              </span>
            </label>
          </div>
        </section>
        <NativeSection />
        <UpdatesSection />
      </Screen>
    </div>
  );
}
