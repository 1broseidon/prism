import { useEffect, useRef, useState } from "preact/hooks";
import { ApiError } from "../../api/client";
import { identityRename } from "../../api/identity";
import { canMutate } from "../../state/me";

interface RenamePencilProps {
  /** Current display name shown next to the pencil. */
  currentName: string;
  /** Identity entity ID (ULID) -- if undefined, pencil is hidden. */
  entityId: string | undefined;
  /** Called with the new name after a successful rename. */
  onSuccess: (newName: string) => void;
  /** Extra CSS class on the wrapper (optional). */
  class?: string;
}

function joinClass(...parts: Array<string | undefined>): string | undefined {
  const cls = parts.filter(Boolean).join(" ");
  return cls || undefined;
}

function inlineError(err: unknown): string {
  if (err instanceof ApiError && err.status === 409 && err.message === "display_name_in_use") {
    return "Name already in use";
  }
  if (err instanceof Error) return err.message;
  return String(err);
}

export function RenamePencil({
  currentName,
  entityId,
  onSuccess,
  class: className,
}: RenamePencilProps) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(currentName);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const inFlightRef = useRef(false);
  const skipBlurCommitRef = useRef(false);

  useEffect(() => {
    if (!editing) {
      setValue(currentName);
    }
  }, [currentName, editing]);

  useEffect(() => {
    if (!editing) return;
    inputRef.current?.focus();
    inputRef.current?.select();
  }, [editing]);

  if (!entityId) {
    return null;
  }

  const wrapperClass = joinClass("rename-pencil", className);

  if (!canMutate()) {
    return <span class={wrapperClass}>{currentName}</span>;
  }

  const startEdit = () => {
    setValue(currentName);
    setError(null);
    setEditing(true);
  };

  const cancelEdit = () => {
    skipBlurCommitRef.current = true;
    setValue(currentName);
    setError(null);
    setEditing(false);
  };

  const commit = async () => {
    if (inFlightRef.current) return;
    const next = value.trim();
    inFlightRef.current = true;
    setSaving(true);
    setError(null);
    try {
      await identityRename(entityId, next);
      onSuccess(next);
      setEditing(false);
    } catch (err) {
      setError(inlineError(err));
    } finally {
      inFlightRef.current = false;
      setSaving(false);
    }
  };

  if (editing) {
    return (
      <span class={joinClass(wrapperClass, "rename-pencil-editing")}>
        <input
          ref={inputRef}
          type="text"
          class="search-input rename-pencil-input"
          value={value}
          disabled={saving}
          aria-label="display name"
          onInput={(e) => {
            setValue((e.currentTarget as HTMLInputElement).value);
            if (error) setError(null);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void commit();
            } else if (e.key === "Escape") {
              e.preventDefault();
              cancelEdit();
            }
          }}
          onBlur={() => {
            if (skipBlurCommitRef.current) {
              skipBlurCommitRef.current = false;
              return;
            }
            void commit();
          }}
        />
        {saving && <span class="rename-pencil-saving">saving...</span>}
        {error && <span class="error-text rename-pencil-error">{error}</span>}
      </span>
    );
  }

  return (
    <span class={wrapperClass}>
      <span class="rename-pencil-name">{currentName}</span>
      <button
        type="button"
        class="section-btn rename-pencil-button"
        onClick={startEdit}
        aria-label={`rename ${currentName}`}
        title="rename display name"
      >
        ✎
      </button>
    </span>
  );
}
