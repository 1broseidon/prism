import { listen } from "@tauri-apps/api/event";
import * as api from "./api";
import {
  agents,
  audit,
  errorMessage,
  pending,
  rules,
  servers,
  signins,
  status,
} from "./state";
import type { GatewayEvent } from "./types";

export async function loadAll(): Promise<void> {
  try {
    const [st, srv, ag, pend, si, ru, au] = await Promise.all([
      api.getStatus(),
      api.listServers(),
      api.listAgents(),
      api.listPending(),
      api.listSignins(),
      api.listRules(),
      api.listAudit(20),
    ]);
    status.value = st;
    servers.value = srv;
    agents.value = ag;
    pending.value = pend;
    signins.value = si.filter((s) => s.needs_consent);
    rules.value = ru;
    audit.value = au;
    errorMessage.value = null;
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err);
  }
}

export async function subscribeEvents(): Promise<() => void> {
  if (!("__TAURI_INTERNALS__" in window)) return () => {};
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
        pending.value = pending.value.filter((p) => p.id !== payload.data.id);
        rules.value = await api.listRules();
        status.value = await api.getStatus();
        break;
      case "audit":
        audit.value = [payload.data, ...audit.value].slice(0, 20);
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
        break;
      default:
        break;
    }
  });
  return unlisten;
}
