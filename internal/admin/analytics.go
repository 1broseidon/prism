package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/analytics"
	"github.com/1broseidon/prism/internal/auth"
)

type AgentGrantResolution struct {
	Bindings        []BindingSummary             `json:"bindings,omitempty"`
	LiveTokens      []TokenSummary               `json:"live_tokens,omitempty"`
	RecentDecisions []auth.GrantEvent            `json:"recent_decisions,omitempty"`
	DriftCount24h   int                          `json:"drift_count_24h"`
	TopDenyDim24h   string                       `json:"top_deny_dim_24h,omitempty"`
	Layers          []AgentPolicyResolutionLayer `json:"layers,omitempty"`
}

type BindingSummary struct {
	ID           string `json:"id"`
	TemplateID   string `json:"template_id"`
	TemplateHash string `json:"template_hash"`
	Via          string `json:"via,omitempty"`
}

type TokenSummary struct {
	JTI        string    `json:"jti"`
	JKT        string    `json:"jkt,omitempty"`
	AuthTime   time.Time `json:"auth_time,omitempty"`
	Acr        string    `json:"acr,omitempty"`
	GrantCount int       `json:"grant_count"`
}

type AdminTemplateAggregate struct {
	TemplateID       string             `json:"template_id,omitempty"`
	TemplateHash     string             `json:"template_hash"`
	Version          int                `json:"version,omitempty"`
	BindingCount     int                `json:"binding_count"`
	AgentCount       int                `json:"agent_count"`
	Allow24h         int                `json:"allow_24h"`
	Deny24h          int                `json:"deny_24h"`
	Challenge24h     int                `json:"challenge_24h"`
	TopDenyDims      []DenyDimAggregate `json:"top_deny_dims,omitempty"`
	DriftEvents24h   int                `json:"drift_events_24h"`
	LastFiredAt      time.Time          `json:"last_fired_at,omitempty"`
	LastEditedAt     time.Time          `json:"last_edited_at,omitempty"`
	LastEditedBy     string             `json:"last_edited_by,omitempty"`
	ActiveTokenCount int                `json:"active_token_count"`
}

type DenyDimAggregate struct {
	Dim   string `json:"dim"`
	Count int    `json:"count"`
}

type AnalyticsStatus struct {
	RetentionDays  int                   `json:"retention_days"`
	RingSize       int                   `json:"ring_size"`
	StoreAvailable bool                  `json:"store_available"`
	Store          *analytics.StoreStats `json:"store,omitempty"`
}

type analyticsStatsProvider interface {
	Stats() (analytics.StoreStats, error)
}

type activeGrantTokenCounter interface {
	ActiveGrantTokenCount(templateHash string) int
}

func (a *API) handleAnalyticsStatus(w http.ResponseWriter, r *http.Request) {
	status := AnalyticsStatus{
		RetentionDays:  a.analyticsRetentionDays,
		StoreAvailable: a.analyticsStore != nil,
	}
	if a.analyticsRing != nil {
		status.RingSize = a.analyticsRing.Len()
	}
	if provider, ok := a.analyticsStore.(analyticsStatsProvider); ok {
		stats, err := provider.Stats()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		status.Store = &stats
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) handleAnalyticsEvents(w http.ResponseWriter, r *http.Request) {
	if a.analyticsStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "analytics store not available"})
		return
	}
	filter, limit, err := analyticsFilterFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Expand `subject=groups/<name>` / `subject=roles/<name>` / bare `subject=<id>`
	// once per request so the persisted store gets an IN-list rather than a
	// per-row N+1 lookup.
	if subject := strings.TrimSpace(r.URL.Query().Get("subject")); subject != "" {
		if err := a.applySubjectFilter(&filter, subject); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// Empty expansion (e.g. groups/<name> with no members) must short-circuit
		// to an empty response rather than ignoring the filter — otherwise
		// `subject=groups/empty` would return everything.
		if subjectFilterEmpty(&filter, subject) {
			writeJSON(w, http.StatusOK, []auth.GrantEvent{})
			return
		}
	}
	events, err := a.analyticsStore.Query(filter, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	reverseGrantEvents(events)
	writeJSON(w, http.StatusOK, events)
}

// applySubjectFilter expands a `subject` query param into IN-list constraints
// on the supplied filter. The three accepted forms are documented on the
// QueryFilter struct itself; this method enforces the single snapshot of
// AgentManager + GrantManager so the request avoids per-row lookups.
func (a *API) applySubjectFilter(filter *analytics.QueryFilter, subject string) error {
	switch {
	case strings.HasPrefix(subject, "groups/"):
		name := strings.TrimPrefix(subject, "groups/")
		if name == "" {
			return fmt.Errorf("subject=groups/<name> requires a group name")
		}
		filter.AgentIDs = a.agentsInGroup(name)
	case strings.HasPrefix(subject, "roles/"):
		name := strings.TrimPrefix(subject, "roles/")
		if name == "" {
			return fmt.Errorf("subject=roles/<name> requires a role name")
		}
		// Roles aren't persisted on AgentPolicy — the binding store is the
		// only authoritative source. Resolve role → template hashes; the
		// event row's template_hash is the join column.
		filter.TemplateHashes = a.templateHashesForRole(name)
	case strings.HasPrefix(subject, "agents/"):
		// agents/<prism_id> is the explicit form; bare <prism_id> below.
		filter.AgentID = strings.TrimPrefix(subject, "agents/")
	default:
		filter.AgentID = subject
	}
	return nil
}

// subjectFilterEmpty reports whether a subject filter expanded to the empty
// set — meaning the caller should return zero events rather than falling
// through to "no filter". Only relevant for the IN-list forms.
func subjectFilterEmpty(filter *analytics.QueryFilter, subject string) bool {
	switch {
	case strings.HasPrefix(subject, "groups/"):
		return len(filter.AgentIDs) == 0
	case strings.HasPrefix(subject, "roles/"):
		return len(filter.TemplateHashes) == 0
	}
	return false
}

// agentsInGroup snapshots ListAgents once and collects prism_ids whose
// AgentPolicy.Groups contains name. Returns a nil slice when the manager
// isn't wired so the caller's empty-set short-circuit fires.
func (a *API) agentsInGroup(name string) []string {
	if a.agentMgr == nil {
		return nil
	}
	out := make([]string, 0, 8)
	for _, raw := range a.agentMgr.ListAgents() {
		prismID, groups := agentGroupsFor(raw)
		if prismID == "" {
			continue
		}
		for _, g := range groups {
			if g == name {
				out = append(out, prismID)
				break
			}
		}
	}
	return out
}

// templateHashesForRole collects template hashes from grant bindings whose
// Subjects.Roles include the named role. Returns a nil slice when grants
// aren't wired so the caller's empty-set short-circuit fires.
//
// TODO: walks all bindings on every request. Fine for v1 (dozens of bindings).
// Consider a reverse index keyed by role name when binding count exceeds ~100.
func (a *API) templateHashesForRole(name string) []string {
	if a.grantMgr == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, b := range a.grantMgr.ListGrantBindings() {
		match := false
		for _, role := range b.Subjects.Roles {
			if role == name {
				match = true
				break
			}
		}
		if !match && b.Subjects.RoleRequired == name {
			match = true
		}
		if !match {
			continue
		}
		if b.TemplateHash == "" {
			continue
		}
		seen[b.TemplateHash] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	return out
}

// agentGroupsFor reflects into the heterogeneous `any` returned by
// AgentManager.ListAgents() to pull the prism_id + groups list. Using a
// type switch keeps the resolver decoupled from any specific concrete type
// (the admin package already round-trips agents through `any`).
func agentGroupsFor(raw any) (string, []string) {
	// Marshal/unmarshal is a one-line way to handle the AgentInfo struct
	// regardless of which package owns the concrete type. The shape we care
	// about (prism_id + policy.groups) is stable JSON across implementations.
	type agentPolicyShape struct {
		Groups []string `json:"groups"`
	}
	type agentShape struct {
		PrismID string            `json:"prism_id"`
		Policy  *agentPolicyShape `json:"policy"`
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return "", nil
	}
	var as agentShape
	if err := json.Unmarshal(data, &as); err != nil {
		return "", nil
	}
	if as.Policy == nil {
		return as.PrismID, nil
	}
	return as.PrismID, as.Policy.Groups
}

func (a *API) handleAnalyticsTail(w http.ResponseWriter, r *http.Request) {
	if a.analyticsRing == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "analytics tail not available"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	enc := json.NewEncoder(w)
	for _, event := range a.analyticsRing.Latest() {
		if _, err := fmt.Fprint(w, "event: grant\n"); err != nil {
			return
		}
		if _, err := fmt.Fprint(w, "data: "); err != nil {
			return
		}
		if err := enc.Encode(event); err != nil {
			return
		}
		if _, err := fmt.Fprint(w, "\n"); err != nil {
			return
		}
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (a *API) handleAnalyticsTemplates(w http.ResponseWriter, r *http.Request) {
	if a.analyticsStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "analytics store not available"})
		return
	}
	window := parseWindow(r.URL.Query().Get("window"), 24*time.Hour)
	aggs, err := a.templateAggregates(window)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, aggs)
}

func (a *API) handleAnalyticsTemplate(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/analytics/templates/")
	if hash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "template hash is required"})
		return
	}
	aggs, err := a.templateAggregates(24 * time.Hour)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, agg := range aggs {
		if agg.TemplateHash == hash {
			writeJSON(w, http.StatusOK, agg)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
}

func (a *API) agentGrantResolution(prismID string, limit int) *AgentGrantResolution {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	res := &AgentGrantResolution{}
	if a.grantMgr != nil {
		for _, binding := range a.grantMgr.ListGrantBindings() {
			if bindingAppliesToAgent(binding, prismID) {
				res.Bindings = append(res.Bindings, BindingSummary{
					ID: binding.ID, TemplateID: binding.TemplateID, TemplateHash: binding.TemplateHash, Via: bindingVia(binding, prismID),
				})
			}
		}
	}
	if a.analyticsStore == nil {
		return res
	}
	now := a.analyticsNow()
	events, err := a.analyticsStore.Query(analytics.QueryFilter{
		AgentID: prismID,
		Since:   now.Add(-24 * time.Hour),
	}, 1000)
	if err != nil {
		return res
	}
	res.DriftCount24h, res.TopDenyDim24h = driftAndTopDeny(events)
	res.LiveTokens = tokenSummaries(events)
	recent, err := a.analyticsStore.Query(analytics.QueryFilter{AgentID: prismID}, limit)
	if err == nil {
		reverseGrantEvents(recent)
		res.RecentDecisions = recent
	}
	return res
}

func (a *API) templateAggregates(window time.Duration) ([]AdminTemplateAggregate, error) {
	filter := analytics.QueryFilter{Since: a.analyticsNow().Add(-window)}
	if window <= 0 {
		filter.Since = time.Time{}
	}
	events, err := a.analyticsStore.Query(filter, 1000)
	if err != nil {
		return nil, err
	}
	byHash := map[string]*AdminTemplateAggregate{}
	for _, event := range events {
		if event.TemplateHash == "" {
			continue
		}
		agg := byHash[event.TemplateHash]
		if agg == nil {
			agg = &AdminTemplateAggregate{TemplateHash: event.TemplateHash, TemplateID: event.TemplateID}
			byHash[event.TemplateHash] = agg
		}
		switch event.Outcome {
		case "allowed":
			agg.Allow24h++
		case "denied":
			agg.Deny24h++
		case "challenged":
			agg.Challenge24h++
		}
		if event.Trace.DenyDim == auth.GrantDenyWorkspaceDrift {
			agg.DriftEvents24h++
		}
		if event.Timestamp.After(agg.LastFiredAt) {
			agg.LastFiredAt = event.Timestamp
		}
	}
	a.enrichTemplateAggregates(byHash, events)
	out := make([]AdminTemplateAggregate, 0, len(byHash))
	for _, agg := range byHash {
		agg.TopDenyDims = topDenyDimsForHash(events, agg.TemplateHash)
		out = append(out, *agg)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastFiredAt.After(out[j].LastFiredAt)
	})
	return out, nil
}

func (a *API) enrichTemplateAggregates(byHash map[string]*AdminTemplateAggregate, events []auth.GrantEvent) {
	var activeCounter activeGrantTokenCounter
	if a.grantMgr != nil {
		if counter, ok := a.grantMgr.(activeGrantTokenCounter); ok {
			activeCounter = counter
		}
		agentsByHash := map[string]map[string]struct{}{}
		for _, binding := range a.grantMgr.ListGrantBindings() {
			agg := byHash[binding.TemplateHash]
			if agg == nil {
				agg = &AdminTemplateAggregate{TemplateHash: binding.TemplateHash, TemplateID: binding.TemplateID}
				byHash[binding.TemplateHash] = agg
			}
			agg.BindingCount++
			if agentsByHash[binding.TemplateHash] == nil {
				agentsByHash[binding.TemplateHash] = map[string]struct{}{}
			}
			for _, agent := range binding.Subjects.AgentIDs {
				agentsByHash[binding.TemplateHash][agent] = struct{}{}
			}
			if t, err := a.grantMgr.GetGrantTemplateByHash(binding.TemplateHash); err == nil {
				agg.Version = t.Version
				agg.LastEditedAt = t.CreatedAt
				agg.LastEditedBy = t.CreatedBy
			}
		}
		for hash, agents := range agentsByHash {
			if agg := byHash[hash]; agg != nil {
				agg.AgentCount = len(agents)
			}
		}
		if activeCounter != nil {
			for hash, agg := range byHash {
				agg.ActiveTokenCount = activeCounter.ActiveGrantTokenCount(hash)
			}
		}
	}
	active := map[string]map[string]struct{}{}
	for _, event := range events {
		if event.TemplateHash == "" || event.TokenJTI == "" {
			continue
		}
		if active[event.TemplateHash] == nil {
			active[event.TemplateHash] = map[string]struct{}{}
		}
		active[event.TemplateHash][event.TokenJTI] = struct{}{}
	}
	for hash, tokens := range active {
		if agg := byHash[hash]; agg != nil {
			if activeCounter == nil {
				agg.ActiveTokenCount = len(tokens)
			}
		}
	}
}

func analyticsFilterFromRequest(r *http.Request) (analytics.QueryFilter, int, error) {
	q := r.URL.Query()
	// `template` is the operator-facing short alias surfaced in chip URLs.
	// `template_hash` remains accepted for back-compat with epic-2 callers.
	templateHash := q.Get("template_hash")
	if templateHash == "" {
		templateHash = q.Get("template")
	}
	// Bare `agent` (used in Activity chip URLs) maps onto the existing
	// `agent_id` filter so the Health-strip deep-link surface stays one
	// canonical set of query keys: outcome, deny_dim, template, agent,
	// backend, subject, since.
	agentID := q.Get("agent_id")
	if agentID == "" {
		agentID = q.Get("agent")
	}
	filter := analytics.QueryFilter{
		AgentID:      agentID,
		TemplateHash: templateHash,
		Outcome:      q.Get("outcome"),
		DenyDim:      q.Get("deny_dim"),
		Backend:      q.Get("backend"),
	}
	var err error
	if filter.Since, err = parseOptionalTime(q.Get("since")); err != nil {
		return filter, 0, err
	}
	if filter.Until, err = parseOptionalTime(q.Get("until")); err != nil {
		return filter, 0, err
	}
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			return filter, 0, fmt.Errorf("limit must be a positive integer")
		}
	}
	filter.Limit = limit
	return filter, limit, nil
}

func parseOptionalTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("time value %q must be RFC3339 or duration", raw)
	}
	return time.Now().Add(-d), nil
}

func parseWindow(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

func reverseGrantEvents(events []auth.GrantEvent) {
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
}

func bindingAppliesToAgent(binding auth.GrantBinding, prismID string) bool {
	for _, id := range binding.Subjects.AgentIDs {
		if id == prismID {
			return true
		}
	}
	return len(binding.Subjects.AgentIDs) == 0
}

func bindingVia(binding auth.GrantBinding, prismID string) string {
	for _, id := range binding.Subjects.AgentIDs {
		if id == prismID {
			return "agent:" + prismID
		}
	}
	if binding.Subjects.RoleRequired != "" {
		return "role:" + binding.Subjects.RoleRequired
	}
	if len(binding.Subjects.Groups) > 0 {
		return "group:" + binding.Subjects.Groups[0]
	}
	return "default"
}

func driftAndTopDeny(events []auth.GrantEvent) (int, string) {
	counts := map[string]int{}
	drift := 0
	for _, event := range events {
		dim := event.Trace.DenyDim
		if dim == "" {
			continue
		}
		counts[dim]++
		if dim == auth.GrantDenyWorkspaceDrift {
			drift++
		}
	}
	top := ""
	topCount := 0
	for dim, count := range counts {
		if count > topCount || (count == topCount && dim < top) {
			top = dim
			topCount = count
		}
	}
	return drift, top
}

func tokenSummaries(events []auth.GrantEvent) []TokenSummary {
	byJTI := map[string]TokenSummary{}
	for _, event := range events {
		if event.TokenJTI == "" {
			continue
		}
		summary := byJTI[event.TokenJTI]
		summary.JTI = event.TokenJTI
		summary.JKT = event.DPoPjkt
		summary.AuthTime = event.AuthTime
		summary.Acr = event.Acr
		summary.GrantCount++
		byJTI[event.TokenJTI] = summary
	}
	out := make([]TokenSummary, 0, len(byJTI))
	for _, summary := range byJTI {
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JTI < out[j].JTI })
	return out
}

func topDenyDimsForHash(events []auth.GrantEvent, hash string) []DenyDimAggregate {
	counts := map[string]int{}
	for _, event := range events {
		if event.TemplateHash == hash && event.Trace.DenyDim != "" {
			counts[event.Trace.DenyDim]++
		}
	}
	out := make([]DenyDimAggregate, 0, len(counts))
	for dim, count := range counts {
		out = append(out, DenyDimAggregate{Dim: dim, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Dim < out[j].Dim
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}
