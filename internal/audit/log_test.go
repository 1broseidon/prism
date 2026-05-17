package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/audit"
)

func TestLogCall_AllowedNoAuth(t *testing.T) {
	var buf bytes.Buffer
	l := audit.New(&buf)

	l.LogCall(context.Background(), "github", "create_issue", "github-backend", true, false, 142, nil)

	if buf.Len() == 0 {
		t.Fatal("expected log output, got none")
	}

	var entry audit.Entry
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, buf.String())
	}

	if entry.Namespace != "github" {
		t.Errorf("namespace = %q, want %q", entry.Namespace, "github")
	}
	if entry.Tool != "create_issue" {
		t.Errorf("tool = %q, want %q", entry.Tool, "create_issue")
	}
	if entry.Backend != "github-backend" {
		t.Errorf("backend = %q, want %q", entry.Backend, "github-backend")
	}
	if !entry.Allowed {
		t.Error("allowed = false, want true")
	}
	if entry.CredInjected {
		t.Error("cred_injected = true, want false")
	}
	if entry.LatencyMS != 142 {
		t.Errorf("latency_ms = %d, want 142", entry.LatencyMS)
	}
	if entry.Error != "" {
		t.Errorf("error = %q, want empty", entry.Error)
	}
	if entry.Subject != "" {
		t.Errorf("subject = %q, want empty (no auth claims in ctx)", entry.Subject)
	}
	if entry.Timestamp == "" {
		t.Error("timestamp must not be empty")
	}
}

func TestLogCall_DeniedEntry(t *testing.T) {
	var buf bytes.Buffer
	l := audit.New(&buf)

	l.LogCall(context.Background(), "fs", "delete_file", "fs-backend", false, false, 0, nil)

	var entry audit.Entry
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if entry.Allowed {
		t.Error("allowed = true, want false")
	}
	if entry.LatencyMS != 0 {
		t.Errorf("latency_ms = %d, want 0 for denied call", entry.LatencyMS)
	}
}

func TestLogCall_WithError(t *testing.T) {
	var buf bytes.Buffer
	l := audit.New(&buf)

	l.LogCall(context.Background(), "github", "list_prs", "github-backend", true, false, 50,
		&testErr{"upstream timeout"})

	var entry audit.Entry
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if !strings.Contains(entry.Error, "upstream timeout") {
		t.Errorf("error = %q, want to contain %q", entry.Error, "upstream timeout")
	}
}

func TestLogCall_CredInjected(t *testing.T) {
	var buf bytes.Buffer
	l := audit.New(&buf)

	l.LogCall(context.Background(), "github", "create_issue", "github-backend", true, true, 50, nil)

	var entry audit.Entry
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !entry.CredInjected {
		t.Error("cred_injected = false, want true")
	}
}

func TestLogCall_MultipleEntriesAreNewlineSeparated(t *testing.T) {
	var buf bytes.Buffer
	l := audit.New(&buf)

	l.LogCall(context.Background(), "ns", "tool1", "be1", true, false, 1, nil)
	l.LogCall(context.Background(), "ns", "tool2", "be1", false, false, 0, nil)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d:\n%s", len(lines), buf.String())
	}

	for i, line := range lines {
		var entry audit.Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
	}
}

func TestNoop_ProducesNoOutput(t *testing.T) {
	l := audit.Noop()
	// Should not panic and produce no output (writes to io.Discard)
	l.LogCall(context.Background(), "ns", "tool", "be", true, false, 0, nil)
}

func TestLogCall_NilLogger_DoesNotPanic(t *testing.T) {
	var l *audit.Logger
	// Nil logger should be a no-op, not a panic
	l.LogCall(context.Background(), "ns", "tool", "be", true, false, 0, nil)
}

// testErr implements error for testing.
type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

func TestLogCall_PolicyTraceFromContext(t *testing.T) {
	var buf bytes.Buffer
	l := audit.New(&buf)

	trace := &audit.PolicyTrace{
		WorkspaceID: "repo-a",
		Selector:    "agent",
		Source:      "defaults",
		Layers: []audit.PolicyTraceLayer{
			{Source: "agent:prism-x"},
			{Source: "defaults", Selector: "agent"},
		},
	}
	ctx := audit.ContextWithPolicyTrace(context.Background(), trace)

	l.LogCall(ctx, "Brainfile", "list_tasks", "brainfile", true, false, 42, nil)

	var entry audit.Entry
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, buf.String())
	}
	if entry.PolicyTrace == nil {
		t.Fatalf("expected policy_trace in entry, got nil; raw=%s", buf.String())
	}
	if entry.PolicyTrace.WorkspaceID != "repo-a" {
		t.Errorf("WorkspaceID = %q, want repo-a", entry.PolicyTrace.WorkspaceID)
	}
	if entry.PolicyTrace.Selector != "agent" || entry.PolicyTrace.Source != "defaults" {
		t.Errorf("selector/source = %q/%q, want agent/defaults",
			entry.PolicyTrace.Selector, entry.PolicyTrace.Source)
	}
	if len(entry.PolicyTrace.Layers) != 2 {
		t.Errorf("layers = %d, want 2", len(entry.PolicyTrace.Layers))
	}
}

func TestLogCall_NoPolicyTraceWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	l := audit.New(&buf)
	l.LogCall(context.Background(), "ns", "tool", "be", true, false, 0, nil)

	var entry audit.Entry
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry.PolicyTrace != nil {
		t.Errorf("expected no policy_trace when context lacks one, got %+v", entry.PolicyTrace)
	}
}
