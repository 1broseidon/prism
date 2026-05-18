package binstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T, cfg Config) *Store {
	t.Helper()
	root := t.TempDir()
	s, err := New(root, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestPutAndGetRoundtrip(t *testing.T) {
	s := newStore(t, Config{})
	entry, err := s.Put("cymbal", []byte("hello-binary"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if entry.Hash == "" || entry.Size != 12 || entry.Name != "cymbal" {
		t.Fatalf("unexpected entry: %+v", entry)
	}

	got, err := s.Stat(entry.Hash)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got != entry {
		t.Fatalf("Stat mismatch: got %+v want %+v", got, entry)
	}

	path, err := s.Path(entry.Hash)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	bytes, err := os.ReadFile(path) //nolint:gosec // test reads file in t.TempDir()
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(bytes) != "hello-binary" {
		t.Fatalf("content mismatch: %q", bytes)
	}
	if !s.Exists(entry.Hash) {
		t.Fatalf("Exists returned false for stored hash")
	}
}

func TestPutDedupSameHash(t *testing.T) {
	s := newStore(t, Config{})
	first, err := s.Put("recoil", []byte("body"))
	if err != nil {
		t.Fatalf("Put first: %v", err)
	}
	second, err := s.Put("different-name", []byte("body"))
	if err != nil {
		t.Fatalf("Put second: %v", err)
	}
	if second.Hash != first.Hash {
		t.Fatalf("expected same hash, got %s vs %s", first.Hash, second.Hash)
	}
	// First name wins — dedup uses the existing on-disk entry rather than
	// rewriting it with the second name.
	if second.Name != "recoil" {
		t.Fatalf("expected name 'recoil' from first Put, got %q", second.Name)
	}
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list))
	}
}

func TestPutRejectsOversize(t *testing.T) {
	s := newStore(t, Config{MaxBinaryBytes: 8})
	_, err := s.Put("big", []byte("123456789"))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

func TestPutRespectsTotalCap(t *testing.T) {
	s := newStore(t, Config{MaxBinaryBytes: 1024, MaxTotalBytes: 12})
	if _, err := s.Put("one", []byte("0123456789")); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	_, err := s.Put("two", []byte("abcdefghij"))
	if !errors.Is(err, ErrCapacity) {
		t.Fatalf("expected ErrCapacity, got %v", err)
	}
}

func TestPutRejectsInvalidName(t *testing.T) {
	s := newStore(t, Config{})
	cases := []string{"", "../escape", "with/slash", ".", "..", "null\x00byte"}
	for _, name := range cases {
		_, err := s.Put(name, []byte("data"))
		if !errors.Is(err, ErrInvalidName) {
			t.Fatalf("name %q: expected ErrInvalidName, got %v", name, err)
		}
	}
}

func TestPruneRemovesUnreferenced(t *testing.T) {
	s := newStore(t, Config{})
	a, _ := s.Put("a", []byte("aaaa"))
	b, _ := s.Put("b", []byte("bbbb"))
	c, _ := s.Put("c", []byte("cccc"))

	referenced := map[string]struct{}{a.Hash: {}, c.Hash: {}}
	deleted, err := s.Prune(referenced)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != b.Hash {
		t.Fatalf("expected only %s deleted, got %v", b.Hash, deleted)
	}
	if !s.Exists(a.Hash) || !s.Exists(c.Hash) {
		t.Fatalf("referenced hashes should still exist")
	}
	if s.Exists(b.Hash) {
		t.Fatalf("pruned hash should be gone")
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	s := newStore(t, Config{})
	if err := s.Delete("a" + strings.Repeat("0", 63)); err != nil {
		t.Fatalf("delete missing hash: %v", err)
	}
	if err := s.Delete("not-hex"); err == nil {
		t.Fatalf("expected invalid hash error")
	}
}

func TestStatNotFound(t *testing.T) {
	s := newStore(t, Config{})
	_, err := s.Stat(strings.Repeat("0", 64))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestNewRejectsRelativeRoot(t *testing.T) {
	if _, err := New("relative/root", Config{}); err == nil {
		t.Fatalf("expected error for relative root")
	}
	if _, err := New("", Config{}); err == nil {
		t.Fatalf("expected error for empty root")
	}
}

func TestContainerPathFormatsForwardSlash(t *testing.T) {
	got := ContainerPath("/opt/prism/bin", Entry{Hash: "abc", Name: "cymbal"})
	if got != "/opt/prism/bin/abc/cymbal" {
		t.Fatalf("unexpected container path: %q", got)
	}
}

func TestStoreLayoutMatchesContract(t *testing.T) {
	// The contract pinning the layout is "<root>/<sha256>/<binary_name>"
	// because the gateway constructs the in-container path from the same
	// shape. Locking it in a test keeps the binstore + gateway in sync.
	s := newStore(t, Config{})
	entry, err := s.Put("cymbal", []byte("test"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	want := filepath.Join(s.Root(), entry.Hash, "cymbal")
	got, err := s.Path(entry.Hash)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != want {
		t.Fatalf("layout drift: got %s want %s", got, want)
	}
}
