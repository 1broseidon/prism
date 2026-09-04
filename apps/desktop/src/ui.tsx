import type { ComponentChildren, JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

type ButtonProps = JSX.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "default" | "primary" | "danger" | "quiet" | "icon";
  busy?: boolean;
  state?: "success" | "error";
  hint?: string;
  children?: ComponentChildren;
};

export function Button({ variant = "default", busy, state, hint, children, class: cls, type, ...rest }: ButtonProps) {
  return (
    <button
      type={type ?? "button"}
      class={`btn ${variant === "default" ? "" : variant} ${cls ?? ""}`}
      aria-busy={busy ? "true" : undefined}
      data-state={state}
      disabled={busy || rest.disabled}
      {...rest}
    >
      {children}
      {hint ? <kbd>{hint}</kbd> : null}
    </button>
  );
}

export function Chip({ tone, children }: { tone?: "ok" | "warn" | "danger" | "accent"; children: ComponentChildren }) {
  return <span class={`chip ${tone ?? ""}`}>{children}</span>;
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

export function Notice({ text, onDismiss }: { text: string; onDismiss: () => void }) {
  return (
    <div class="notice" role="alert">
      <span>{text}</span>
      <Button variant="icon" aria-label="Dismiss" onClick={onDismiss}>
        ×
      </Button>
    </div>
  );
}

export function describeError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
