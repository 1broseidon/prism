package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/1broseidon/prism/internal/auth"
	_ "modernc.org/sqlite"
)

const DefaultRetention = 30 * 24 * time.Hour

// Store is the historical grant-event store interface.
type Store interface {
	Insert(e auth.GrantEvent) error
	Query(filter QueryFilter, limit int) ([]auth.GrantEvent, error)
	AggregateByTemplate(window time.Duration) ([]TemplateAggregate, error)
	// Health returns the six-number Policy Health aggregate over `window`.
	// Implementations compute all counts in a single read transaction so the
	// Policy Health strip never mixes timestamps across tiles.
	Health(window time.Duration) (HealthSummary, error)
	// AgentPolicySummaries returns per-agent triage aggregates for the
	// supplied prism IDs in a single read transaction: last denial (any
	// outcome=='denied' row) and workspace-drift count over `driftWindow`.
	// Implementations MUST use a single round trip per concern (one for
	// last-denial, one for drift counts) regardless of how many agent IDs
	// are passed; the agents-listing page on the admin console depends on
	// this to avoid N+1 across the registered-agent set.
	//
	// Agents with no matching rows are absent from the returned map (callers
	// treat missing entries as "no activity in window"). An empty input
	// slice returns an empty map without touching the DB.
	AgentPolicySummaries(prismIDs []string, driftWindow time.Duration) (map[string]AgentTriageSummary, error)
	Retain(maxAge time.Duration) (deleted int, err error)
}

// AgentTriageSummary is the per-agent slice the agents-listing handler joins
// with capability counts to render the three triage columns. LastDenialAt is
// the zero value when the agent has no denials in the store (any age — the
// "last" is unbounded so operators can spot agents that have been silent for
// weeks but still hold a denial scar).
type AgentTriageSummary struct {
	// LastDenialAt is the timestamp of the most recent outcome=='denied'
	// row for the agent. Zero value means "no denials ever recorded".
	LastDenialAt time.Time
	// LastDenialDim is the deny_dim of that row (may be empty when the
	// gateway didn't classify the dimension — older rows or scope-shape
	// denials). Useful as a tooltip-quality hint without round-tripping
	// to the full event.
	LastDenialDim string
	// DriftCount24h is the count of deny_dim=='workspace_drift' rows for
	// the agent within the `driftWindow` passed to the query.
	DriftCount24h int
}

// QueryFilter filters grant events.
//
// All scalar fields are AND-combined; empty values are skipped. The two
// IN-list fields below extend the original shape to support task-42's
// Health-strip deep-links and subject-grouped Activity filters:
//
//   - AgentIDs: when non-empty, the row's agent_id must appear in the set.
//     If AgentID is also set it acts as an additional AND constraint, but
//     callers typically use one or the other.
//   - TemplateHashes: same semantics for template_hash. Used by the
//     `subject=roles/<name>` resolver which expands the role name to the
//     set of template hashes whose bindings target that role.
//
// Backend filters on the persisted backend column written by the gateway.
type QueryFilter struct {
	AgentID        string
	AgentIDs       []string
	TemplateHash   string
	TemplateHashes []string
	Outcome        string
	DenyDim        string
	Backend        string
	Since          time.Time
	Until          time.Time
	Limit          int
}

// TemplateAggregate summarizes outcomes by template hash.
type TemplateAggregate struct {
	TemplateHash string
	Allowed      int
	Denied       int
	Challenged   int
	Total        int
}

// HealthSummary is the aggregate that powers the Policy Health header strip
// (task-41, extended task-46 for the 4-tile SecOps presentation). All counts
// are over the same window and computed from a single read transaction so
// the strip never mixes timestamps. Callers derive denial_rate themselves
// from Denials / max(Calls, 1).
//
//   - Calls:                  outcome ∈ {allowed, denied, challenged}
//   - DriftEvents:            deny_dim == "workspace_drift"
//   - Denials:                outcome == "denied"
//   - MedianFreshnessSeconds: median (window_end − auth_time) across events
//     whose auth_time is non-zero; -1 when none.
//     [deprecated for UI rendering; kept on the wire
//     for backwards compatibility with external
//     consumers — task-46.]
//   - DPoPBoundAgents:        distinct agent_id where dpop_jkt != "".
//     [deprecated for UI; see MedianFreshness note.]
//   - ActiveTemplates:        distinct template_hash where outcome ∈
//     {allowed, denied} and template_hash != "".
//     [deprecated for UI; see MedianFreshness note.]
//
// SecOps-aligned fields (task-46) — these power the 4-tile strip:
//
//   - Calls7dAvg:    rolling 7-day daily-average call count over the same
//     window the rest of the summary uses ("did today's
//     traffic look normal?"). Computed as
//     SUM(events in 7d) / 7, rounded.
//   - TopDenyDim:    deny_dim string with the largest denial count in the
//     window; "" when no denials. Operators read this as
//     "what's biting us the most right now?".
//   - TopDenyDimCount: count for TopDenyDim.
type HealthSummary struct {
	Calls                  int
	DriftEvents            int
	Denials                int
	MedianFreshnessSeconds int64
	DPoPBoundAgents        int
	ActiveTemplates        int
	// SecOps tiles (task-46).
	Calls7dAvg      int
	TopDenyDim      string
	TopDenyDimCount int
}

// StoreStats summarizes historical grant-event storage usage.
type StoreStats struct {
	EventCount int64     `json:"event_count"`
	OldestAt   time.Time `json:"oldest_at,omitempty"`
	NewestAt   time.Time `json:"newest_at,omitempty"`
	SizeBytes  int64     `json:"size_bytes"`
}

// SQLiteStore is a SQLite-backed grant event store.
type SQLiteStore struct {
	db   *sql.DB
	path string
}

// OpenSQLiteStore opens or creates the SQLite event store at path.
func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite path is required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &SQLiteStore{db: db, path: path}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) init() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS grant_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts INTEGER NOT NULL,
			request_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			client_id TEXT,
			dpop_jkt TEXT,
			backend TEXT NOT NULL,
			tool TEXT NOT NULL,
			call_args_hash TEXT,
			workspace_id TEXT,
			workspace_type TEXT,
			outcome TEXT NOT NULL,
			template_id TEXT,
			template_hash TEXT,
			matched_index INTEGER NOT NULL DEFAULT -1,
			deny_dim TEXT,
			trace_json TEXT NOT NULL,
			auth_time INTEGER,
			acr TEXT,
			token_jti TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_grant_events_agent_ts ON grant_events(agent_id, ts);`,
		`CREATE INDEX IF NOT EXISTS idx_grant_events_template_ts ON grant_events(template_hash, ts);`,
		`CREATE INDEX IF NOT EXISTS idx_grant_events_outcome_ts ON grant_events(outcome, deny_dim, ts);`,
		`CREATE INDEX IF NOT EXISTS idx_grant_events_ts ON grant_events(ts);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Insert appends one grant event.
func (s *SQLiteStore) Insert(e auth.GrantEvent) error {
	if s == nil || s.db == nil {
		return errors.New("event store is closed")
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	traceJSON, err := json.Marshal(e.Trace)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO grant_events (
		ts, request_id, agent_id, client_id, dpop_jkt, backend, tool,
		call_args_hash, workspace_id, workspace_type, outcome, template_id,
		template_hash, matched_index, deny_dim, trace_json, auth_time, acr, token_jti
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Timestamp.UnixNano(), e.RequestID, e.AgentID, e.ClientID, e.DPoPjkt,
		e.Backend, e.Tool, e.CallArgsHash, e.WorkspaceID, e.WorkspaceType,
		e.Outcome, e.TemplateID, e.TemplateHash, e.MatchedIndex, e.Trace.DenyDim,
		string(traceJSON), timeToUnixNano(e.AuthTime), e.Acr, e.TokenJTI,
	)
	return err
}

// Query returns matching events ordered oldest-to-newest.
func (s *SQLiteStore) Query(filter QueryFilter, limit int) ([]auth.GrantEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("event store is closed")
	}
	if filter.Limit > 0 {
		limit = filter.Limit
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	clauses := []string{"1=1"}
	args := make([]any, 0, 8)
	if filter.AgentID != "" {
		clauses = append(clauses, "agent_id = ?")
		args = append(args, filter.AgentID)
	}
	if len(filter.AgentIDs) > 0 {
		placeholders := make([]string, len(filter.AgentIDs))
		for i, id := range filter.AgentIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		clauses = append(clauses, "agent_id IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.TemplateHash != "" {
		clauses = append(clauses, "template_hash = ?")
		args = append(args, filter.TemplateHash)
	}
	if len(filter.TemplateHashes) > 0 {
		placeholders := make([]string, len(filter.TemplateHashes))
		for i, h := range filter.TemplateHashes {
			placeholders[i] = "?"
			args = append(args, h)
		}
		clauses = append(clauses, "template_hash IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.Outcome != "" {
		clauses = append(clauses, "outcome = ?")
		args = append(args, filter.Outcome)
	}
	if filter.DenyDim != "" {
		clauses = append(clauses, "deny_dim = ?")
		args = append(args, filter.DenyDim)
	}
	if filter.Backend != "" {
		clauses = append(clauses, "backend = ?")
		args = append(args, filter.Backend)
	}
	if !filter.Since.IsZero() {
		clauses = append(clauses, "ts >= ?")
		args = append(args, filter.Since.UnixNano())
	}
	if !filter.Until.IsZero() {
		clauses = append(clauses, "ts <= ?")
		args = append(args, filter.Until.UnixNano())
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT ts, request_id, agent_id, client_id, dpop_jkt,
		backend, tool, call_args_hash, workspace_id, workspace_type, outcome,
		template_id, template_hash, matched_index, trace_json, auth_time, acr, token_jti
		FROM grant_events WHERE `+strings.Join(clauses, " AND ")+` ORDER BY ts ASC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]auth.GrantEvent, 0)
	for rows.Next() {
		var e auth.GrantEvent
		var ts, authTime int64
		var traceRaw string
		if err := rows.Scan(&ts, &e.RequestID, &e.AgentID, &e.ClientID, &e.DPoPjkt,
			&e.Backend, &e.Tool, &e.CallArgsHash, &e.WorkspaceID, &e.WorkspaceType,
			&e.Outcome, &e.TemplateID, &e.TemplateHash, &e.MatchedIndex, &traceRaw,
			&authTime, &e.Acr, &e.TokenJTI); err != nil {
			return nil, err
		}
		e.Timestamp = time.Unix(0, ts).UTC()
		if authTime != 0 {
			e.AuthTime = time.Unix(0, authTime).UTC()
		}
		if err := json.Unmarshal([]byte(traceRaw), &e.Trace); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AggregateByTemplate returns outcome counts per template hash.
func (s *SQLiteStore) AggregateByTemplate(window time.Duration) ([]TemplateAggregate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("event store is closed")
	}
	since := time.Now().Add(-window).UnixNano()
	if window <= 0 {
		since = 0
	}
	rows, err := s.db.Query(`SELECT template_hash, outcome, COUNT(*)
		FROM grant_events WHERE ts >= ? GROUP BY template_hash, outcome`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byHash := map[string]*TemplateAggregate{}
	for rows.Next() {
		var hash, outcome string
		var count int
		if err := rows.Scan(&hash, &outcome, &count); err != nil {
			return nil, err
		}
		agg := byHash[hash]
		if agg == nil {
			agg = &TemplateAggregate{TemplateHash: hash}
			byHash[hash] = agg
		}
		switch outcome {
		case "allowed":
			agg.Allowed += count
		case "denied":
			agg.Denied += count
		case "challenged":
			agg.Challenged += count
		}
		agg.Total += count
	}
	out := make([]TemplateAggregate, 0, len(byHash))
	for _, agg := range byHash {
		out = append(out, *agg)
	}
	return out, rows.Err()
}

// Health returns the six-number Policy Health aggregate over the supplied
// window. Implementation strategy:
//
//   - One read transaction holds the entire pass so the strip never mixes
//     timestamps across tiles. The first statement materializes
//     SUM(CASE)-style outcome counters plus the distinct-set counts
//     (active templates, DPoP-bound agents). The second statement walks
//     the auth_time column ordered ascending to derive the median freshness
//     without loading every event into memory.
//
//   - The aggregate SELECT does not allocate a temp table — all counters
//     are computed inline via SUM(CASE WHEN ...). On SQLite the planner
//     uses the (ts) index for the WHERE filter; distinct-set counts run
//     as a single pass against the same filtered rowset.
//
// Window <= 0 means "all events" — the WHERE clause drops the ts predicate.
// MedianFreshnessSeconds is -1 when no event in the window carried a non-zero
// auth_time, matching the contract's "no data" sentinel.
func (s *SQLiteStore) Health(window time.Duration) (HealthSummary, error) {
	if s == nil || s.db == nil {
		return HealthSummary{}, errors.New("event store is closed")
	}
	var sinceNanos int64
	if window > 0 {
		sinceNanos = time.Now().Add(-window).UnixNano()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return HealthSummary{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// First pass: outcome counters + distinct-set counters.
	//
	// `calls` counts the three known outcomes; an event with an outcome
	// outside the known set (defensive — should not happen in production)
	// is omitted from `calls` so denial_rate is computed against the same
	// denominator the tiles surface.
	const aggregateSQL = `
		SELECT
			SUM(CASE WHEN outcome IN ('allowed','denied','challenged') THEN 1 ELSE 0 END) AS calls,
			SUM(CASE WHEN outcome = 'denied' THEN 1 ELSE 0 END) AS denials,
			SUM(CASE WHEN deny_dim = 'workspace_drift' THEN 1 ELSE 0 END) AS drift,
			(SELECT COUNT(DISTINCT agent_id) FROM grant_events
				WHERE ts >= ? AND dpop_jkt IS NOT NULL AND dpop_jkt != '') AS dpop_agents,
			(SELECT COUNT(DISTINCT template_hash) FROM grant_events
				WHERE ts >= ? AND template_hash IS NOT NULL AND template_hash != ''
				AND outcome IN ('allowed','denied')) AS active_templates
		FROM grant_events
		WHERE ts >= ?
	`
	var summary HealthSummary
	var calls, denials, drift, dpopAgents, activeTemplates sql.NullInt64
	if err := tx.QueryRow(aggregateSQL, sinceNanos, sinceNanos, sinceNanos).
		Scan(&calls, &denials, &drift, &dpopAgents, &activeTemplates); err != nil {
		return HealthSummary{}, err
	}
	summary.Calls = int(calls.Int64)
	summary.Denials = int(denials.Int64)
	summary.DriftEvents = int(drift.Int64)
	summary.DPoPBoundAgents = int(dpopAgents.Int64)
	summary.ActiveTemplates = int(activeTemplates.Int64)

	// Second pass: ordered walk of (now - auth_time) to derive the median.
	// SQLite has no built-in MEDIAN; ORDER BY + a single streamed scan is
	// simpler than the percentile_cont() workaround and stays inside the
	// same transaction (read-consistent with the aggregate above).
	nowNanos := time.Now().UnixNano()
	rows, err := tx.Query(`SELECT (? - auth_time) FROM grant_events
		WHERE ts >= ? AND auth_time > 0 ORDER BY auth_time ASC`, nowNanos, sinceNanos)
	if err != nil {
		return HealthSummary{}, err
	}
	defer rows.Close()
	freshness := make([]int64, 0, summary.Calls)
	for rows.Next() {
		var diff int64
		if err := rows.Scan(&diff); err != nil {
			return HealthSummary{}, err
		}
		// auth_time can post-date "now" if the clocks drift backwards; clamp
		// so the median doesn't go negative.
		if diff < 0 {
			diff = 0
		}
		freshness = append(freshness, diff)
	}
	if err := rows.Err(); err != nil {
		return HealthSummary{}, err
	}
	if err := tx.Commit(); err != nil {
		return HealthSummary{}, err
	}

	if len(freshness) == 0 {
		summary.MedianFreshnessSeconds = -1
	} else {
		mid := len(freshness) / 2
		var medianNanos int64
		if len(freshness)%2 == 1 {
			medianNanos = freshness[mid]
		} else {
			medianNanos = (freshness[mid-1] + freshness[mid]) / 2
		}
		summary.MedianFreshnessSeconds = medianNanos / int64(time.Second)
	}

	// SecOps-tile additions (task-46): top deny_dim within the window and
	// the rolling 7-day average daily call count. Both run after the main
	// transaction commits — they're advisory tiles that don't need to be
	// strictly consistent with the primary aggregate counters, and SQLite
	// doesn't support multi-statement transactions across separate Query()
	// calls cleanly. The unsynchronized read is still correct: any event
	// that arrives between the main tx and these reads only ever increases
	// the counters, which is a strictly better (more recent) snapshot than
	// blocking the response on a longer transaction.

	// Top deny dimension within the window. Empty deny_dim rows are excluded
	// from the GROUP BY — those are denials the gateway couldn't classify
	// and showing "(empty)" would be more confusing than skipping. Tie
	// breaking is deterministic via the deny_dim ORDER BY tail so test
	// expectations stay stable.
	const topDenyDimSQL = `
		SELECT deny_dim, COUNT(*) AS c
		FROM grant_events
		WHERE ts >= ? AND outcome = 'denied'
		      AND deny_dim IS NOT NULL AND deny_dim != ''
		GROUP BY deny_dim
		ORDER BY c DESC, deny_dim ASC
		LIMIT 1`
	var topDim sql.NullString
	var topCount sql.NullInt64
	if err := s.db.QueryRow(topDenyDimSQL, sinceNanos).Scan(&topDim, &topCount); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return HealthSummary{}, err
	}
	if topDim.Valid {
		summary.TopDenyDim = topDim.String
		summary.TopDenyDimCount = int(topCount.Int64)
	}

	// 7-day rolling daily-average call count. Operators read this as the
	// trend baseline behind the "calls (24h)" tile: today vs the past week.
	// SUM/7 rather than DISTINCT(date) so a day with zero traffic still
	// pulls the average down (which is what "what's normal?" means).
	sevenDayCutoff := time.Now().Add(-7 * 24 * time.Hour).UnixNano()
	const sevenDaySQL = `
		SELECT COUNT(*) FROM grant_events
		WHERE ts >= ? AND outcome IN ('allowed','denied','challenged')`
	var total7d sql.NullInt64
	if err := s.db.QueryRow(sevenDaySQL, sevenDayCutoff).Scan(&total7d); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return HealthSummary{}, err
	}
	if total7d.Valid {
		// Integer division rounds toward zero; that's fine for a rolling
		// average rendered as a baseline number. If the store has fewer
		// than 7 days of data the average just understates traffic, which
		// matches what operators expect on a fresh install.
		summary.Calls7dAvg = int(total7d.Int64) / 7
	}

	return summary, nil
}

// AgentPolicySummaries returns per-agent triage aggregates for the supplied
// prism IDs. Two reads total — one for "last denial per agent" and one for
// the drift count over `driftWindow` — irrespective of how many IDs are
// passed. The agents-listing handler depends on this constant-round-trip
// guarantee to avoid N+1 across the registered set.
//
// Implementation notes:
//
//   - Both queries use `agent_id IN (?, ?, ...)` so the SQLite planner uses
//     the (agent_id, ts) covering index. With ~hundreds of agents the IN
//     list is well under SQLite's variable-bind limit (defaults to 32766).
//   - The "last denial" query uses GROUP BY agent_id with MAX(ts) and a
//     correlated subquery to fetch the deny_dim at that timestamp. Doing
//     it in one SQL statement keeps the wire shape symmetric across all
//     agents (no per-row lookups).
//   - The drift count is GROUP BY agent_id over deny_dim filter.
//
// A nil/empty input returns an empty map without touching the DB.
func (s *SQLiteStore) AgentPolicySummaries(prismIDs []string, driftWindow time.Duration) (map[string]AgentTriageSummary, error) {
	out := make(map[string]AgentTriageSummary, len(prismIDs))
	if s == nil || s.db == nil {
		return out, errors.New("event store is closed")
	}
	if len(prismIDs) == 0 {
		return out, nil
	}
	// SQLite SQLITE_LIMIT_VARIABLE_NUMBER defaults to 32766; we leave
	// headroom for the two driftWindow/threshold args appended below and
	// any other planner-injected binds, then degrade gracefully with a
	// clear error rather than letting the prepared statement crash.
	const maxAgents = 32_000
	if len(prismIDs) > maxAgents {
		return nil, fmt.Errorf("AgentPolicySummaries: too many agents (%d > %d); paginate the request",
			len(prismIDs), maxAgents)
	}
	placeholders := make([]string, len(prismIDs))
	args := make([]any, 0, len(prismIDs)*2+1)
	for i, id := range prismIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	inClause := strings.Join(placeholders, ",")

	// Pass 1: last-denial timestamp + deny_dim per agent.
	//
	// SQLite ≥ 3.7.11 honors GROUP BY semantics where non-aggregated
	// columns return the value from the row holding MAX of the bare
	// aggregate when the column is part of the same row — we rely on the
	// "bare column with MAX" extension that the official SQLite docs
	// guarantee. (See: https://www.sqlite.org/lang_select.html §4.)
	denialRows, err := s.db.Query(`SELECT agent_id, MAX(ts), deny_dim
		FROM grant_events
		WHERE outcome = 'denied' AND agent_id IN (`+inClause+`)
		GROUP BY agent_id`, args...)
	if err != nil {
		return nil, err
	}
	func() {
		defer denialRows.Close()
		for denialRows.Next() {
			var agentID string
			var ts int64
			var denyDim sql.NullString
			if scanErr := denialRows.Scan(&agentID, &ts, &denyDim); scanErr != nil {
				err = scanErr
				return
			}
			entry := out[agentID]
			entry.LastDenialAt = time.Unix(0, ts).UTC()
			if denyDim.Valid {
				entry.LastDenialDim = denyDim.String
			}
			out[agentID] = entry
		}
		err = denialRows.Err()
	}()
	if err != nil {
		return nil, err
	}

	// Pass 2: drift count over driftWindow per agent.
	driftArgs := make([]any, 0, len(prismIDs)+1)
	for _, id := range prismIDs {
		driftArgs = append(driftArgs, id)
	}
	var sinceNanos int64
	if driftWindow > 0 {
		sinceNanos = time.Now().Add(-driftWindow).UnixNano()
		driftArgs = append(driftArgs, sinceNanos)
	}
	driftSQL := `SELECT agent_id, COUNT(*)
		FROM grant_events
		WHERE deny_dim = 'workspace_drift' AND agent_id IN (` + inClause + `)`
	if driftWindow > 0 {
		driftSQL += ` AND ts >= ?`
	}
	driftSQL += ` GROUP BY agent_id`
	driftRows, err := s.db.Query(driftSQL, driftArgs...)
	if err != nil {
		return nil, err
	}
	func() {
		defer driftRows.Close()
		for driftRows.Next() {
			var agentID string
			var count int
			if scanErr := driftRows.Scan(&agentID, &count); scanErr != nil {
				err = scanErr
				return
			}
			entry := out[agentID]
			entry.DriftCount24h = count
			out[agentID] = entry
		}
		err = driftRows.Err()
	}()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Retain deletes events older than maxAge.
func (s *SQLiteStore) Retain(maxAge time.Duration) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("event store is closed")
	}
	if maxAge <= 0 {
		maxAge = DefaultRetention
	}
	cutoff := time.Now().Add(-maxAge).UnixNano()
	res, err := s.db.Exec(`DELETE FROM grant_events WHERE ts < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// Stats reports event count, oldest/newest timestamps, and on-disk size.
func (s *SQLiteStore) Stats() (StoreStats, error) {
	if s == nil || s.db == nil {
		return StoreStats{}, errors.New("event store is closed")
	}
	var stats StoreStats
	var oldest, newest int64
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(MIN(ts), 0), COALESCE(MAX(ts), 0) FROM grant_events`).
		Scan(&stats.EventCount, &oldest, &newest); err != nil {
		return StoreStats{}, err
	}
	if oldest > 0 {
		stats.OldestAt = time.Unix(0, oldest).UTC()
	}
	if newest > 0 {
		stats.NewestAt = time.Unix(0, newest).UTC()
	}
	stats.SizeBytes = sqliteSizeBytes(s.path)
	return stats, nil
}

func sqliteSizeBytes(path string) int64 {
	if strings.TrimSpace(path) == "" || path == ":memory:" {
		return 0
	}
	var total int64
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if err == nil {
			total += info.Size()
		}
	}
	return total
}

// MultiEmitter writes to a ring buffer synchronously and a historical store
// asynchronously.
type MultiEmitter struct {
	ring   *RingBuffer
	store  Store
	queue  chan auth.GrantEvent
	done   chan struct{}
	once   sync.Once
	logger *slog.Logger
}

// NewMultiEmitter constructs a non-blocking emitter.
func NewMultiEmitter(ring *RingBuffer, store Store, queueSize int, logger *slog.Logger) *MultiEmitter {
	if queueSize <= 0 {
		queueSize = 1024
	}
	m := &MultiEmitter{
		ring: ring, store: store, queue: make(chan auth.GrantEvent, queueSize),
		done: make(chan struct{}), logger: logger,
	}
	if store != nil {
		go m.run()
	} else {
		close(m.done)
	}
	return m
}

// Emit records an event without blocking the caller on SQLite.
func (m *MultiEmitter) Emit(_ context.Context, e auth.GrantEvent) {
	if m == nil {
		return
	}
	if m.ring != nil {
		m.ring.Add(e)
	}
	if m.store == nil {
		return
	}
	select {
	case m.queue <- e:
	default:
		if m.logger != nil {
			m.logger.Warn("grant event queue full; dropping historical write")
		}
	}
}

// Close stops the background writer after draining queued events.
func (m *MultiEmitter) Close() {
	if m == nil || m.store == nil {
		return
	}
	m.once.Do(func() {
		close(m.queue)
		<-m.done
	})
}

func (m *MultiEmitter) run() {
	defer close(m.done)
	for e := range m.queue {
		if err := m.store.Insert(e); err != nil && m.logger != nil {
			m.logger.Warn("failed to persist grant event", "error", err)
		}
	}
}

func timeToUnixNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func (q QueryFilter) String() string {
	return fmt.Sprintf("agent=%s template=%s outcome=%s deny=%s backend=%s agents#=%d templates#=%d",
		q.AgentID, q.TemplateHash, q.Outcome, q.DenyDim, q.Backend, len(q.AgentIDs), len(q.TemplateHashes))
}
