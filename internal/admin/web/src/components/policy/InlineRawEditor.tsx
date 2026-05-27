// InlineRawEditor — per-row JSON editor that appears next to a CapabilityRow
// when Advanced is ON AND the underlying capability is grant-shaped
// (source === "grant"). Spec §11: operators in Advanced mode can view +
// hand-tune the raw GrantSpec / CapabilitySpec without leaving the row.
//
// Scope-shape capabilities (legacy AgentPolicy.Grant strings) are not edited
// here — that flow lives on the Advanced cross-cutting page where the
// scope string can be re-bound through the normal add modal.
//
// On Save: serializes the textarea back into CapabilitySpec JSON, calls
// updateCapability, and bubbles the new view up via onSaved so the parent
// list can refresh. Parse errors render inline; the textarea is the source
// of truth for the operator's last edit.

import { useState } from "preact/hooks";
import {
  updateCapability,
  type CapabilitySpec,
  type CapabilityView,
  type SubjectType,
} from "../../api/policy";

export interface InlineRawEditorProps {
  /** The row we're editing. Only grant-shape rows should mount this. */
  view: CapabilityView;
  subjectType: SubjectType;
  subjectID: string;
  /** Fires after a successful save so the parent list can re-fetch. */
  onSaved?: (next: CapabilityView) => void;
  /** Operator clicked "cancel" without saving. */
  onCancel?: () => void;
}

export function InlineRawEditor({
  view,
  subjectType,
  subjectID,
  onSaved,
  onCancel,
}: InlineRawEditorProps) {
  const [text, setText] = useState<string>(() => pretty(view.spec));
  const [error, setError] = useState<string>("");
  const [busy, setBusy] = useState(false);

  const save = async () => {
    let parsed: CapabilitySpec;
    try {
      parsed = JSON.parse(text) as CapabilitySpec;
    } catch (err) {
      setError(`invalid JSON: ${err instanceof Error ? err.message : String(err)}`);
      return;
    }
    if (!parsed || typeof parsed !== "object" || !parsed.action) {
      setError("missing required field: action");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const next = await updateCapability(
        subjectType,
        subjectID,
        view.id,
        parsed,
      );
      onSaved?.(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="inline-raw-editor" role="region" aria-label="Raw capability JSON">
      <div class="inline-raw-editor-head">
        <span class="inline-raw-editor-title">Raw GrantSpec</span>
        <span class="inline-raw-editor-id" title="capability id">
          {view.id}
        </span>
      </div>
      <textarea
        class="inline-raw-editor-text"
        spellcheck={false}
        rows={Math.min(20, Math.max(8, text.split("\n").length))}
        value={text}
        disabled={busy}
        onInput={(e) => setText((e.currentTarget as HTMLTextAreaElement).value)}
      />
      {error && (
        <div class="inline-raw-editor-error" role="alert">
          {error}
        </div>
      )}
      <div class="inline-raw-editor-actions">
        <button
          type="button"
          class="save-btn"
          disabled={busy}
          onClick={save}
        >
          {busy ? "saving…" : "save"}
        </button>
        <button
          type="button"
          class="cancel-btn"
          disabled={busy}
          onClick={onCancel}
        >
          cancel
        </button>
      </div>
    </div>
  );
}

function pretty(value: unknown): string {
  return JSON.stringify(value, null, 2);
}
