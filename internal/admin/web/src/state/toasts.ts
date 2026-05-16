import { signal } from "@preact/signals";

export type ToastKind = "info" | "success" | "error";

export interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
}

let nextId = 0;
export const toasts = signal<Toast[]>([]);

const DEFAULT_DURATION: Record<ToastKind, number> = {
  info: 3500,
  success: 3500,
  error: 6000,
};

function add(kind: ToastKind, message: string, durationMs?: number): void {
  const id = ++nextId;
  toasts.value = [...toasts.value, { id, kind, message }];
  const ms = durationMs ?? DEFAULT_DURATION[kind];
  if (ms > 0) {
    setTimeout(() => dismiss(id), ms);
  }
}

export function dismiss(id: number): void {
  toasts.value = toasts.value.filter((t) => t.id !== id);
}

export const toast = {
  info: (m: string, durationMs?: number) => add("info", m, durationMs),
  success: (m: string, durationMs?: number) => add("success", m, durationMs),
  error: (m: string, durationMs?: number) => add("error", m, durationMs),
};

// Wraps an async mutation: shows success or error toast based on outcome.
export async function withToast<T>(
  fn: () => Promise<T>,
  opts: { success?: string; error?: string } = {},
): Promise<T | undefined> {
  try {
    const result = await fn();
    if (opts.success) toast.success(opts.success);
    return result;
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    toast.error(opts.error ? `${opts.error}: ${msg}` : msg);
    return undefined;
  }
}
