package admin

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/1broseidon/prism/internal/analytics"
)

// AgentPolicySummary is the per-agent triage slice returned by
// GET /api/v1/agents/policy-summary. The Agents listing (Members tab) joins
// this onto the polled `/agents` payload to render the three triage columns
// (Capabilities count, Last denial, Drift 24h).
//
// Endpoint shape choice (Option B from the contract):
//   - The existing GET /agents handler is a 3-line passthrough over an
//     intentionally type-erased `func() []any` source. Re-typing that
//     pipeline to optionally inline policy_summary would couple capability
//     composition and analytics aggregates into the 5-second poll hot path.
//   - A sibling endpoint keeps /agents fast and lets this expensive read
//     stay 60s-cached behind a separate fetch the frontend calls on view.
//
// The response is keyed by prism_id; agents without a prism_id (pre-DCR
// pending-consent rows) are omitted — there's nothing meaningful to compute
// for them yet.
type AgentPolicySummary struct {
	PrismID string `json:"prism_id"`
	// CapabilitiesCount is the length of composeAgentCapabilityViews —
	// every distinct capability (direct + group + role inheritance) the
	// agent currently holds. Zero is highlighted in the UI as an unusual
	// state worth flagging (an agent with no capabilities can't do
	// anything; usually means it was registered but never granted).
	CapabilitiesCount int `json:"capabilities_count"`
	// LastDenialAt is the timestamp of the agent's most recent denied
	// grant event. Omitted (zero value, omitempty) when there are no
	// denials in the store for this agent.
	LastDenialAt time.Time `json:"last_denial_at,omitempty"`
	// LastDenialDim is the deny_dim of that row — useful as an at-a-glance
	// hint without round-tripping the full event. May be empty even when
	// LastDenialAt is set (older rows or scope-shape denials don't carry
	// a dimension classification).
	LastDenialDim string `json:"last_denial_dim,omitempty"`
	// DriftCount24h is the count of workspace_drift denials in the last
	// 24h. Zero is omitted from JSON so the UI can lean on
	// `summary?.drift_count_24h ?? 0` cleanly.
	DriftCount24h int `json:"drift_count_24h,omitempty"`
}

// agentPolicySummaryCache is the 60-second cache backing
// GET /agents/policy-summary. Cache shape and lifetime:
//
//   - TTL: 60s — fresh enough for operator triage, infrequent enough that
//     the capability-views + analytics roundtrip doesn't run on every
//     poll tick (5s for /agents).
//   - Per-agent map keying for O(1) lookup of warm entries; mutations
//     evict the entire map via invalidateAll because most mutation paths
//     (template/binding edits, group changes, role rewires) can affect
//     any subject's cap count and figuring out the impacted set is more
//     expensive than just recomputing on the next read.
//   - Singleflight-by-batch: the handler computes summaries for any
//     missing/expired agents in a single batched analytics call (see
//     Store.AgentPolicySummaries) so even a cold-cache fetch is two
//     SQL round-trips total regardless of agent count.
//
// Concurrency: a single sync.Mutex guards the map. Cache lookups and
// inserts are O(1); the per-call overhead is dominated by the batched
// analytics query, not the cache operations.
type agentPolicySummaryCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]agentPolicySummaryEntry
}

type agentPolicySummaryEntry struct {
	summary  AgentPolicySummary
	expireAt time.Time
}

func newAgentPolicySummaryCache(ttl time.Duration) *agentPolicySummaryCache {
	return &agentPolicySummaryCache{
		ttl:     ttl,
		entries: make(map[string]agentPolicySummaryEntry),
	}
}

// get returns the cached summary if fresh; ok==false means the caller
// should recompute.
func (c *agentPolicySummaryCache) get(prismID string, now time.Time) (AgentPolicySummary, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[prismID]
	if !ok || now.After(e.expireAt) {
		return AgentPolicySummary{}, false
	}
	return e.summary, true
}

func (c *agentPolicySummaryCache) put(s AgentPolicySummary, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[s.PrismID] = agentPolicySummaryEntry{
		summary:  s,
		expireAt: now.Add(c.ttl),
	}
}

// invalidateAll clears the cache. Most admin mutations can transitively
// affect any subject's capability count (template/binding edits, role
// rewires, group membership shifts, agent policy edits that toggle group
// membership, etc.), so the simplest correct policy is to drop the whole
// map on any mutation. The next listing request rebuilds in two SQL reads.
func (c *agentPolicySummaryCache) invalidateAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]agentPolicySummaryEntry)
}

// invalidateAllAgentPolicySummaries is the entry point for any mutation that
// could affect a cached summary. Safe to call when the cache is nil — e.g.,
// in tests that don't exercise the listing. Called from agent-policy edits,
// agent deletion, stale-agent purge, and grant template/binding writes (any
// of which can shift the inherited capability count for some set of agents).
func (a *API) invalidateAllAgentPolicySummaries() {
	if a == nil || a.policySummaryCache == nil {
		return
	}
	a.policySummaryCache.invalidateAll()
}

// handleAgentsPolicySummary serves GET /api/v1/agents/policy-summary —
// returns an array of AgentPolicySummary, one per registered agent that has
// a prism_id. The agents-listing page joins this against its polled
// `/agents` list to render the Capabilities / Last denial / Drift 24h
// columns.
//
// Behavior:
//   - Reads the agent list once via agentsFn, extracts prism_ids via JSON
//     roundtrip (the source returns []any to keep the admin package
//     independent of authserver types).
//   - Skips agents whose prism_id is empty (pre-DCR pending-consent rows).
//   - Serves cached entries straight from the per-agent cache; for any
//     missing/expired agents computes a fresh batch in one analytics call
//     (two SQL reads) + one capability-view composition per agent.
//   - Returns 200 even when the analytics store is unwired (cap-count-only
//     mode); the frontend treats missing denial/drift fields as zero.
func (a *API) handleAgentsPolicySummary(w http.ResponseWriter, _ *http.Request) {
	if a.agentsFn == nil {
		writeJSON(w, http.StatusOK, []AgentPolicySummary{})
		return
	}
	prismIDs := extractPrismIDs(a.agentsFn())
	if len(prismIDs) == 0 {
		writeJSON(w, http.StatusOK, []AgentPolicySummary{})
		return
	}
	if a.policySummaryCache == nil {
		// Lazy init — keeps the constructor signature stable. The cache
		// has no external resources so it's safe to instantiate on
		// first use.
		a.policySummaryCache = newAgentPolicySummaryCache(60 * time.Second)
	}
	now := time.Now()

	// Sort prism_ids for deterministic response order; the page sorts by
	// name client-side but a stable backend order makes the response
	// snapshot-testable and avoids reshuffles between cache states.
	sort.Strings(prismIDs)

	results := make([]AgentPolicySummary, 0, len(prismIDs))
	missing := make([]string, 0)
	for _, id := range prismIDs {
		if cached, ok := a.policySummaryCache.get(id, now); ok {
			results = append(results, cached)
			continue
		}
		missing = append(missing, id)
	}

	if len(missing) > 0 {
		fresh := a.computeAgentPolicySummaries(missing)
		for _, s := range fresh {
			a.policySummaryCache.put(s, now)
			results = append(results, s)
		}
		// Re-sort because the missing batch was appended at the end.
		sort.Slice(results, func(i, j int) bool {
			return results[i].PrismID < results[j].PrismID
		})
	}

	writeJSON(w, http.StatusOK, results)
}

// computeAgentPolicySummaries builds summaries for the supplied prism_ids in
// the minimum number of round trips:
//
//   - One batched analytics call (which itself is 2 SQL reads — see
//     SQLiteStore.AgentPolicySummaries) covers last-denial + drift for all
//     agents.
//   - composeAgentCapabilityViews runs per-agent because it joins
//     KV-backed grant bindings (already in-memory in grantMgr) with the
//     agent's policy. There's no SQL involved — the per-agent loop is
//     in-process map work.
//
// Total wire-cost: 2 SQL reads + N in-memory composes, irrespective of how
// many agents are passed.
func (a *API) computeAgentPolicySummaries(prismIDs []string) []AgentPolicySummary {
	if len(prismIDs) == 0 {
		return nil
	}
	var triage map[string]analytics.AgentTriageSummary
	if a.analyticsStore != nil {
		if t, err := a.analyticsStore.AgentPolicySummaries(prismIDs, 24*time.Hour); err == nil {
			triage = t
		}
	}
	out := make([]AgentPolicySummary, 0, len(prismIDs))
	for _, id := range prismIDs {
		summary := AgentPolicySummary{PrismID: id}
		// Capability count — defensive nil-check so an unwired grant
		// manager surfaces as zero, not a panic.
		if a.grantMgr != nil && a.agentMgr != nil {
			if views, err := a.composeAgentCapabilityViews(id); err == nil {
				summary.CapabilitiesCount = len(views)
			}
		}
		if t, ok := triage[id]; ok {
			summary.LastDenialAt = t.LastDenialAt
			summary.LastDenialDim = t.LastDenialDim
			summary.DriftCount24h = t.DriftCount24h
		}
		out = append(out, summary)
	}
	return out
}

// extractPrismIDs pulls the prism_id off each element of agentsFn()'s
// result. The source is `[]any` to keep the admin package independent of
// authserver types; we JSON-roundtrip the slice into a minimal shape that
// only carries the field we need.
//
// Items without a non-empty prism_id are skipped — typically the
// pending-consent rows where DCR hasn't completed yet. Duplicates (should
// not happen but defensive) are removed via the seen-set.
func extractPrismIDs(agents []any) []string {
	if len(agents) == 0 {
		return nil
	}
	data, err := json.Marshal(agents)
	if err != nil {
		return nil
	}
	var minimal []struct {
		PrismID string `json:"prism_id"`
	}
	if err := json.Unmarshal(data, &minimal); err != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(minimal))
	out := make([]string, 0, len(minimal))
	for _, m := range minimal {
		if m.PrismID == "" {
			continue
		}
		if _, dup := seen[m.PrismID]; dup {
			continue
		}
		seen[m.PrismID] = struct{}{}
		out = append(out, m.PrismID)
	}
	return out
}
