import type { BackendStatus, ServerView } from "./types";

/** Claims synchronously, including before a component has re-rendered its disabled control. */
export class ExclusiveActions {
  private pending = new Set<string>();

  async run(key: string, action: () => Promise<void>): Promise<boolean> {
    if (this.pending.has(key)) return false;
    this.pending.add(key);
    try {
      await action();
      return true;
    } finally {
      this.pending.delete(key);
    }
  }
}

// Survives screen remounts while an update is still reaching the gateway.
export const toolExposureActions = new ExclusiveActions();

/** A failed read after a committed exposure write must not undo the optimistic value. */
export async function persistExposure(
  save: () => Promise<void>,
  refresh: () => Promise<void>,
  rollback: () => void,
): Promise<void> {
  try {
    await save();
  } catch (err) {
    rollback();
    throw err;
  }
  await refresh();
}

export type ServerPrimaryAction = "sign-in" | "retry" | null;

/** Recovery stays in the row; everything else waits on the server's own screen. */
export function serverPrimaryAction(auth: ServerView["auth"], statusKind: BackendStatus["kind"]): ServerPrimaryAction {
  if (statusKind === "sign_in_required") return auth === "oauth" ? "sign-in" : "retry";
  if (statusKind === "failed") return "retry";
  if (statusKind === "stopped") return auth === "oauth" ? "sign-in" : "retry";
  return null;
}
