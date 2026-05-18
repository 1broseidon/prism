package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/1broseidon/prism/internal/admin"
)

// fakeGatewayBinaryStore implements gateway.BinaryStore in-memory. The test
// suite never actually executes the binary — we only need Stat and Root to
// return values the spawn path can serialize.
type fakeGatewayBinaryStore struct {
	mu      sync.Mutex
	root    string
	entries map[string]BinaryEntry
}

func newFakeGatewayBinaryStore(root string) *fakeGatewayBinaryStore {
	return &fakeGatewayBinaryStore{root: root, entries: make(map[string]BinaryEntry)}
}

func (f *fakeGatewayBinaryStore) Root() string { return f.root }

func (f *fakeGatewayBinaryStore) Stat(hash string) (BinaryEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.entries[hash]; ok {
		return e, nil
	}
	return BinaryEntry{}, errors.New("not found")
}

func (f *fakeGatewayBinaryStore) Exists(hash string) bool {
	_, err := f.Stat(hash)
	return err == nil
}

func (f *fakeGatewayBinaryStore) put(hash, name string, size int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[hash] = BinaryEntry{Hash: hash, Name: name, Size: size}
}

// spawnRecorder is an httptest.Server-backed bridge stub that records the
// spawn request payload, then returns a 201 with a synthesized endpoint
// pointing back at itself. ConnectBackend will then try to dial that
// endpoint and fail at the MCP handshake — we accept that failure and only
// assert what we recorded.
type spawnRecorder struct {
	mu       sync.Mutex
	payloads []map[string]any
	srv      *httptest.Server
}

func newSpawnRecorder() *spawnRecorder {
	rec := &spawnRecorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/manage/spawn") {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			rec.mu.Lock()
			rec.payloads = append(rec.payloads, payload)
			rec.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       payload["id"],
				"endpoint": "/mcp/" + payload["id"].(string),
				"status":   "ok",
			})
			return
		}
		// DELETE /manage/{id} for cleanup; respond 200.
		w.WriteHeader(http.StatusOK)
	}))
	return rec
}

func (s *spawnRecorder) last() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.payloads) == 0 {
		return nil
	}
	return s.payloads[len(s.payloads)-1]
}

func (s *spawnRecorder) Close() { s.srv.Close() }

func (s *spawnRecorder) URL() string { return s.srv.URL }

// TestAddBinaryBackendSpawnPayload exercises the gateway's binary AddBackend
// path end to end up to the spawn HTTP call. We don't connect to the bridge
// for real — the recorder server returns success on /manage/spawn, then the
// downstream MCP connection fails (no real MCP server). The test only
// asserts on the captured spawn payload: command path, args, and sandbox
// mount all reflect the binstore hash/name layout.
func TestAddBinaryBackendSpawnPayload(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	hash := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	store := newFakeGatewayBinaryStore("/var/lib/prism/binaries")
	store.put(hash, "recoil", 1024)
	gw.SetBinaryStore(store)

	recorder := newSpawnRecorder()
	defer recorder.Close()
	gw.SetBridgeURL(recorder.URL())

	cfg := admin.BackendConfig{
		BinaryHash: hash,
		BinaryName: "recoil",
		BinaryArgs: []string{"mcp", "serve"},
	}
	// AddBackend will spawn, then try to connect to the recorder's
	// /mcp/{id} which isn't a real MCP server — we expect a connect error
	// but the spawn payload has already been captured.
	_ = gw.AddBackend(context.Background(), "rec", cfg)

	payload := recorder.last()
	if payload == nil {
		t.Fatalf("no spawn payload recorded")
	}

	wantCmd := "/opt/prism/bin/" + hash + "/recoil"
	if got, _ := payload["command"].(string); got != wantCmd {
		t.Fatalf("command = %q want %q", got, wantCmd)
	}
	argsRaw, _ := payload["args"].([]any)
	if len(argsRaw) != 2 || argsRaw[0] != "mcp" || argsRaw[1] != "serve" {
		t.Fatalf("args = %v", argsRaw)
	}
	sandbox, _ := payload["sandbox"].(map[string]any)
	if sandbox == nil {
		t.Fatalf("sandbox missing from payload: %v", payload)
	}
	mounts, _ := sandbox["mounts"].([]any)
	if len(mounts) == 0 {
		t.Fatalf("expected at least one sandbox mount: %v", sandbox)
	}
	found := false
	for _, m := range mounts {
		entry, _ := m.(map[string]any)
		if entry == nil {
			continue
		}
		if entry["source"] == "/var/lib/prism/binaries" && entry["target"] == "/opt/prism/bin" {
			ro, _ := entry["readonly"].(bool)
			if !ro {
				t.Errorf("binstore mount should be read-only: %v", entry)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("binstore mount not present in payload: %v", mounts)
	}
}

func TestAddBinaryBackendBlankArgsProducesNoArgs(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	hash := strings.Repeat("a", 64)
	store := newFakeGatewayBinaryStore("/var/lib/prism/binaries")
	store.put(hash, "cymbal", 512)
	gw.SetBinaryStore(store)

	recorder := newSpawnRecorder()
	defer recorder.Close()
	gw.SetBridgeURL(recorder.URL())

	cfg := admin.BackendConfig{BinaryHash: hash, BinaryName: "cymbal"}
	_ = gw.AddBackend(context.Background(), "cym", cfg)
	payload := recorder.last()
	if payload == nil {
		t.Fatalf("no spawn payload recorded")
	}
	args, _ := payload["args"].([]any)
	if len(args) != 0 {
		t.Fatalf("expected zero args, got %v", args)
	}
}

func TestAddBinaryBackendRejectsMissingHash(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	store := newFakeGatewayBinaryStore("/var/lib/prism/binaries")
	gw.SetBinaryStore(store)
	gw.SetBridgeURL("http://example.invalid")

	err := gw.AddBackend(context.Background(), "missing", admin.BackendConfig{
		BinaryHash: strings.Repeat("b", 64),
		BinaryName: "cymbal",
	})
	if err == nil {
		t.Fatalf("expected error for unknown hash")
	}
}

func TestAddBinaryBackendRequiresStore(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	gw.SetBridgeURL("http://example.invalid")
	err := gw.AddBackend(context.Background(), "x", admin.BackendConfig{
		BinaryHash: strings.Repeat("c", 64),
	})
	if err == nil {
		t.Fatalf("expected error when binstore is not configured")
	}
}

// TestBinaryMountTargetCustomizable confirms the operator override path so a
// future deployment can land the binary at a different in-container target
// without code changes.
func TestBinaryMountTargetCustomizable(t *testing.T) {
	gw := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	hash := strings.Repeat("d", 64)
	store := newFakeGatewayBinaryStore("/srv/prism/bin")
	store.put(hash, "recoil", 1)
	gw.SetBinaryStore(store)
	gw.SetBinaryMount("/sandbox/bin")

	recorder := newSpawnRecorder()
	defer recorder.Close()
	gw.SetBridgeURL(recorder.URL())

	_ = gw.AddBackend(context.Background(), "rec", admin.BackendConfig{
		BinaryHash: hash,
		BinaryName: "recoil",
	})
	payload := recorder.last()
	if payload == nil {
		t.Fatalf("no spawn payload")
	}
	want := "/sandbox/bin/" + hash + "/recoil"
	if got, _ := payload["command"].(string); got != want {
		t.Fatalf("command path = %q want %q", got, want)
	}
}

// TestJoinContainerPathForwardSlashes guards the path-shape contract: the
// admin UI surfaces /opt/prism/bin/<hash>/<name> in its preview, so the
// gateway must produce identical paths regardless of host filepath
// separators.
func TestJoinContainerPathForwardSlashes(t *testing.T) {
	got := joinContainerPath("/opt/prism/bin", "abc", "cymbal")
	if got != "/opt/prism/bin/abc/cymbal" {
		t.Fatalf("unexpected: %q", got)
	}
	// Use filepath.Join on the host to ensure the result still matches even
	// if a future refactor accidentally pulls in filepath.Join.
	withHost := filepath.Join("/opt/prism/bin", "abc", "cymbal")
	if filepath.ToSlash(withHost) != got {
		t.Fatalf("drift between joinContainerPath and filepath: %q vs %q", got, withHost)
	}
}
