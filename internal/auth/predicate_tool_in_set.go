package auth

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
)

// ToolInSetMaxEntries is the v1 cap on tool_in_set predicate entries.
//
// Capped so a single verb-with-constraints template doesn't compile into an
// unbounded match list. 64 covers every shipped verb's resolved (backend,
// tool) pairs with headroom; operators with more should split into multiple
// capabilities (each capability is one verb anyway).
const ToolInSetMaxEntries = 64

// toolInSetEntryRE matches one entry in a tool_in_set predicate. Format is
// "<backend>:<tool>" where each side is one identifier segment.
//
//   - backend: 1..64 chars of [A-Za-z0-9_.-], must not start with '.' or '-'
//   - tool:    1..128 chars of [A-Za-z0-9_.-*], must not start with '.' or '-'
//     (* allowed only as the entire tool segment)
//
// The trailing "*" form is permitted so verbs that resolve to a backend
// wildcard (e.g. the "call-tools" verb shipping with Patterns Backend "*"
// Tools "*") still produce valid entries. A literal tool name like
// "list_issues" is the common case.
var toolInSetEntryRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}:(?:\*|[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})$`)

// ValidateToolInSet checks a tool_in_set predicate value list and canonicalizes
// it in place by sorting the entries lexicographically.
//
// Rejects:
//   - empty list
//   - more than ToolInSetMaxEntries entries
//   - entries that don't match the "<backend>:<tool>" format
//
// Each entry is one canonical "backend:tool" string. The matcher compares this
// directly against the call's "<backend>:<tool>" composition (see MatchGrants).
//
// Sorting after validation makes ComputeTemplateHash deterministic across
// callers who construct equivalent sets in different orders — two operators
// authoring the same capability shouldn't fall out of dedup just because their
// entry order differs. The mutation is in-place so callers using a value
// receiver still benefit (slice headers share the underlying array).
func ValidateToolInSet(entries []string) error {
	if len(entries) == 0 {
		return errors.New("tool_in_set must not be empty")
	}
	if len(entries) > ToolInSetMaxEntries {
		return fmt.Errorf("tool_in_set must contain at most %d entries (got %d)", ToolInSetMaxEntries, len(entries))
	}
	seen := make(map[string]struct{}, len(entries))
	for i, e := range entries {
		if !toolInSetEntryRE.MatchString(e) {
			return fmt.Errorf("tool_in_set[%d] = %q does not match backend:tool format", i, e)
		}
		if _, dup := seen[e]; dup {
			return fmt.Errorf("tool_in_set[%d] = %q is duplicated", i, e)
		}
		seen[e] = struct{}{}
	}
	sort.Strings(entries)
	return nil
}

// matchToolInSet reports whether v (a "<backend>:<tool>" string) is in the
// predicate's set. v is normally synthesized by the matcher from the call's
// Backend + Tool before predicate evaluation.
func matchToolInSet(v any, entries []string) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, e := range entries {
		if e == s {
			return true
		}
	}
	return false
}
