import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { errorMessage, status } from "../state";
import type { Settings } from "../types";
import { Label, Segmented, Switch, describeError } from "../ui";

export function PolicySection() {
  const [settings, setSettings] = useState<Settings | null>(null);

  useEffect(() => {
    api.getSettings().then(setSettings).catch((err) => {
      errorMessage.value = describeError(err);
    });
  }, []);

  if (!settings) return null;

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
    <section class="section">
      <Label>Policy</Label>
      <div class="list">
        <div class="setting">
          <div>
            <div class="setting-title">Do not disturb</div>
            <div class="hint">Held calls follow the rule below.</div>
          </div>
          <Switch label="Do not disturb" checked={settings.do_not_disturb} onChange={(v) => void save({ do_not_disturb: v })} />
        </div>
        <div class="setting">
          <div>
            <div class="setting-title">When nobody answers</div>
            <Segmented
              label="Timeout behaviour"
              value={settings.on_timeout}
              options={[
                { value: "deny", label: "Deny the call" },
                { value: "allow_read_only", label: "Allow if read-only" },
              ]}
              onChange={(on_timeout) => void save({ on_timeout })}
            />
          </div>
        </div>
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
            <div class="hint">Allowed calls ask above this.</div>
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
  );
}
