// AdvancedFields — full DSL editor surface inside the Add Capability modal
// (spec §6.2). Hidden by default. Provides:
//
//   - Argument predicates: per-key (field name + predicate-type + value)
//     covering all 7 predicates (equals, prefix, oneOf, pattern, size_max,
//     range, tool_in_set).
//   - Workspace constraints: explicit {id, type, write_mode} predicate
//     editors. Overrides the simple-mode "Where?" preset.
//   - Custom acr_required: text input with datalist autocomplete
//     (urn:prism:mfa, urn:prism:hwk).
//   - RoleRequired: free-form text v1; switches to a dropdown when
//     `availableRoles` is provided.
//
// All state lives in the parent. This component is presentational — it
// receives an AdvancedSpec value and emits a new one on change.

import { useState } from "preact/hooks";
import type { GrantPredicate } from "../../api/grants";
import type { AdvancedSpec, WorkspaceConstraint } from "../../api/policy";

export interface AdvancedFieldsProps {
  value: AdvancedSpec | undefined;
  onChange: (next: AdvancedSpec | undefined) => void;
  disabled?: boolean;
  /** When provided, RoleRequired surfaces as a dropdown. */
  availableRoles?: readonly string[];
}

type PredicateKind =
  | "equals"
  | "prefix"
  | "oneOf"
  | "pattern"
  | "size_max"
  | "range"
  | "tool_in_set";

const PREDICATE_KINDS: PredicateKind[] = [
  "equals",
  "prefix",
  "oneOf",
  "pattern",
  "size_max",
  "range",
  "tool_in_set",
];

const ACR_SUGGESTIONS = ["urn:prism:mfa", "urn:prism:hwk"];

export function AdvancedFields({
  value,
  onChange,
  disabled,
  availableRoles,
}: AdvancedFieldsProps) {
  const adv = value || {};

  const setArgs = (args: Record<string, GrantPredicate> | undefined) => {
    const next: AdvancedSpec = { ...adv, args: args && Object.keys(args).length > 0 ? args : undefined };
    emit(next);
  };
  const setWorkspace = (ws: WorkspaceConstraint | undefined) => {
    const next: AdvancedSpec = { ...adv, workspace: ws && hasAnyWsField(ws) ? ws : undefined };
    emit(next);
  };
  const setAcr = (acr: string | undefined) => {
    const next: AdvancedSpec = { ...adv, acr_required: acr || undefined };
    emit(next);
  };
  const setRole = (role: string | undefined) => {
    const next: AdvancedSpec = { ...adv, role_required: role || undefined };
    emit(next);
  };

  function emit(next: AdvancedSpec) {
    const empty =
      !next.args &&
      !next.workspace &&
      !next.acr_required &&
      !next.role_required;
    onChange(empty ? undefined : next);
  }

  return (
    <div class="advanced-fields" role="group" aria-label="advanced fields">
      <ArgsEditor value={adv.args} onChange={setArgs} disabled={disabled} />
      <WorkspaceEditor
        value={adv.workspace}
        onChange={setWorkspace}
        disabled={disabled}
      />
      <div class="advanced-field-row">
        <label class="picker-label" htmlFor="adv-acr">
          Custom acr_required
        </label>
        <input
          id="adv-acr"
          class="picker-input"
          type="text"
          list="adv-acr-suggestions"
          value={adv.acr_required || ""}
          disabled={disabled}
          placeholder="urn:prism:mfa"
          onInput={(e) =>
            setAcr((e.currentTarget as HTMLInputElement).value.trim() || undefined)
          }
        />
        <datalist id="adv-acr-suggestions">
          {ACR_SUGGESTIONS.map((s) => (
            <option key={s} value={s} />
          ))}
        </datalist>
      </div>
      <div class="advanced-field-row">
        <label class="picker-label" htmlFor="adv-role">
          RoleRequired
        </label>
        {availableRoles && availableRoles.length > 0 ? (
          <select
            id="adv-role"
            class="picker-select"
            value={adv.role_required || ""}
            disabled={disabled}
            onChange={(e) => {
              const v = (e.currentTarget as HTMLSelectElement).value;
              setRole(v || undefined);
            }}
          >
            <option value="">— none —</option>
            {availableRoles.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        ) : (
          <input
            id="adv-role"
            class="picker-input"
            type="text"
            value={adv.role_required || ""}
            disabled={disabled}
            placeholder="role name"
            onInput={(e) =>
              setRole((e.currentTarget as HTMLInputElement).value.trim() || undefined)
            }
          />
        )}
      </div>
    </div>
  );
}

function hasAnyWsField(ws: WorkspaceConstraint): boolean {
  return Boolean(ws.id || ws.type || ws.write_mode);
}

// ── Argument predicates ─────────────────────────────────────────────────────

function ArgsEditor({
  value,
  onChange,
  disabled,
}: {
  value: Record<string, GrantPredicate> | undefined;
  onChange: (next: Record<string, GrantPredicate> | undefined) => void;
  disabled?: boolean;
}) {
  const entries = Object.entries(value || {});

  const setEntry = (idx: number, key: string, pred: GrantPredicate) => {
    const next = { ...(value || {}) };
    const prevKey = entries[idx]?.[0];
    if (prevKey !== undefined && prevKey !== key) delete next[prevKey];
    next[key] = pred;
    onChange(next);
  };

  const removeEntry = (idx: number) => {
    const k = entries[idx]?.[0];
    if (k === undefined) return;
    const next = { ...(value || {}) };
    delete next[k];
    onChange(Object.keys(next).length ? next : undefined);
  };

  const addEntry = () => {
    let i = 1;
    let key = "arg";
    const existing = value || {};
    while (key in existing) {
      i += 1;
      key = `arg${i}`;
    }
    onChange({ ...existing, [key]: { equals: "" } });
  };

  return (
    <fieldset class="advanced-fieldset">
      <legend class="picker-legend">Argument predicates</legend>
      {entries.length === 0 && (
        <div class="picker-hint">No argument predicates set.</div>
      )}
      {entries.map(([k, p], idx) => (
        <ArgRow
          key={`${idx}-${k}`}
          fieldKey={k}
          predicate={p}
          disabled={disabled}
          onChange={(nk, np) => setEntry(idx, nk, np)}
          onRemove={() => removeEntry(idx)}
        />
      ))}
      <button
        type="button"
        class="advanced-add-btn"
        disabled={disabled}
        onClick={addEntry}
      >
        + add predicate
      </button>
    </fieldset>
  );
}

function ArgRow({
  fieldKey,
  predicate,
  disabled,
  onChange,
  onRemove,
}: {
  fieldKey: string;
  predicate: GrantPredicate;
  disabled?: boolean;
  onChange: (key: string, pred: GrantPredicate) => void;
  onRemove: () => void;
}) {
  const kind = detectPredicateKind(predicate);

  const setKind = (k: PredicateKind) => {
    onChange(fieldKey, blankPredicateFor(k));
  };

  const setKey = (k: string) => {
    onChange(k, predicate);
  };

  return (
    <div class="advanced-arg-row">
      <input
        class="picker-input picker-input-narrow"
        type="text"
        value={fieldKey}
        placeholder="field"
        disabled={disabled}
        onInput={(e) => setKey((e.currentTarget as HTMLInputElement).value)}
        aria-label="argument name"
      />
      <select
        class="picker-select picker-select-narrow"
        value={kind}
        disabled={disabled}
        onChange={(e) =>
          setKind(
            (e.currentTarget as HTMLSelectElement).value as PredicateKind,
          )
        }
        aria-label="predicate kind"
      >
        {PREDICATE_KINDS.map((k) => (
          <option key={k} value={k}>
            {k}
          </option>
        ))}
      </select>
      <PredicateValueEditor
        kind={kind}
        value={predicate}
        disabled={disabled}
        onChange={(np) => onChange(fieldKey, np)}
      />
      <button
        type="button"
        class="advanced-remove-btn"
        disabled={disabled}
        onClick={onRemove}
        aria-label="remove predicate"
      >
        ×
      </button>
    </div>
  );
}

function detectPredicateKind(p: GrantPredicate): PredicateKind {
  if (p.equals !== undefined) return "equals";
  if (p.prefix !== undefined) return "prefix";
  if (p.oneOf !== undefined) return "oneOf";
  if (p.pattern !== undefined) return "pattern";
  if (p.size_max !== undefined) return "size_max";
  if (p.range !== undefined) return "range";
  if (p.tool_in_set !== undefined) return "tool_in_set";
  return "equals";
}

function blankPredicateFor(k: PredicateKind): GrantPredicate {
  switch (k) {
    case "equals":
      return { equals: "" };
    case "prefix":
      return { prefix: "" };
    case "oneOf":
      return { oneOf: [] };
    case "pattern":
      return { pattern: "" };
    case "size_max":
      return { size_max: 0 };
    case "range":
      return { range: {} };
    case "tool_in_set":
      return { tool_in_set: [] };
  }
}

function PredicateValueEditor({
  kind,
  value,
  disabled,
  onChange,
}: {
  kind: PredicateKind;
  value: GrantPredicate;
  disabled?: boolean;
  onChange: (next: GrantPredicate) => void;
}) {
  switch (kind) {
    case "equals":
      return (
        <input
          class="picker-input"
          type="text"
          value={asScalarString(value.equals)}
          disabled={disabled}
          placeholder="value"
          onInput={(e) =>
            onChange({ equals: (e.currentTarget as HTMLInputElement).value })
          }
        />
      );
    case "prefix":
      return (
        <input
          class="picker-input"
          type="text"
          value={value.prefix || ""}
          disabled={disabled}
          placeholder="prefix"
          onInput={(e) =>
            onChange({ prefix: (e.currentTarget as HTMLInputElement).value })
          }
        />
      );
    case "pattern":
      return (
        <input
          class="picker-input"
          type="text"
          value={value.pattern || ""}
          disabled={disabled}
          placeholder="regex"
          onInput={(e) =>
            onChange({ pattern: (e.currentTarget as HTMLInputElement).value })
          }
        />
      );
    case "size_max":
      return (
        <input
          class="picker-input picker-input-narrow"
          type="number"
          min={0}
          value={value.size_max ?? 0}
          disabled={disabled}
          onInput={(e) => {
            const n = parseInt(
              (e.currentTarget as HTMLInputElement).value,
              10,
            );
            onChange({ size_max: Number.isFinite(n) ? n : 0 });
          }}
        />
      );
    case "range":
      return (
        <RangeEditor
          value={value.range || {}}
          disabled={disabled}
          onChange={(r) => onChange({ range: r })}
        />
      );
    case "oneOf":
      return (
        <ListEditor
          value={(value.oneOf || []).map((v) => String(v))}
          placeholder="comma-separated values"
          disabled={disabled}
          onChange={(arr) => onChange({ oneOf: arr })}
        />
      );
    case "tool_in_set":
      return (
        <ListEditor
          value={value.tool_in_set || []}
          placeholder="backend:tool,backend:tool"
          disabled={disabled}
          onChange={(arr) => onChange({ tool_in_set: arr })}
        />
      );
  }
}

function asScalarString(v: unknown): string {
  if (v === undefined || v === null) return "";
  if (typeof v === "string") return v;
  return String(v);
}

function RangeEditor({
  value,
  disabled,
  onChange,
}: {
  value: { min?: number; max?: number };
  disabled?: boolean;
  onChange: (r: { min?: number; max?: number }) => void;
}) {
  return (
    <span class="advanced-range">
      <input
        class="picker-input picker-input-narrow"
        type="number"
        placeholder="min"
        value={value.min ?? ""}
        disabled={disabled}
        onInput={(e) => {
          const raw = (e.currentTarget as HTMLInputElement).value;
          const n = raw === "" ? undefined : Number(raw);
          onChange({
            min: n === undefined || Number.isNaN(n) ? undefined : n,
            max: value.max,
          });
        }}
      />
      <span class="advanced-range-sep">–</span>
      <input
        class="picker-input picker-input-narrow"
        type="number"
        placeholder="max"
        value={value.max ?? ""}
        disabled={disabled}
        onInput={(e) => {
          const raw = (e.currentTarget as HTMLInputElement).value;
          const n = raw === "" ? undefined : Number(raw);
          onChange({
            min: value.min,
            max: n === undefined || Number.isNaN(n) ? undefined : n,
          });
        }}
      />
    </span>
  );
}

function ListEditor({
  value,
  placeholder,
  disabled,
  onChange,
}: {
  value: readonly string[];
  placeholder?: string;
  disabled?: boolean;
  onChange: (arr: string[]) => void;
}) {
  // Comma-separated edit buffer keeps the input simple and round-trippable;
  // empty tokens are filtered on emit so trailing commas while typing don't
  // create blank entries on the server.
  const [buf, setBuf] = useState<string>(value.join(", "));
  return (
    <input
      class="picker-input"
      type="text"
      value={buf}
      placeholder={placeholder}
      disabled={disabled}
      onInput={(e) => {
        const next = (e.currentTarget as HTMLInputElement).value;
        setBuf(next);
        onChange(
          next
            .split(",")
            .map((s) => s.trim())
            .filter((s) => s.length > 0),
        );
      }}
    />
  );
}

// ── Workspace constraints ───────────────────────────────────────────────────

function WorkspaceEditor({
  value,
  onChange,
  disabled,
}: {
  value: WorkspaceConstraint | undefined;
  onChange: (next: WorkspaceConstraint | undefined) => void;
  disabled?: boolean;
}) {
  const ws = value || {};
  const setField = (field: "id" | "type" | "write_mode", pred: GrantPredicate | undefined) => {
    const next = { ...ws };
    if (pred === undefined) delete next[field];
    else next[field] = pred;
    onChange(hasAnyWsField(next) ? next : undefined);
  };

  return (
    <fieldset class="advanced-fieldset">
      <legend class="picker-legend">Workspace constraints</legend>
      <WsPredicateRow
        label="id"
        predicate={ws.id}
        disabled={disabled}
        onChange={(p) => setField("id", p)}
      />
      <WsPredicateRow
        label="type"
        predicate={ws.type}
        disabled={disabled}
        onChange={(p) => setField("type", p)}
      />
      <WsPredicateRow
        label="write_mode"
        predicate={ws.write_mode}
        disabled={disabled}
        onChange={(p) => setField("write_mode", p)}
      />
    </fieldset>
  );
}

function WsPredicateRow({
  label,
  predicate,
  disabled,
  onChange,
}: {
  label: string;
  predicate: GrantPredicate | undefined;
  disabled?: boolean;
  onChange: (next: GrantPredicate | undefined) => void;
}) {
  const enabled = predicate !== undefined;
  const kind = predicate ? detectPredicateKind(predicate) : "equals";
  return (
    <div class="advanced-ws-row">
      <label class="advanced-ws-toggle">
        <input
          type="checkbox"
          checked={enabled}
          disabled={disabled}
          onChange={(e) => {
            const on = (e.currentTarget as HTMLInputElement).checked;
            onChange(on ? blankPredicateFor("equals") : undefined);
          }}
        />
        <span>{label}</span>
      </label>
      {enabled && predicate && (
        <>
          <select
            class="picker-select picker-select-narrow"
            value={kind}
            disabled={disabled}
            onChange={(e) =>
              onChange(
                blankPredicateFor(
                  (e.currentTarget as HTMLSelectElement).value as PredicateKind,
                ),
              )
            }
          >
            {PREDICATE_KINDS.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
          </select>
          <PredicateValueEditor
            kind={kind}
            value={predicate}
            disabled={disabled}
            onChange={onChange}
          />
        </>
      )}
    </div>
  );
}
