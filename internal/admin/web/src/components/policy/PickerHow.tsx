// PickerHow — "How securely?" (spec §6.1).
//
// Three presets:
//   - "token"     → no constraint (parent strips the field)
//   - "mfa"       → acr_required = "urn:prism:mfa"
//                   auth_freshness_max = 60 * N  (N is operator-provided
//                                                 "MFA within N minutes")
//   - "mfa_dpop"  → above + cnf_required = true
//
// HowSecureSpec carries the freshness in seconds via mfa_freshness_sec; the
// UI displays minutes. Default N = 10 (600 seconds) per spec §6.1.

import type { HowSecureSpec } from "../../api/policy";

export type HowMode = HowSecureSpec["mode"];

const DEFAULT_MFA_MINUTES = 10;

export interface PickerHowProps {
  value: HowSecureSpec | undefined;
  onChange: (next: HowSecureSpec | undefined) => void;
  disabled?: boolean;
  overriddenByAdvanced?: boolean;
}

export function PickerHow({
  value,
  onChange,
  disabled,
  overriddenByAdvanced,
}: PickerHowProps) {
  const mode: HowMode = value?.mode || "token";
  const freshnessSec = value?.mfa_freshness_sec ?? DEFAULT_MFA_MINUTES * 60;
  const minutes = Math.max(1, Math.round(freshnessSec / 60));

  const setMode = (next: HowMode) => {
    if (next === "token") {
      onChange(undefined);
      return;
    }
    if (next === "mfa") {
      onChange({ mode: "mfa", mfa_freshness_sec: minutes * 60 });
      return;
    }
    onChange({
      mode: "mfa_dpop",
      mfa_freshness_sec: minutes * 60,
      require_dpop: true,
    });
  };

  const updateMinutes = (m: number) => {
    if (!value) return;
    const next = { ...value, mfa_freshness_sec: Math.max(1, m) * 60 };
    onChange(next);
  };

  return (
    <div class="picker-how" data-mode={mode}>
      <label class="picker-label" htmlFor="picker-how-select">
        How securely?
      </label>
      <select
        id="picker-how-select"
        class="picker-select"
        value={mode}
        disabled={disabled || overriddenByAdvanced}
        onChange={(e) =>
          setMode((e.currentTarget as HTMLSelectElement).value as HowMode)
        }
      >
        <option value="token">Just a valid token</option>
        <option value="mfa">MFA required (within N minutes)</option>
        <option value="mfa_dpop">MFA + key-bound (DPoP)</option>
      </select>
      {(mode === "mfa" || mode === "mfa_dpop") && (
        <div class="picker-how-detail">
          <label class="picker-label" htmlFor="picker-how-minutes">
            N (minutes)
          </label>
          <input
            id="picker-how-minutes"
            class="picker-input picker-input-narrow"
            type="number"
            min={1}
            value={minutes}
            disabled={disabled || overriddenByAdvanced}
            onInput={(e) => {
              const v = parseInt(
                (e.currentTarget as HTMLInputElement).value,
                10,
              );
              if (Number.isFinite(v)) updateMinutes(v);
            }}
            aria-describedby="picker-how-hint"
          />
          <div id="picker-how-hint" class="picker-hint">
            acr_required = <code>urn:prism:mfa</code>; auth_freshness_max ={" "}
            <code>{minutes * 60}</code>s
            {mode === "mfa_dpop" ? "; cnf_required = true" : ""}.
          </div>
        </div>
      )}
      {overriddenByAdvanced && (
        <div class="picker-hint picker-hint-warn">
          Advanced fields override simple presets.
        </div>
      )}
    </div>
  );
}
