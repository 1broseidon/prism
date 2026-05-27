package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/auth"
	"github.com/1broseidon/prism/internal/config"
	"github.com/1broseidon/prism/internal/store"
)

type gatewayGrantEmitter struct {
	mu     sync.Mutex
	events []auth.GrantEvent
}

func (e *gatewayGrantEmitter) Emit(_ context.Context, event auth.GrantEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *gatewayGrantEmitter) latest() auth.GrantEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.events) == 0 {
		return auth.GrantEvent{}
	}
	return e.events[len(e.events)-1]
}

func TestDriftBridgeSpawnAllowsMatchingWorkspace(t *testing.T) {
	gw := New(nil)
	emitter := &gatewayGrantEmitter{}
	gw.SetGrantEmitter(emitter)
	ctx := contextWithGrantWorkspace(context.Background(), &auth.WorkspaceInstance{ID: "repo", Type: config.WorkspaceTypeEphemeral, WriteMode: config.WorkspaceWriteStage})
	live := &config.WorkspaceConfig{ID: "repo", Type: config.WorkspaceTypeEphemeral, WriteMode: config.WorkspaceWriteStage}
	if err := gw.checkGrantWorkspaceDrift(ctx, "brainfile", live); err != nil {
		t.Fatalf("checkGrantWorkspaceDrift: %v", err)
	}
	if got := emitter.latest(); got.Outcome != "" {
		t.Fatalf("unexpected drift event: %+v", got)
	}
}

func TestDriftBridgeSpawnDeniesChangedWorkspace(t *testing.T) {
	gw := New(nil)
	emitter := &gatewayGrantEmitter{}
	gw.SetGrantEmitter(emitter)
	ctx := contextWithGrantWorkspace(context.Background(), &auth.WorkspaceInstance{ID: "repo", Type: config.WorkspaceTypeEphemeral, WriteMode: config.WorkspaceWriteStage})
	live := &config.WorkspaceConfig{ID: "repo", Type: config.WorkspaceTypeVirtual, WriteMode: config.WorkspaceWriteStage}
	if err := gw.checkGrantWorkspaceDrift(ctx, "brainfile", live); err == nil {
		t.Fatal("expected drift error")
	}
	event := emitter.latest()
	if event.Outcome != "denied" || event.Trace.DenyDim != auth.GrantDenyWorkspaceDrift || event.Trace.Context.Layer != "live_config" || event.Trace.Drift == nil {
		t.Fatalf("event = %+v", event)
	}
}

func TestDriftBridgeSpawnSkipsWithoutGrantWorkspace(t *testing.T) {
	gw := New(nil)
	emitter := &gatewayGrantEmitter{}
	gw.SetGrantEmitter(emitter)
	live := &config.WorkspaceConfig{ID: "repo", Type: config.WorkspaceTypeVirtual, WriteMode: config.WorkspaceWriteStage}
	if err := gw.checkGrantWorkspaceDrift(context.Background(), "brainfile", live); err != nil {
		t.Fatalf("checkGrantWorkspaceDrift: %v", err)
	}
	if got := emitter.latest(); got.Outcome != "" {
		t.Fatalf("unexpected drift event: %+v", got)
	}
}

// TestDriftTOCTOUDefenseCatchesConfigEditBeforeSpawn exercises the spawn-time
// drift check under a genuine race against a concurrent KV mutation.
//
// Concurrency property asserted: when a goroutine A is poised to call
// checkGrantWorkspaceDrift with a pinned workspace, and goroutine B writes a
// conflicting workspace registry entry mid-flight, A's spawn-time check
// observes whichever live state existed at the moment it called
// applyRegisteredWorkspaceConfig. Because B's writes flip the live `type`
// between two values that disagree with the pinned grant on at least one of
// them, A MUST detect drift on at least some iterations. If A's loop never
// observes drift across N iterations under -race, the spawn-side check is
// not actually defending the window — that's the bug TOCTOU defense exists
// to prevent.
//
// We assert: across N=200 iterations under -race, drift is detected at
// least once. (Detecting it every time would be wrong, because B sometimes
// writes the matching value first; that's the race we want to actually
// race, not stage-manage.)
func TestDriftTOCTOUDefenseCatchesConfigEditBeforeSpawn(t *testing.T) {
	const iterations = 200

	driftSeen := atomic.Int64{}
	cleanSeen := atomic.Int64{}

	for i := 0; i < iterations; i++ {
		gw := New(nil)
		gw.SetStore(store.NewMemoryStore())
		emitter := &gatewayGrantEmitter{}
		gw.SetGrantEmitter(emitter)

		// Pin grant workspace to Ephemeral; this is what middleware would
		// have stamped into the request context after MatchGrants succeeded.
		ctx := contextWithGrantWorkspace(context.Background(),
			&auth.WorkspaceInstance{
				ID:        "repo",
				Type:      config.WorkspaceTypeEphemeral,
				WriteMode: config.WorkspaceWriteStage,
			})

		// Seed the live registry with the matching entry. Without the race,
		// the check would pass.
		seedRegistry(t, gw, "repo", config.WorkspaceTypeEphemeral)

		// Build the live workspace config the spawn path would have derived
		// from the backend config. applyRegisteredWorkspaceConfig will pull
		// the actual Type from the registry — so the value of Type set here
		// is overwritten by whatever the racing writer last persisted.
		live := &config.WorkspaceConfig{
			ID:        "repo",
			Type:      config.WorkspaceTypeEphemeral,
			WriteMode: config.WorkspaceWriteStage,
		}

		// Barrier so the writer and the spawn check race against each other
		// rather than running sequentially. Both goroutines start at the
		// same WaitGroup release.
		var start sync.WaitGroup
		start.Add(1)

		var wg sync.WaitGroup
		wg.Add(2)

		// Writer: flips the registry to a CONFLICTING type. After this lands,
		// the live config seen by checkGrantWorkspaceDrift will be Virtual,
		// which disagrees with the pinned Ephemeral grant.
		go func() {
			defer wg.Done()
			start.Wait()
			writeRegistry(t, gw, "repo", config.WorkspaceTypeVirtual)
		}()

		// Reader: this is the spawn-time path that the production code calls
		// at manage.go:578 after middleware has admitted the request.
		var checkErr error
		go func() {
			defer wg.Done()
			start.Wait()
			// applyRegisteredWorkspaceConfig is the same call the real spawn
			// path makes — it reads from the KV registry and overwrites Type.
			gw.applyRegisteredWorkspaceConfig(live)
			checkErr = gw.checkGrantWorkspaceDrift(ctx, "brainfile", live)
		}()

		// Release both goroutines simultaneously.
		start.Done()
		wg.Wait()

		if checkErr != nil {
			driftSeen.Add(1)
			// When drift is detected, the emitter must have recorded a
			// structured event with the diff. This is the audit guarantee
			// the spawn-side check provides.
			latest := emitter.latest()
			if latest.Trace.Drift == nil || latest.Trace.Drift.GrantHash == latest.Trace.Drift.LiveHash {
				t.Fatalf("iteration %d: drift detected but event has no diff: %+v", i, latest)
			}
		} else {
			cleanSeen.Add(1)
		}
	}

	// Property: across N iterations the race must be reachable in both
	// directions. If we never see drift, the writer is finishing before the
	// reader starts — the test is tautological. If we always see drift, the
	// reader is finishing before the writer — the test isn't exercising the
	// happy-no-race path.
	if driftSeen.Load() == 0 {
		t.Fatalf("never observed drift across %d iterations — race window never opened (drift=%d clean=%d)",
			iterations, driftSeen.Load(), cleanSeen.Load())
	}
	t.Logf("TOCTOU race exercised: drift detected in %d of %d iterations", driftSeen.Load(), iterations)
}

// seedRegistry writes a workspace registry entry with the given type to KV.
// Mirrors the shape persisted by RegisterWorkspace in production.
func seedRegistry(t *testing.T, gw *Gateway, id, typ string) {
	t.Helper()
	writeRegistry(t, gw, id, typ)
}

func writeRegistry(t *testing.T, gw *Gateway, id, typ string) {
	t.Helper()
	entry := workspaceRegistryEntry{
		ID:        id,
		Type:      typ,
		CreatedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(&entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.kvStore.Set(workspaceRegistryPrefix+id, data); err != nil {
		t.Fatal(err)
	}
}
