// Package binstore is a content-addressed storage layer for operator-supplied
// MCP server binaries. Binaries are written under a managed root directory
// keyed by sha256 so two backends that point at the same artifact share
// storage on disk. The bridge sandbox bind-mounts the root read-only at
// /opt/prism/bin, so a stored entry at <root>/<sha256>/<name> is reachable
// inside the container as /opt/prism/bin/<sha256>/<name>.
//
// The store does not execute anything itself — it owns the bytes on disk and
// answers "is this hash present, where is it on the host, how big is it." The
// gateway resolves a backend's hash through Stat/Path to build the spawn
// payload (SandboxMount + command path).
package binstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Default caps. Per-binary cap mirrors the admin upload limit so an oversized
// binary is rejected before any state mutates; total cap is a coarse ceiling
// to keep a misconfigured operator from filling the data dir.
const (
	DefaultMaxBinaryBytes int64 = 64 * 1024 * 1024   // 64 MB
	DefaultMaxTotalBytes  int64 = 1024 * 1024 * 1024 // 1 GB
	hashDirPerm                 = 0o755
	binFilePerm                 = 0o555
)

// ErrTooLarge is returned when a Put would exceed the per-binary cap.
var ErrTooLarge = errors.New("binary exceeds per-binary size cap")

// ErrCapacity is returned when a Put would push the store past the total cap.
var ErrCapacity = errors.New("binary store is at capacity")

// ErrNotFound is returned when Get/Stat is asked for an unknown hash.
var ErrNotFound = errors.New("binary not found")

// ErrInvalidName is returned when the operator-supplied binary name contains
// path separators or otherwise unsafe characters. The name is used both as a
// filesystem leaf and as part of the in-container command path so it must be
// a plain basename.
var ErrInvalidName = errors.New("invalid binary name")

// Config controls the per-binary and aggregate caps. Zero values fall back to
// the package defaults so callers can pass an empty Config to get safe limits.
type Config struct {
	MaxBinaryBytes int64
	MaxTotalBytes  int64
}

// Store owns a managed directory containing sha256-keyed subdirectories.
// Layout: <root>/<sha256>/<binary_name>. Multiple backends may reference the
// same hash; pruning is responsibility of the caller (Prune helper) once it
// knows which hashes are still referenced.
type Store struct {
	mu     sync.Mutex
	root   string
	cfg    Config
	loaded bool // true once the root has been ensured to exist
}

// Entry is the metadata for a stored binary.
type Entry struct {
	Hash string
	Name string
	Size int64
}

// New constructs a Store. Root must be an absolute path; it is created on
// first write with 0o755 permissions if it does not already exist. cfg fields
// that are zero fall back to the package defaults.
func New(root string, cfg Config) (*Store, error) {
	if root == "" {
		return nil, errors.New("binstore root is required")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("binstore root must be absolute: %q", root)
	}
	if cfg.MaxBinaryBytes <= 0 {
		cfg.MaxBinaryBytes = DefaultMaxBinaryBytes
	}
	if cfg.MaxTotalBytes <= 0 {
		cfg.MaxTotalBytes = DefaultMaxTotalBytes
	}
	return &Store{root: filepath.Clean(root), cfg: cfg}, nil
}

// Root returns the absolute host path of the managed directory. This is what
// gets bind-mounted into the sandbox.
func (s *Store) Root() string {
	return s.root
}

// MaxBinaryBytes returns the configured per-binary cap.
func (s *Store) MaxBinaryBytes() int64 {
	return s.cfg.MaxBinaryBytes
}

// ensureRoot lazily creates the managed directory. Holding s.mu is the
// caller's responsibility.
func (s *Store) ensureRoot() error {
	if s.loaded {
		return nil
	}
	if err := os.MkdirAll(s.root, hashDirPerm); err != nil {
		return fmt.Errorf("create binstore root: %w", err)
	}
	s.loaded = true
	return nil
}

// validateBinaryName rejects names that aren't safe to use as both a
// filesystem leaf and an in-container path component.
func validateBinaryName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalidName)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: name contains path separator", ErrInvalidName)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%w: reserved name %q", ErrInvalidName, name)
	}
	if strings.ContainsAny(name, "\x00\n") {
		return fmt.Errorf("%w: name contains control character", ErrInvalidName)
	}
	return nil
}

// Put writes data under sha256(data) keyed by the resulting hash. The caller
// supplies a stable display name (a basename — no path separators) which is
// preserved verbatim as the leaf filename. Returns the resulting Entry. Hash
// collisions on the same name are a no-op (dedup). A second Put with a
// different name but the same content is also a no-op — the first name wins.
//
// Size caps are enforced before any bytes are written so an oversize upload
// never leaves a partial file on disk. The total-cap check counts bytes for
// hashes that are not already stored (so re-uploading a known hash never
// fails for capacity reasons).
func (s *Store) Put(name string, data []byte) (Entry, error) {
	if err := validateBinaryName(name); err != nil {
		return Entry{}, err
	}
	size := int64(len(data))
	if size <= 0 {
		return Entry{}, errors.New("binary is empty")
	}
	if size > s.cfg.MaxBinaryBytes {
		return Entry{}, fmt.Errorf("%w: %d bytes (cap %d)", ErrTooLarge, size, s.cfg.MaxBinaryBytes)
	}

	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureRoot(); err != nil {
		return Entry{}, err
	}

	// Dedup: if the hash directory already has any file, return its entry.
	if existing, ok, err := s.lookupLocked(hash); err != nil {
		return Entry{}, err
	} else if ok {
		return existing, nil
	}

	// Total-cap check: only counts bytes for hashes not already present, so
	// re-uploading a known hash is always free. (We already returned above on
	// the dedup path, so this branch only runs for genuinely new content.)
	used, err := s.usedBytesLocked()
	if err != nil {
		return Entry{}, fmt.Errorf("scan binstore for total size: %w", err)
	}
	if used+size > s.cfg.MaxTotalBytes {
		return Entry{}, fmt.Errorf("%w: would use %d / %d bytes", ErrCapacity, used+size, s.cfg.MaxTotalBytes)
	}

	dir := filepath.Join(s.root, hash)
	if err := os.MkdirAll(dir, hashDirPerm); err != nil {
		return Entry{}, fmt.Errorf("create hash dir: %w", err)
	}
	target := filepath.Join(dir, name)
	tmp := target + ".part"
	if err := os.WriteFile(tmp, data, binFilePerm); err != nil { //nolint:gosec // binFilePerm = 0o555 (read+execute, no write)
		return Entry{}, fmt.Errorf("write binary: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return Entry{}, fmt.Errorf("commit binary: %w", err)
	}
	return Entry{Hash: hash, Name: name, Size: size}, nil
}

// Stat returns the metadata for a stored hash. Returns ErrNotFound when the
// hash is unknown.
func (s *Store) Stat(hash string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureRoot(); err != nil {
		return Entry{}, err
	}
	entry, ok, err := s.lookupLocked(hash)
	if err != nil {
		return Entry{}, err
	}
	if !ok {
		return Entry{}, ErrNotFound
	}
	return entry, nil
}

// Exists reports whether the hash is present. Does not distinguish missing
// hashes from filesystem errors; callers that care should use Stat.
func (s *Store) Exists(hash string) bool {
	_, err := s.Stat(hash)
	return err == nil
}

// Path returns the absolute host path to the stored binary for hash. Returns
// ErrNotFound when the hash is unknown.
func (s *Store) Path(hash string) (string, error) {
	entry, err := s.Stat(hash)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, entry.Hash, entry.Name), nil
}

// ContainerPath returns the in-sandbox path for a stored hash, derived from
// the mount target. The store itself doesn't track mount targets — the
// caller passes the same target used when constructing the SandboxMount.
func ContainerPath(mountTarget string, entry Entry) string {
	return filepath.ToSlash(filepath.Join(mountTarget, entry.Hash, entry.Name))
}

// Delete removes the entry for hash, if present. Returns nil on missing
// hashes — pruning is idempotent.
func (s *Store) Delete(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !isHexSHA256(hash) {
		return fmt.Errorf("invalid hash %q", hash)
	}
	dir := filepath.Join(s.root, hash)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete hash dir: %w", err)
	}
	return nil
}

// Prune removes every stored hash that is not in referenced. Returns the list
// of deleted hashes for logging. A nil or empty referenced set deletes
// everything in the store, so callers must always pass the full live set.
func (s *Store) Prune(referenced map[string]struct{}) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureRoot(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("read binstore root: %w", err)
	}
	var deleted []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		hash := e.Name()
		if !isHexSHA256(hash) {
			continue
		}
		if _, keep := referenced[hash]; keep {
			continue
		}
		if rmErr := os.RemoveAll(filepath.Join(s.root, hash)); rmErr != nil {
			return deleted, fmt.Errorf("delete %s: %w", hash, rmErr)
		}
		deleted = append(deleted, hash)
	}
	sort.Strings(deleted)
	return deleted, nil
}

// List returns every stored entry. Stable order by hash so tests and the
// admin UI both see deterministic ordering.
func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureRoot(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !isHexSHA256(e.Name()) {
			continue
		}
		entry, ok, err := s.lookupLocked(e.Name())
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hash < out[j].Hash })
	return out, nil
}

// lookupLocked reads the hash directory and returns the (sole) entry inside.
// Multiple files in the same hash dir is unexpected (Put writes one), but we
// pick the first non-hidden one defensively.
func (s *Store) lookupLocked(hash string) (Entry, bool, error) {
	if !isHexSHA256(hash) {
		return Entry{}, false, fmt.Errorf("invalid hash %q", hash)
	}
	dir := filepath.Join(s.root, hash)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Entry{}, false, nil
		}
		return Entry{}, false, err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return Entry{}, false, err
		}
		return Entry{Hash: hash, Name: e.Name(), Size: info.Size()}, true, nil
	}
	return Entry{}, false, nil
}

// usedBytesLocked sums every stored file's size. Callers must hold s.mu.
func (s *Store) usedBytesLocked() (int64, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var total int64
	for _, e := range entries {
		if !e.IsDir() || !isHexSHA256(e.Name()) {
			continue
		}
		entry, ok, err := s.lookupLocked(e.Name())
		if err != nil {
			return 0, err
		}
		if ok {
			total += entry.Size
		}
	}
	return total, nil
}

// HashBytes returns the hex sha256 of data. Exported so callers (admin layer)
// can avoid duplicating the hashing logic when they already have the bytes.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HashReader streams r through a sha256 hasher and returns the hex digest
// plus a buffered copy of the bytes. Used by handlers that need both the
// hash and a re-readable body without holding the whole upload in two slices.
func HashReader(r io.Reader, limit int64) (body []byte, hash string, err error) {
	if limit <= 0 {
		limit = DefaultMaxBinaryBytes
	}
	limited := io.LimitReader(r, limit+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", err
	}
	if int64(len(buf)) > limit {
		return nil, "", ErrTooLarge
	}
	return buf, HashBytes(buf), nil
}

// isHexSHA256 reports whether s is a valid hex-encoded sha256 string.
func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
