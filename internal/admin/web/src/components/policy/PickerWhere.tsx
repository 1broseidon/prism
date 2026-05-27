// PickerWhere — "Where?" (spec §6.1).
//
// Preset choices compile down to the WhereSpec the backend understands:
//   - "anywhere"     → no WhereSpec emitted (parent strips the field)
//   - "path_prefix"  → args.path.prefix = <input>
//   - "agent_home"   → args.path.prefix = "/workspace/${agent.prism_id}/"
//   - "shared"       → workspace.type.equals = "virtual"
//   - "ephemeral"    → workspace.type.equals = "ephemeral"
//
// "agent_home" is the template-substitution preset — the literal value the
// backend receives is "/workspace/${agent.prism_id}/" (dollar-brace syntax
// from the grants spec). Round-tripping that exact value back into the
// modal MUST recognize it as the preset and not as a free-form prefix; the
// reverse mapper in AddCapabilityModal handles that.

import type { WhereSpec } from "../../api/policy";

/** AGENT_HOME_PREFIX is the canonical template prefix for "their own workspace". */
export const AGENT_HOME_PREFIX = "/workspace/${agent.prism_id}/";

export type WhereMode = WhereSpec["mode"];

export interface PickerWhereProps {
  value: WhereSpec | undefined;
  onChange: (next: WhereSpec | undefined) => void;
  disabled?: boolean;
  /** When true, the picker renders a small "advanced overrides this" hint. */
  overriddenByAdvanced?: boolean;
}

const PRESETS: Array<{ mode: WhereMode; label: string }> = [
  { mode: "anywhere", label: "Anywhere they want" },
  { mode: "path_prefix", label: "Under a path I specify…" },
  { mode: "agent_home", label: "Only their own workspace" },
  { mode: "shared", label: "Only on shared (virtual) storage" },
  { mode: "ephemeral", label: "Only ephemeral storage" },
];

export function PickerWhere({
  value,
  onChange,
  disabled,
  overriddenByAdvanced,
}: PickerWhereProps) {
  // "anywhere" + undefined spec are the same observable state — we render
  // the dropdown defaulted to "anywhere" so the form is never blank.
  const mode: WhereMode = value?.mode || "anywhere";
  const prefix = value?.path_prefix || "";

  const setMode = (next: WhereMode) => {
    if (next === "anywhere") {
      onChange(undefined);
      return;
    }
    if (next === "path_prefix") {
      onChange({ mode: "path_prefix", path_prefix: prefix });
      return;
    }
    onChange({ mode: next });
  };

  return (
    <div class="picker-where" data-mode={mode}>
      <label class="picker-label" htmlFor="picker-where-select">
        Where?
      </label>
      <select
        id="picker-where-select"
        class="picker-select"
        value={mode}
        disabled={disabled || overriddenByAdvanced}
        onChange={(e) => setMode((e.currentTarget as HTMLSelectElement).value as WhereMode)}
      >
        {PRESETS.map((p) => (
          <option key={p.mode} value={p.mode}>
            {p.label}
          </option>
        ))}
      </select>
      {mode === "path_prefix" && (
        <div class="picker-where-prefix">
          <label class="picker-label" htmlFor="picker-where-prefix-input">
            Path prefix
          </label>
          <input
            id="picker-where-prefix-input"
            class="picker-input"
            type="text"
            placeholder="/var/data/"
            value={prefix}
            disabled={disabled || overriddenByAdvanced}
            onInput={(e) =>
              onChange({
                mode: "path_prefix",
                path_prefix: (e.currentTarget as HTMLInputElement).value,
              })
            }
            aria-describedby="picker-where-prefix-hint"
          />
          <div id="picker-where-prefix-hint" class="picker-hint">
            Compiled to args.path = {`{prefix: "${prefix || "…"}"}`}.
          </div>
        </div>
      )}
      {mode === "agent_home" && (
        <div class="picker-hint">
          Compiled to args.path with prefix <code>{AGENT_HOME_PREFIX}</code> —
          each agent only sees their own home.
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
