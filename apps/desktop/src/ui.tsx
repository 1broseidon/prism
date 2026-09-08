import type { ComponentChildren, JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

type ButtonProps = JSX.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "default" | "primary" | "danger" | "quiet" | "icon";
  busy?: boolean;
  state?: "success" | "error";
  hint?: string;
  children?: ComponentChildren;
};

export function Button({ variant = "default", busy, state, hint, children, class: cls, type, disabled, ...rest }: ButtonProps) {
  return (
    <button
      type={type ?? "button"}
      class={`btn ${variant === "default" ? "" : variant} ${cls ?? ""}`}
      aria-busy={busy ? "true" : undefined}
      data-state={state}
      disabled={busy || disabled}
      {...rest}
    >
      {children}
      {hint ? <kbd>{hint}</kbd> : null}
    </button>
  );
}

/** A destructive button that asks once, on itself: the first tap arms it, the second within 3 s fires. */
export function ConfirmButton({ confirm, onConfirm, children, class: cls, ...rest }: Omit<ButtonProps, "onClick"> & { confirm: string; onConfirm: () => void }) {
  const [armed, setArmed] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const click = () => {
    if (armed) {
      window.clearTimeout(timer.current);
      setArmed(false);
      onConfirm();
      return;
    }
    setArmed(true);
    timer.current = window.setTimeout(() => setArmed(false), 3000);
  };
  return (
    <Button {...rest} class={`${cls ?? ""} ${armed ? "armed" : ""}`} aria-pressed={armed} onClick={click} onBlur={() => setArmed(false)}>
      {armed ? confirm : children}
    </Button>
  );
}

export function Chip({ tone, children }: { tone?: "ok" | "warn" | "danger" | "accent"; children: ComponentChildren }) {
  return <span class={`chip ${tone ?? ""}`}>{children}</span>;
}

export function StatusText({ tone, children }: { tone?: "ok" | "danger" | "accent"; children: ComponentChildren }) {
  return <span class={`status-text ${tone ?? ""}`}>{children}</span>;
}

export function BackIcon() {
  return <svg class="control-icon" viewBox="0 0 16 16" aria-hidden="true"><path d="M10.5 3.5 6 8l4.5 4.5M6.5 8H14" /></svg>;
}

export function CloseIcon() {
  return <svg class="control-icon" viewBox="0 0 16 16" aria-hidden="true"><path d="m4.5 4.5 7 7m0-7-7 7" /></svg>;
}

export function SettingsIcon() {
  return (
    <svg class="control-icon" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2 4.5h12M2 11.5h12" />
      <circle cx="6" cy="4.5" r="1.75" />
      <circle cx="10.5" cy="11.5" r="1.75" />
    </svg>
  );
}

export function ChevronIcon({ direction = "right" }: { direction?: "left" | "right" | "down" | "up" }) {
  return (
    <svg class={`control-icon chevron-icon ${direction}`} viewBox="0 0 16 16" aria-hidden="true">
      <path d="m6 3.5 4.5 4.5L6 12.5" />
    </svg>
  );
}

export function Label({ children, right }: { children: ComponentChildren; right?: ComponentChildren }) {
  return (
    <div class="label">
      <span>{children}</span>
      {right ? <span>{right}</span> : null}
    </div>
  );
}

export function Empty({ title, children }: { title: string; children?: ComponentChildren }) {
  return (
    <div class="empty">
      <strong>{title}</strong>
      {children ? <p>{children}</p> : null}
    </div>
  );
}

/** Copies text, reports success on the button itself for a moment, no toast. */
export function useCopy(): [state: "success" | "error" | undefined, copy: (text: string) => Promise<void>] {
  const [state, setState] = useState<"success" | "error" | undefined>(undefined);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setState("success");
    } catch {
      setState("error");
    }
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setState(undefined), 1500);
  };
  return [state, copy];
}

export function CodeBlock({ text, copyable, emptyText }: { text: string; copyable?: boolean; emptyText?: string }) {
  const [state, copy] = useCopy();
  const empty = text.trim() === "" && emptyText;
  return (
    <div class="code-wrap">
      <pre class={`code ${empty ? "empty-args" : ""}`}>{empty ? emptyText : text}</pre>
      {copyable ? (
        <Button variant="quiet" class="copy" state={state} onClick={() => void copy(text)}>
          {state === "success" ? "Copied" : state === "error" ? "Copy failed" : "Copy"}
        </Button>
      ) : null}
    </div>
  );
}

/** A segmented control. One value, a few options, the chosen one lifted onto paper. */
export function Segmented<T extends string>({
  value,
  options,
  onChange,
  small,
  label,
}: {
  value: T | null;
  options: { value: T; label: string }[];
  onChange: (value: T) => void;
  small?: boolean;
  label: string;
}) {
  return (
    <div class={`seg ${small ? "small" : ""}`} role="group" aria-label={label}>
      {options.map((opt) => (
        <button
          key={opt.value}
          type="button"
          aria-pressed={value === opt.value}
          onClick={() => onChange(opt.value)}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}

export function Switch({ checked, onChange, label }: { checked: boolean; onChange: (next: boolean) => void; label: string }) {
  return (
    <button type="button" role="switch" aria-checked={checked} aria-label={label} class="switch" onClick={() => onChange(!checked)}>
      <i />
    </button>
  );
}

/** A screen owns its own scroll region and an optional action bar pinned to the bottom, phone-style. */
/** `log` marks a screen that is a long list by nature and so shows a scrollbar; others scroll without one. */
export function Screen({ children, footer, log }: { children: ComponentChildren; footer?: ComponentChildren; log?: boolean }) {
  return (
    <>
      <div class={`screen-body ${log ? "log" : ""}`}>{children}</div>
      {footer ? <div class="screen-footer">{footer}</div> : null}
    </>
  );
}

/** A bounded page of a collection. The start clamps when the collection shrinks and resets when `key` changes. */
export function usePage<T>(items: T[], size: number, key?: string): { rows: T[]; offset: number; setOffset: (offset: number) => void; total: number } {
  const [offset, setOffset] = useState(0);
  useEffect(() => setOffset(0), [key]);
  const total = items.length;
  const start = Math.min(offset, Math.max(0, Math.floor((total - 1) / size) * size));
  return { rows: items.slice(start, start + size), offset: start, setOffset, total };
}

/** Where a page sits in its collection and the two moves. Renders nothing while one page holds everything. */
export function Pager({ offset, size, total, onOffset, disabled = false }: { offset: number; size: number; total: number; onOffset: (offset: number) => void; disabled?: boolean }) {
  if (total <= size) return null;
  const end = Math.min(offset + size, total);
  return (
    <div class="pager" role="navigation" aria-label="Pages">
      <Button variant="icon" aria-label="Previous page" disabled={disabled || offset === 0} onClick={() => onOffset(Math.max(0, offset - size))}>
        <ChevronIcon direction="left" />
      </Button>
      <span class="range">
        {offset + 1}–{end} <span class="muted">of {total}</span>
      </span>
      <Button variant="icon" aria-label="Next page" disabled={disabled || end >= total} onClick={() => onOffset(offset + size)}>
        <ChevronIcon />
      </Button>
    </div>
  );
}

/** A summary row that leads to a bounded subscreen: label, a short value, a chevron. The whole row is the button. */
export function HubRow({ label, value, tone, onClick }: { label: string; value?: ComponentChildren; tone?: "accent"; onClick: () => void }) {
  return (
    <button type="button" class="hub-row" onClick={onClick}>
      <span class="hub-label truncate">{label}</span>
      {value ? <span class={`hub-value ${tone ?? ""}`}>{value}</span> : null}
      <span class="chev"><ChevronIcon /></span>
    </button>
  );
}

export function Notice({ text, onDismiss }: { text: string; onDismiss: () => void }) {
  return (
    <div class="notice" role="alert">
      <span>{text}</span>
      <Button variant="icon" aria-label="Dismiss" onClick={onDismiss}>
        <CloseIcon />
      </Button>
    </div>
  );
}

export function describeError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
