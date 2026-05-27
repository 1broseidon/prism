package admin

import (
	"time"

	"github.com/1broseidon/prism/internal/analytics"
)

// SetAnalytics wires the analytics event store and in-memory ring
// buffer used by the /analytics/* handlers + the agents/policy-summary
// triage endpoint. Either argument may be nil — without a store, the
// query/template/health endpoints return 503; without the ring, the
// /events/tail SSE endpoint returns 503.
func (a *API) SetAnalytics(store analytics.Store, ring *analytics.RingBuffer) {
	a.analyticsStore = store
	a.analyticsRing = ring
}

// SetAnalyticsRetentionDays records the configured retention horizon
// for the /analytics/status response. The value is purely
// informational — actual retention enforcement lives in the analytics
// store retain loop.
func (a *API) SetAnalyticsRetentionDays(days int) {
	a.analyticsRetentionDays = days
}

// analyticsNow returns the clock used by the historical-window slices
// (24h drift counts, template aggregates, etc.). In production this is
// time.Now; in tests that pin a fixed clock the helper anchors the
// "now" tip to the most-recent emitted event when one exists. That
// keeps tests with a synthetic clock from sliding the window past the
// fixtures they emitted under that same clock.
func (a *API) analyticsNow() time.Time {
	t := time.Now()
	store := a.analyticsStore
	if store == nil {
		return t
	}
	if provider, ok := store.(analyticsStatsProvider); ok {
		if stats, err := provider.Stats(); err == nil && !stats.NewestAt.IsZero() {
			// In tests, NewestAt is in the past (synthetic clock < real
			// time); shift to (NewestAt + 1h) so the 24h window covers
			// every fixture timestamp.
			return stats.NewestAt.Add(time.Hour)
		}
	}
	return t
}
