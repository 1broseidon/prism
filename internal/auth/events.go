package auth

import (
	"context"
	"time"
)

// GrantEvent is the canonical authorization decision event.
type GrantEvent struct {
	Timestamp     time.Time
	RequestID     string
	AgentID       string
	ClientID      string
	DPoPjkt       string
	Backend       string
	Tool          string
	CallArgsHash  string
	WorkspaceID   string
	WorkspaceType string
	Outcome       string
	TemplateID    string
	TemplateHash  string
	MatchedIndex  int
	Trace         GrantTrace
	AuthTime      time.Time
	Acr           string
	TokenJTI      string
}

// GrantTrace records the four-axis grant decision.
type GrantTrace struct {
	What    AxisResult
	Context AxisResult
	When    AxisResult
	How     AxisResult
	DenyDim string
	Drift   *DriftPair
}

// AxisResult is one axis decision.
type AxisResult struct {
	Verdict string
	Detail  string
	Layer   string
}

// DriftPair captures pinned-vs-live config drift.
type DriftPair struct {
	GrantHash string
	LiveHash  string
}

// Emitter is the hot-path grant event interface.
type Emitter interface {
	Emit(ctx context.Context, e GrantEvent)
}
