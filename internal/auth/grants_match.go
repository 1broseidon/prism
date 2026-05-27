package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CallContext + MatchResult are legacy aliases retained for callers that
// imported the early task-21 names. New code should use GrantCall / GrantMatchResult.
type CallContext = GrantCall
type MatchResult = GrantMatchResult

// GrantCall is the normalized request state evaluated by MatchGrants.
type GrantCall struct {
	Tool      string
	Backend   string
	Arguments json.RawMessage
	Workspace *WorkspaceInstance
	Now       time.Time
	AuthTime  int64
	Acr       string
}

// GrantMatchResult describes the matcher outcome.
type GrantMatchResult struct {
	Allowed      bool
	DenyDim      string
	Grant        *IssuedGrant
	MatchedIndex int
	Detail       string
}

// MatchGrant is the task-21 alias for MatchGrants.
func MatchGrant(call CallContext, grants []IssuedGrant) MatchResult {
	return MatchGrants(call, grants)
}

// MatchGrants evaluates token grants against one tool call.
func MatchGrants(call GrantCall, grants []IssuedGrant) GrantMatchResult {
	if call.Now.IsZero() {
		call.Now = time.Unix(0, 0)
	}
	bestRank := -1
	best := GrantMatchResult{DenyDim: GrantDenyNoGrant, MatchedIndex: -1}
	for i := range grants {
		g := &grants[i]
		if g.Type != "" && g.Type != GrantTypeMCPCall {
			continue
		}
		// "*" on Tool/Backend matches any concrete value. Used by verb-shape
		// templates that cover several (backend, tool) pairs and then refine
		// via the tool_in_set predicate below.
		if g.Tool != "*" && g.Tool != call.Tool {
			continue
		}
		if g.Backend != "*" && g.Backend != call.Backend {
			continue
		}
		if ok, detail := grantArgsMatchWithCall(g.Args, call.Arguments, call); !ok {
			bestRank, best = preferDeny(bestRank, best, 1, GrantMatchResult{
				DenyDim: GrantDenyArgs, Grant: g, MatchedIndex: i, Detail: detail,
			})
			continue
		}
		now := call.Now.Unix()
		if g.NotBefore != 0 && now < g.NotBefore {
			bestRank, best = preferDeny(bestRank, best, 2, GrantMatchResult{DenyDim: GrantDenyNotYet, Grant: g, MatchedIndex: i})
			continue
		}
		if g.ExpiresAt != 0 && now > g.ExpiresAt {
			bestRank, best = preferDeny(bestRank, best, 2, GrantMatchResult{DenyDim: GrantDenyExpired, Grant: g, MatchedIndex: i})
			continue
		}
		if g.Hours != "" && !TimeInGrantHours(call.Now, g.Hours) {
			bestRank, best = preferDeny(bestRank, best, 2, GrantMatchResult{DenyDim: GrantDenyOutOfWindow, Grant: g, MatchedIndex: i})
			continue
		}
		if g.AuthFreshnessMax != 0 {
			if call.AuthTime == 0 || now-call.AuthTime > g.AuthFreshnessMax {
				bestRank, best = preferDeny(bestRank, best, 3, GrantMatchResult{DenyDim: GrantDenyNeedsStepUp, Grant: g, MatchedIndex: i})
				continue
			}
		}
		if g.AcrRequired != "" && call.Acr != g.AcrRequired {
			bestRank, best = preferDeny(bestRank, best, 4, GrantMatchResult{DenyDim: GrantDenyACRRequired, Grant: g, MatchedIndex: i})
			continue
		}
		if g.Workspace != nil {
			if ok, _ := CompareWorkspace(g.Workspace, call.Workspace); !ok {
				bestRank, best = preferDeny(bestRank, best, 5, GrantMatchResult{DenyDim: GrantDenyWorkspaceDrift, Grant: g, MatchedIndex: i})
				continue
			}
		}
		return GrantMatchResult{Allowed: true, DenyDim: GrantDenyNone, Grant: g, MatchedIndex: i}
	}
	return best
}

// CompareWorkspace compares a grant-pinned workspace instance to the live
// workspace instance and returns a drift pair when they diverge.
func CompareWorkspace(grant, live *WorkspaceInstance) (bool, *DriftPair) {
	if grant == nil {
		return true, nil
	}
	if live != nil && *grant == *live {
		return true, nil
	}
	return false, &DriftPair{
		GrantHash: WorkspaceInstanceHash(grant),
		LiveHash:  WorkspaceInstanceHash(live),
	}
}

// WorkspaceInstanceHash returns the canonical drift hash for a workspace tuple.
func WorkspaceInstanceHash(ws *WorkspaceInstance) string {
	if ws == nil {
		return ""
	}
	data, _ := json.Marshal(ws)
	sum := sha256.Sum256(data)
	return "sha256-" + hex.EncodeToString(sum[:])
}

// TimeInGrantHours evaluates the narrow v1 hours grammar:
//
//	"09:00-18:00 America/Toronto"
//	"weekdays 09:00-18:00 America/Toronto"
//	"Mon-Fri 09:00-18:00 America/Toronto"
func TimeInGrantHours(t time.Time, expr string) bool {
	window, err := parseHoursWindow(expr)
	if err != nil {
		return false
	}
	local := t.In(window.Location)
	if len(window.Weekdays) > 0 && !window.Weekdays[local.Weekday()] {
		return false
	}
	minute := local.Hour()*60 + local.Minute()
	if window.StartMinute <= window.EndMinute {
		return minute >= window.StartMinute && minute < window.EndMinute
	}
	return minute >= window.StartMinute || minute < window.EndMinute
}

type hoursWindow struct {
	Weekdays    map[time.Weekday]bool
	StartMinute int
	EndMinute   int
	Location    *time.Location
}

func parseHoursWindow(expr string) (hoursWindow, error) {
	fields := strings.Fields(expr)
	if len(fields) != 2 && len(fields) != 3 {
		return hoursWindow{}, errors.New("expected '[days] HH:MM-HH:MM TZ'")
	}
	var days, span, tz string
	if len(fields) == 2 {
		span, tz = fields[0], fields[1]
	} else {
		days, span, tz = fields[0], fields[1], fields[2]
	}
	parts := strings.Split(span, "-")
	if len(parts) != 2 {
		return hoursWindow{}, errors.New("expected time range HH:MM-HH:MM")
	}
	start, err := parseHourMinute(parts[0])
	if err != nil {
		return hoursWindow{}, err
	}
	end, err := parseHourMinute(parts[1])
	if err != nil {
		return hoursWindow{}, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return hoursWindow{}, err
	}
	weekdays, err := parseWeekdays(days)
	if err != nil {
		return hoursWindow{}, err
	}
	return hoursWindow{Weekdays: weekdays, StartMinute: start, EndMinute: end, Location: loc}, nil
}

func parseHourMinute(s string) (int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	return h*60 + m, nil
}

func parseWeekdays(s string) (map[time.Weekday]bool, error) {
	if s == "" {
		return nil, nil
	}
	if strings.EqualFold(s, "weekdays") {
		return map[time.Weekday]bool{
			time.Monday: true, time.Tuesday: true, time.Wednesday: true,
			time.Thursday: true, time.Friday: true,
		}, nil
	}
	names := map[string]time.Weekday{
		"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday,
		"wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday,
		"sat": time.Saturday,
	}
	parts := strings.Split(s, "-")
	if len(parts) == 1 {
		day, ok := names[strings.ToLower(parts[0])]
		if !ok {
			return nil, fmt.Errorf("invalid weekday %q", s)
		}
		return map[time.Weekday]bool{day: true}, nil
	}
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid weekday range %q", s)
	}
	start, ok := names[strings.ToLower(parts[0])]
	if !ok {
		return nil, fmt.Errorf("invalid weekday %q", parts[0])
	}
	end, ok := names[strings.ToLower(parts[1])]
	if !ok {
		return nil, fmt.Errorf("invalid weekday %q", parts[1])
	}
	out := map[time.Weekday]bool{}
	for d := start; ; d = (d + 1) % 7 {
		out[d] = true
		if d == end {
			break
		}
	}
	return out, nil
}

func preferDeny(bestRank int, best GrantMatchResult, rank int, candidate GrantMatchResult) (int, GrantMatchResult) {
	if rank >= bestRank {
		return rank, candidate
	}
	return bestRank, best
}

func intersects(a, b []string) bool {
	for _, x := range a {
		if x != "" && slices.Contains(b, x) {
			return true
		}
	}
	return false
}
