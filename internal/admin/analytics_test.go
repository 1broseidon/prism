package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/analytics"
	"github.com/1broseidon/prism/internal/auth"
)

type fakeAnalyticsStore struct {
	events []auth.GrantEvent
}

func (s *fakeAnalyticsStore) Insert(e auth.GrantEvent) error {
	s.events = append(s.events, e)
	return nil
}

func (s *fakeAnalyticsStore) Query(filter analytics.QueryFilter, limit int) ([]auth.GrantEvent, error) {
	if filter.Limit > 0 {
		limit = filter.Limit
	}
	if limit <= 0 {
		limit = 1000
	}
	out := make([]auth.GrantEvent, 0)
	for _, event := range s.events {
		if filter.AgentID != "" && event.AgentID != filter.AgentID {
			continue
		}
		if len(filter.AgentIDs) > 0 && !containsString(filter.AgentIDs, event.AgentID) {
			continue
		}
		if filter.TemplateHash != "" && event.TemplateHash != filter.TemplateHash {
			continue
		}
		if len(filter.TemplateHashes) > 0 && !containsString(filter.TemplateHashes, event.TemplateHash) {
			continue
		}
		if filter.Outcome != "" && event.Outcome != filter.Outcome {
			continue
		}
		if filter.DenyDim != "" && event.Trace.DenyDim != filter.DenyDim {
			continue
		}
		if filter.Backend != "" && event.Backend != filter.Backend {
			continue
		}
		if !filter.Since.IsZero() && event.Timestamp.Before(filter.Since) {
			continue
		}
		if !filter.Until.IsZero() && event.Timestamp.After(filter.Until) {
			continue
		}
		out = append(out, event)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *fakeAnalyticsStore) AggregateByTemplate(_ time.Duration) ([]analytics.TemplateAggregate, error) {
	return nil, nil
}

func (s *fakeAnalyticsStore) Health(_ time.Duration) (analytics.HealthSummary, error) {
	return analytics.HealthSummary{MedianFreshnessSeconds: -1}, nil
}

func (s *fakeAnalyticsStore) Retain(_ time.Duration) (int, error) {
	return 0, nil
}

func (s *fakeAnalyticsStore) AgentPolicySummaries(_ []string, _ time.Duration) (map[string]analytics.AgentTriageSummary, error) {
	return nil, nil
}

type fakeStatsStore struct {
	*fakeAnalyticsStore
	stats analytics.StoreStats
}

func (s *fakeStatsStore) Stats() (analytics.StoreStats, error) {
	return s.stats, nil
}

func TestAnalyticsAgentResolutionIncludesGrantsAndRecentEvents(t *testing.T) {
	api, mgr := newTestGrantAPI(t)
	template := auth.GrantTemplate{ID: "tpl", Version: 1, Spec: auth.GrantSpec{Type: auth.GrantTypeMCPCall, Tool: "fs.write_file", Backend: "local"}}
	template, err := mgr.SaveGrantTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeAnalyticsStore{events: []auth.GrantEvent{
		sampleGrantEvent("old", "prism-uuid-1", "allowed", "", template.Hash, time.Now().Add(-time.Hour)),
		sampleGrantEvent("new", "prism-uuid-1", "denied", auth.GrantDenyWorkspaceDrift, template.Hash, time.Now()),
		sampleGrantEvent("other", "prism-uuid-2", "allowed", "", template.Hash, time.Now()),
	}}
	api.SetAnalytics(store, nil)
	if _, err := mgr.SetGrantBinding(auth.GrantBinding{ID: "bind", TemplateID: "tpl", TemplateHash: template.Hash, Subjects: auth.SubjectSelector{AgentIDs: []string{"prism-uuid-1"}}}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/prism-uuid-1", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Grant AgentGrantResolution `json:"grant_resolution"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Grant.Bindings) != 1 || len(body.Grant.RecentDecisions) != 2 || body.Grant.DriftCount24h != 1 {
		t.Fatalf("grant resolution = %+v", body.Grant)
	}
	if body.Grant.RecentDecisions[0].RequestID != "new" {
		t.Fatalf("recent decisions not newest first: %+v", body.Grant.RecentDecisions)
	}
}

func TestAnalyticsStatusIncludesRetentionAndUsage(t *testing.T) {
	api, _ := newTestAPI()
	ring := analytics.NewRingBuffer(10)
	ring.Add(sampleGrantEvent("a", "agent-a", "allowed", "", "hash-a", time.Now()))
	store := &fakeStatsStore{
		fakeAnalyticsStore: &fakeAnalyticsStore{},
		stats: analytics.StoreStats{
			EventCount: 12,
			SizeBytes:  4096,
			OldestAt:   time.Unix(100, 0).UTC(),
			NewestAt:   time.Unix(200, 0).UTC(),
		},
	}
	api.SetAnalytics(store, ring)
	api.SetAnalyticsRetentionDays(14)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/status", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body AnalyticsStatus
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.RetentionDays != 14 || body.RingSize != 1 || !body.StoreAvailable {
		t.Fatalf("status body = %+v", body)
	}
	if body.Store == nil || body.Store.EventCount != 12 || body.Store.SizeBytes != 4096 {
		t.Fatalf("store stats = %+v", body.Store)
	}
}

func TestAnalyticsEventsFilters(t *testing.T) {
	api, _ := newTestAPI()
	api.SetAnalytics(&fakeAnalyticsStore{events: []auth.GrantEvent{
		sampleGrantEvent("a", "agent-a", "allowed", "", "hash-a", time.Now().Add(-2*time.Hour)),
		sampleGrantEvent("b", "agent-a", "denied", auth.GrantDenyArgs, "hash-b", time.Now().Add(-time.Hour)),
		sampleGrantEvent("c", "agent-b", "denied", auth.GrantDenyArgs, "hash-b", time.Now()),
	}}, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/events?agent_id=agent-a&outcome=denied&deny_dim=args&limit=10", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var events []auth.GrantEvent
	if err := json.NewDecoder(w.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].RequestID != "b" {
		t.Fatalf("events = %+v", events)
	}
}

func TestAnalyticsSSETail(t *testing.T) {
	api, _ := newTestAPI()
	ring := analytics.NewRingBuffer(10)
	ring.Add(sampleGrantEvent("a", "agent-a", "allowed", "", "hash-a", time.Now()))
	ring.Add(sampleGrantEvent("b", "agent-a", "denied", auth.GrantDenyArgs, "hash-a", time.Now()))
	api.SetAnalytics(nil, ring)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/events/tail", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"RequestID":"a"`) || !strings.Contains(body, `"RequestID":"b"`) {
		t.Fatalf("tail body = %s", body)
	}
}

func TestCoverageAggregates(t *testing.T) {
	api, mgr := newTestGrantAPI(t)
	events := make([]auth.GrantEvent, 0, 100)
	for i := 0; i < 100; i++ {
		hash := "hash-a"
		outcome := "allowed"
		deny := ""
		if i%3 == 1 {
			hash = "hash-b"
			outcome = "denied"
			deny = auth.GrantDenyArgs
		}
		if i%3 == 2 {
			hash = "hash-c"
			outcome = "denied"
			deny = auth.GrantDenyWorkspaceDrift
		}
		events = append(events, sampleGrantEvent(string(rune('a'+(i%26))), "agent", outcome, deny, hash, time.Now()))
	}
	api.SetAnalytics(&fakeAnalyticsStore{events: events}, nil)
	for _, hash := range []string{"hash-a", "hash-b", "hash-c"} {
		_, _ = mgr.SetGrantBinding(auth.GrantBinding{ID: "bind-" + hash, TemplateID: "tpl", TemplateHash: hash, Subjects: auth.SubjectSelector{AgentIDs: []string{"agent"}}})
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/templates?window=24h", nil)
	api.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var aggs []AdminTemplateAggregate
	if err := json.NewDecoder(w.Body).Decode(&aggs); err != nil {
		t.Fatal(err)
	}
	if len(aggs) != 3 {
		t.Fatalf("aggregates = %+v", aggs)
	}
	var drift int
	for _, agg := range aggs {
		drift += agg.DriftEvents24h
		if agg.TemplateHash == "hash-a" && agg.ActiveTokenCount == 0 {
			t.Fatalf("expected active token count for hash-a: %+v", agg)
		}
	}
	if drift == 0 {
		t.Fatalf("expected drift events: %+v", aggs)
	}
}

func sampleGrantEvent(requestID, agentID, outcome, deny, hash string, ts time.Time) auth.GrantEvent {
	return auth.GrantEvent{
		Timestamp:    ts,
		RequestID:    requestID,
		AgentID:      agentID,
		ClientID:     agentID,
		Backend:      "local",
		Tool:         "fs.write_file",
		Outcome:      outcome,
		TemplateID:   "tpl",
		TemplateHash: hash,
		TokenJTI:     "token-" + hash,
		Trace:        auth.GrantTrace{DenyDim: deny},
	}
}

var _ analytics.Store = (*fakeAnalyticsStore)(nil)
