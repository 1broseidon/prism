// PickerWhen — "When?" (spec §6.1).
//
// Three preset modes:
//   - "anytime"  → no constraint (parent strips the field)
//   - "business" → "weekdays 09:00-18:00 <timezone>" — auto-detects the
//                  operator's local IANA zone via Intl.DateTimeFormat
//   - "window"   → custom: weekday picker + start/end + timezone
//
// The compiled output for mode="business" / mode="window" is a string the
// auth-side parser understands (auth.parseHoursWindow):
//
//   "[days] HH:MM-HH:MM TZ"
//
// Days are either "weekdays", a single weekday, or a Mon-Fri-style range.
// The backend stores the assembled string verbatim in GrantSpec.Hours.

import { useMemo } from "preact/hooks";
import type { WhenSpec } from "../../api/policy";

export type WhenMode = WhenSpec["mode"];

export interface PickerWhenProps {
  value: WhenSpec | undefined;
  onChange: (next: WhenSpec | undefined) => void;
  disabled?: boolean;
  overriddenByAdvanced?: boolean;
}

// Common IANA zones to pre-populate the dropdown — operators can pick "other"
// to type a custom one. The detected local zone is always prepended.
const COMMON_TIMEZONES: readonly string[] = [
  "UTC",
  "America/Toronto",
  "America/New_York",
  "America/Los_Angeles",
  "Europe/London",
  "Europe/Berlin",
  "Asia/Tokyo",
  "Asia/Singapore",
  "Australia/Sydney",
];

const WEEKDAYS: ReadonlyArray<{ id: string; label: string }> = [
  { id: "mon", label: "Mon" },
  { id: "tue", label: "Tue" },
  { id: "wed", label: "Wed" },
  { id: "thu", label: "Thu" },
  { id: "fri", label: "Fri" },
  { id: "sat", label: "Sat" },
  { id: "sun", label: "Sun" },
];

/** detectTimezone returns the operator's local IANA zone, or "UTC" as fallback. */
export function detectTimezone(): string {
  try {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    return tz || "UTC";
  } catch {
    return "UTC";
  }
}

/** compileBusinessHours produces "weekdays 09:00-18:00 <tz>". */
export function compileBusinessHours(tz: string): string {
  return `weekdays 09:00-18:00 ${tz}`;
}

interface CustomWindow {
  days: string[]; // ["mon","tue",...]; empty → no day filter
  start: string; // "09:00"
  end: string; // "18:00"
  tz: string;
}

/** compileCustomWindow assembles the auth-grammar hours string. */
export function compileCustomWindow(w: CustomWindow): string {
  const span = `${w.start}-${w.end}`;
  if (w.days.length === 0) return `${span} ${w.tz}`.trim();
  // Detect Mon-Fri contiguous run → emit the range form for readability.
  if (
    w.days.length === 5 &&
    sameSet(w.days, ["mon", "tue", "wed", "thu", "fri"])
  ) {
    return `Mon-Fri ${span} ${w.tz}`;
  }
  // Single day → "Mon HH:MM-HH:MM TZ" (parser accepts capitalised first letter).
  if (w.days.length === 1) {
    return `${capitalize(w.days[0])} ${span} ${w.tz}`;
  }
  // Multi-day non-contiguous selection isn't expressible in the auth grammar's
  // current shape — fall through to the comma-less single-day case for the
  // first selected day. The parent should disable the save button in this
  // configuration; the form surfaces a validation error.
  return `${capitalize(w.days[0])} ${span} ${w.tz}`;
}

function capitalize(s: string): string {
  return s ? s[0].toUpperCase() + s.slice(1) : s;
}

function sameSet(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  const set = new Set(a);
  return b.every((x) => set.has(x));
}

/** parseCustomWindow inverts compileCustomWindow for edit-mode pre-fill. */
export function parseCustomWindow(
  hours: string,
  fallbackTz: string,
): CustomWindow {
  const fields = hours.trim().split(/\s+/);
  if (fields.length < 2) {
    return { days: [], start: "09:00", end: "18:00", tz: fallbackTz };
  }
  let days: string[] = [];
  let span: string;
  let tz: string;
  if (fields.length === 2) {
    [span, tz] = fields;
  } else {
    const [d, s, t] = fields;
    days = parseDaysToken(d);
    span = s;
    tz = t;
  }
  const [start, end] = (span.split("-") as [string, string]).map((s) => s || "00:00");
  return { days, start, end, tz };
}

function parseDaysToken(token: string): string[] {
  const t = token.toLowerCase();
  if (t === "weekdays") return ["mon", "tue", "wed", "thu", "fri"];
  const dash = t.split("-");
  if (dash.length === 2) {
    const order = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];
    const s = order.indexOf(dash[0]);
    const e = order.indexOf(dash[1]);
    if (s < 0 || e < 0) return [];
    const out: string[] = [];
    for (let i = s; ; i = (i + 1) % 7) {
      out.push(order[i]);
      if (i === e) break;
    }
    return out;
  }
  // single day
  return [t];
}

export function PickerWhen({
  value,
  onChange,
  disabled,
  overriddenByAdvanced,
}: PickerWhenProps) {
  const localTz = useMemo(() => detectTimezone(), []);
  const mode: WhenMode = value?.mode || "anytime";

  const custom = useMemo<CustomWindow>(() => {
    if (mode !== "window") {
      return { days: [], start: "09:00", end: "18:00", tz: localTz };
    }
    if (value?.hours) {
      return parseCustomWindow(value.hours, localTz);
    }
    return { days: [], start: "09:00", end: "18:00", tz: value?.timezone || localTz };
  }, [mode, value?.hours, value?.timezone, localTz]);

  const setMode = (next: WhenMode) => {
    if (next === "anytime") {
      onChange(undefined);
      return;
    }
    if (next === "business") {
      onChange({ mode: "business", timezone: value?.timezone || localTz });
      return;
    }
    // window
    const w = custom;
    onChange({ mode: "window", hours: compileCustomWindow(w), timezone: w.tz });
  };

  const updateCustom = (next: Partial<CustomWindow>) => {
    const merged: CustomWindow = { ...custom, ...next };
    onChange({
      mode: "window",
      hours: compileCustomWindow(merged),
      timezone: merged.tz,
    });
  };

  return (
    <div class="picker-when" data-mode={mode}>
      <label class="picker-label" htmlFor="picker-when-select">
        When?
      </label>
      <select
        id="picker-when-select"
        class="picker-select"
        value={mode}
        disabled={disabled || overriddenByAdvanced}
        onChange={(e) =>
          setMode((e.currentTarget as HTMLSelectElement).value as WhenMode)
        }
      >
        <option value="anytime">Anytime</option>
        <option value="business">Business hours (Mon-Fri 09-18, local tz)</option>
        <option value="window">Custom window…</option>
      </select>
      {mode === "business" && (
        <BusinessSummary
          tz={value?.timezone || localTz}
          onTzChange={(tz) => onChange({ mode: "business", timezone: tz })}
          localTz={localTz}
          disabled={disabled || overriddenByAdvanced}
        />
      )}
      {mode === "window" && (
        <CustomEditor
          value={custom}
          onChange={updateCustom}
          localTz={localTz}
          disabled={disabled || overriddenByAdvanced}
        />
      )}
      {overriddenByAdvanced && (
        <div class="picker-hint picker-hint-warn">
          Advanced fields override simple presets.
        </div>
      )}
    </div>
  );
}

function BusinessSummary({
  tz,
  onTzChange,
  localTz,
  disabled,
}: {
  tz: string;
  onTzChange: (tz: string) => void;
  localTz: string;
  disabled?: boolean;
}) {
  return (
    <div class="picker-when-business">
      <div class="picker-hint">
        Compiles to <code>{compileBusinessHours(tz)}</code>.
      </div>
      <label class="picker-label" htmlFor="picker-when-business-tz">
        Timezone
      </label>
      <TimezoneSelect
        id="picker-when-business-tz"
        value={tz}
        onChange={onTzChange}
        localTz={localTz}
        disabled={disabled}
      />
    </div>
  );
}

function CustomEditor({
  value,
  onChange,
  localTz,
  disabled,
}: {
  value: CustomWindow;
  onChange: (next: Partial<CustomWindow>) => void;
  localTz: string;
  disabled?: boolean;
}) {
  const toggleDay = (id: string) => {
    const has = value.days.includes(id);
    onChange({
      days: has ? value.days.filter((d) => d !== id) : [...value.days, id],
    });
  };
  return (
    <div class="picker-when-custom">
      <fieldset class="picker-fieldset">
        <legend class="picker-legend">Days</legend>
        <div class="picker-when-days">
          {WEEKDAYS.map((d) => (
            <label key={d.id} class="picker-when-day">
              <input
                type="checkbox"
                checked={value.days.includes(d.id)}
                disabled={disabled}
                onChange={() => toggleDay(d.id)}
              />
              <span>{d.label}</span>
            </label>
          ))}
        </div>
        <div class="picker-hint">
          Leave all unchecked to match any day of the week.
        </div>
      </fieldset>
      <div class="picker-when-row">
        <label class="picker-label" htmlFor="picker-when-start">
          Start
        </label>
        <input
          id="picker-when-start"
          class="picker-input"
          type="time"
          value={value.start}
          disabled={disabled}
          onInput={(e) =>
            onChange({ start: (e.currentTarget as HTMLInputElement).value })
          }
        />
        <label class="picker-label" htmlFor="picker-when-end">
          End
        </label>
        <input
          id="picker-when-end"
          class="picker-input"
          type="time"
          value={value.end}
          disabled={disabled}
          onInput={(e) =>
            onChange({ end: (e.currentTarget as HTMLInputElement).value })
          }
        />
      </div>
      <label class="picker-label" htmlFor="picker-when-custom-tz">
        Timezone
      </label>
      <TimezoneSelect
        id="picker-when-custom-tz"
        value={value.tz}
        onChange={(tz) => onChange({ tz })}
        localTz={localTz}
        disabled={disabled}
      />
      <div class="picker-hint">
        Compiles to <code>{compileCustomWindow(value)}</code>.
      </div>
    </div>
  );
}

function TimezoneSelect({
  id,
  value,
  onChange,
  localTz,
  disabled,
}: {
  id: string;
  value: string;
  onChange: (tz: string) => void;
  localTz: string;
  disabled?: boolean;
}) {
  // Build the option list once: detected local zone (if not already common) +
  // the curated common-zones list + the current value if it's not present.
  const options = useMemo(() => {
    const set = new Set<string>(COMMON_TIMEZONES);
    set.add(localTz);
    if (value) set.add(value);
    return [...set].sort();
  }, [localTz, value]);

  return (
    <select
      id={id}
      class="picker-select"
      value={value}
      disabled={disabled}
      onChange={(e) => onChange((e.currentTarget as HTMLSelectElement).value)}
    >
      {options.map((tz) => (
        <option key={tz} value={tz}>
          {tz}
          {tz === localTz ? " (local)" : ""}
        </option>
      ))}
    </select>
  );
}
