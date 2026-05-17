import type { Agent } from "../api/types";

const CLIENT_ROUTE_PREFIX = "client:";

export function agentDetailHref(agent: Agent): string | null {
  if (!agent.dynamic) return null;
  const id = agent.prism_id || `${CLIENT_ROUTE_PREFIX}${agent.client_id}`;
  return `/identity/agents/${encodeURIComponent(id)}`;
}

export function decodeAgentRouteID(value: string | undefined): string {
  if (!value) return "";
  if (!value.includes("%")) return value;
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

export function findAgentForRoute(
  list: Agent[],
  routeID: string,
): Agent | undefined {
  if (routeID.startsWith(CLIENT_ROUTE_PREFIX)) {
    const clientID = routeID.slice(CLIENT_ROUTE_PREFIX.length);
    return list.find((a) => a.client_id === clientID);
  }
  return list.find((a) => a.prism_id === routeID);
}
