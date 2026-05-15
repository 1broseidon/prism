import { useEffect, useRef, useState } from "preact/hooks";

interface Props {
  initial: string[];
  hints?: string[];
  placeholder?: string;
  inputWidth?: string;
  onCommit: (scopes: string[]) => void;
  onCancel?: () => void;
  autoFocus?: boolean;
}

// Pill-style scope editor with autocomplete. Saves on blur, cancels on Escape.
// Used for agent scopes, group scopes, and global default scopes.
export function ScopeEditor({
  initial,
  hints = [],
  placeholder = "+ scope",
  inputWidth,
  onCommit,
  onCancel,
  autoFocus = true,
}: Props) {
  const [scopes, setScopes] = useState<string[]>([...initial]);
  const [value, setValue] = useState("");
  const [selectedIdx, setSelectedIdx] = useState(-1);
  const inputRef = useRef<HTMLInputElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const committedRef = useRef(false);

  useEffect(() => {
    if (autoFocus) inputRef.current?.focus();
  }, [autoFocus]);

  const commit = () => {
    if (committedRef.current) return;
    committedRef.current = true;
    const same =
      scopes.length === initial.length &&
      scopes.every((s, i) => s === initial[i]);
    if (same) {
      onCancel?.();
      return;
    }
    onCommit(scopes);
  };

  const cancel = () => {
    if (committedRef.current) return;
    committedRef.current = true;
    onCancel?.();
  };

  const filteredHints = (() => {
    const v = value.toLowerCase();
    if (!v) return [];
    const base = hints
      .filter((h) => h.toLowerCase().includes(v) && !scopes.includes(h))
      .slice(0, 6);
    if (v.includes(":") && !base.includes(v) && !scopes.includes(v)) {
      return [v, ...base].slice(0, 6);
    }
    return base;
  })();

  const addScope = (s: string) => {
    if (!s || scopes.includes(s)) return;
    const next = [...scopes, s].sort();
    setScopes(next);
    setValue("");
    setSelectedIdx(-1);
  };

  const removeScope = (s: string) => {
    setScopes(scopes.filter((x) => x !== s));
  };

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelectedIdx(Math.min(selectedIdx + 1, filteredHints.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelectedIdx(Math.max(selectedIdx - 1, -1));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (selectedIdx >= 0 && filteredHints[selectedIdx]) {
        addScope(filteredHints[selectedIdx]);
      } else if (value.trim()) {
        addScope(value.trim());
      }
    } else if (e.key === "Escape") {
      e.preventDefault();
      cancel();
    } else if (e.key === "Backspace" && !value && scopes.length) {
      const next = [...scopes];
      next.pop();
      setScopes(next);
    }
  };

  const onBlur = () => {
    // Defer so clicks on suggestions/x buttons land first
    setTimeout(() => {
      if (!containerRef.current) return;
      if (containerRef.current.contains(document.activeElement)) return;
      commit();
    }, 200);
  };

  return (
    <div class="scope-editor" ref={containerRef}>
      {scopes.map((s) => (
        <span class="scope-pill" key={s}>
          {s}
          <span
            class="x"
            onMouseDown={(e) => {
              e.preventDefault();
              removeScope(s);
            }}
          >
            x
          </span>
        </span>
      ))}
      <input
        ref={inputRef}
        class="scope-input"
        type="text"
        placeholder={placeholder}
        spellcheck={false}
        autocomplete="off"
        style={inputWidth ? `width:${inputWidth}` : undefined}
        value={value}
        onInput={(e) => {
          setValue((e.target as HTMLInputElement).value);
          setSelectedIdx(-1);
        }}
        onKeyDown={onKeyDown}
        onBlur={onBlur}
      />
      {filteredHints.length > 0 && (
        <div class="scope-suggest">
          {filteredHints.map((h, i) => (
            <div
              class={
                i === selectedIdx
                  ? "scope-suggest-item selected"
                  : "scope-suggest-item"
              }
              key={h}
              onMouseDown={(e) => {
                e.preventDefault();
                addScope(h);
              }}
            >
              {h}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
