// policy_access.go — reverse-policy view for backends.
//
// Powers the "Who can use this?" section on /servers/{id} (spec §10.2,
// surfaced as a v1 feature in epic-4 / task-43). Given a backend id (and
// optionally a tool name), returns every subject (group / role / agent)
// whose stored policy grants access to at least one tool on that backend,
// together with that subject's allow + deny counts over the last 24h.
//
// Single-shot contract — all aggregates come from one analytics Query +
// one snapshot each of ListAgents / ListGrantBindings / ListGroups. No
// per-subject store query (no N+1).

package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/1broseidon/prism/internal/analytics"
	"github.com/1broseidon/prism/internal/auth"
)

// PolicyAccessResponse is the JSON shape returned by GET /policy/access.
//
// Entries is sorted: agents first, then groups, then roles, then within each
// type by subject id — stable so the operator's eye doesn't jump between
// refreshes. Counts cover the last 24h.
type PolicyAccessResponse struct {
	Backend       string              `json:"backend"`
	Tool          string              `json:"tool,omitempty"`
	WindowSeconds int                 `json:"window_seconds"`
	GeneratedAt   time.Time           `json:"generated_at"`
	Entries       []PolicyAccessEntry `json:"entries"`
	Empty         bool                `json:"empty"`
}

// PolicyAccessEntry is one row on the section: which subject has access via
// which capability shape, with the 24h call/denial counts for that subject
// constrained to the backend in question.
//
// TemplateHash is empty for scope-shape rows (the grant lives directly on the
// subject's stored policy as a "backend:tool" or "backend:*" string).
// CapabilityID matches the IDs surfaced by /policy/subjects/{type}/{id}/capabilities
// — the UI uses it to route the "Edit policy →" link to the correct subject
// page.
type PolicyAccessEntry struct {
	SubjectType  string `json:"subject_type"`
	SubjectID    string `json:"subject_id"`
	Source       string `json:"source"` // "scope" or "grant"
	Summary      string `json:"summary"`
	TemplateHash string `json:"template_hash,omitempty"`
	CapabilityID string `json:"capability_id"`
	Calls24h     int    `json:"calls_24h"`
	Denials24h   int    `json:"denials_24h"`
}

// handlePolicyAccess serves GET /policy/access?backend=<id>[&tool=<name>].
//
// Returns 400 on missing/invalid backend id. Returns 200 with empty Entries
// (and Empty=true) when no policy grants access — the UI renders the explicit
// "No policy grants access" empty state and links to /policy.
//
// Auth: read-only — requires a session (admin or viewer) just like the rest
// of the /policy/* read surface.
func (a *API) handlePolicyAccess(w http.ResponseWriter, r *http.Request) {
	backend := strings.TrimSpace(r.URL.Query().Get("backend"))
	if backend == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backend query parameter is required"})
		return
	}
	if !isValidID(backend) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid backend id"})
		return
	}
	tool := strings.TrimSpace(r.URL.Query().Get("tool"))
	// `tool` is treated as a display/aggregation filter only — we don't
	// reject unknown tools here because the tool list on a backend changes
	// out from under the operator (re-import, hot-add). Garbage values
	// simply produce 0 counts.

	entries := a.collectPolicyAccessEntries(backend, tool)
	a.enrichPolicyAccessCounts(entries, backend, tool)
	sortPolicyAccessEntries(entries)

	resp := PolicyAccessResponse{
		Backend:       backend,
		Tool:          tool,
		WindowSeconds: int((24 * time.Hour) / time.Second),
		GeneratedAt:   time.Now().UTC(),
		Entries:       entries,
		Empty:         len(entries) == 0,
	}
	writeJSON(w, http.StatusOK, resp)
}

// collectPolicyAccessEntries scans every stored policy shape (group scopes,
// agent scope grants, and grant bindings) and emits one entry per
// (subject, capability) pair that touches the backend.
//
// The function takes one snapshot each of ListGrantBindings + ListAgents +
// (per-group) GetGroup; it never round-trips through the analytics store.
// All event-store aggregation happens in enrichPolicyAccessCounts so the two
// concerns stay separate and so the function is cheap to call even when
// analytics is disabled.
func (a *API) collectPolicyAccessEntries(backend, tool string) []PolicyAccessEntry {
	out := make([]PolicyAccessEntry, 0, 16)

	// Scope-shape grants on groups.
	if a.groupMgr != nil {
		for _, g := range a.groupMgr.ListGroups() {
			for _, scope := range g.Scopes {
				if !scopeMatchesBackend(scope, backend, tool) {
					continue
				}
				spec := specFromScope(scope)
				out = append(out, PolicyAccessEntry{
					SubjectType:  subjectTypeGroups,
					SubjectID:    g.Name,
					Source:       capabilitySourceScope,
					Summary:      renderDisplaySummary(spec),
					CapabilityID: encodeScopeCapabilityID(spec, []string{scope}),
				})
			}
		}
	}

	// Scope-shape grants stored on agent policies (via AgentPolicy.Grant
	// entries; role: prefixes are skipped here — they're surfaced via the
	// grant bindings join below).
	if a.agentMgr != nil {
		for _, raw := range a.agentMgr.ListAgents() {
			prismID, scopes := agentScopeGrantsFor(raw)
			if prismID == "" {
				continue
			}
			for _, scope := range scopes {
				if strings.HasPrefix(scope, "role:") {
					continue
				}
				if !scopeMatchesBackend(scope, backend, tool) {
					continue
				}
				spec := specFromScope(scope)
				out = append(out, PolicyAccessEntry{
					SubjectType:  subjectTypeAgents,
					SubjectID:    prismID,
					Source:       capabilitySourceScope,
					Summary:      renderDisplaySummary(spec),
					CapabilityID: encodeScopeCapabilityID(spec, []string{scope}),
				})
			}
		}
	}

	// Grant-shape bindings. One binding maps to one template; the template
	// carries the (backend, tool) pair (literal or wildcard or tool_in_set
	// over a specific verb expansion). We need to expand the binding's
	// Subjects.Groups / Subjects.Roles / Subjects.AgentIDs into one entry
	// per concrete subject so the UI can show "edit on group X" vs
	// "edit on agent Y".
	if a.grantMgr != nil {
		for _, b := range a.grantMgr.ListGrantBindings() {
			tmpl, err := a.grantMgr.GetGrantTemplateByHash(b.TemplateHash)
			if err != nil {
				// Dangling binding — Power Tools is the place to repair
				// these. Skip rather than blowing up the section.
				continue
			}
			if !templateMatchesBackend(tmpl.Spec, backend, tool) {
				continue
			}
			spec := specFromGrantTemplate(tmpl, b)
			summary := renderDisplaySummary(spec)
			capID := "bind-" + b.ID
			for _, name := range b.Subjects.Groups {
				out = append(out, PolicyAccessEntry{
					SubjectType:  subjectTypeGroups,
					SubjectID:    name,
					Source:       capabilitySourceGrant,
					Summary:      summary,
					TemplateHash: b.TemplateHash,
					CapabilityID: capID,
				})
			}
			for _, name := range b.Subjects.Roles {
				out = append(out, PolicyAccessEntry{
					SubjectType:  subjectTypeRoles,
					SubjectID:    name,
					Source:       capabilitySourceGrant,
					Summary:      summary,
					TemplateHash: b.TemplateHash,
					CapabilityID: capID,
				})
			}
			for _, id := range b.Subjects.AgentIDs {
				out = append(out, PolicyAccessEntry{
					SubjectType:  subjectTypeAgents,
					SubjectID:    id,
					Source:       capabilitySourceGrant,
					Summary:      summary,
					TemplateHash: b.TemplateHash,
					CapabilityID: capID,
				})
			}
		}
	}

	return dedupPolicyAccessEntries(out)
}

// enrichPolicyAccessCounts attaches 24h call + denial counts to each entry.
//
// Two passes against ONE Store.Query result (filtered to backend + 24h
// window):
//
//   - For agent entries, the join is direct: event.AgentID == subject id.
//   - For group entries, the join uses the agentsInGroup snapshot (mirrors
//     task-42's applySubjectFilter `groups/<name>` resolver).
//   - For role entries, the join is via template hash — role membership is
//     implicit through the grant binding's Subjects.Roles selector and event
//     rows carry template_hash. We pre-build the set of template hashes for
//     each role and OR-match on event.TemplateHash.
//
// This is intentionally O(events + subjects * memberLookup) rather than
// O(subjects * events) — one scan, hash-map joins.
func (a *API) enrichPolicyAccessCounts(entries []PolicyAccessEntry, backend, tool string) {
	if a.analyticsStore == nil || len(entries) == 0 {
		return
	}
	events, err := a.analyticsStore.Query(analytics.QueryFilter{
		Backend: backend,
		Since:   time.Now().Add(-24 * time.Hour),
	}, 50_000)
	if err != nil {
		// Counts default to zero; the section still renders with subject
		// rows so the operator at least sees who has access.
		return
	}
	if len(events) == 50_000 {
		slog.Warn("policy/access event scan hit 50k cap; counts may under-report",
			"backend", backend, "tool", tool)
	}

	// Tool filter (post-query) — Store.QueryFilter doesn't expose a Tool
	// column today and adding one just for this surface would widen the
	// store interface for a single caller. Bounded by the 50k limit above.
	if tool != "" {
		filtered := events[:0]
		for _, e := range events {
			if e.Tool == tool || matchesNamespacedTool(e.Tool, backend, tool) {
				filtered = append(filtered, e)
			}
		}
		events = filtered
	}

	// Index events by agent_id and template_hash for O(1) lookup.
	byAgent := make(map[string]struct{ calls, denials int }, 32)
	byTemplate := make(map[string]struct{ calls, denials int }, 32)
	for _, e := range events {
		if e.AgentID != "" {
			c := byAgent[e.AgentID]
			c.calls++
			if e.Outcome == "denied" {
				c.denials++
			}
			byAgent[e.AgentID] = c
		}
		if e.TemplateHash != "" {
			c := byTemplate[e.TemplateHash]
			c.calls++
			if e.Outcome == "denied" {
				c.denials++
			}
			byTemplate[e.TemplateHash] = c
		}
	}

	// Pre-resolve group membership once per unique group across entries.
	groupMembers := make(map[string][]string, 8)
	for _, e := range entries {
		if e.SubjectType != subjectTypeGroups {
			continue
		}
		if _, ok := groupMembers[e.SubjectID]; ok {
			continue
		}
		groupMembers[e.SubjectID] = a.agentsInGroup(e.SubjectID)
	}

	for i := range entries {
		ent := &entries[i]
		switch ent.SubjectType {
		case subjectTypeAgents:
			c := byAgent[ent.SubjectID]
			ent.Calls24h = c.calls
			ent.Denials24h = c.denials
		case subjectTypeGroups:
			for _, prismID := range groupMembers[ent.SubjectID] {
				c := byAgent[prismID]
				ent.Calls24h += c.calls
				ent.Denials24h += c.denials
			}
		case subjectTypeRoles:
			// Role rows always have a template hash (roles only exist on
			// the grant-shape side). Use that hash to aggregate.
			if ent.TemplateHash != "" {
				c := byTemplate[ent.TemplateHash]
				ent.Calls24h = c.calls
				ent.Denials24h = c.denials
			}
		}
	}
}

// scopeMatchesBackend reports whether a "backend:tool" or "backend:*" scope
// string covers (backend, tool). When tool is empty, any tool on that backend
// matches.
func scopeMatchesBackend(scope, backend, tool string) bool {
	parts := strings.SplitN(scope, ":", 2)
	if len(parts) != 2 {
		return false
	}
	if parts[0] != backend {
		return false
	}
	if parts[1] == "*" {
		return true
	}
	if tool == "" {
		return true
	}
	return parts[1] == tool
}

// templateMatchesBackend reports whether the compiled grant spec covers
// (backend, tool). Mirrors the gateway's authorization-time match logic:
// wildcard backend ("*") in tool_in_set entries counts as "all backends"
// at compile time but at runtime each call resolves to a concrete backend,
// so a wildcard tool_in_set on backend="*" matches any backend whose verb
// expansion is in the predicate.
func templateMatchesBackend(gs auth.GrantSpec, backend, tool string) bool {
	// Direct (Backend, Tool) match — the simple-spec compile path.
	if gs.Backend == backend {
		if gs.Tool == "*" {
			return true
		}
		if tool == "" {
			return true
		}
		return gs.Tool == tool
	}
	// Verb-compiled templates carry Backend="*", Tool="*" and use a
	// _tool predicate. Walk the predicate's set looking for any entry
	// whose backend literal matches (or the "*" sentinel which the
	// compile-down router writes when no enabled-backends snapshot was
	// available).
	if gs.Backend == "*" && gs.Tool == "*" {
		pred, ok := gs.Args["_tool"]
		if !ok {
			return false
		}
		for _, entry := range pred.ToolInSet {
			parts := strings.SplitN(entry, ":", 2)
			if len(parts) != 2 {
				continue
			}
			if parts[0] != backend && parts[0] != "*" {
				continue
			}
			if tool == "" {
				return true
			}
			if parts[1] == tool || parts[1] == "*" {
				return true
			}
		}
	}
	return false
}

// matchesNamespacedTool reports whether a stored event's `Tool` column (which
// the gateway writes as "<namespace>.<tool>" — e.g. "fs.write_file") refers
// to the operator-filter's `(backend, tool)` pair.
//
// Backends typically use their id as the namespace so we accept both forms:
//   - "fs.write_file" when backend="fs", tool="write_file" → true (namespaced).
//   - "write_file"    when backend="fs", tool="write_file" → false here, but
//     the direct e.Tool == tool path above already matches.
func matchesNamespacedTool(eventTool, backend, tool string) bool {
	if backend == "" || tool == "" {
		return false
	}
	prefix := backend + "."
	if !strings.HasPrefix(eventTool, prefix) {
		return false
	}
	return eventTool[len(prefix):] == tool
}

// dedupPolicyAccessEntries collapses identical (subjectType, subjectID,
// capabilityID) tuples that can appear when a binding targets the same
// subject through multiple selectors. Keeps first occurrence to preserve
// the order produced by collectPolicyAccessEntries.
func dedupPolicyAccessEntries(in []PolicyAccessEntry) []PolicyAccessEntry {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, e := range in {
		key := e.SubjectType + "|" + e.SubjectID + "|" + e.CapabilityID
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	return out
}

// sortPolicyAccessEntries orders entries as:
//
//  1. by subject type (agents, groups, roles) — agents lead because that's
//     the most-actionable "who is calling me?" row in the operator's mind;
//  2. by subject id ascending;
//  3. by capability id ascending (stable across refreshes).
func sortPolicyAccessEntries(entries []PolicyAccessEntry) {
	subjectTypeOrder := func(t string) int {
		switch t {
		case subjectTypeAgents:
			return 0
		case subjectTypeGroups:
			return 1
		case subjectTypeRoles:
			return 2
		}
		return 3
	}
	sort.SliceStable(entries, func(i, j int) bool {
		ai, aj := subjectTypeOrder(entries[i].SubjectType), subjectTypeOrder(entries[j].SubjectType)
		if ai != aj {
			return ai < aj
		}
		if entries[i].SubjectID != entries[j].SubjectID {
			return entries[i].SubjectID < entries[j].SubjectID
		}
		return entries[i].CapabilityID < entries[j].CapabilityID
	})
}

// agentScopeGrantsFor reflects into the heterogeneous ListAgents() shape to
// pull the prism_id + AgentPolicy.Grant list. Mirrors agentGroupsFor — both
// avoid coupling the reverse-policy view to the concrete agent record type
// owned by the authserver package.
func agentScopeGrantsFor(raw any) (string, []string) {
	type agentPolicyShape struct {
		Grant []string `json:"grant"`
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
	return as.PrismID, as.Policy.Grant
}
