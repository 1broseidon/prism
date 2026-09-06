import { listen } from "@tauri-apps/api/event";
import * as api from "./api";
import {
  activity,
  activityError,
  agents,
  audit,
  errorMessage,
  native,
  pending,
  rules,
  servers,
  signins,
  status,
  update,
  updateProgress,
} from "./state";
import type { GatewayEvent, UpdateEvent } from "./types";

export async function loadAll(): Promise<void> {
  try {
    const [st, srv, ag, pend, si, ru] = await Promise.all([
      api.getStatus(),
      api.listServers(),
      api.listAgents(),
      api.listPending(),
      api.listSignins(),
      api.listRules(),
    ]);
    status.value = st;
    servers.value = srv;
    agents.value = ag;
    pending.value = pend;
    signins.value = si.filter((s) => s.needs_consent);
    rules.value = ru;
    void api.listAudit(60).then(entries => { audit.value = entries; }).catch(() => {});
    errorMessage.value = null;
    loadNativeStatus();
    loadActivity();
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err);
  }
}

let nativeTimer: ReturnType<typeof setTimeout> | null = null;

/** Native events can arrive many times a second; one refresh every couple of seconds is plenty. */
export function loadNativeStatus(debounce = false): void {
  if (debounce) {
    if (nativeTimer) return;
    nativeTimer = setTimeout(() => {
      nativeTimer = null;
      loadNativeStatus();
    }, 2000);
    return;
  }
  api.getNativeStatus().then((st) => (native.value = st)).catch(err => {
    native.value = null;
    errorMessage.value = err instanceof Error ? err.message : String(err);
  });
}

let activityTimer: ReturnType<typeof setTimeout> | null = null;

/** The summary on the Now tab. Refreshed a beat after any audit entry, never more than once a second. */
export function loadActivity(debounce = false): void {
  if (debounce) {
    if (activityTimer) return;
    activityTimer = setTimeout(() => {
      activityTimer = null;
      loadActivity();
    }, 1000);
    return;
  }
  api.getActivity().then(summary => { activity.value = summary; activityError.value = null; }).catch(err => {
    activity.value = null;
    activityError.value = err instanceof Error ? err.message : String(err);
  });
}

export async function loadUpdateStatus(): Promise<void> {
  try {
    const st = await api.getUpdateStatus();
    update.value = st.available;
  } catch {
    // The updater is optional; a missing command in dev is not an error worth showing.
  }
}

export async function subscribeEvents(): Promise<() => void> {
  if (!("__TAURI_INTERNALS__" in window)) return () => {};
  const unlistenUpdate = await listen<UpdateEvent>("prism://update", (event) => {
    const p = event.payload;
    switch (p.state) {
      case "available":
        update.value = { version: p.version, current: p.current, notes: p.notes, date: p.date, installable: p.installable };
        updateProgress.value = null;
        break;
      case "up_to_date":
        update.value = null;
        updateProgress.value = null;
        break;
      default:
        updateProgress.value = p;
        break;
    }
  });
  const unlisten = await listen<GatewayEvent>("prism://event", async (event) => {
    const payload = event.payload;
    switch (payload.type) {
      case "pending_call":
        pending.value = [
          payload.data,
          ...pending.value.filter((p) => p.id !== payload.data.id),
        ];
        status.value = await api.getStatus();
        break;
      case "call_decided":
      case "call_cancelled":
        pending.value = pending.value.filter((p) => p.id !== payload.data.id);
        rules.value = await api.listRules();
        status.value = await api.getStatus();
        break;
      case "audit":
        audit.value = [payload.data, ...audit.value].slice(0, 60);
        if (payload.data.native) loadNativeStatus(true);
        loadActivity(true);
        break;
      case "rules_changed":
        rules.value = await api.listRules();
        break;
      case "server_status":
        servers.value = await api.listServers();
        status.value = await api.getStatus();
        break;
      case "sign_in_requested":
      case "sign_in_decided":
        signins.value = (await api.listSignins()).filter((s) => s.needs_consent);
        status.value = await api.getStatus();
        break;
      case "agent_requested":
      case "agent_decided":
      case "agent_connected":
      case "agent_disconnected":
      case "agent_updated":
        agents.value = await api.listAgents();
        status.value = await api.getStatus();
        break;
      case "settings_changed":
        status.value = await api.getStatus();
        loadNativeStatus();
        break;
      default:
        break;
    }
  });
  return () => {
    unlisten();
    unlistenUpdate();
  };
}
