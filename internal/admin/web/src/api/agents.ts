// Agents API helpers — typed wrappers over /agents and /agents/policy-summary.
//
// The shared Agent shape lives in api/types.ts because it predates this file
// and is consumed by half the console; this module re-exports it for one-stop
// imports and adds the task-44 triage summary types + fetcher.
//
// The policy-summary endpoint is a sibling of /agents (not an inlined field)
// because the existing /agents handler is a 3-line passthrough over an
// intentionally type-erased `[]any` agent source. Folding capability counts +
// analytics queries into that hot 5-second poll would couple the listing's
// refresh cadence to expensive SQL. See agents_policy_summary.go for the
// rationale + the 60s server-side cache.

import { getJSON } from "./client";
import type { Agent } from "./types";

export type { Agent };

// AgentPolicySummary mirrors admin.AgentPolicySummary in Go. Optional fields
// are absent from the JSON when zero/empty (the handler uses `omitempty`),
// so the page-side renderers read them as `?? 0` / `?? null`.
export interface AgentPolicySummary {
  prism_id: string;
  capabilities_count: number;
  // ISO-8601 timestamp; absent when the agent has no recorded denials.
  last_denial_at?: string;
  // Last denial's deny_dim classification (workspace_drift, args, etc.).
  // May be absent even when last_denial_at is set — older rows or scope-
  // shape denials don't carry a dimension.
  last_denial_dim?: string;
  // Count of workspace_drift denials in the last 24h. Absent when zero.
  drift_count_24h?: number;
}

// listAgentPolicySummaries fetches the per-agent triage rollup used by the
// Agents listing's Capabilities / Last denial / Drift columns. The endpoint
// is server-cached for 60 seconds and batched (constant SQL round-trips
// regardless of agent count), so the page can call this on every navigation
// without worrying about N+1.
export function listAgentPolicySummaries(): Promise<AgentPolicySummary[]> {
  return getJSON<AgentPolicySummary[]>("/agents/policy-summary");
}
