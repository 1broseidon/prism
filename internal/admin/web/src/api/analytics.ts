import { getJSON } from "./client";

export interface GrantTrace {
  deny_dim?: string;
  drift?: { grant_hash?: string; live_hash?: string };
  what?: { verdict?: string; detail?: string; layer?: string };
  context?: { verdict?: string; detail?: string; layer?: string };
  when?: { verdict?: string; detail?: string; layer?: string };
  how?: { verdict?: string; detail?: string; layer?: string };
}

export interface GrantEvent {
  Timestamp?: string;
  RequestID?: string;
  AgentID?: string;
  ClientID?: string;
  Backend?: string;
  Tool?: string;
  Outcome?: string;
  TemplateID?: string;
  TemplateHash?: string;
  MatchedIndex?: number;
  Trace?: GrantTrace;
  TokenJTI?: string;
}

export interface AgentGrantResolution {
  bindings?: Array<{ id: string; template_id: string; template_hash: string; via?: string }>;
  live_tokens?: Array<{ jti: string; jkt?: string; auth_time?: string; acr?: string; grant_count: number }>;
  recent_decisions?: GrantEvent[];
  drift_count_24h?: number;
  top_deny_dim_24h?: string;
}

export interface TemplateAggregate {
  template_id?: string;
  template_hash: string;
  version?: number;
  binding_count: number;
  agent_count: number;
  allow_24h: number;
  deny_24h: number;
  challenge_24h: number;
  drift_events_24h: number;
  active_token_count: number;
  last_fired_at?: string;
  top_deny_dims?: Array<{ dim: string; count: number }>;
}

export interface AnalyticsStatus {
  retention_days: number;
  ring_size: number;
  store_available: boolean;
  store?: {
    event_count: number;
    oldest_at?: string;
    newest_at?: string;
    size_bytes: number;
  };
}

export interface EventFilters {
  agent_id?: string;
  agent?: string;
  template_hash?: string;
  template?: string;
  outcome?: string;
  deny_dim?: string;
  backend?: string;
  subject?: string;
  since?: string;
  until?: string;
  limit?: number;
}

export function listGrantEvents(filters: EventFilters = {}) {
  const params = new URLSearchParams();
  Object.entries(filters).forEach(([key, value]) => {
    if (value !== undefined && value !== "") params.set(key, String(value));
  });
  const qs = params.toString();
  return getJSON<GrantEvent[]>(`/analytics/events${qs ? `?${qs}` : ""}`);
}

export function listTemplateAggregates(window = "24h") {
  return getJSON<TemplateAggregate[]>(`/analytics/templates?window=${encodeURIComponent(window)}`);
}

export function getTemplateAggregate(hash: string) {
  return getJSON<TemplateAggregate>(`/analytics/templates/${encodeURIComponent(hash)}`);
}

export function getAnalyticsStatus() {
  return getJSON<AnalyticsStatus>("/analytics/status");
}
