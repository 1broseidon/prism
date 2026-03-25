// Package audit provides structured JSON audit logging for Prism.
//
// Every tool call — allowed or denied — produces a single-line JSON entry
// so security teams can ingest the log into any SIEM without additional parsing.
package audit

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	"github.com/1broseidon/prism/internal/auth"
)

// Entry is a single structured audit log record.
type Entry struct {
	// Timestamp is the UTC time the entry was recorded (RFC 3339).
	Timestamp string `json:"ts"`
	// Subject is the OAuth token subject ("sub" claim). Empty if unauthenticated.
	Subject string `json:"subject"`
	// ClientID is the OAuth client_id claim. Empty if unauthenticated.
	ClientID string `json:"client_id"`
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

// Logger writes structured JSON audit log entries, one per line.
// All methods are safe for concurrent use.
type Logger struct {
	mu  sync.Mutex
	out io.Writer
}

// New returns an audit Logger that writes to w.
// If w is nil, os.Stderr is used.
func New(w io.Writer) *Logger {
	if w == nil {
		w = os.Stderr
	}
	return &Logger{out: w}
}

// Noop returns a Logger that discards all entries.
// Use this when audit logging is disabled so call-sites need no nil checks.
func Noop() *Logger {
	return &Logger{out: io.Discard}
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
	}

	if callErr != nil {
		entry.Error = callErr.Error()
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return // should never happen with a flat struct
	}
	data = append(data, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.out.Write(data)
}
