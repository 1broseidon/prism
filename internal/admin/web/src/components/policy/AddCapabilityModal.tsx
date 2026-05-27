// AddCapabilityModal — sentence-builder modal for authoring capabilities
// (spec §6). Handles both add and edit modes via the `mode` prop.
//
// Composition:
//   - Header: title (Add / Edit) + subject label + close button.
//   - Body: 4 stacked pickers (Action / Where / When / How), then the
//     "Show advanced fields" disclosure, then optional raw-JSON fallback
//     for specs we couldn't reverse-map.
//   - Footer: Cancel + primary action (Add capability / Save).
//
// State: every input is mirrored into the modal-level `spec` state. There is
// NO global store — the parent feeds the initial spec via props (or nothing
// for add mode) and reads back the resulting CapabilityView through onSaved.
//
// Edit-mode reverse mapping: when an existing CapabilitySpec is passed in,
// we try to express each sub-shape via a preset. If a shape doesn't fit any
// preset (mostly the Advanced surface), the disclosure opens automatically
// and an inline note explains why.
//
// If the spec is so unusual that we can't even decode it cleanly (very rare —
// the backend authors all current specs), the modal falls back to a raw JSON
// editor that the operator can hand-tune and re-save.
//
// Accessibility: role=dialog, aria-modal=true, focus trap, Esc to close (with
// confirm when dirty), Enter on the last input submits.

import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { ApiError } from "../../api/client";
import type {
  CapabilitySpec,
  CapabilityView,
  SubjectType,
  Verb,
} from "../../api/policy";
import { createCapability, listVerbs, updateCapability } from "../../api/policy";
import { backends as backendsPolled } from "../../state";
import type { Backend } from "../../api/types";
import { showError } from "../../state/toasts";
import { PickerAction, type BackendOption } from "./PickerAction";
import { PickerWhere, AGENT_HOME_PREFIX } from "./PickerWhere";
import { PickerWhen } from "./PickerWhen";
import { PickerHow } from "./PickerHow";
import { AdvancedFields } from "./AdvancedFields";

export type CapabilityModalMode = "add" | "edit";

export interface AddCapabilityModalProps {
  mode: CapabilityModalMode;
  subjectType: SubjectType;
  subjectID: string;
  /** Display name shown in the header copy ("for engineering"). */
  subjectLabel?: string;
  /** Existing spec for edit mode. Ignored when mode === "add". */
  initialSpec?: CapabilitySpec;
  /** Existing view, used to PUT against the right capability id. */
  initialView?: CapabilityView;
  /** Roles surfaced in the AdvancedFields RoleRequired dropdown. */
  availableRoles?: readonly string[];
  onCancel: () => void;
  onSaved: (view: CapabilityView) => void;
}

const EMPTY_SPEC: CapabilitySpec = { action: { mode: "verb" } };

export function AddCapabilityModal(props: AddCapabilityModalProps) {
  const {
    mode,
    subjectType,
    subjectID,
    subjectLabel,
    initialSpec,
    initialView,
    availableRoles,
    onCancel,
    onSaved,
  } = props;

  // ── Local state ───────────────────────────────────────────────────────────
  const [spec, setSpec] = useState<CapabilitySpec>(() =>
    initialSpec ? cloneSpec(initialSpec) : { ...EMPTY_SPEC },
  );
  const [showAdvanced, setShowAdvanced] = useState<boolean>(() => {
    return Boolean(initialSpec?.advanced) || hasRawFallback(initialSpec);
  });
  const [rawFallback, setRawFallback] = useState<boolean>(() =>
    hasRawFallback(initialSpec),
  );
  const [rawJson, setRawJson] = useState<string>(() =>
    initialSpec ? JSON.stringify(initialSpec, null, 2) : "",
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const initialJsonRef = useRef<string>(
    initialSpec ? JSON.stringify(initialSpec) : JSON.stringify(EMPTY_SPEC),
  );

  // ── Refs for focus trap + initial focus ───────────────────────────────────
  const cardRef = useRef<HTMLDivElement | null>(null);
  const initialFocusRef = useRef<HTMLElement | null>(null);

  // ── Verb library ──────────────────────────────────────────────────────────
  const [verbs, setVerbs] = useState<readonly Verb[]>([]);
  useEffect(() => {
    let cancelled = false;
    listVerbs()
      .then((vs) => {
        if (!cancelled) setVerbs(vs);
      })
      .catch((err) => {
        if (!cancelled) showError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // ── Backend options ───────────────────────────────────────────────────────
  // Reuse the polled backends signal so we don't double-fetch when the modal
  // opens — admin pages keep this live. We pull the current value via the
  // signal accessor in render and refresh once on mount in case the modal
  // opens before any other page has primed the cache.
  const backendList: readonly Backend[] = backendsPolled.data.value || [];
  useEffect(() => {
    // Fire-and-forget refresh; if it fails the picker simply shows fewer
    // tools — the operator can still pick a verb or wildcard.
    void backendsPolled.refresh();
  }, []);
  const backendOptions: readonly BackendOption[] = useMemo(
    () =>
      backendList
        .filter((b) => b.enabled !== false)
        .map((b) => ({
          id: b.id,
          label: b.id,
          tools: (b.tools || [])
            .filter((t) => !t.disabled)
            .map((t) => t.name),
        })),
    [backendList],
  );

  // ── Dirty tracking ────────────────────────────────────────────────────────
  useEffect(() => {
    const now = rawFallback ? rawJson : JSON.stringify(spec);
    setDirty(now !== initialJsonRef.current);
  }, [spec, rawJson, rawFallback]);

  // ── Esc to close (with dirty confirm) ─────────────────────────────────────
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        attemptCancel();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // attemptCancel reads `dirty`/`busy`/`onCancel`; recompute on each render
    // is fine — the listener body is cheap.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dirty, busy]);

  // ── Focus trap + initial focus ────────────────────────────────────────────
  useEffect(() => {
    initialFocusRef.current?.focus();
  }, []);

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key !== "Tab") return;
    const root = cardRef.current;
    if (!root) return;
    const focusables = getFocusable(root);
    if (focusables.length === 0) return;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    const active = document.activeElement as HTMLElement | null;
    if (e.shiftKey) {
      if (active === first || !active || !root.contains(active)) {
        e.preventDefault();
        last.focus();
      }
    } else {
      if (active === last) {
        e.preventDefault();
        first.focus();
      }
    }
  };

  // ── Cancel + save handlers ────────────────────────────────────────────────
  const attemptCancel = () => {
    if (busy) return;
    if (dirty) {
      const ok =
        typeof window !== "undefined"
          ? window.confirm("Discard unsaved permission changes?")
          : true;
      if (!ok) return;
    }
    onCancel();
  };

  // Compose the spec that will be sent on save. We strip defaulted optional
  // fields so the wire form matches the backend's "no constraint" sentinel.
  const toWire = (): CapabilitySpec => {
    if (rawFallback) {
      // Parse here so submit can surface JSON errors inline.
      return JSON.parse(rawJson) as CapabilitySpec;
    }
    const out: CapabilitySpec = { action: { ...spec.action } };
    if (spec.where && spec.where.mode !== "anywhere") out.where = spec.where;
    if (spec.when && spec.when.mode !== "anytime") out.when = spec.when;
    if (spec.how_secure && spec.how_secure.mode !== "token") {
      out.how_secure = spec.how_secure;
    }
    if (spec.advanced) out.advanced = spec.advanced;
    return out;
  };

  const advancedActive = Boolean(spec.advanced);

  const validation = useMemo(
    () => validateForSubmit(spec, rawFallback, rawJson),
    [spec, rawFallback, rawJson],
  );

  const save = async () => {
    if (busy) return;
    if (!validation.ok) {
      setError(validation.reason || "form is incomplete");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const wire = toWire();
      const view =
        mode === "edit" && initialView
          ? await updateCapability(subjectType, subjectID, initialView.id, wire)
          : await createCapability(subjectType, subjectID, wire);
      onSaved(view);
    } catch (err) {
      handleSaveError(err, setError);
    } finally {
      setBusy(false);
    }
  };

  // ── Render ────────────────────────────────────────────────────────────────
  const title =
    mode === "edit"
      ? `Edit permission${subjectLabel ? ` for ${subjectLabel}` : ""}`
      : `Add permission${subjectLabel ? ` for ${subjectLabel}` : ""}`;
  const submitLabel = mode === "edit" ? "Save" : "Add permission";

  return (
    <div
      class="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) attemptCancel();
      }}
    >
      <div
        ref={cardRef}
        class="modal-card add-capability-modal"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onKeyDown={onKeyDown}
      >
        <div class="modal-header">
          <div class="modal-title">{title}</div>
          {subjectID && (
            <div class="modal-sub">
              {subjectType}: <code>{subjectID}</code>
            </div>
          )}
          <button
            type="button"
            class="modal-close"
            onClick={attemptCancel}
            aria-label="close"
          >
            ×
          </button>
        </div>

        <div class="modal-body">
          {rawFallback ? (
            <RawJsonEditor
              value={rawJson}
              onChange={setRawJson}
              disabled={busy}
              note="This permission uses fields the simple editor can't represent. Edit the raw JSON below and save."
              initialFocusRef={initialFocusRef}
            />
          ) : (
            <>
              <section class="modal-section" aria-label="What can they do?">
                <PickerAction
                  value={spec.action}
                  onChange={(action) => setSpec((s) => ({ ...s, action }))}
                  verbs={verbs}
                  backends={backendOptions}
                  disabled={busy}
                />
              </section>
              <section class="modal-section" aria-label="Where?">
                <PickerWhere
                  value={spec.where}
                  onChange={(where) =>
                    setSpec((s) => ({
                      ...s,
                      where: where || undefined,
                    }))
                  }
                  disabled={busy}
                  overriddenByAdvanced={Boolean(spec.advanced?.workspace)}
                />
              </section>
              <section class="modal-section" aria-label="When?">
                <PickerWhen
                  value={spec.when}
                  onChange={(when) =>
                    setSpec((s) => ({ ...s, when: when || undefined }))
                  }
                  disabled={busy}
                />
              </section>
              <section class="modal-section" aria-label="How securely?">
                <PickerHow
                  value={spec.how_secure}
                  onChange={(hs) =>
                    setSpec((s) => ({ ...s, how_secure: hs || undefined }))
                  }
                  disabled={busy}
                  overriddenByAdvanced={Boolean(spec.advanced?.acr_required)}
                />
              </section>
              <div class="add-capability-divider" />
              <label class="add-capability-advanced-toggle">
                <input
                  type="checkbox"
                  checked={showAdvanced}
                  disabled={busy}
                  onChange={(e) =>
                    setShowAdvanced(
                      (e.currentTarget as HTMLInputElement).checked,
                    )
                  }
                />
                <span>Show advanced fields</span>
              </label>
              {showAdvanced && (
                <section
                  class="modal-section add-capability-advanced"
                  aria-label="Advanced fields"
                >
                  {advancedActive && (
                    <div class="picker-hint">
                      Advanced fields override simple presets.
                    </div>
                  )}
                  <AdvancedFields
                    value={spec.advanced}
                    onChange={(advanced) =>
                      setSpec((s) => ({
                        ...s,
                        advanced: advanced || undefined,
                      }))
                    }
                    disabled={busy}
                    availableRoles={availableRoles}
                  />
                  <RawFallbackOptIn
                    onActivate={() => {
                      setRawJson(JSON.stringify(toWire(), null, 2));
                      setRawFallback(true);
                    }}
                  />
                </section>
              )}
              {hasRawFallback(initialSpec) && !rawFallback && (
                <div class="picker-hint picker-hint-warn">
                  This permission uses advanced fields.
                </div>
              )}
            </>
          )}

          {error && (
            <div class="add-capability-error" role="alert">
              {error}
            </div>
          )}
        </div>

        <div class="modal-actions add-capability-actions">
          <button
            type="button"
            class="section-btn"
            onClick={attemptCancel}
            disabled={busy}
          >
            Cancel
          </button>
          <button
            type="button"
            class="section-btn section-btn-primary"
            onClick={save}
            disabled={busy || !validation.ok}
            onKeyDown={(e) => {
              // Enter on the primary button submits even if the focus moved
              // off the form fields — purely a keyboard convenience.
              if (e.key === "Enter") {
                e.preventDefault();
                save();
              }
            }}
          >
            {busy ? "saving…" : submitLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

// ── Helpers ────────────────────────────────────────────────────────────────

function cloneSpec(s: CapabilitySpec): CapabilitySpec {
  // Structured clone is overkill — JSON round-trip is enough since the spec
  // is plain JSON. This also strips undefined slots so subsequent checks see
  // a consistent shape.
  return JSON.parse(JSON.stringify(s)) as CapabilitySpec;
}

/**
 * hasRawFallback returns true when the spec carries an `advanced` block that
 * the simple-mode pickers cannot fully represent on their own. We use it to
 * pop the disclosure open automatically and (when no field maps cleanly) to
 * fall back to the raw JSON editor.
 *
 * Right now any non-empty `advanced` block triggers the disclosure, but the
 * raw fallback is reserved for the case where AdvancedSpec.args carries the
 * `_tool` internal predicate the server uses for verb compilation — that's
 * never operator-authored and signals a shape we shouldn't try to roundtrip
 * through the structured editor.
 */
export function hasRawFallback(spec: CapabilitySpec | undefined): boolean {
  if (!spec || !spec.advanced) return false;
  const args = spec.advanced.args;
  if (!args) return false;
  return Object.prototype.hasOwnProperty.call(args, "_tool");
}

/**
 * specMatchesAgentHome reports whether a path_prefix value is the canonical
 * "agent home" template substitution. Exported so the row tests can verify
 * the round-trip mapping behaviour.
 */
export function specMatchesAgentHome(prefix: string): boolean {
  return prefix === AGENT_HOME_PREFIX;
}

interface Validation {
  ok: boolean;
  reason?: string;
}

function validateForSubmit(
  spec: CapabilitySpec,
  rawFallback: boolean,
  raw: string,
): Validation {
  if (rawFallback) {
    try {
      const parsed = JSON.parse(raw) as CapabilitySpec;
      if (!parsed.action || !parsed.action.mode) {
        return { ok: false, reason: "raw JSON must include an action.mode" };
      }
      return { ok: true };
    } catch (err) {
      return {
        ok: false,
        reason: `invalid JSON: ${err instanceof Error ? err.message : String(err)}`,
      };
    }
  }
  const a = spec.action;
  if (!a || !a.mode) return { ok: false, reason: "pick an action" };
  if (a.mode === "verb" && !a.verb_slug) {
    return { ok: false, reason: "pick a verb" };
  }
  if (a.mode === "tool" && (!a.backend || !a.tool)) {
    return { ok: false, reason: "pick a specific tool" };
  }
  if (a.mode === "backend_wildcard" && !a.backend) {
    return { ok: false, reason: "pick a backend" };
  }
  if (
    spec.where?.mode === "path_prefix" &&
    !(spec.where.path_prefix && spec.where.path_prefix.length > 0)
  ) {
    return { ok: false, reason: "path prefix can't be empty" };
  }
  return { ok: true };
}

function handleSaveError(
  err: unknown,
  setError: (msg: string) => void,
): void {
  if (err instanceof ApiError) {
    if (err.status >= 400 && err.status < 500) {
      setError(err.message || `request failed (${err.status})`);
      return;
    }
    // 5xx: surface a toast and keep the modal open per task spec.
    showError(`server error (${err.status}): ${err.message}`);
    setError(`server error (${err.status}) — please retry`);
    return;
  }
  setError(err instanceof Error ? err.message : String(err));
}

function getFocusable(root: HTMLElement): HTMLElement[] {
  // Standard focusable-elements selector — same set Preact-iso ships in
  // its dialog primitive. We exclude `[tabindex="-1"]` so programmatically
  // managed nodes (e.g. resolved tool chips) don't intercept Tab cycles.
  const sel = [
    "a[href]",
    "button:not([disabled])",
    "textarea:not([disabled])",
    "input:not([disabled])",
    "select:not([disabled])",
    "[tabindex]:not([tabindex='-1'])",
  ].join(",");
  return Array.from(root.querySelectorAll<HTMLElement>(sel)).filter(
    (el) => !el.hasAttribute("hidden"),
  );
}

// ── Raw JSON fallback ──────────────────────────────────────────────────────

function RawJsonEditor({
  value,
  onChange,
  disabled,
  note,
  initialFocusRef,
}: {
  value: string;
  onChange: (next: string) => void;
  disabled?: boolean;
  note?: string;
  initialFocusRef: { current: HTMLElement | null };
}) {
  return (
    <div class="add-capability-raw">
      {note && <div class="picker-hint">{note}</div>}
      <textarea
        ref={(el) => {
          initialFocusRef.current = el;
        }}
        class="add-capability-raw-textarea"
        value={value}
        rows={16}
        disabled={disabled}
        spellcheck={false}
        onInput={(e) => onChange((e.currentTarget as HTMLTextAreaElement).value)}
      />
    </div>
  );
}

function RawFallbackOptIn({ onActivate }: { onActivate: () => void }) {
  return (
    <div class="add-capability-raw-opt-in">
      <button
        type="button"
        class="advanced-add-btn"
        onClick={onActivate}
      >
        Switch to raw JSON editor
      </button>
      <div class="picker-hint">
        Use this when the form can't express the constraint you need.
      </div>
    </div>
  );
}
