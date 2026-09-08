import type { BackendStatus, ServerView } from "./types";

export type ServerPrimaryAction = "sign-in" | "retry" | null;

/** Recovery stays in the row; everything else waits on the server's own screen. */
export function serverPrimaryAction(auth: ServerView["auth"], statusKind: BackendStatus["kind"]): ServerPrimaryAction {
  if (statusKind === "sign_in_required") return auth === "oauth" ? "sign-in" : "retry";
  if (statusKind === "failed") return "retry";
  if (statusKind === "stopped") return auth === "oauth" ? "sign-in" : "retry";
  return null;
}
