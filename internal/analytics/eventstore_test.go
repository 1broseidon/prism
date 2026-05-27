package analytics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/auth"
)

func TestEventStoreInsertQueryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.sqlite")
	store := openTestStore(t, path)
	defer store.Close()
	event := sampleEvent("r1", "allowed", "")
	if err := store.Insert(event); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sqlite file not created: %v", err)
	}
	got, err := store.Query(QueryFilter{AgentID: event.AgentID}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].RequestID != event.RequestID ||
		got[0].Trace.Context.Detail != event.Trace.Context.Detail ||
		got[0].AuthTime.UnixNano() != event.AuthTime.UnixNano() {
		t.Fatalf("round trip mismatch: %+v want %+v", got[0], event)
	}
}

func TestEventStoreQueryByAgentAndTimeRange(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "events.sqlite"))
	defer store.Close()
	old := sampleEvent("old", "allowed", "")
	old.Timestamp = time.Unix(100, 0)
	mid := sampleEvent("mid", "denied", "args")
	mid.Timestamp = time.Unix(200, 0)
	newer := sampleEvent("new", "allowed", "")
	newer.Timestamp = time.Unix(300, 0)
	for _, e := range []auth.GrantEvent{old, mid, newer} {
		if err := store.Insert(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Query(QueryFilter{
		AgentID: old.AgentID,
		Since:   time.Unix(150, 0),
		Until:   time.Unix(250, 0),
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestID != "mid" {
		t.Fatalf("query = %+v", got)
	}
}

func TestEventStoreAggregateByTemplate(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "events.sqlite"))
	defer store.Close()
	for _, e := range []auth.GrantEvent{
		sampleEvent("a", "allowed", ""),
		sampleEvent("b", "denied", "args"),
		sampleEvent("c", "challenged", "needs_step_up"),
	} {
		if err := store.Insert(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.AggregateByTemplate(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Allowed != 1 || got[0].Denied != 1 || got[0].Challenged != 1 || got[0].Total != 3 {
		t.Fatalf("aggregate = %+v", got)
	}
}

func TestEventStoreRetain(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "events.sqlite"))
	defer store.Close()
	old := sampleEvent("old", "allowed", "")
	old.Timestamp = time.Now().Add(-48 * time.Hour)
	newer := sampleEvent("new", "allowed", "")
	newer.Timestamp = time.Now()
	if err := store.Insert(old); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(newer); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.Retain(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	got, err := store.Query(QueryFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestID != "new" {
		t.Fatalf("remaining = %+v", got)
	}
}

func TestEventStoreStats(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "events.sqlite"))
	defer store.Close()
	first := sampleEvent("old", "allowed", "")
	first.Timestamp = time.Unix(100, 0)
	second := sampleEvent("new", "denied", auth.GrantDenyArgs)
	second.Timestamp = time.Unix(200, 0)
	if err := store.Insert(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(second); err != nil {
		t.Fatal(err)
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.EventCount != 2 {
		t.Fatalf("event count = %d, want 2", stats.EventCount)
	}
	if !stats.OldestAt.Equal(first.Timestamp) || !stats.NewestAt.Equal(second.Timestamp) {
		t.Fatalf("time bounds = %s/%s, want %s/%s", stats.OldestAt, stats.NewestAt, first.Timestamp, second.Timestamp)
	}
	if stats.SizeBytes <= 0 {
		t.Fatalf("size bytes = %d, want > 0", stats.SizeBytes)
	}
}

func TestEventStoreHealth(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "events.sqlite"))
	defer store.Close()

	now := time.Now()
	// Seven events in the last 24h: 3 allowed (1 with dpop_jkt+auth_time),
	// 3 denied (1 drift), 1 challenged. Plus one stale event outside the
	// window that must not contribute to any counter.
	mk := func(req, agent, outcome, deny, dpop, hash string, authAge, ts time.Duration) auth.GrantEvent {
		e := sampleEvent(req, outcome, deny)
		e.AgentID = agent
		e.DPoPjkt = dpop
		e.TemplateHash = hash
		e.Timestamp = now.Add(-ts)
		if authAge > 0 {
			e.AuthTime = now.Add(-authAge)
		} else {
			e.AuthTime = time.Time{}
		}
		return e
	}
	events := []auth.GrantEvent{
		mk("e1", "agent-a", "allowed", "", "jkt-a", "hash-1", 10*time.Second, 1*time.Hour),
		mk("e2", "agent-b", "allowed", "", "", "hash-1", 30*time.Second, 2*time.Hour),
		mk("e3", "agent-a", "allowed", "", "jkt-a", "hash-2", 50*time.Second, 3*time.Hour),
		mk("e4", "agent-c", "denied", auth.GrantDenyWorkspaceDrift, "jkt-c", "hash-2", 0, 4*time.Hour),
		mk("e5", "agent-d", "denied", "args", "", "hash-3", 90*time.Second, 5*time.Hour),
		mk("e6", "agent-e", "denied", "", "", "hash-3", 0, 6*time.Hour),
		mk("e7", "agent-a", "challenged", "needs_step_up", "jkt-a", "", 0, 7*time.Hour),
		mk("e8-stale", "agent-z", "denied", auth.GrantDenyWorkspaceDrift, "jkt-z", "hash-z", 0, 48*time.Hour),
	}
	for _, e := range events {
		if err := store.Insert(e); err != nil {
			t.Fatal(err)
		}
	}

	summary, err := store.Health(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Calls != 7 {
		t.Fatalf("calls = %d, want 7 (excluding stale)", summary.Calls)
	}
	if summary.Denials != 3 {
		t.Fatalf("denials = %d, want 3", summary.Denials)
	}
	if summary.DriftEvents != 1 {
		t.Fatalf("drift = %d, want 1 (stale drift excluded)", summary.DriftEvents)
	}
	// agent-a (jkt-a), agent-c (jkt-c). agent-b/d/e have empty jkt; agent-z
	// is outside the window.
	if summary.DPoPBoundAgents != 2 {
		t.Fatalf("dpop_bound_agents = %d, want 2", summary.DPoPBoundAgents)
	}
	// allowed/denied-with-non-empty-hash: hash-1, hash-2, hash-3. challenged
	// e7 has empty hash and challenged doesn't count anyway. hash-z is
	// outside window.
	if summary.ActiveTemplates != 3 {
		t.Fatalf("active_templates = %d, want 3", summary.ActiveTemplates)
	}
	// Median freshness: four events have non-zero auth_time (10s, 30s, 50s,
	// 90s). Median of (10, 30, 50, 90) = (30+50)/2 = 40s.
	if summary.MedianFreshnessSeconds < 35 || summary.MedianFreshnessSeconds > 45 {
		t.Fatalf("median_freshness_seconds = %d, want ~40", summary.MedianFreshnessSeconds)
	}
}

func TestEventStoreHealthEmpty(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "events.sqlite"))
	defer store.Close()
	summary, err := store.Health(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Calls != 0 || summary.Denials != 0 || summary.DriftEvents != 0 ||
		summary.DPoPBoundAgents != 0 || summary.ActiveTemplates != 0 {
		t.Fatalf("expected zero summary, got %+v", summary)
	}
	if summary.MedianFreshnessSeconds != -1 {
		t.Fatalf("median_freshness_seconds = %d, want -1 sentinel", summary.MedianFreshnessSeconds)
	}
}

func TestEventStoreMultiEmitterConcurrentRing(t *testing.T) {
	ring := NewRingBuffer(10_000)
	emitter := NewMultiEmitter(ring, nil, 1, nil)
	for i := 0; i < 10_000; i++ {
		emitter.Emit(context.Background(), auth.GrantEvent{RequestID: "r"})
	}
	if ring.Len() != 10_000 {
		t.Fatalf("ring len = %d", ring.Len())
	}
}

func openTestStore(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	store, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func sampleEvent(requestID, outcome, deny string) auth.GrantEvent {
	now := time.Unix(1_700_000_000, 123)
	return auth.GrantEvent{
		Timestamp:     now,
		RequestID:     requestID,
		AgentID:       "agent-a",
		ClientID:      "client-a",
		DPoPjkt:       "jkt-a",
		Backend:       "local",
		Tool:          "fs.write_file",
		CallArgsHash:  "hash-a",
		WorkspaceID:   "ws-a",
		WorkspaceType: "ephemeral",
		Outcome:       outcome,
		TemplateID:    "tmpl-a",
		TemplateHash:  "sha256-a",
		MatchedIndex:  0,
		Trace: auth.GrantTrace{
			Context: auth.AxisResult{Verdict: "fail", Detail: "predicate_failed", Layer: "grant"},
			DenyDim: deny,
			Drift:   &auth.DriftPair{GrantHash: "sha256-old", LiveHash: "sha256-new"},
		},
		AuthTime: now.Add(-time.Minute),
		Acr:      "urn:prism:mfa",
		TokenJTI: "token-jti",
	}
}
