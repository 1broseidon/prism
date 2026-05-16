import { signal } from "@preact/signals";

export interface Me {
  // "open" → admin auth disabled, full access
  // "session" → signed in
  // "required" → not signed in, must authenticate
  auth: "open" | "session" | "required";
  role?: "admin" | "viewer";
  email?: string;
  name?: string;
  issuer?: string;
  expires_at?: string;
}

export const me = signal<Me | null>(null);
const meError = signal<string | null>(null);

async function refresh(): Promise<void> {
  try {
    const res = await fetch("/auth/me");
    // 401 is the not-signed-in signal — still valid JSON, parse it.
    if (res.status === 401) {
      const body = (await res.json()) as Me;
      me.value = body;
      meError.value = null;
      return;
    }
    if (!res.ok) {
      throw new Error(`/auth/me returned ${res.status}`);
    }
    me.value = (await res.json()) as Me;
    meError.value = null;
  } catch (e) {
    meError.value = e instanceof Error ? e.message : String(e);
  }
}

export function startMePolling(): () => void {
  refresh();
  const id = setInterval(refresh, 60_000); // session info changes slowly
  return () => clearInterval(id);
}

export function refreshMe(): Promise<void> {
  return refresh();
}

// True when the user can mutate. Open mode grants full access; signed-in
// users need the admin role. Viewers (and not-yet-loaded state) cannot mutate.
export function canMutate(): boolean {
  const v = me.value;
  if (!v) return false;
  if (v.auth === "open") return true;
  if (v.auth === "session") return v.role === "admin";
  return false;
}
