import type * as preact from "preact";
import { signal } from "@preact/signals";
import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { errorMessage, push, status, update, updateProgress } from "../state";
import type { ListenAddress, ListenerState, PanelAnchor, Settings, UpdateStatus } from "../types";
import { Button, HubRow, Label, Screen, Segmented, StatusText, Switch, describeError } from "../ui";
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
  return <div class="update-summary">{blocks}</div>;
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
            Full release notes ↗
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
      const report = await api.exportNativeReport();
      setExported(`${report.total} matches. ${report.path}`);
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };
  return (
    <section class="section">
      <Label right={st?.last_event_at ? <StatusText tone="ok">Receiving</StatusText> : <StatusText>None yet</StatusText>}>Native actions</Label>
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
          Export observed matches
        </Button>
      </div>
      <p class="hint">Retained history: up to 30 days / 20 MiB. Coverage file included.</p>
      {exported ? <p class="hint">Saved {exported}</p> : null}
    </section>
  );
}

/** One line on what holds the port. A second Prism is the common case: a dev build next to the installed one. */
function holderText(state: Extract<ListenerState, { kind: "port_in_use" }>): string {
  return state.holder === "prism" ? "Another Prism is using it." : "Another program is using it.";
}

/** Shown above every root tab while agents cannot connect. Retry keeps the port; switching is
 * offered second, because every connected agent dials the port that is configured now. */
export function ListenerNotice({ state }: { state: ListenerState }) {
  const [busy, setBusy] = useState(false);
  if (state.kind === "listening") return null;
  const retry = async () => {
    setBusy(true);
    try {
      await api.retryListener();
      status.value = await api.getStatus();
    } catch (err) {
      errorMessage.value = describeError(err);
    } finally {
      setBusy(false);
    }
  };
  const title =
    state.kind === "port_in_use" ? `Port ${state.port} is in use.` :
    state.kind === "failed" ? `Port ${state.port} could not be opened.` :
    "The gateway is stopped.";
  const detail =
    state.kind === "port_in_use" ? holderText(state) :
    state.kind === "failed" ? state.error :
    "Agents cannot connect.";
  return (
    <div class="listener-notice" role="alert">
      <div>
        <div class="listener-title">{title}</div>
        <div class="hint">{detail}</div>
      </div>
      {state.kind === "stopped" ? null : (
        <div class="actions">
          <Button variant="primary" busy={busy} onClick={() => void retry()}>Retry</Button>
          <Button variant="quiet" onClick={() => push({ kind: "settings" })}>Use another port</Button>
        </div>
      )}
    </div>
  );
}

/** The port agents dial. Prism never moves off it on its own: a clash offers a free port, the operator decides. */
function NetworkSection() {
  const st = status.value;
  const [draft, setDraft] = useState<string | null>(null);
  const [suggested, setSuggested] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const clash = st?.listener.kind === "port_in_use" || st?.listener.kind === "failed";

  useEffect(() => {
    if (!clash) { setSuggested(null); return; }
    api.suggestPort().then(setSuggested).catch(() => setSuggested(null));
  }, [clash, st?.listen_port]);

  const reach = async (address: ListenAddress) => {
    if (address === st?.listen_address) return;
    setBusy(true);
    try {
      await api.setListenAddress(address);
      status.value = await api.getStatus();
    } catch (err) {
      errorMessage.value = describeError(err);
    } finally {
      setBusy(false);
    }
  };

  const move = async (port: number) => {
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      errorMessage.value = "Choose a port between 1 and 65535.";
      setDraft(null);
      return;
    }
    if (port === st?.listen_port && st?.listening) { setDraft(null); return; }
    setBusy(true);
    try {
      await api.setListenPort(port);
      status.value = await api.getStatus();
      setDraft(null);
    } catch (err) {
      errorMessage.value = describeError(err);
      setDraft(null);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section class="section">
      <Label right={st ? <StatusText tone={st.listening ? "ok" : "danger"}>{st.listening ? "Listening" : clash ? "Port in use" : "Stopped"}</StatusText> : null}>Network</Label>
      <div class="list">
        <label class="setting">
          <div>
            <div class="setting-title">Port</div>
            <div class="hint">
              {clash && st?.listener.kind === "port_in_use"
                ? holderText(st.listener)
                : "Changing it means updating every agent."}
            </div>
          </div>
          <span class="num">
            <input
              class="input mono"
              type="number"
              min={1}
              max={65535}
              aria-label="Listen port"
              disabled={busy}
              value={draft ?? st?.listen_port ?? ""}
              onInput={(e) => setDraft((e.currentTarget as HTMLInputElement).value)}
              onChange={(e) => void move(Number.parseInt((e.currentTarget as HTMLInputElement).value, 10))}
            />
          </span>
        </label>
      </div>
      <div class="reach">
        <Segmented
          label="Reachable from"
          value={st?.listen_address ?? "loopback"}
          options={[
            { value: "loopback", label: "This machine" },
            { value: "network", label: "Local network" },
          ]}
          onChange={(address) => void reach(address)}
        />
        {st?.listen_address === "network" ? (
          <p class="hint">
            {st.network_url ? <><span class="mono">{st.network_url}</span><br /></> : <>No route out; nothing can reach it.<br /></>}
            Plain HTTP: anything on the network can read it. Approval stays here.
          </p>
        ) : null}
      </div>
      {clash ? (
        <div class="actions secondary">
          {suggested ? <Button variant="quiet" busy={busy} onClick={() => void move(suggested)}>Use {suggested} instead</Button> : null}
          <Button variant="quiet" busy={busy} onClick={() => { void api.retryListener().then(async () => { status.value = await api.getStatus(); }).catch((err) => { errorMessage.value = describeError(err); }); }}>Retry {st?.listen_port}</Button>
        </div>
      ) : null}
    </section>
  );
}

/** The installed version, learned once from the updater for the Updates row. */
const currentVersion = signal<string | null>(null);

export function SettingsScreen() {
  const [settings, setSettings] = useState<Settings | null>(null);

  useEffect(() => {
    api.getSettings().then(setSettings).catch((err) => {
      errorMessage.value = describeError(err);
    });
    if (!currentVersion.value) api.getUpdateStatus().then((st) => { currentVersion.value = st.current; }).catch(() => undefined);
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

  return (
    <div class="screen pushed settings-hub">
      <Screen>
        <section class="section">
          <Label>Panel</Label>
          <div class="list">
            <label class="setting">
              <div>
                <div class="setting-title">Opens at</div>
                <div class="hint">{settings.panel_shortcut === "" ? "No shortcut." : `Shortcut ${settings.panel_shortcut ?? "Ctrl+Alt+P"}.`}</div>
              </div>
              <select aria-label="Panel corner" value={settings.panel_anchor} onChange={(e) => void save({ panel_anchor: e.currentTarget.value as PanelAnchor })}>
                <option value="auto">Auto</option>
                <option value="top-right">Top right</option>
                <option value="bottom-right">Bottom right</option>
                <option value="top-left">Top left</option>
                <option value="bottom-left">Bottom left</option>
              </select>
            </label>
            <div class="setting">
              <div>
                <div class="setting-title">Open on hold</div>
                <div class="hint">Off: notification and badge only.</div>
              </div>
              <Switch label="Open the panel on request" checked={settings.auto_open_on_pending} onChange={(v) => void save({ auto_open_on_pending: v })} />
            </div>
          </div>
        </section>

        <NetworkSection />

        <section class="section hub">
          <HubRow
            label="Observation"
            value={!(native.value?.observe_native ?? true) ? "off" : native.value?.last_event_at ? "observed" : "none yet"}
            onClick={() => push({ kind: "settings-observe" })}
          />
          <HubRow
            label="Updates"
            value={update.value ? `${update.value.version} ready` : `v${currentVersion.value ?? "…"}`}
            tone={update.value ? "accent" : undefined}
            onClick={() => push({ kind: "settings-updates" })}
          />
        </section>
      </Screen>
    </div>
  );
}

/** Native observation on its own screen: the switch, the export, the retention note. */
export function ObserveScreen() {
  return (
    <div class="screen pushed">
      <Screen>
        <NativeSection />
      </Screen>
    </div>
  );
}

/** Updates on their own screen: version, a short summary of what is new, and the install. */
export function UpdatesScreen() {
  return (
    <div class="screen pushed">
      <Screen>
        <UpdatesSection />
      </Screen>
    </div>
  );
}
