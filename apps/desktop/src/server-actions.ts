import type { BackendStatus, ServerView } from "./types";

export type ServerPrimaryAction = "sign-in" | "retry" | null;

/** Keeps recovery in the row while routine maintenance stays behind Manage. */
export function serverPrimaryAction(auth: ServerView["auth"], statusKind: BackendStatus["kind"]): ServerPrimaryAction {
  if (statusKind === "sign_in_required") return "sign-in";
  if (statusKind === "failed") return "retry";
  if (statusKind === "stopped") return auth === "oauth" ? "sign-in" : "retry";
  return null;
}
