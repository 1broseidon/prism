import type { PendingSignIn, SignInChoice } from "./types";

/** A replacement always names one of the connections offered by this specific request. */
export function signinChoice(signin: PendingSignIn, selection = "add"): SignInChoice {
  if (!signin.suggested_group) return { kind: "add" };
  if (selection === "separate") return { kind: "separate" };
  if (selection.startsWith("replace:")) {
    const client_id = selection.slice(8);
    if (signin.suggested_group.connections.some(connection => connection.client_id === client_id)) {
      return { kind: "replace", client_id };
    }
    throw new Error("That connection is no longer available. Review this sign-in again.");
  }
  return { kind: "add" };
}

export function signinAction(signin: PendingSignIn, selection = "add"): string {
  if (!signin.suggested_group) return "Allow";
  if (selection === "separate") return "Create agent";
  if (selection.startsWith("replace:")) return "Replace connection";
  return "Add connection";
}
