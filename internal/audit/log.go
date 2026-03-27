// Package audit provides structured JSON audit logging for Prism.
//
// Every tool call — allowed or denied — produces a single-line JSON entry
// so security teams can ingest the log into any SIEM without additional parsing.
//
// When a KV store is configured, entries are persisted and survive restarts.
// A configurable retention period (default 30 days) automatically cleans up
// old entries.
package audit

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/1broseidon/prism/internal/auth"
)

// kvStore is the subset of store.Store used by the audit logger.
// Defined here to avoid importing the store package.
type kvStore interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
	List(prefix string) ([]string, error)
}

// Entry is a single structured audit log record.
type Entry struct {
	// Timestamp is the UTC time the entry was recorded (RFC 3339).
	Timestamp string `json:"ts"`
	// Subject is the OAuth token subject ("sub" claim). Empty if unauthenticated.
	Subject string `json:"subject"`
	// ClientID is the OAuth client_id claim. Empty if unauthenticated.
	ClientID string `json:"client_id"`
	// PrismID is the stable agent identity from the JWT prism_id claim.
	// Present only for DCR agents that have been consented. For audit enrichment only.
	PrismID string `json:"prism_id,omitempty"`
	// Namespace is the backend namespace (e.g. "github").
	Namespace string `json:"namespace"`
	// Tool is the unqualified tool name (e.g. "create_issue").
	Tool string `json:"tool"`
	// Allowed is true if the call was permitted, false if denied by policy.
	Allowed bool `json:"allowed"`
	// LatencyMS is the round-trip time to the backend in milliseconds.
	// Zero for denied calls (they never reach the backend).
	LatencyMS int64 `json:"latency_ms"`
	// Backend is the backend server ID.
	Backend string `json:"backend"`
	// Error holds the error message if the call failed, empty otherwise.
	Error string `json:"error"`
	// CredInjected is true if Prism injected a backend credential for this call.
	// The credential value itself is never logged.
	CredInjected bool `json:"cred_injected"`
}

const (
	recentCap        = 500
	auditKVPrefix    = "audit/"
	defaultRetention = 30 * 24 * time.Hour // 30 days
)

// Logger writes structured JSON audit log entries, one per line.
// Keeps the most recent entries in memory for the admin dashboard.
// Optionally persists to a KV store for restart survival.
// All methods are safe for concurrent use.
type Logger struct {
	mu        sync.Mutex
	out       io.Writer
	recent    []Entry
	store     kvStore
	retention time.Duration
	seqN      int64 // monotonic counter for unique KV keys within the same second
}

// New returns an audit Logger that writes to w.
// If w is nil, os.Stderr is used.
func New(w io.Writer) *Logger {
	if w == nil {
		w = os.Stderr
	}
	return &Logger{out: w, recent: make([]Entry, 0, recentCap), retention: defaultRetention}
}

// Noop returns a Logger that discards all entries.
// Use this when audit logging is disabled so call-sites need no nil checks.
func Noop() *Logger {
	return &Logger{out: io.Discard, recent: make([]Entry, 0, recentCap)}
}

// SetStore enables KV persistence for audit entries.
// Call this before serving requests. When set, entries are written to the KV
// store and restored on startup.
func (l *Logger) SetStore(s kvStore) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store = s
}

// SetRetention sets the retention period for persisted audit entries.
// Entries older than this are deleted during cleanup. Default: 30 days.
func (l *Logger) SetRetention(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.retention = d
}

// LoadPersistedEntries restores audit entries from the KV store into the
// in-memory ring buffer. Call this after SetStore during startup.
func (l *Logger) LoadPersistedEntries() {
	l.mu.Lock()
	store := l.store
	l.mu.Unlock()

	if store == nil {
		return
	}

	keys, err := store.List(auditKVPrefix)
	if err != nil {
		return
	}

	// Sort keys chronologically (they're timestamped).
	sort.Strings(keys)

	// Load only the most recent recentCap entries.
	start := 0
	if len(keys) > recentCap {
		start = len(keys) - recentCap
	}

	entries := make([]Entry, 0, recentCap)
	for _, key := range keys[start:] {
		data, err := store.Get(key)
		if err != nil {
			continue
		}
		var e Entry
		if err := json.Unmarshal(data, &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	l.mu.Lock()
	l.recent = entries
	l.mu.Unlock()
}

// Cleanup removes audit entries older than the retention period.
// Call this periodically (e.g. once per hour) or at startup.
func (l *Logger) Cleanup() {
	l.mu.Lock()
	store := l.store
	retention := l.retention
	l.mu.Unlock()

	if store == nil {
		return
	}

	cutoff := time.Now().Add(-retention).UTC().Format(time.RFC3339)

	keys, err := store.List(auditKVPrefix)
	if err != nil {
		return
	}

	for _, key := range keys {
		// Key format: audit/{timestamp}_{seq}
		ts := strings.TrimPrefix(key, auditKVPrefix)
		if idx := strings.IndexByte(ts, '_'); idx > 0 {
			ts = ts[:idx]
		}
		if ts < cutoff {
			_ = store.Delete(key)
		}
	}
}

// Recent returns the most recent audit entries (up to 500), newest first.
func (l *Logger) Recent() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Return a reversed copy.
	out := make([]Entry, len(l.recent))
	for i := range l.recent {
		out[len(l.recent)-1-i] = l.recent[i]
	}
	return out
}

// LogCall records the outcome of a single tool call.
//
//   - ctx must carry the HTTP request context so that OAuth claims can be extracted.
//   - namespace and tool are the backend namespace and unqualified tool name.
//   - backend is the backend server ID.
//   - allowed indicates whether the call passed scope policy.
//   - credInjected is true if Prism injected a backend credential (value is never logged).
//   - latencyMS is the backend round-trip time; pass 0 for denied calls.
//   - callErr is any error returned by the backend (nil on success or denial).
func (l *Logger) LogCall(ctx context.Context, namespace, tool, backend string, allowed, credInjected bool, latencyMS int64, callErr error) {
	if l == nil {
		return
	}

	entry := Entry{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Namespace:    namespace,
		Tool:         tool,
		Backend:      backend,
		Allowed:      allowed,
		CredInjected: credInjected,
		LatencyMS:    latencyMS,
	}

	if claims := auth.ClaimsFromContext(ctx); claims != nil {
		entry.Subject = claims.Subject
		entry.ClientID = claims.ClientID
		entry.PrismID = claims.PrismID
	}

	if callErr != nil {
		entry.Error = callErr.Error()
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return // should never happen with a flat struct
	}

	l.mu.Lock()
	_, _ = l.out.Write(append(data, '\n'))

	// Ring buffer: keep last recentCap entries for the admin dashboard.
	if len(l.recent) >= recentCap {
		copy(l.recent, l.recent[1:])
		l.recent[len(l.recent)-1] = entry
	} else {
		l.recent = append(l.recent, entry)
	}

	// Persist to KV store if configured.
	if l.store != nil {
		l.seqN++
		key := auditKVPrefix + entry.Timestamp + "_" + itoa(l.seqN)
		_ = l.store.Set(key, data)
	}

	l.mu.Unlock()
}

// itoa converts an int64 to a string without importing strconv.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
