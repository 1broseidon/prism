package identity

import (
	"errors"
	"strings"
	"testing"

	"github.com/1broseidon/prism/internal/store"
)

func TestAllocateMintsValidULID(t *testing.T) {
	d := New(store.NewMemoryStore())
	ent, err := d.Allocate(KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if !IsULID(ent.ID) {
		t.Fatalf("Allocate returned non-ULID %q", ent.ID)
	}
	if ent.DisplayName != "engineering" {
		t.Fatalf("DisplayName = %q want %q", ent.DisplayName, "engineering")
	}
	if ent.Kind != KindGroup {
		t.Fatalf("Kind = %q want %q", ent.Kind, KindGroup)
	}
	if ent.CreatedAt.IsZero() || ent.UpdatedAt.IsZero() {
		t.Fatalf("timestamps must be set: %+v", ent)
	}
}

func TestAllocateRejectsInvalidDisplayName(t *testing.T) {
	d := New(store.NewMemoryStore())
	// Names are leading/trailing-space trimmed before validation so
	// operator typos don't slip through. The cases below all must
	// still fail after trimming (either trimmed-empty or
	// trimmed-invalid).
	cases := map[string]string{
		"empty":           "",
		"only spaces":     "   ",
		"leading dot":     ".hidden",
		"leading dash":    "-prefix",
		"invalid char":    "engineering!",
		"too long":        strings.Repeat("a", 64),
		"non-ascii":       "engineering€",
		"slash":           "eng/team",
		"newline":         "eng\nteam",
		"leading null":    "\x00engineering",
		"empty after pad": "    \t   ",
	}
	for label, name := range cases {
		_, err := d.Allocate(KindGroup, name)
		if !errors.Is(err, ErrInvalidDisplayName) {
			t.Fatalf("%s: err = %v, want ErrInvalidDisplayName", label, err)
		}
	}
}

func TestAllocateRejectsInvalidKind(t *testing.T) {
	d := New(store.NewMemoryStore())
	_, err := d.Allocate(Kind("notakind"), "engineering")
	if !errors.Is(err, ErrKindMismatch) {
		t.Fatalf("err = %v, want ErrKindMismatch", err)
	}
}

func TestAllocateDuplicateDisplayNameSameKind(t *testing.T) {
	d := New(store.NewMemoryStore())
	if _, err := d.Allocate(KindGroup, "engineering"); err != nil {
		t.Fatalf("first allocate: %v", err)
	}
	_, err := d.Allocate(KindGroup, "engineering")
	if !errors.Is(err, ErrDisplayNameInUse) {
		t.Fatalf("err = %v, want ErrDisplayNameInUse", err)
	}
}

func TestAllocateDuplicateDisplayNameCaseInsensitive(t *testing.T) {
	d := New(store.NewMemoryStore())
	if _, err := d.Allocate(KindGroup, "Engineering"); err != nil {
		t.Fatalf("first allocate: %v", err)
	}
	_, err := d.Allocate(KindGroup, "engineering")
	if !errors.Is(err, ErrDisplayNameInUse) {
		t.Fatalf("case-insensitive dup err = %v, want ErrDisplayNameInUse", err)
	}
}

func TestAllocateSameNameAcrossKindsAllowed(t *testing.T) {
	d := New(store.NewMemoryStore())
	if _, err := d.Allocate(KindGroup, "engineering"); err != nil {
		t.Fatalf("group allocate: %v", err)
	}
	if _, err := d.Allocate(KindRole, "engineering"); err != nil {
		t.Fatalf("role allocate: %v", err)
	}
}

func TestAllocateTrimsDisplayName(t *testing.T) {
	d := New(store.NewMemoryStore())
	ent, err := d.Allocate(KindGroup, "  engineering  ")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if ent.DisplayName != "engineering" {
		t.Fatalf("DisplayName = %q want trimmed %q", ent.DisplayName, "engineering")
	}
	// Lookup by the un-trimmed name should still resolve.
	out, err := d.ResolveByName(KindGroup, "  engineering  ")
	if err != nil {
		t.Fatalf("ResolveByName trimmed: %v", err)
	}
	if out.ID != ent.ID {
		t.Fatalf("ResolveByName id mismatch: %q vs %q", out.ID, ent.ID)
	}
}

func TestAllocateWithIDRegistersExistingID(t *testing.T) {
	d := New(store.NewMemoryStore())
	const existingID = "01HZX7K3M9YBN4WXYZWXYZWXYZ"
	ent, err := d.AllocateWithID(KindAgent, existingID, "scout-prism")
	if err != nil {
		t.Fatalf("AllocateWithID: %v", err)
	}
	if ent.ID != existingID {
		t.Fatalf("ID = %q want preserved %q", ent.ID, existingID)
	}
	resolved, err := d.Resolve(existingID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.DisplayName != "scout-prism" {
		t.Fatalf("DisplayName = %q want %q", resolved.DisplayName, "scout-prism")
	}
}

func TestAllocateWithIDRejectsEmptyID(t *testing.T) {
	d := New(store.NewMemoryStore())
	_, err := d.AllocateWithID(KindAgent, "", "scout")
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("err = %v, want ErrInvalidID", err)
	}
}

func TestAllocateWithIDRejectsDuplicateID(t *testing.T) {
	d := New(store.NewMemoryStore())
	const id = "01HZX7K3M9YBN4WXYZWXYZWXYZ"
	if _, err := d.AllocateWithID(KindAgent, id, "scout"); err != nil {
		t.Fatalf("first AllocateWithID: %v", err)
	}
	_, err := d.AllocateWithID(KindAgent, id, "scout-v2")
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("dup id err = %v, want ErrInvalidID", err)
	}
}

func TestAllocateWithIDIdempotentSamePayload(t *testing.T) {
	d := New(store.NewMemoryStore())
	const id = "01HZX7K3M9YBN4WXYZWXYZWXYZ"
	first, err := d.AllocateWithID(KindAgent, id, "scout")
	if err != nil {
		t.Fatalf("first AllocateWithID: %v", err)
	}
	second, err := d.AllocateWithID(KindAgent, id, "scout")
	if err != nil {
		t.Fatalf("replay AllocateWithID: %v", err)
	}
	if first.ID != second.ID || first.DisplayName != second.DisplayName {
		t.Fatalf("idempotent replay mismatch: %+v vs %+v", first, second)
	}
}

func TestAllocateWithIDRejectsDuplicateDisplayName(t *testing.T) {
	d := New(store.NewMemoryStore())
	if _, err := d.Allocate(KindAgent, "scout"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	_, err := d.AllocateWithID(KindAgent, "external-id-1", "scout")
	if !errors.Is(err, ErrDisplayNameInUse) {
		t.Fatalf("err = %v, want ErrDisplayNameInUse", err)
	}
}

func TestResolveCacheMiss(t *testing.T) {
	kv := store.NewMemoryStore()
	d1 := New(kv)
	ent, err := d1.Allocate(KindBackend, "github-prod")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	// New dispatcher instance — cache is cold, but KV should still
	// serve the entity.
	d2 := New(kv)
	out, err := d2.Resolve(ent.ID)
	if err != nil {
		t.Fatalf("Resolve cold cache: %v", err)
	}
	if out.DisplayName != "github-prod" {
		t.Fatalf("DisplayName = %q want %q", out.DisplayName, "github-prod")
	}
}

func TestResolveUnknownReturnsNotFound(t *testing.T) {
	d := New(store.NewMemoryStore())
	_, err := d.Resolve("does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolveEmptyID(t *testing.T) {
	d := New(store.NewMemoryStore())
	_, err := d.Resolve("")
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("err = %v, want ErrInvalidID", err)
	}
}

func TestResolveByNameUnknownReturnsNotFound(t *testing.T) {
	d := New(store.NewMemoryStore())
	_, err := d.ResolveByName(KindGroup, "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolveByNameAcrossDispatcherInstances(t *testing.T) {
	kv := store.NewMemoryStore()
	d1 := New(kv)
	ent, err := d1.Allocate(KindRole, "senior")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	d2 := New(kv)
	out, err := d2.ResolveByName(KindRole, "senior")
	if err != nil {
		t.Fatalf("ResolveByName cold cache: %v", err)
	}
	if out.ID != ent.ID {
		t.Fatalf("ID mismatch: %q vs %q", out.ID, ent.ID)
	}
}

func TestRenameUpdatesNameIndex(t *testing.T) {
	d := New(store.NewMemoryStore())
	ent, err := d.Allocate(KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	created := ent.CreatedAt
	updated := ent.UpdatedAt
	_ = updated // staying explicit about the field we're going to check below

	renamed, err := d.Rename(ent.ID, "platform-engineering")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if renamed.ID != ent.ID {
		t.Fatalf("Rename should not change ID: %q vs %q", renamed.ID, ent.ID)
	}
	if renamed.DisplayName != "platform-engineering" {
		t.Fatalf("DisplayName = %q want %q", renamed.DisplayName, "platform-engineering")
	}
	if !renamed.UpdatedAt.After(renamed.CreatedAt) && !renamed.UpdatedAt.Equal(renamed.CreatedAt) {
		t.Fatalf("UpdatedAt should be set: %v", renamed.UpdatedAt)
	}
	if !renamed.CreatedAt.Equal(created) {
		t.Fatalf("Rename must not touch CreatedAt: %v vs %v", renamed.CreatedAt, created)
	}

	// New name resolves.
	out, err := d.ResolveByName(KindGroup, "platform-engineering")
	if err != nil {
		t.Fatalf("ResolveByName new: %v", err)
	}
	if out.ID != ent.ID {
		t.Fatalf("new-name lookup wrong id: %q vs %q", out.ID, ent.ID)
	}
	// Old name no longer resolves.
	if _, err := d.ResolveByName(KindGroup, "engineering"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old name should no longer resolve, got err=%v", err)
	}
}

func TestRenameToSameDisplayNameIsNoOp(t *testing.T) {
	d := New(store.NewMemoryStore())
	ent, err := d.Allocate(KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	out, err := d.Rename(ent.ID, "engineering")
	if err != nil {
		t.Fatalf("Rename no-op: %v", err)
	}
	if !out.UpdatedAt.Equal(ent.UpdatedAt) {
		t.Fatalf("no-op rename should not bump UpdatedAt: %v vs %v", out.UpdatedAt, ent.UpdatedAt)
	}
}

func TestRenameDuplicateDisplayName(t *testing.T) {
	d := New(store.NewMemoryStore())
	if _, err := d.Allocate(KindGroup, "platform"); err != nil {
		t.Fatalf("first Allocate: %v", err)
	}
	ent, err := d.Allocate(KindGroup, "engineering")
	if err != nil {
		t.Fatalf("second Allocate: %v", err)
	}
	_, err = d.Rename(ent.ID, "platform")
	if !errors.Is(err, ErrDisplayNameInUse) {
		t.Fatalf("err = %v, want ErrDisplayNameInUse", err)
	}
	// The failed rename must not have changed the entity.
	out, err := d.Resolve(ent.ID)
	if err != nil {
		t.Fatalf("Resolve after failed rename: %v", err)
	}
	if out.DisplayName != "engineering" {
		t.Fatalf("display name corrupted by failed rename: %q", out.DisplayName)
	}
}

func TestRenameUnknownID(t *testing.T) {
	d := New(store.NewMemoryStore())
	_, err := d.Rename("does-not-exist", "platform")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRenameInvalidDisplayName(t *testing.T) {
	d := New(store.NewMemoryStore())
	ent, err := d.Allocate(KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	_, err = d.Rename(ent.ID, ".hidden")
	if !errors.Is(err, ErrInvalidDisplayName) {
		t.Fatalf("err = %v, want ErrInvalidDisplayName", err)
	}
}

func TestRenameEmptyID(t *testing.T) {
	d := New(store.NewMemoryStore())
	_, err := d.Rename("", "platform")
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("err = %v, want ErrInvalidID", err)
	}
}

func TestListReturnsEntitiesOfKind(t *testing.T) {
	d := New(store.NewMemoryStore())
	wantNames := []string{"alpha", "beta", "Gamma"}
	for _, n := range wantNames {
		if _, err := d.Allocate(KindGroup, n); err != nil {
			t.Fatalf("Allocate %s: %v", n, err)
		}
	}
	// Add a role to verify kind isolation.
	if _, err := d.Allocate(KindRole, "alpha"); err != nil {
		t.Fatalf("role alpha allocate: %v", err)
	}

	groups := d.List(KindGroup)
	if len(groups) != len(wantNames) {
		t.Fatalf("List = %d entries, want %d", len(groups), len(wantNames))
	}
	// Must be sorted case-insensitive by display name.
	wantSorted := []string{"alpha", "beta", "Gamma"}
	for i, g := range groups {
		if g.DisplayName != wantSorted[i] {
			t.Fatalf("List[%d] = %q want %q (full: %v)", i, g.DisplayName, wantSorted[i], names(groups))
		}
	}
}

func TestListEmptyKind(t *testing.T) {
	d := New(store.NewMemoryStore())
	if got := d.List(KindGroup); len(got) != 0 {
		t.Fatalf("empty List want zero entries, got %d", len(got))
	}
}

func TestListInvalidKind(t *testing.T) {
	d := New(store.NewMemoryStore())
	if got := d.List(Kind("notakind")); len(got) != 0 {
		t.Fatalf("List with invalid kind should be empty, got %d", len(got))
	}
}

func TestListReloadsFromKVOnNewDispatcher(t *testing.T) {
	kv := store.NewMemoryStore()
	d1 := New(kv)
	for _, n := range []string{"alpha", "beta"} {
		if _, err := d1.Allocate(KindRole, n); err != nil {
			t.Fatalf("Allocate %s: %v", n, err)
		}
	}

	d2 := New(kv)
	got := d2.List(KindRole)
	if len(got) != 2 {
		t.Fatalf("cold-cache List = %d, want 2 (got %v)", len(got), names(got))
	}
}

func TestDeleteRemovesEntityAndName(t *testing.T) {
	d := New(store.NewMemoryStore())
	ent, err := d.Allocate(KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if err := d.Delete(ent.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := d.Resolve(ent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve after delete: err=%v want ErrNotFound", err)
	}
	if _, err := d.ResolveByName(KindGroup, "engineering"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveByName after delete: err=%v want ErrNotFound", err)
	}
	// Name should be reusable.
	if _, err := d.Allocate(KindGroup, "engineering"); err != nil {
		t.Fatalf("re-Allocate after delete: %v", err)
	}
}

func TestDeleteUnknown(t *testing.T) {
	d := New(store.NewMemoryStore())
	err := d.Delete("does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteEmptyID(t *testing.T) {
	d := New(store.NewMemoryStore())
	err := d.Delete("")
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("err = %v, want ErrInvalidID", err)
	}
}

func TestIsULID(t *testing.T) {
	// Real ULID.
	d := New(store.NewMemoryStore())
	ent, err := d.Allocate(KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if !IsULID(ent.ID) {
		t.Fatalf("IsULID(%q) = false, want true", ent.ID)
	}
	// Display-name shaped strings are not ULIDs.
	for _, s := range []string{"engineering", "01HZX", "", strings.Repeat("0", 27)} {
		if IsULID(s) {
			t.Fatalf("IsULID(%q) = true, want false", s)
		}
	}
}

func TestValidKind(t *testing.T) {
	for _, k := range []Kind{KindAgent, KindGroup, KindRole, KindBackend} {
		if !ValidKind(k) {
			t.Fatalf("ValidKind(%q) = false, want true", k)
		}
	}
	for _, k := range []Kind{Kind(""), Kind("user"), Kind("AGENT")} {
		if ValidKind(k) {
			t.Fatalf("ValidKind(%q) = true, want false", k)
		}
	}
}

// names is a test helper for legible failure messages.
func names(ents []Entity) []string {
	out := make([]string, len(ents))
	for i, e := range ents {
		out[i] = e.DisplayName
	}
	return out
}
