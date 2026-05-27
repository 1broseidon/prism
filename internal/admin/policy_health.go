package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/analytics"
)

// PolicyHealth is the response shape for GET /api/v1/policy/health.
//
// Computed over the same 24h window with a single read transaction against
// the analytics store so the strip never shows a row of numbers from
// different points in time. Frontend (task-46) renders the 4 SecOps tiles —
// calls + trend, denials + top deny_dim, drift events, and permissions in
// force — and refreshes the whole struct every 30s.
//
// Field semantics:
//
//   - Calls24h:               total events with outcome ∈ {allowed, denied, challenged}
//   - DriftEvents24h:         events with deny_dim == "workspace_drift"
//   - Denials24h:             events with outcome == "denied"
//   - DenialRatePct24h:       100 * denials / max(calls, 1)
//   - PermissionsInForce:     composite — distinct (binding+template) +
//     scope-grant count across all subjects. Owned by
//     admin (joins the grant manager and agent
//     policies). Operators read this as "how many
//     permissions does Prism currently enforce?".
//   - Calls7dAvg:             rolling 7-day daily average call count, used
//     as the trend baseline behind Calls24h.
//   - TopDenyDim / Count:     the dominant deny_dim in the window plus its
//     count; empty string when no denials happened.
//
// Deprecated for UI rendering but kept on the wire for backwards-compat
// (task-46): MedianFreshnessSeconds, DPoPBoundAgents, ActiveTemplates.
// External consumers of GET /policy/health still receive them; the admin
// frontend simply stops rendering tiles for them.
type PolicyHealth struct {
	WindowSeconds      int       `json:"window_seconds"`
	GeneratedAt        time.Time `json:"generated_at"`
	Calls24h           int       `json:"calls_24h"`
	DriftEvents24h     int       `json:"drift_events_24h"`
	Denials24h         int       `json:"denials_24h"`
	DenialRatePct24h   float64   `json:"denial_rate_24h"`
	PermissionsInForce int       `json:"permissions_in_force"`
	Calls7dAvg         int       `json:"calls_7d_avg"`
	TopDenyDim         string    `json:"top_deny_dim,omitempty"`
	TopDenyDimCount    int       `json:"top_deny_dim_count,omitempty"`
	// Deprecated (kept for wire compatibility — UI no longer renders these).
	MedianFreshnessSeconds int64 `json:"median_freshness_seconds"`
	DPoPBoundAgents        int   `json:"dpop_bound_agents"`
	ActiveTemplates        int   `json:"active_templates"`
}

// healthProvider is the optional capability admin uses to read a single-shot
// aggregate from the event store. The production SQLite store implements it
// via Store.Health; tests that pass a different Store fall back to returning
// 503.
type healthProvider interface {
	Health(window time.Duration) (analytics.HealthSummary, error)
}

// handlePolicyHealth serves GET /policy/health. Returns 503 when the analytics
// store either isn't configured or doesn't implement the Health aggregate;
// returns 500 when the underlying query fails. Both error shapes match the
// frontend's "compact inline error + Retry" contract — the page keeps working.
func (a *API) handlePolicyHealth(w http.ResponseWriter, _ *http.Request) {
	if a.analyticsStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "analytics store not available"})
		return
	}
	provider, ok := a.analyticsStore.(healthProvider)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "analytics store does not support health aggregate"})
		return
	}
	const window = 24 * time.Hour
	summary, err := provider.Health(window)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := PolicyHealth{
		WindowSeconds:          int(window / time.Second),
		GeneratedAt:            time.Now().UTC(),
		Calls24h:               summary.Calls,
		DriftEvents24h:         summary.DriftEvents,
		Denials24h:             summary.Denials,
		Calls7dAvg:             summary.Calls7dAvg,
		TopDenyDim:             summary.TopDenyDim,
		TopDenyDimCount:        summary.TopDenyDimCount,
		PermissionsInForce:     a.permissionsInForce(),
		MedianFreshnessSeconds: summary.MedianFreshnessSeconds,
		DPoPBoundAgents:        summary.DPoPBoundAgents,
		ActiveTemplates:        summary.ActiveTemplates,
	}
	if summary.Calls > 0 {
		resp.DenialRatePct24h = float64(summary.Denials) / float64(summary.Calls) * 100
	}
	writeJSON(w, http.StatusOK, resp)
}

// permissionsInForce returns the composite count of permissions Prism is
// currently enforcing: the number of GrantBindings (each one attaches a
// template to a subject) plus the count of scope grants stored directly on
// agent policies. Group scopes are intentionally NOT counted as separate
// rows here — a group scope translates to N agents inheriting it, and
// counting per-agent would double-count the same authorization decision.
// We count each binding once even when it targets multiple subjects: that
// matches the operator's mental model ("Prism has N permissions configured")
// rather than "Prism makes N×M agent-permission pairs available".
//
// The count is computed cheaply (in-memory walks over the grant manager
// listing + an agent walk for direct grants) so it runs inline inside the
// /policy/health response. The handler tolerates a missing grantMgr or
// agentMgr by returning zero — the strip just shows 0 until those wire up.
func (a *API) permissionsInForce() int {
	count := 0
	if a.grantMgr != nil {
		count += len(a.grantMgr.ListGrantBindings())
	}
	if a.agentMgr != nil {
		reader, ok := a.agentMgr.(PolicyAgentReader)
		if ok {
			for _, prismID := range extractPrismIDs(a.agentMgr.ListAgents()) {
				policy, err := reader.GetAgentPolicy(prismID)
				if err != nil || policy == nil {
					continue
				}
				for _, g := range policy.Grant {
					// Role-membership entries live inline in Grant but
					// don't represent enforced scope authorizations.
					if strings.HasPrefix(g, "role:") {
						continue
					}
					count++
				}
			}
		}
	}
	return count
}
