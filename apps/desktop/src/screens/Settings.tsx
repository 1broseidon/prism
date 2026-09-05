import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { errorMessage, status } from "../state";
import type { Settings } from "../types";
import { Label, Screen, Segmented, Switch, describeError } from "../ui";

/** Operator-level knobs. Every control saves as soon as it changes. */
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
                <div class="hint">Held calls resolve on their own, using the rule below. New agents still ask.</div>
              </div>
              <Switch label="Do not disturb" checked={settings.do_not_disturb} onChange={(v) => void save({ do_not_disturb: v })} />
            </div>
            <div class="setting">
              <div>
                <div class="setting-title">Open the panel when something needs you</div>
                <div class="hint">Off means a notification and the tray badge only.</div>
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
              ? "The agent gets a refusal and can retry once you are back."
              : "Tools the server marks read-only go through. Anything that writes is refused."}
          </p>
        </section>

        <section class="section">
          <Label>Limits</Label>
          <div class="list">
            <label class="setting">
              <div>
                <div class="setting-title">Hold a call for</div>
                <div class="hint">Seconds before the rule above kicks in.</div>
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
                <div class="hint">Above this many calls a minute, an agent's allowed calls start asking. Empty turns it off.</div>
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
      </Screen>
    </div>
  );
}
