import { makePolled } from "./polling";
import { getJSON } from "../api/client";
import { startMePolling } from "./me";
import type {
  Info,
  Agent,
  Backend,
  Group,
  DefaultsResponse,
  AuditEvent,
} from "../api/types";

export const info = makePolled(() => getJSON<Info>("/info"), 5000);
export const agents = makePolled(
  () => getJSON<Agent[]>("/agents"),
  5000,
);
export const backends = makePolled(
  () => getJSON<Backend[]>("/backends"),
  10000,
);
export const groups = makePolled(() => getJSON<Group[]>("/groups"), 5000);
export const defaults = makePolled(
  () => getJSON<DefaultsResponse>("/defaults"),
  5000,
);
export const events = makePolled(
  () => getJSON<AuditEvent[]>("/events"),
  3000,
);

export function startAllPolling(): () => void {
  const stops = [
    startMePolling(),
    info.start(),
    agents.start(),
    backends.start(),
    groups.start(),
    defaults.start(),
    events.start(),
  ];
  return () => stops.forEach((s) => s());
}
