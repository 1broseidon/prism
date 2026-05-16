import { signal } from "@preact/signals";

export interface Toast {
  id: number;
  message: string;
}

let nextId = 0;
export const toasts = signal<Toast[]>([]);

export function dismiss(id: number): void {
  toasts.value = toasts.value.filter((t) => t.id !== id);
}

// Surface errors only. Successful state changes are confirmed by the visible
// data update — no need for a toast.
export function showError(message: string, durationMs = 6000): void {
  const id = ++nextId;
  toasts.value = [...toasts.value, { id, message }];
  if (durationMs > 0) setTimeout(() => dismiss(id), durationMs);
}

// Wraps an async mutation; surfaces failures as a toast and swallows the throw.
// Returns the resolved value on success, undefined on error.
export async function withToast<T>(fn: () => Promise<T>): Promise<T | undefined> {
  try {
    return await fn();
  } catch (e) {
    showError(e instanceof Error ? e.message : String(e));
    return undefined;
  }
}
