import type { UpdateServerArgs } from "./api";
import type { HttpAuth, ServerView } from "./types";

export interface ServerEditDraft {
  name: string;
  url: string;
  auth: HttpAuth;
  header: string;
  secret: string;
  command: string;
  args: string;
  argsTouched: boolean;
  env: string;
  clearEnv: boolean;
}

export function newServerEdit(server: ServerView): ServerEditDraft {
  return { name: server.name, url: server.url ?? "", auth: server.auth, header: "", secret: "",
    command: server.command, args: "", argsTouched: false, env: "", clearEnv: false };
}

export function changedServerOrigin(server: ServerView, draft: ServerEditDraft): boolean {
  if (!server.url) return false;
  try { return new URL(server.url).origin !== new URL(draft.url.trim()).origin; }
  catch { return true; }
}

/** Omission preserves a secret; an explicit empty value removes it. */
export function serverEditArgs(server: ServerView, draft: ServerEditDraft): UpdateServerArgs {
  const update: UpdateServerArgs = { name: draft.name.trim() };
  if (server.url !== null) {
    update.url = draft.url.trim();
    update.auth = draft.auth;
    if (draft.auth === "header") {
      const key = draft.secret.trim();
      if (!key && (server.auth !== "header" || changedServerOrigin(server, draft))) {
        throw new Error("Enter the API key for this server address.");
      }
      if (!key && draft.header.trim()) throw new Error("Enter the key again to change its header.");
      if (key) {
        const field = draft.header.trim() || "Authorization";
        update.headers = { [field]: field.toLowerCase() === "authorization" && !/^\S+\s/.test(key) ? `Bearer ${key}` : key };
      }
    }
  } else {
    update.command = draft.command.trim();
    if (draft.argsTouched) update.args = draft.args.trim() ? draft.args.trim().split(/\s+/) : [];
    if (draft.clearEnv) update.env = {};
    else if (draft.env.trim()) {
      update.env = {};
      for (const line of draft.env.split("\n")) {
        if (!line.trim()) continue;
        const eq = line.indexOf("=");
        const key = line.slice(0, eq).trim();
        if (eq <= 0 || !key || /[\s\0]/.test(key)) throw new Error("Use KEY=value on each environment line.");
        update.env[key] = line.slice(eq + 1).trim();
      }
    }
  }
  return update;
}
