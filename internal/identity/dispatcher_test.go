package identity

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/1broseidon/prism/internal/store"
)

// TestRaceConcurrentAllocateDistinctNames ensures the dispatcher
// produces N entities for N goroutines each allocating with a unique
// display name, with no panics, no errors, and no orphan name indexes.
func TestRaceConcurrentAllocateDistinctNames(t *testing.T) {
	d := New(store.NewMemoryStore())
	const N = 100
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("group-%03d", i)
			if _, err := d.Allocate(KindGroup, name); err != nil {
				errs <- fmt.Errorf("Allocate %s: %w", name, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := len(d.List(KindGroup)); got != N {
		t.Fatalf("List = %d, want %d", got, N)
	}
}

// TestRaceConcurrentAllocateSameName ensures that when many goroutines
// race to allocate the same display name, exactly one wins and the
// rest see ErrDisplayNameInUse — never a successful duplicate.
func TestRaceConcurrentAllocateSameName(t *testing.T) {
	d := New(store.NewMemoryStore())
	const N = 50
	var wg sync.WaitGroup
	var wins atomic.Int32
	var losses atomic.Int32
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.Allocate(KindGroup, "engineering")
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrDisplayNameInUse):
				losses.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if w := wins.Load(); w != 1 {
		t.Fatalf("wins = %d, want exactly 1", w)
	}
	if l := losses.Load(); l != int32(N-1) {
		t.Fatalf("losses = %d, want %d", l, N-1)
	}
	if got := len(d.List(KindGroup)); got != 1 {
		t.Fatalf("List = %d, want 1", got)
	}
}

// TestRaceConcurrentAllocateAndRename runs allocate and rename in
// parallel and confirms the byName index never carries a stale entry
// pointing at a renamed-away name. This is the invariant from the
// task contract: "concurrent allocate + rename must not produce
// orphan name indexes".
func TestRaceConcurrentAllocateAndRename(t *testing.T) {
	d := New(store.NewMemoryStore())
	// Seed an entity that we'll rename in a tight loop.
	ent, err := d.Allocate(KindGroup, "engineering")
	if err != nil {
		t.Fatalf("seed Allocate: %v", err)
	}

	const renameIters = 200
	const allocators = 8

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Renamer goroutine bounces the display name between two values.
	wg.Add(1)
	go func() {
		defer wg.Done()
		names := []string{"engineering", "platform"}
		for i := 0; i < renameIters; i++ {
			next := names[i%2]
			if _, err := d.Rename(ent.ID, next); err != nil {
				if !errors.Is(err, ErrDisplayNameInUse) {
					t.Errorf("Rename iter %d: %v", i, err)
				}
			}
		}
		close(stop)
	}()

	// Allocator goroutines race to claim the second name. Whether
	// they succeed depends on the renamer's current pos — the test
	// invariant is that we never see a corrupted state, not which
	// side wins each round.
	for i := 0; i < allocators; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("alloc-side-%d", idx)
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Round-trip allocate + delete so each goroutine
				// contributes write traffic without exhausting the
				// name space.
				ent, err := d.Allocate(KindGroup, name)
				if err != nil {
					if !errors.Is(err, ErrDisplayNameInUse) {
						t.Errorf("alloc %s: %v", name, err)
					}
					continue
				}
				if err := d.Delete(ent.ID); err != nil {
					t.Errorf("delete %s: %v", name, err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Invariant: every entity returned by List must round-trip
	// through ResolveByName(DisplayName) → Resolve(ID) and land on
	// the same record.
	for _, e := range d.List(KindGroup) {
		got, err := d.ResolveByName(KindGroup, e.DisplayName)
		if err != nil {
			t.Fatalf("ResolveByName(%q): %v", e.DisplayName, err)
		}
		if got.ID != e.ID {
			t.Fatalf("byName index orphan: %q→%q, but List says %q", e.DisplayName, got.ID, e.ID)
		}
	}
}

// TestRaceConcurrentResolve hammers Resolve from many goroutines after
// a single Allocate. The cache hot-path is RWLocked and we want the
// race detector to see no torn reads.
func TestRaceConcurrentResolve(t *testing.T) {
	d := New(store.NewMemoryStore())
	ent, err := d.Allocate(KindRole, "senior")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	const N = 200
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := d.Resolve(ent.ID)
			if err != nil {
				t.Errorf("Resolve: %v", err)
				return
			}
			if out.ID != ent.ID || out.DisplayName != "senior" {
				t.Errorf("torn read: %+v", out)
			}
		}()
	}
	wg.Wait()
}

// TestRaceConcurrentReadAcrossKinds ensures the dispatcher correctly
// serves cross-kind reads when the cache is hot for one kind but cold
// for another. The cold-side List read goes through ensureKindLoaded
// which can race with parallel cached Resolves.
func TestRaceConcurrentReadAcrossKinds(t *testing.T) {
	kv := store.NewMemoryStore()
	d1 := New(kv)
	// Seed via one dispatcher so a second dispatcher sees fully cold
	// caches.
	groupEnt, err := d1.Allocate(KindGroup, "engineering")
	if err != nil {
		t.Fatalf("Allocate group: %v", err)
	}
	roleEnt, err := d1.Allocate(KindRole, "senior")
	if err != nil {
		t.Fatalf("Allocate role: %v", err)
	}

	d2 := New(kv)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := d2.Resolve(groupEnt.ID); err != nil {
				t.Errorf("Resolve group: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if got := d2.List(KindRole); len(got) != 1 || got[0].ID != roleEnt.ID {
				t.Errorf("List role: %v", got)
			}
		}()
	}
	wg.Wait()
}

// TestRaceDeleteWhileResolving exercises the read-vs-delete race: one
// goroutine deletes an entity while many others try to resolve it.
// The expected outcomes are "found" or ErrNotFound — never a torn
// record.
func TestRaceDeleteWhileResolving(t *testing.T) {
	d := New(store.NewMemoryStore())
	const N = 32
	ids := make([]string, N)
	for i := 0; i < N; i++ {
		ent, err := d.Allocate(KindBackend, fmt.Sprintf("backend-%03d", i))
		if err != nil {
			t.Fatalf("Allocate %d: %v", i, err)
		}
		ids[i] = ent.ID
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 16; j++ {
				_, err := d.Resolve(id)
				if err != nil && !errors.Is(err, ErrNotFound) {
					t.Errorf("Resolve %s: unexpected error %v", id, err)
				}
			}
		}()
		go func() {
			defer wg.Done()
			if err := d.Delete(id); err != nil && !errors.Is(err, ErrNotFound) {
				t.Errorf("Delete %s: unexpected error %v", id, err)
			}
		}()
	}
	wg.Wait()
}

// TestNewPanicOnNilStore documents the explicit panic contract.
func TestNewPanicOnNilStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("New(nil) did not panic")
		}
	}()
	_ = New(nil)
}
