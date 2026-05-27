package admin

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

func TestVerbsTableHasShippedVerbs(t *testing.T) {
	want := map[string]string{
		"write-files":  "Write files",
		"read-files":   "Read files",
		"read-github":  "Read github issues",
		"write-github": "Create github issues / PRs",
		"deploy":       "Deploy",
		"read-data":    "Read data",
		"call-tools":   "Call tools (anything enabled)",
	}
	for slug, label := range want {
		v, ok := FindVerb(slug)
		if !ok {
			t.Errorf("missing verb %q", slug)
			continue
		}
		if v.Label != label {
			t.Errorf("verb %q label = %q want %q", slug, v.Label, label)
		}
	}
}

func TestResolveVerb_UnknownReturnsError(t *testing.T) {
	_, err := ResolveVerb("nope", []string{"fs"})
	if !errors.Is(err, ErrUnknownVerb) {
		t.Fatalf("expected ErrUnknownVerb, got %v", err)
	}
}

func TestResolveVerb_FiltersByEnabledBackends(t *testing.T) {
	got, err := ResolveVerb("write-files", []string{"fs", "github"})
	if err != nil {
		t.Fatalf("ResolveVerb: %v", err)
	}
	want := []ResolvedTool{
		{Backend: "fs", Tool: "append_file"},
		{Backend: "fs", Tool: "create_dir"},
		{Backend: "fs", Tool: "delete_file"},
		{Backend: "fs", Tool: "write_file"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("write-files resolution mismatch\n got = %+v\nwant = %+v", got, want)
	}
}

func TestResolveVerb_FilesystemAliasDeduped(t *testing.T) {
	// Both "fs" and "filesystem" patterns exist for read-files; enable both
	// and confirm dedup keeps the two backends distinct but neither produces
	// duplicate tools.
	got, err := ResolveVerb("read-files", []string{"fs", "filesystem"})
	if err != nil {
		t.Fatalf("ResolveVerb: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("expected 6 pairs (3 tools * 2 backends), got %d: %+v", len(got), got)
	}
	// Spot-check ordering is deterministic.
	sorted := make([]ResolvedTool, len(got))
	copy(sorted, got)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Backend != sorted[j].Backend {
			return sorted[i].Backend < sorted[j].Backend
		}
		return sorted[i].Tool < sorted[j].Tool
	})
	if !reflect.DeepEqual(got, sorted) {
		t.Fatalf("output not sorted: %+v", got)
	}
}

func TestResolveVerb_WildcardBackendExpandsAcrossEnabled(t *testing.T) {
	got, err := ResolveVerb("deploy", []string{"k8s", "render"})
	if err != nil {
		t.Fatalf("ResolveVerb: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("expected 6 pairs (2 backends * 3 tools), got %d: %+v", len(got), got)
	}
	for _, rt := range got {
		if rt.Backend != "k8s" && rt.Backend != "render" {
			t.Errorf("unexpected backend %q", rt.Backend)
		}
	}
}

func TestResolveVerb_NoEnabledBackendsReturnsEmpty(t *testing.T) {
	got, err := ResolveVerb("write-files", nil)
	if err != nil {
		t.Fatalf("ResolveVerb: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no pairs, got %+v", got)
	}
}

func TestResolveVerb_BackendNotEnabledDroppedSilently(t *testing.T) {
	got, err := ResolveVerb("read-github", []string{"fs"})
	if err != nil {
		t.Fatalf("ResolveVerb: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no pairs (github disabled), got %+v", got)
	}
}

func TestScopeStringsForVerb_ProducesCanonicalForm(t *testing.T) {
	scopes, err := ScopeStringsForVerb("write-files", []string{"fs"})
	if err != nil {
		t.Fatalf("ScopeStringsForVerb: %v", err)
	}
	want := []string{
		"fs:append_file",
		"fs:create_dir",
		"fs:delete_file",
		"fs:write_file",
	}
	if !reflect.DeepEqual(scopes, want) {
		t.Fatalf("scopes mismatch\n got = %v\nwant = %v", scopes, want)
	}
}
