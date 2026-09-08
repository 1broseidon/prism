import type { BackendStatus, ServerView } from "./types";

export type ServerPrimaryAction = "sign-in" | "retry" | null;

/** Keeps recovery in the row while routine maintenance stays behind Manage. */
export function serverPrimaryAction(auth: ServerView["auth"], statusKind: BackendStatus["kind"]): ServerPrimaryAction {
  if (statusKind === "sign_in_required") return auth === "oauth" ? "sign-in" : "retry";
  if (statusKind === "failed") return "retry";
  if (statusKind === "stopped") return auth === "oauth" ? "sign-in" : "retry";
  return null;
}

/** Opening another row replaces the current disclosure; activating the same row closes it. */
export function toggleServerDisclosure(current: string | null, serverId: string): string | null {
  return current === serverId ? null : serverId;
}

/** After removal, prefer the row that took its place, then the previous row, then Add server. */
export function serverFocusIndexAfterRemoval(removedIndex: number, remainingRows: number): number | null {
  if (remainingRows === 0) return null;
  return Math.min(removedIndex, remainingRows - 1);
}
