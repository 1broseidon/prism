import { listen } from "@tauri-apps/api/event";
import * as api from "./api";
import {
  agents,
  audit,
  errorMessage,
  pending,
  rules,
  servers,
  status,
} from "./state";
import type { GatewayEvent } from "./types";

export async function loadAll(): Promise<void> {
  try {
    const [st, srv, ag, pend, ru, au] = await Promise.all([
      api.getStatus(),
      api.listServers(),
      api.listAgents(),
      api.listPending(),
      api.listRules(),
      api.listAudit(20),
    ]);
    status.value = st;
    servers.value = srv;
    agents.value = ag;
    pending.value = pend;
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
      case "agent_requested":
      case "agent_decided":
      case "agent_connected":
      case "agent_disconnected":
        agents.value = await api.listAgents();
        status.value = await api.getStatus();
        break;
      default:
        break;
    }
  });
  return unlisten;
}
