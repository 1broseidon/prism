package admin

import (
	"errors"
	"sort"
)

// Verb is one entry in the policy-builder verb vocabulary. Verbs are the
// "default surface" of the action picker — operators pick a verb instead of
// hunting through every individual tool when they want a familiar grouping
// like "write files" or "read github issues".
//
// Each verb expands at save time (not request time) into a concrete set of
// (backend, tool) pairs via ResolveVerb. The expansion is filtered by the
// list of currently-enabled backends so a verb shipping with a fs+filesystem
// fallback pattern doesn't add scopes for a backend the operator never
// configured.
type Verb struct {
	Slug     string        `json:"slug"`
	Label    string        `json:"label"`
	Patterns []ToolPattern `json:"patterns"`
}

// ToolPattern is one (backend, tools) entry within a Verb. Backend may be
// the literal "*" wildcard to match every enabled backend; Tools entries may
// be the literal "*" to mean "every tool on that backend".
type ToolPattern struct {
	Backend string   `json:"backend"`
	Tools   []string `json:"tools"`
}

// ResolvedTool is one concrete (backend, tool) pair produced by ResolveVerb.
type ResolvedTool struct {
	Backend string `json:"backend"`
	Tool    string `json:"tool"`
}

// Verbs is the hard-coded v1 vocabulary shipped with prism. Editing requires
// a recompile; operators who need finer-grained control use "Pick specific
// tool…" in the action picker or fall back to Power Tools mode.
//
// Order is the display order in the operator picker — chosen so the common
// cases (read/write files; github read; github write) sit at the top.
//
// nolint:gochecknoglobals // intentional package-level table per spec §8.
var Verbs = []Verb{
	{
		Slug:  "write-files",
		Label: "Write files",
		Patterns: []ToolPattern{
			{Backend: "fs", Tools: []string{"write_file", "append_file", "delete_file", "create_dir"}},
			{Backend: "filesystem", Tools: []string{"write_file", "append_file", "delete_file", "create_dir"}},
		},
	},
	{
		Slug:  "read-files",
		Label: "Read files",
		Patterns: []ToolPattern{
			{Backend: "fs", Tools: []string{"read_file", "list", "stat"}},
			{Backend: "filesystem", Tools: []string{"read_file", "list", "stat"}},
		},
	},
	{
		Slug:  "read-github",
		Label: "Read github issues",
		Patterns: []ToolPattern{
			{Backend: "github", Tools: []string{"list_issues", "get_issue", "search_issues", "list_comments"}},
		},
	},
	{
		Slug:  "write-github",
		Label: "Create github issues / PRs",
		Patterns: []ToolPattern{
			{Backend: "github", Tools: []string{"create_issue", "create_pull_request", "create_comment"}},
		},
	},
	{
		Slug:  "deploy",
		Label: "Deploy",
		Patterns: []ToolPattern{
			{Backend: "*", Tools: []string{"deploy", "release", "rollout"}},
		},
	},
	{
		Slug:  "read-data",
		Label: "Read data",
		Patterns: []ToolPattern{
			{Backend: "postgres", Tools: []string{"query", "select"}},
			{Backend: "mysql", Tools: []string{"query", "select"}},
			{Backend: "redis", Tools: []string{"get", "scan"}},
		},
	},
	{
		Slug:     "call-tools",
		Label:    "Call tools (anything enabled)",
		Patterns: []ToolPattern{{Backend: "*", Tools: []string{"*"}}},
	},
}

// ErrUnknownVerb is returned when a slug has no matching entry in the Verbs
// table. Handlers surface this as 404; resolution failure inside the compiler
// surfaces as a 400 (operator provided a bad slug).
var ErrUnknownVerb = errors.New("unknown verb")

// FindVerb returns the verb with the given slug, or false if absent.
func FindVerb(slug string) (Verb, bool) {
	for _, v := range Verbs {
		if v.Slug == slug {
			return v, true
		}
	}
	return Verb{}, false
}

// ResolveVerb expands a verb slug into concrete (backend, tool) pairs filtered
// to the set of currently-enabled backends.
//
// Rules:
//   - A Pattern with Backend "*" expands across every enabled backend.
//   - A Pattern with a concrete Backend is dropped if that backend is not
//     enabled (the operator has not configured it).
//   - A Tools list containing "*" produces one ResolvedTool per backend with
//     Tool == "*"; this is the "wildcard tool on this backend" form and is
//     handled by the matcher's existing wildcard support in MatchGrants.
//
// Duplicate (backend, tool) pairs are dropped; output is sorted (backend,
// tool) for determinism.
func ResolveVerb(slug string, enabledBackends []string) ([]ResolvedTool, error) {
	v, ok := FindVerb(slug)
	if !ok {
		return nil, ErrUnknownVerb
	}
	if len(enabledBackends) == 0 {
		return nil, nil
	}
	enabledSet := make(map[string]struct{}, len(enabledBackends))
	for _, b := range enabledBackends {
		if b == "" {
			continue
		}
		enabledSet[b] = struct{}{}
	}

	seen := make(map[ResolvedTool]struct{})
	out := make([]ResolvedTool, 0)
	for _, pat := range v.Patterns {
		var backends []string
		if pat.Backend == "*" {
			backends = make([]string, 0, len(enabledSet))
			for b := range enabledSet {
				backends = append(backends, b)
			}
		} else if _, ok := enabledSet[pat.Backend]; ok {
			backends = []string{pat.Backend}
		} else {
			continue
		}
		for _, b := range backends {
			for _, tool := range pat.Tools {
				rt := ResolvedTool{Backend: b, Tool: tool}
				if _, dup := seen[rt]; dup {
					continue
				}
				seen[rt] = struct{}{}
				out = append(out, rt)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Backend != out[j].Backend {
			return out[i].Backend < out[j].Backend
		}
		return out[i].Tool < out[j].Tool
	})
	return out, nil
}

// ScopeStringsForVerb is the convenience wrapper used by the compile-down
// router when a verb capability has NO constraints: each (backend, tool) pair
// becomes one scope string of the form "backend:tool". This is the canonical
// scope format checked by the existing scope policy at request time.
func ScopeStringsForVerb(slug string, enabledBackends []string) ([]string, error) {
	pairs, err := ResolveVerb(slug, enabledBackends)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p.Backend+":"+p.Tool)
	}
	return out, nil
}
