/** Pure lifecycle helpers shared by the UI state and its regression tests. */

export type PanelTransition = "dismiss" | "reopen" | "refresh";

/** Repeated visibility events refresh data but must not disturb the visible interaction. */
export function panelTransition(previous: boolean | null, visible: boolean): PanelTransition {
  if (previous === visible) return "refresh";
  return visible ? "reopen" : "dismiss";
}

/** Keep the last recoverable navigation until it is resumed or explicitly discarded. */
export function preserveRecoverable<T>(current: T[], saved: T[] | null, recoverable: boolean): T[] | null {
  return recoverable && current.length > 0 ? current.slice() : saved;
}

/** One-time values are replaced by key in volatile memory and removed only explicitly. */
export function rememberVolatile<T>(values: Record<string, T>, key: string, value: T): Record<string, T> {
  return { ...values, [key]: value };
}

export function discardVolatile<T>(values: Record<string, T>, key: string): Record<string, T> {
  const next = { ...values };
  delete next[key];
  return next;
}

export interface QueueSelection {
  index: number;
  key: string | null;
}

/** Keep the visible identity when possible; after removal, stay at its former bounded position. */
export function reconcileQueue(keys: string[], cursor: string | null, previousIndex: number): QueueSelection {
  const cursorIndex = cursor === null ? -1 : keys.indexOf(cursor);
  const index = cursorIndex >= 0
    ? cursorIndex
    : Math.min(Math.max(0, previousIndex), Math.max(0, keys.length - 1));
  return { index, key: keys[index] ?? null };
}
