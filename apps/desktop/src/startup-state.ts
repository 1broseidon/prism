import type { StartupStatus } from "./types";

/** A failed IPC write has an unknown outcome; re-read the OS instead of rolling back. */
export async function changeStartup(
  enabled: boolean,
  write: (enabled: boolean) => Promise<StartupStatus>,
  read: () => Promise<StartupStatus>,
): Promise<StartupStatus> {
  try { return await write(enabled); }
  catch {
    try { return { ...await read(), error: "The change could not be confirmed. Showing the current OS state." }; }
    catch { return { enabled: null, needs_repair: false, can_enable: false, error: "Startup state is unavailable. Check OS startup settings and retry." }; }
  }
}
