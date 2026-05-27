// CapabilityRow — renders one CapabilityView as a three-line ACL-style row
// (task-46). The visual grammar:
//
//   ┌─┐ ACTION chip                                         [?] [⋯] [🗑]
//   │ │ on <resource>
//   │ │ when <conditions>
//   └─┘
//
// The left rail (3-4px) is colour-coded by Effect — green for allow, red for
// deny — so operators can spot deny rows by peripheral vision while
// scrolling. The colour uses CSS tokens (--effect-allow / --effect-deny)
// rather than hard-coded literals so both light and dark themes can swap
// the value in tokens.css without touching this component.
//
// Line composition:
//   1. Action  — the canonical action chip rendered by CapabilityChip
//                (e.g. "write files", "call github.create_issue"). This is
//                the only chip that always appears.
//   2. Resource — "on <where>" if the spec carries a Where chip; otherwise
//                blank. Operators read this as the WHERE clause of an ACL
//                entry ("write files ON /workspace/${agent}/").
//   3. Conditions — comma-joined time / freshness / auth-manner chips. Same
//                presentation as the original chip strip; the visual
//                grouping is the only thing changing.
//
// `aria-label` is the server-rendered `display_summary` so screen readers
// don't have to reconstruct the sentence from individual chips.
//
// Click behavior:
//   - The edit button ([⋯]) emits `onEdit(view.spec)`. The actual modal
//     ships in task-35.
//   - The delete button ([🗑]) opens an inline confirm prompt, calls
//     `api/policy.deleteCapability`, then emits `onDelete(view.id)` so the
//     parent list can refresh.
//
// The Source badge ("scope" vs "grant") is only visible in Advanced mode
// (spec §11) — default view stays uncluttered.

import { useState } from "preact/hooks";
import type { CapabilityView, Chip, CapabilitySpec, SubjectType } from "../../api/policy";
import { deleteCapability } from "../../api/policy";
import { useAdvanced } from "../../hooks/useAdvanced";
import { CapabilityChip } from "./CapabilityChip";
import { InlineRawEditor } from "./InlineRawEditor";

// Canonical chip order per spec §5.2 (after the action chip, which is
// always rendered first). Anything not listed here is appended at the end
// in stable order so we never silently drop server-provided chips.
const CONSTRAINT_ORDER: readonly string[] = [
  "where",
  "storage",
  "time",
  "freshness",
  "auth",
];

/** orderedChips returns chips in the spec §5.2 canonical render order. */
export function orderedChips(chips: readonly Chip[] | undefined): Chip[] {
  if (!chips || chips.length === 0) return [];
  const action: Chip[] = [];
  const byKind = new Map<string, Chip[]>();
  const tail: Chip[] = [];
  for (const c of chips) {
    const k = (c.kind || "").toLowerCase();
    if (k === "action") {
      action.push(c);
      continue;
    }
    if (CONSTRAINT_ORDER.includes(k)) {
      const bucket = byKind.get(k) ?? [];
      bucket.push(c);
      byKind.set(k, bucket);
      continue;
    }
    tail.push(c);
  }
  const sorted: Chip[] = [...action];
  for (const kind of CONSTRAINT_ORDER) {
    const bucket = byKind.get(kind);
    if (bucket) sorted.push(...bucket);
  }
  sorted.push(...tail);
  return sorted;
}

// Resource and condition kinds split the constraint chips into the two
// ACL-style lines. "Where" / "storage" describe the *thing* being acted on
// (resource); "time", "freshness", "auth", and unknown kinds describe *how*
// the call must happen (conditions).
const RESOURCE_KINDS = new Set(["where", "storage"]);

export interface CapabilityRowProps {
  /** The row's data, including pre-tokenized chips + display_summary. */
  view: CapabilityView;
  /** Identifies the row's parent subject — used for the delete call. */
  subjectType: SubjectType;
  /** Subject id (group/role/agent id). */
  subjectID: string;
  /** Human-facing subject label, used in the delete confirmation copy. */
  subjectLabel?: string;
  /**
   * Invoked when the operator clicks [⋯]. Receives the structured spec so
   * the parent can pre-fill the Add/Edit modal (task-35 ships the modal).
   */
  onEdit?: (spec: CapabilitySpec, view: CapabilityView) => void;
  /**
   * Fired after a successful delete. The row already removed itself
   * visually; the parent typically refreshes its list signal.
   */
  onDelete?: (id: string) => void;
  /** Surface mutation failures to the caller (CapabilityList shows a toast). */
  onError?: (err: unknown) => void;
  /**
   * Optional "Why?" handler — when set, the row renders an inline [?]
   * button between the chip group and [⋯]. Spec context: task-39 folds
   * the standalone PolicyResolutionSection into a per-row trace popover
   * on the Effective Policy list. The CapabilityRow itself stays a thin
   * rendering primitive; the caller wires up the actual WhyTraceModal.
   */
  onWhy?: (view: CapabilityView) => void;
}

export function CapabilityRow({
  view,
  subjectType,
  subjectID,
  subjectLabel,
  onEdit,
  onDelete,
  onError,
  onWhy,
}: CapabilityRowProps) {
  const advanced = useAdvanced();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [rawOpen, setRawOpen] = useState(false);

  // Inline raw JSON editor (spec §11) shows only when:
  //   - Advanced toggle is ON, and
  //   - the row's source is a grant (not a plain scope string).
  // Scope-shape rows don't get raw editing here; operators who want raw
  // scope editing use the Advanced cross-cutting page.
  const rawEditable = advanced && view.source === "grant";

  const chips = orderedChips(view.chips);
  const actionChip = chips.find((c) => (c.kind || "").toLowerCase() === "action");
  const constraintChips = chips.filter(
    (c) => (c.kind || "").toLowerCase() !== "action",
  );
  const resourceChips = constraintChips.filter((c) =>
    RESOURCE_KINDS.has((c.kind || "").toLowerCase()),
  );
  const conditionChips = constraintChips.filter(
    (c) => !RESOURCE_KINDS.has((c.kind || "").toLowerCase()),
  );

  const effect = view.effect === "deny" ? "deny" : "allow";

  const confirm = async () => {
    if (busy) return;
    setBusy(true);
    try {
      await deleteCapability(subjectType, subjectID, view.id);
      setConfirming(false);
      onDelete?.(view.id);
    } catch (err) {
      onError?.(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      class={`capability-row capability-row-${effect}`}
      role="listitem"
      aria-label={view.display_summary || undefined}
      data-effect={effect}
    >
      <div class="capability-row-rail" aria-hidden="true" />
      <div class="capability-row-top">
        <div class="capability-row-main">
          <div class="capability-row-line capability-row-line-action">
            {actionChip ? (
              <CapabilityChip
                kind="action"
                label={actionChip.label}
                value={actionChip.value}
              />
            ) : (
              // Fallback when the server omitted the action chip — surface
              // the summary text so the row is never silently empty.
              <span class="capability-row-fallback">
                {view.display_summary || view.id}
              </span>
            )}
            {advanced && view.source && (
              <span
                class={`policy-source-badge policy-source-${view.source}`}
                title={`stored as a ${view.source}`}
              >
                {view.source}
              </span>
            )}
          </div>

          {resourceChips.length > 0 && (
            <div class="capability-row-line capability-row-line-resource">
              <span class="capability-row-prefix">on</span>
              {resourceChips.map((c, i) => (
                <CapabilityChip
                  key={`r-${c.kind}-${i}`}
                  kind={c.kind}
                  label={c.label}
                  value={c.value}
                />
              ))}
            </div>
          )}

          {conditionChips.length > 0 && (
            <div class="capability-row-line capability-row-line-conditions">
              <span class="capability-row-prefix">when</span>
              {conditionChips.map((c, i) => (
                <CapabilityChip
                  key={`c-${c.kind}-${i}`}
                  kind={c.kind}
                  label={c.label}
                  value={c.value}
                />
              ))}
            </div>
          )}
        </div>

        <div class="capability-row-actions">
          {confirming ? (
            <ConfirmPrompt
              subjectLabel={subjectLabel}
              busy={busy}
              onConfirm={confirm}
              onCancel={() => setConfirming(false)}
            />
          ) : (
            <>
              {onWhy && (
                <button
                  type="button"
                  class="capability-row-btn capability-row-btn-why"
                  aria-label="explain why this permission resolves"
                  title="why does this resolve?"
                  onClick={() => onWhy(view)}
                >
                  ?
                </button>
              )}
              {rawEditable && (
                <button
                  type="button"
                  class="capability-row-btn capability-row-btn-raw"
                  aria-label={rawOpen ? "close raw editor" : "open raw editor"}
                  title={rawOpen ? "close raw JSON" : "edit raw JSON"}
                  onClick={() => setRawOpen((v) => !v)}
                >
                  {`{}`}
                </button>
              )}
              <button
                type="button"
                class="capability-row-btn capability-row-btn-edit"
                aria-label="edit permission"
                title="edit"
                onClick={() => onEdit?.(view.spec, view)}
              >
                ⋯
              </button>
              <button
                type="button"
                class="capability-row-btn capability-row-btn-delete"
                aria-label="remove permission"
                title="remove"
                onClick={() => setConfirming(true)}
              >
                🗑
              </button>
            </>
          )}
        </div>
      </div>
      {rawEditable && rawOpen && (
        <InlineRawEditor
          view={view}
          subjectType={subjectType}
          subjectID={subjectID}
          onCancel={() => setRawOpen(false)}
          onSaved={() => {
            // Closing the editor and letting the parent list refresh on its
            // own keeps state simple. CapabilityList re-fetches when the
            // operator next interacts; if a hard refresh is needed,
            // surface it via onDelete-style callback.
            setRawOpen(false);
            // Treat as a delete+reinsert from the list's perspective so the
            // stale row vanishes immediately — the list will reload itself
            // on next mount or when the modal closes elsewhere.
            onDelete?.(view.id);
          }}
        />
      )}
    </div>
  );
}

function ConfirmPrompt({
  subjectLabel,
  busy,
  onConfirm,
  onCancel,
}: {
  subjectLabel: string | undefined;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const who = subjectLabel ? ` from ${subjectLabel}` : "";
  return (
    <div class="capability-row-confirm" role="alertdialog" aria-live="polite">
      <span class="capability-row-confirm-text">
        Remove this permission{who}?
      </span>
      <button
        type="button"
        class="capability-row-btn capability-row-btn-confirm"
        onClick={onConfirm}
        disabled={busy}
      >
        {busy ? "removing…" : "remove"}
      </button>
      <button
        type="button"
        class="capability-row-btn"
        onClick={onCancel}
        disabled={busy}
      >
        cancel
      </button>
    </div>
  );
}
