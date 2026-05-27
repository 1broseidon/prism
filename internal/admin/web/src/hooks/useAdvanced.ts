// useAdvanced — operator-visible "show me the raw plumbing" toggle.
//
// This hook was previously named usePowerTools and lived behind the
// `prism.policy.power_tools` localStorage key. As part of the policy-refine
// rework (task-38) the user-facing name became "Advanced" and the storage
// key migrated to `prism.policy.advanced`. The migration is a one-shot copy
// performed at module init: if the legacy key is set and the new key is
// absent, we copy the value over and delete the legacy key forever. After
// that first run, the legacy key never resurfaces.
//
// State persists in localStorage (no server roundtrip per spec §11) and is
// shared across components via a @preact/signals signal. When the value
// flips, a window-level CustomEvent is dispatched so non-Preact code can
// react.
//
// Persistence key:   prism.policy.advanced       ("true" | "false")
// Legacy key:        prism.policy.power_tools    (migrated once, then deleted)
// Custom event name: prism:advanced-changed      (detail: { enabled: boolean })
//
// Default is OFF on fresh installs; previously-set values are honored on
// return visits.

import { signal } from "@preact/signals";

export const ADVANCED_STORAGE_KEY = "prism.policy.advanced";
export const ADVANCED_EVENT = "prism:advanced-changed";

// The legacy key we migrate from. Exported so a future cleanup audit can
// grep for any stragglers; not part of the public API.
const LEGACY_STORAGE_KEY = "prism.policy.power_tools";

function migrateLegacyKey(): void {
  // localStorage is absent during SSR and may throw in privacy modes —
  // either case is a no-op; the signal will read its default.
  if (typeof window === "undefined") return;
  try {
    const storage = window.localStorage;
    const existing = storage.getItem(ADVANCED_STORAGE_KEY);
    if (existing !== null) {
      // New key already present — clear the legacy key if it happens to be
      // there too so we don't carry forward two competing values.
      storage.removeItem(LEGACY_STORAGE_KEY);
      return;
    }
    const legacy = storage.getItem(LEGACY_STORAGE_KEY);
    if (legacy === null) return;
    storage.setItem(ADVANCED_STORAGE_KEY, legacy);
    storage.removeItem(LEGACY_STORAGE_KEY);
  } catch {
    // ignore — migration is best-effort.
  }
}

migrateLegacyKey();

function readInitial(): boolean {
  // Server-side rendering safety — localStorage is absent during SSR.
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(ADVANCED_STORAGE_KEY) === "true";
  } catch {
    // localStorage can throw in privacy modes or sandboxed iframes; treat
    // those as "default off" rather than crash the whole admin shell.
    return false;
  }
}

// One module-scoped signal so every consumer sees the same value without
// needing a Provider — keeps the API a plain hook.
const advancedSignal = signal<boolean>(readInitial());

// Listen for cross-tab toggles. The browser fires `storage` only in tabs
// that did NOT make the write; the toggling tab updates the signal itself
// via setAdvanced below.
if (typeof window !== "undefined") {
  window.addEventListener("storage", (e) => {
    if (e.key !== ADVANCED_STORAGE_KEY) return;
    advancedSignal.value = e.newValue === "true";
  });
}

export function getAdvanced(): boolean {
  return advancedSignal.value;
}

export function setAdvanced(next: boolean): void {
  const prev = advancedSignal.value;
  advancedSignal.value = next;
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(ADVANCED_STORAGE_KEY, next ? "true" : "false");
  } catch {
    // ignore — signal still reflects the requested value for this session
  }
  if (prev !== next) {
    window.dispatchEvent(
      new CustomEvent(ADVANCED_EVENT, { detail: { enabled: next } }),
    );
  }
}

export function toggleAdvanced(): void {
  setAdvanced(!advancedSignal.value);
}

// useAdvanced is a tiny hook so components don't need to import the signal
// directly. Preact's signal proxy makes the .value read participate in the
// component's reactive subscription automatically.
export function useAdvanced(): boolean {
  return advancedSignal.value;
}
