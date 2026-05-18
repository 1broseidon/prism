import { useEffect, useMemo, useState } from "preact/hooks";
import { getJSON, patchJSON, postJSON } from "../api/client";
import type {
  Backend,
  BackendUpdateBody,
  OpenAPIDiffResponse,
  OpenAPISkippedOperation,
  OpenAPISpecSource,
} from "../api/types";
import { MethodPill } from "./OperationPicker";

// ReimportDiffModal drives the spec re-import flow on an existing OpenAPI
// backend. Two stages:
//   1. Spec source — URL or file upload. "Preview diff" calls
//      POST /backends/{id}/openapi-diff and shows the result.
//   2. Diff — operator picks which newly-added operations to keep disabled
//      from the start; everything else is read-only metadata.
// "Apply" calls POST /backends/{id}/reimport with disabled_tools_resolution
// fixed to "preserve" per the contract.

interface Props {
  backendId: string;
  initialSourceURL?: string;
  // initialInlineSpec is the raw persisted spec bytes (UTF-8). When supplied
  // the modal defaults to inline-edit mode, pre-filled with this text. The
  // operator can switch to URL refetch via the source-mode tabs.
  initialInlineSpec?: string;
  onClose: () => void;
  onApplied: () => void | Promise<void>;
}

interface DiffState {
  source: OpenAPISpecSource;
  diff: OpenAPIDiffResponse;
}

type ReimportSourceMode = "url" | "file" | "inline";

async function readFileAsBase64(file: File): Promise<string> {
  // FileReader is the most compatible base64 path; manually building the
  // string from ArrayBuffer would mishandle large UTF-8 specs on older
  // browsers. result is "data:...;base64,xxx" so we strip the prefix.
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error ?? new Error("file read failed"));
    reader.onload = () => {
      const result = reader.result;
      if (typeof result !== "string") {
        reject(new Error("unexpected file reader result"));
        return;
      }
      const comma = result.indexOf(",");
      resolve(comma >= 0 ? result.slice(comma + 1) : result);
    };
    reader.readAsDataURL(file);
  });
}

export function ReimportDiffModal({
  backendId,
  initialSourceURL,
  initialInlineSpec,
  onClose,
  onApplied,
}: Props) {
  // Default source mode: URL when the backend was URL-sourced, inline when
  // we have a persisted spec we can pre-fill (file or inline-sourced). The
  // operator can flip tabs to use any of the three.
  const initialMode: ReimportSourceMode = initialSourceURL
    ? "url"
    : initialInlineSpec
      ? "inline"
      : "url";
  const [mode, setMode] = useState<ReimportSourceMode>(initialMode);
  const [url, setUrl] = useState(initialSourceURL || "");
  const [fileBase64, setFileBase64] = useState<string | null>(null);
  const [fileName, setFileName] = useState<string>("");
  const [inlineText, setInlineText] = useState<string>(
    initialInlineSpec || "",
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [stage, setStage] = useState<"source" | "diff">("source");
  const [diffState, setDiffState] = useState<DiffState | null>(null);
  // Operators commonly want every brand-new op enabled by default; "disable
  // from the start" is opt-in per row. The set holds names slated for
  // disabling.
  const [newDisabled, setNewDisabled] = useState<Set<string>>(new Set());

  useEffect(() => {
    // Esc closes the modal everywhere — matches the inline forms which use
    // the same key in the add-backend flow.
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const resetSource = () => {
    setFileBase64(null);
    setFileName("");
  };

  const onFileChange = async (e: Event) => {
    const input = e.target as HTMLInputElement;
    const file = input.files && input.files[0];
    if (!file) return;
    setError(null);
    setBusy(true);
    try {
      const data = await readFileAsBase64(file);
      setFileBase64(data);
      setFileName(file.name);
      setUrl("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const buildSource = (): OpenAPISpecSource | null => {
    if (mode === "url") {
      const trimmed = url.trim();
      return trimmed ? { url: trimmed } : null;
    }
    if (mode === "file") {
      return fileBase64 ? { file: fileBase64 } : null;
    }
    return inlineText ? { inline: inlineText } : null;
  };

  const previewDiff = async () => {
    const source = buildSource();
    if (!source) {
      setError("provide a spec URL or upload a file");
      return;
    }
    setError(null);
    setBusy(true);
    try {
      const diff = await postJSON<OpenAPIDiffResponse>(
        `/backends/${encodeURIComponent(backendId)}/openapi-diff`,
        { source },
      );
      setDiffState({ source, diff });
      setNewDisabled(new Set());
      setStage("diff");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const apply = async () => {
    if (!diffState) return;
    setError(null);
    setBusy(true);
    try {
      await postJSON(
        `/backends/${encodeURIComponent(backendId)}/reimport`,
        {
          source: diffState.source,
          // Contract: re-import preserves the operator's per-tool toggles so
          // disabled ops stay disabled across spec swaps (rename-aware).
          disabled_tools_resolution: "preserve",
        },
      );
      // Merge the operator's "disable from the start" selection on newly-added
      // ops into the gateway-resolved disabled set. Two steps because
      // /reimport doesn't accept a disabled_tools override: it preserves the
      // prior curation and would silently drop any new names we tried to
      // smuggle in. PATCH /backends/{id} accepts the full replacement list, so
      // we read it back, union in the new names, and write it out.
      if (newDisabled.size > 0) {
        const fresh = await getJSON<Backend[]>("/backends");
        const me = fresh.find((b) => b.id === backendId);
        const prefix = `${me?.namespace || backendId}__`;
        const bareDisabled = new Set<string>();
        for (const t of me?.tools || []) {
          if (!t.disabled) continue;
          const bare = t.name.startsWith(prefix)
            ? t.name.slice(prefix.length)
            : t.name;
          bareDisabled.add(bare);
        }
        for (const name of newDisabled) bareDisabled.add(name);
        const next = Array.from(bareDisabled).sort();
        await patchJSON<unknown>(
          `/backends/${encodeURIComponent(backendId)}`,
          { disabled_tools: next } satisfies BackendUpdateBody,
        );
      }
      await onApplied();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      class="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        class="modal-card reimport-modal"
        role="dialog"
        aria-modal="true"
        aria-label={`re-import spec for ${backendId}`}
      >
        <div class="modal-header">
          <div class="modal-title">re-import openapi spec</div>
          <div class="modal-sub">backend: {backendId}</div>
          <button
            type="button"
            class="modal-close"
            onClick={onClose}
            aria-label="close"
          >
            ×
          </button>
        </div>

        {stage === "source" ? (
          <SourceStage
            mode={mode}
            onModeChange={(next) => {
              setMode(next);
              setError(null);
            }}
            allowedModes={
              initialSourceURL
                ? (["url"] as ReimportSourceMode[])
                : (["file", "inline"] as ReimportSourceMode[])
            }
            url={url}
            setUrl={(v) => {
              setUrl(v);
              if (v) resetSource();
            }}
            fileName={fileName}
            onFileChange={onFileChange}
            inlineText={inlineText}
            setInlineText={setInlineText}
            busy={busy}
            error={error}
            onPreview={previewDiff}
            onCancel={onClose}
          />
        ) : diffState ? (
          <DiffStage
            diff={diffState.diff}
            newDisabled={newDisabled}
            setNewDisabled={setNewDisabled}
            onBack={() => setStage("source")}
            onApply={apply}
            busy={busy}
            error={error}
          />
        ) : null}
      </div>
    </div>
  );
}

function SourceStage({
  mode,
  onModeChange,
  allowedModes,
  url,
  setUrl,
  fileName,
  onFileChange,
  inlineText,
  setInlineText,
  busy,
  error,
  onPreview,
  onCancel,
}: {
  mode: ReimportSourceMode;
  onModeChange: (next: ReimportSourceMode) => void;
  allowedModes: ReimportSourceMode[];
  url: string;
  setUrl: (v: string) => void;
  fileName: string;
  onFileChange: (e: Event) => void | Promise<void>;
  inlineText: string;
  setInlineText: (v: string) => void;
  busy: boolean;
  error: string | null;
  onPreview: () => void;
  onCancel: () => void;
}) {
  // URL-sourced backends only allow URL re-fetch; file/inline-sourced expose
  // the file + inline tabs (no URL fetch) so the spec stays operator-owned.
  const allTabs: { id: ReimportSourceMode; label: string }[] = [
    { id: "url", label: "url" },
    { id: "file", label: "file" },
    { id: "inline", label: "inline" },
  ];
  const tabs = allTabs.filter((t) => allowedModes.includes(t.id));
  return (
    <div class="modal-body">
      <div class="modal-section">
        <div class="openapi-source-tabs" role="tablist">
          {tabs.map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              aria-selected={mode === t.id}
              class={
                mode === t.id
                  ? "openapi-source-tab openapi-source-tab-active"
                  : "openapi-source-tab"
              }
              onClick={() => onModeChange(t.id)}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>
      {mode === "url" && (
        <div class="modal-section">
          <label class="config-label">spec url</label>
          <input
            type="text"
            class="config-input"
            value={url}
            autoFocus
            spellcheck={false}
            placeholder="https://example.com/openapi.yaml"
            onInput={(e) => setUrl((e.target as HTMLInputElement).value)}
          />
        </div>
      )}
      {mode === "file" && (
        <div class="modal-section">
          <label class="config-label">upload file</label>
          <div class="inline-form">
            <input
              type="file"
              accept=".json,.yaml,.yml,application/json,application/x-yaml,text/yaml,text/x-yaml"
              onChange={onFileChange}
            />
            {fileName && (
              <span class="hint-text">selected: {fileName}</span>
            )}
          </div>
        </div>
      )}
      {mode === "inline" && (
        <div class="modal-section openapi-inline-editor">
          <label class="config-label">edit spec</label>
          <textarea
            class="openapi-inline-textarea"
            value={inlineText}
            spellcheck={false}
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
            placeholder={
              "openapi: 3.1.0\ninfo:\n  title: My API\n  version: \"1.0\"\n…"
            }
            onInput={(e) =>
              setInlineText((e.target as HTMLTextAreaElement).value)
            }
          />
        </div>
      )}
      <div class="modal-actions">
        <button
          type="button"
          class="save-btn"
          onClick={onPreview}
          disabled={busy}
        >
          {busy ? "loading…" : "preview diff"}
        </button>
        <button
          type="button"
          class="cancel-btn"
          onClick={onCancel}
          disabled={busy}
        >
          cancel
        </button>
        {error && <span class="error-text">{error}</span>}
      </div>
    </div>
  );
}

function DiffStage({
  diff,
  newDisabled,
  setNewDisabled,
  onBack,
  onApply,
  busy,
  error,
}: {
  diff: OpenAPIDiffResponse;
  newDisabled: Set<string>;
  setNewDisabled: (s: Set<string>) => void;
  onBack: () => void;
  onApply: () => void;
  busy: boolean;
  error: string | null;
}) {
  const counts = useMemo(
    () => ({
      added: diff.added.length,
      removed: diff.removed.length,
      renamed: diff.renamed.length,
      signature: diff.signature_changed.length,
      newlySkipped: diff.newly_skipped.length,
      unchanged: diff.unchanged_count,
    }),
    [diff],
  );

  const totalChanges =
    counts.added +
    counts.removed +
    counts.renamed +
    counts.signature +
    counts.newlySkipped;

  const toggleNewDisabled = (name: string) => {
    const next = new Set(newDisabled);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    setNewDisabled(next);
  };

  return (
    <div class="modal-body diff-body">
      <div class="diff-summary">
        <DiffSummaryItem label="added" count={counts.added} tone="ok" />
        <DiffSummaryItem
          label="removed"
          count={counts.removed}
          tone="error"
        />
        <DiffSummaryItem
          label="renamed"
          count={counts.renamed}
          tone="neutral"
        />
        <DiffSummaryItem
          label="signature changed"
          count={counts.signature}
          tone="warn"
        />
        <DiffSummaryItem
          label="newly skipped"
          count={counts.newlySkipped}
          tone="warn"
        />
        <DiffSummaryItem
          label="unchanged"
          count={counts.unchanged}
          tone="neutral"
        />
      </div>

      {totalChanges === 0 ? (
        <div class="empty-state">
          this spec matches the persisted version — no changes to apply.
        </div>
      ) : (
        <div class="diff-sections">
          {counts.added > 0 && (
            <DiffBlock
              title={`added (${counts.added})`}
              hint="default-enabled. tick to disable from the start."
            >
              {diff.added.map((op) => (
                <div class="diff-row diff-row-added" key={op.name}>
                  <label class="diff-row-toggle">
                    <input
                      type="checkbox"
                      checked={newDisabled.has(op.name)}
                      onChange={() => toggleNewDisabled(op.name)}
                    />
                    <span class="hint-text">disable</span>
                  </label>
                  {op.method && <MethodPill method={op.method} />}
                  <span class="diff-row-name">{op.name}</span>
                  {op.path && (
                    <span class="diff-row-path">{op.path}</span>
                  )}
                </div>
              ))}
            </DiffBlock>
          )}
          {counts.removed > 0 && (
            <DiffBlock title={`removed (${counts.removed})`}>
              {diff.removed.map((op) => (
                <div class="diff-row diff-row-removed" key={op.name}>
                  {op.method && <MethodPill method={op.method} />}
                  <span class="diff-row-name">{op.name}</span>
                  {op.path && (
                    <span class="diff-row-path">{op.path}</span>
                  )}
                </div>
              ))}
            </DiffBlock>
          )}
          {counts.renamed > 0 && (
            <DiffBlock title={`renamed (${counts.renamed})`}>
              {diff.renamed.map((entry) => (
                <div
                  class="diff-row diff-row-renamed"
                  key={`${entry.from}->${entry.to}`}
                >
                  <span class="diff-row-name">{entry.from}</span>
                  <span class="diff-row-arrow">→</span>
                  <span class="diff-row-name">{entry.to}</span>
                </div>
              ))}
            </DiffBlock>
          )}
          {counts.signature > 0 && (
            <DiffBlock
              title={`signature changed (${counts.signature})`}
              hint="same name, different request/response shape — tools update automatically."
            >
              {diff.signature_changed.map((entry) => (
                <div class="diff-row diff-row-signature" key={entry.name}>
                  <span class="diff-row-name">{entry.name}</span>
                  <span class="op-row-deprecated">signature</span>
                </div>
              ))}
            </DiffBlock>
          )}
          {counts.newlySkipped > 0 && (
            <DiffBlock
              title={`newly skipped (${counts.newlySkipped})`}
              hint="these operations used to import but the new spec makes them untranslatable."
            >
              {diff.newly_skipped.map((s, idx) => (
                <SkippedRow key={`${s.name}-${idx}`} skipped={s} />
              ))}
            </DiffBlock>
          )}
        </div>
      )}

      <div class="modal-actions">
        <button
          type="button"
          class="save-btn"
          onClick={onApply}
          disabled={busy}
        >
          {busy ? "applying…" : "apply"}
        </button>
        <button
          type="button"
          class="section-btn"
          onClick={onBack}
          disabled={busy}
        >
          back
        </button>
        {error && <span class="error-text">{error}</span>}
      </div>
    </div>
  );
}

function DiffSummaryItem({
  label,
  count,
  tone,
}: {
  label: string;
  count: number;
  tone: "ok" | "warn" | "error" | "neutral";
}) {
  const cls =
    count > 0
      ? `diff-summary-item diff-summary-${tone}`
      : "diff-summary-item diff-summary-zero";
  return (
    <div class={cls}>
      <span class="diff-summary-count">{count}</span>
      <span class="diff-summary-label">{label}</span>
    </div>
  );
}

function DiffBlock({
  title,
  hint,
  children,
}: {
  title: string;
  hint?: string;
  children: preact.ComponentChildren;
}) {
  return (
    <div class="diff-block">
      <div class="diff-block-header">
        <span class="diff-block-title">{title}</span>
        {hint && <span class="section-sub">{hint}</span>}
      </div>
      <div class="diff-block-rows">{children}</div>
    </div>
  );
}

function SkippedRow({ skipped }: { skipped: OpenAPISkippedOperation }) {
  return (
    <div class="diff-row diff-row-skipped">
      <div class="diff-row-skipped-head">
        {skipped.method && <MethodPill method={skipped.method} />}
        <span class="diff-row-name">{skipped.name}</span>
        {skipped.path && (
          <span class="diff-row-path">{skipped.path}</span>
        )}
        <span class="op-skipped-reason">{skipped.reason}</span>
      </div>
      {skipped.detail && (
        <div class="op-skipped-detail">{skipped.detail}</div>
      )}
    </div>
  );
}
