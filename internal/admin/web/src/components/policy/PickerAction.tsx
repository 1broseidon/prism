// PickerAction — "What can they do?" (spec §6.1).
//
// Three operating modes flagged on the ActionSpec we emit:
//   1. mode="verb"             — a curated verb shortcut, default surface.
//                                After a verb is picked we resolve the verb
//                                against the operator's enabled backends and
//                                render the resolved tool list as small chips
//                                ("This covers: …"). 0 matching tools → a
//                                yellow warning chip per spec §15.
//   2. mode="tool"             — pick a single "<backend>.<tool>" pair via
//                                a typeahead. Tools come from the parent's
//                                `tools` prop (the parent owns the GET /backends
//                                fetch so this component stays presentational).
//   3. mode="backend_wildcard" — backend.* — escape hatch for "everything on
//                                this backend". Dropdown over enabled backends.
//
// The picker is purely presentational: all state lives in modal-level state.
// `value` is the current ActionSpec; `onChange` emits a new one. The parent
// owns verbs/tools/backends loading and passes them as props.

import { useEffect, useMemo, useState } from "preact/hooks";
import type { ActionSpec, ResolvedTool, Verb } from "../../api/policy";
import { resolveVerb } from "../../api/policy";

export interface BackendOption {
  id: string;
  /** Optional human label for the dropdown — falls back to id. */
  label?: string;
  /** When supplied, only tools listed here are surfaced in the typeahead. */
  tools?: string[];
}

export interface PickerActionProps {
  value: ActionSpec;
  onChange: (next: ActionSpec) => void;
  /** Verb library — usually fetched once by the parent via listVerbs(). */
  verbs: readonly Verb[];
  /** Currently enabled backends — used to resolve verbs and drive the wildcard / specific-tool modes. */
  backends: readonly BackendOption[];
  /** Disable the entire picker (used while saving). */
  disabled?: boolean;
}

export function PickerAction({
  value,
  onChange,
  verbs,
  backends,
  disabled,
}: PickerActionProps) {
  const mode = value.mode || "verb";

  return (
    <div class="picker-action" data-mode={mode}>
      <ModeTabs mode={mode} disabled={disabled} onChange={(m) => onChange(blankActionFor(m))} />
      {mode === "verb" && (
        <VerbMode
          value={value}
          onChange={onChange}
          verbs={verbs}
          backends={backends}
          disabled={disabled}
        />
      )}
      {mode === "tool" && (
        <ToolMode
          value={value}
          onChange={onChange}
          backends={backends}
          disabled={disabled}
        />
      )}
      {mode === "backend_wildcard" && (
        <BackendWildcardMode
          value={value}
          onChange={onChange}
          backends={backends}
          disabled={disabled}
        />
      )}
    </div>
  );
}

function blankActionFor(mode: ActionSpec["mode"]): ActionSpec {
  return { mode };
}

function ModeTabs({
  mode,
  disabled,
  onChange,
}: {
  mode: ActionSpec["mode"];
  disabled?: boolean;
  onChange: (m: ActionSpec["mode"]) => void;
}) {
  // Three radio-style tabs. We keep them as buttons (not <select>) because
  // each switches into a distinct sub-form, and tabs make the affordance
  // visible at a glance.
  const tabs: Array<{ id: ActionSpec["mode"]; label: string }> = [
    { id: "verb", label: "Verb" },
    { id: "tool", label: "Specific tool…" },
    { id: "backend_wildcard", label: "Backend wildcard…" },
  ];
  return (
    <div class="picker-action-modes" role="tablist" aria-label="action mode">
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          role="tab"
          aria-selected={mode === t.id}
          class={`picker-action-mode${mode === t.id ? " picker-action-mode-active" : ""}`}
          onClick={() => onChange(t.id)}
          disabled={disabled}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

function VerbMode({
  value,
  onChange,
  verbs,
  backends,
  disabled,
}: {
  value: ActionSpec;
  onChange: (a: ActionSpec) => void;
  verbs: readonly Verb[];
  backends: readonly BackendOption[];
  disabled?: boolean;
}) {
  const slug = value.verb_slug || "";
  const enabledIDs = useMemo(() => backends.map((b) => b.id), [backends]);

  return (
    <div class="picker-action-verb">
      <label class="picker-label" htmlFor="picker-action-verb-select">
        Pick a verb
      </label>
      <select
        id="picker-action-verb-select"
        class="picker-select"
        value={slug}
        disabled={disabled}
        onChange={(e) =>
          onChange({
            mode: "verb",
            verb_slug: (e.currentTarget as HTMLSelectElement).value,
          })
        }
      >
        <option value="">— pick a verb —</option>
        {verbs.map((v) => (
          <option key={v.slug} value={v.slug}>
            {v.label}
          </option>
        ))}
      </select>
      {slug && (
        <VerbResolutionPreview slug={slug} enabledBackends={enabledIDs} />
      )}
    </div>
  );
}

/**
 * VerbResolutionPreview is split out so it can re-fetch whenever the slug or
 * the enabled-backends set changes without re-rendering the dropdown.
 */
export function VerbResolutionPreview({
  slug,
  enabledBackends,
}: {
  slug: string;
  enabledBackends: readonly string[];
}) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ready"; tools: ResolvedTool[] }
    | { kind: "error"; message: string }
  >({ kind: "loading" });

  useEffect(() => {
    if (!slug) return;
    let cancelled = false;
    setState({ kind: "loading" });
    resolveVerb(slug, [...enabledBackends])
      .then((tools) => {
        if (!cancelled) setState({ kind: "ready", tools });
      })
      .catch((err) => {
        if (!cancelled)
          setState({
            kind: "error",
            message: err instanceof Error ? err.message : String(err),
          });
      });
    return () => {
      cancelled = true;
    };
  }, [slug, enabledBackends.join(",")]);

  if (state.kind === "loading") {
    return (
      <div class="picker-action-resolved" aria-live="polite">
        resolving…
      </div>
    );
  }
  if (state.kind === "error") {
    return (
      <div class="picker-action-resolved picker-action-resolved-error" role="alert">
        could not resolve verb: {state.message}
      </div>
    );
  }
  if (state.tools.length === 0) {
    // Spec §15 risk note: a verb resolves to zero tools when no enabled
    // backend supplies any of its patterns. Save still works (it produces
    // an effective deny), but the UI must warn so operators don't silently
    // ship dead capabilities.
    return (
      <div
        class="picker-action-resolved picker-action-resolved-warn"
        role="alert"
      >
        <span class="policy-chip policy-chip-warn">⚠</span>
        This verb resolves to <strong>0 tools</strong> with the currently
        enabled backends — saving will produce a capability that grants
        nothing.
      </div>
    );
  }
  return (
    <div class="picker-action-resolved" aria-live="polite">
      <span class="picker-action-resolved-label">This covers:</span>
      <span class="picker-action-resolved-list">
        {state.tools.map((t) => (
          <span
            key={`${t.backend}.${t.tool}`}
            class="policy-chip policy-chip-resolved"
            title={`${t.backend}.${t.tool}`}
          >
            {t.backend}.{t.tool}
          </span>
        ))}
      </span>
    </div>
  );
}

function ToolMode({
  value,
  onChange,
  backends,
  disabled,
}: {
  value: ActionSpec;
  onChange: (a: ActionSpec) => void;
  backends: readonly BackendOption[];
  disabled?: boolean;
}) {
  // Flat "<backend>.<tool>" pairs power the typeahead. We sort alphabetically
  // so the list is deterministic and matches operator expectations.
  const flat = useMemo(() => {
    const out: Array<{ backend: string; tool: string; label: string }> = [];
    for (const b of backends) {
      for (const t of b.tools || []) {
        out.push({ backend: b.id, tool: t, label: `${b.id}.${t}` });
      }
    }
    out.sort((a, b) => a.label.localeCompare(b.label));
    return out;
  }, [backends]);

  const current =
    value.backend && value.tool ? `${value.backend}.${value.tool}` : "";
  const [query, setQuery] = useState(current);

  // Filter as you type. Empty query → show all.
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return flat.slice(0, 50);
    return flat.filter((t) => t.label.toLowerCase().includes(q)).slice(0, 50);
  }, [flat, query]);

  return (
    <div class="picker-action-tool">
      <label class="picker-label" htmlFor="picker-action-tool-input">
        Filter tools
      </label>
      <input
        id="picker-action-tool-input"
        class="picker-input"
        type="text"
        placeholder="backend.tool"
        value={query}
        disabled={disabled}
        onInput={(e) => setQuery((e.currentTarget as HTMLInputElement).value)}
      />
      <div class="picker-action-tool-list" role="listbox">
        {filtered.length === 0 ? (
          <div class="picker-action-tool-empty">no tools match</div>
        ) : (
          filtered.map((t) => {
            const active =
              value.backend === t.backend && value.tool === t.tool;
            return (
              <button
                key={t.label}
                type="button"
                role="option"
                aria-selected={active}
                class={`picker-action-tool-item${active ? " picker-action-tool-item-active" : ""}`}
                disabled={disabled}
                onClick={() =>
                  onChange({ mode: "tool", backend: t.backend, tool: t.tool })
                }
              >
                {t.label}
              </button>
            );
          })
        )}
      </div>
    </div>
  );
}

function BackendWildcardMode({
  value,
  onChange,
  backends,
  disabled,
}: {
  value: ActionSpec;
  onChange: (a: ActionSpec) => void;
  backends: readonly BackendOption[];
  disabled?: boolean;
}) {
  const selected = value.backend || "";
  return (
    <div class="picker-action-wildcard">
      <label class="picker-label" htmlFor="picker-action-wildcard-select">
        Pick a backend
      </label>
      <select
        id="picker-action-wildcard-select"
        class="picker-select"
        value={selected}
        disabled={disabled}
        onChange={(e) =>
          onChange({
            mode: "backend_wildcard",
            backend: (e.currentTarget as HTMLSelectElement).value,
          })
        }
      >
        <option value="">— pick a backend —</option>
        {backends.map((b) => (
          <option key={b.id} value={b.id}>
            {b.label || b.id}
          </option>
        ))}
      </select>
      {selected && (
        <div class="picker-action-resolved" aria-live="polite">
          <span class="picker-action-resolved-label">Covers:</span>
          <span class="policy-chip policy-chip-resolved">{selected}.*</span>
        </div>
      )}
    </div>
  );
}
