package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/1broseidon/prism/internal/store"
)

// KV key prefixes. The dispatcher owns these — nothing else in prism
// should read or write these keys.
const (
	entityKeyPrefix = "identity/"         // identity/{kind}/{id}
	nameKeyPrefix   = "identity_by_name/" // identity_by_name/{kind}/{name}
)

// New constructs a KV-backed [Dispatcher]. The supplied [store.Store]
// is consulted lazily on first access for each entity; the dispatcher
// keeps an in-process cache to avoid re-reading hot entries.
//
// Passing a nil store panics — every prism build wires a store, so a
// missing store is a programmer error, not a runtime config decision.
func New(kv store.Store) Dispatcher {
	if kv == nil {
		panic("identity: New called with nil store.Store")
	}
	d := &dispatcherImpl{
		kv:       kv,
		byID:     make(map[string]Entity),
		byName:   make(map[Kind]map[string]string),
		listed:   make(map[Kind]bool),
		clock:    time.Now,
		newULIDx: newULID,
	}
	return d
}

// dispatcherImpl is the in-process + KV-backed [Dispatcher]. A single
// RWMutex guards both maps; we aren't anywhere near write-heavy enough
// for sharding to matter, and the simpler locking story makes the
// race-test invariants tractable (orphan name indexes can't happen
// because Allocate, Rename, and Delete all serialize against each
// other while still permitting concurrent reads).
type dispatcherImpl struct {
	kv store.Store

	mu sync.RWMutex
	// byID is the authoritative cache: id → Entity. Populated lazily
	// from KV on Resolve / ResolveByName / List.
	byID map[string]Entity
	// byName is a kind-scoped reverse index: kind → display_name → id.
	// Kept in sync with byID under the write lock so renames cannot
	// leave an orphan stale name pointing at the renamed entity.
	byName map[Kind]map[string]string
	// listed records whether we've performed the cold-start List read
	// for a kind. The cache is authoritative once a kind is listed.
	listed map[Kind]bool

	// clock and newULIDx are seams for tests; nil-checked? No — only
	// New populates them and it never assigns nil.
	clock    func() time.Time
	newULIDx func(time.Time) string
}

// Allocate implements [Dispatcher].
func (d *dispatcherImpl) Allocate(kind Kind, displayName string) (Entity, error) {
	if !ValidKind(kind) {
		return Entity{}, fmt.Errorf("%w: %q", ErrKindMismatch, string(kind))
	}
	displayName = strings.TrimSpace(displayName)
	if err := validateDisplayName(displayName); err != nil {
		return Entity{}, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.ensureKindLoadedLocked(kind); err != nil {
		return Entity{}, err
	}
	if _, taken := d.byName[kind][nameKey(displayName)]; taken {
		return Entity{}, fmt.Errorf("%w: %q", ErrDisplayNameInUse, displayName)
	}

	now := d.clock().UTC()
	id := d.newULIDx(now)
	// Defensive: ULID collision is astronomically unlikely (80 bits of
	// randomness), but a re-allocation under the same clock would still
	// fail loudly rather than silently overwrite.
	if _, exists := d.byID[id]; exists {
		return Entity{}, fmt.Errorf("identity: ulid collision for %q", id)
	}
	ent := Entity{
		ID:          id,
		Kind:        kind,
		DisplayName: displayName,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := d.persistLocked(ent); err != nil {
		return Entity{}, err
	}
	return ent, nil
}

// AllocateWithID implements [Dispatcher].
func (d *dispatcherImpl) AllocateWithID(kind Kind, id, displayName string) (Entity, error) {
	if !ValidKind(kind) {
		return Entity{}, fmt.Errorf("%w: %q", ErrKindMismatch, string(kind))
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Entity{}, ErrInvalidID
	}
	displayName = strings.TrimSpace(displayName)
	if err := validateDisplayName(displayName); err != nil {
		return Entity{}, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.ensureKindLoadedLocked(kind); err != nil {
		return Entity{}, err
	}
	// Cross-kind id collision check: an id is globally unique across
	// all kinds (we route resolves by id without a kind, so reusing
	// one id under two kinds would be ambiguous).
	if existing, err := d.resolveLocked(id); err == nil {
		if existing.Kind == kind && existing.DisplayName == displayName {
			// Idempotent: same id+kind+name returns the existing
			// record. Lets migrations replay safely.
			return existing, nil
		}
		return Entity{}, fmt.Errorf("%w: id %q already registered", ErrInvalidID, id)
	} else if !errors.Is(err, ErrNotFound) {
		return Entity{}, err
	}
	if _, taken := d.byName[kind][nameKey(displayName)]; taken {
		return Entity{}, fmt.Errorf("%w: %q", ErrDisplayNameInUse, displayName)
	}

	now := d.clock().UTC()
	ent := Entity{
		ID:          id,
		Kind:        kind,
		DisplayName: displayName,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := d.persistLocked(ent); err != nil {
		return Entity{}, err
	}
	return ent, nil
}

// Resolve implements [Dispatcher].
func (d *dispatcherImpl) Resolve(id string) (Entity, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Entity{}, ErrInvalidID
	}
	d.mu.RLock()
	if ent, ok := d.byID[id]; ok {
		d.mu.RUnlock()
		return ent, nil
	}
	d.mu.RUnlock()

	// Cache miss — load from KV under the write lock so concurrent
	// callers don't double-load.
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.resolveLocked(id)
}

// resolveLocked must be called with d.mu held (read or write — we
// upgrade on miss by acquiring write outside). Returns from cache on
// hit; falls back to KV scan across kinds on miss because the entity
// key encodes kind.
func (d *dispatcherImpl) resolveLocked(id string) (Entity, error) {
	if ent, ok := d.byID[id]; ok {
		return ent, nil
	}
	// id-keyed lookup doesn't know the kind. We probe each kind's
	// prefix; this is O(4) KV gets in the worst case, only on cold
	// path.
	for _, kind := range []Kind{KindAgent, KindGroup, KindRole, KindBackend} {
		raw, err := d.kv.Get(entityKey(kind, id))
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return Entity{}, fmt.Errorf("identity: kv get: %w", err)
		}
		var ent Entity
		if err := json.Unmarshal(raw, &ent); err != nil {
			return Entity{}, fmt.Errorf("identity: decode entity %q: %w", id, err)
		}
		d.cacheLocked(ent)
		return ent, nil
	}
	return Entity{}, fmt.Errorf("%w: id %q", ErrNotFound, id)
}

// ResolveByName implements [Dispatcher].
func (d *dispatcherImpl) ResolveByName(kind Kind, name string) (Entity, error) {
	if !ValidKind(kind) {
		return Entity{}, fmt.Errorf("%w: %q", ErrKindMismatch, string(kind))
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Entity{}, fmt.Errorf("%w: empty name", ErrNotFound)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.ensureKindLoadedLocked(kind); err != nil {
		return Entity{}, err
	}
	id, ok := d.byName[kind][nameKey(name)]
	if !ok {
		return Entity{}, fmt.Errorf("%w: kind %q name %q", ErrNotFound, kind, name)
	}
	ent, ok := d.byID[id]
	if !ok {
		// Cache invariant broken: name index points to an id we
		// don't have. Re-load from KV; if KV also doesn't have it,
		// drop the stale name index entry.
		ent, err := d.resolveLocked(id)
		if err != nil {
			delete(d.byName[kind], nameKey(name))
			return Entity{}, err
		}
		return ent, nil
	}
	return ent, nil
}

// Rename implements [Dispatcher].
func (d *dispatcherImpl) Rename(id, newDisplayName string) (Entity, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Entity{}, ErrInvalidID
	}
	newDisplayName = strings.TrimSpace(newDisplayName)
	if err := validateDisplayName(newDisplayName); err != nil {
		return Entity{}, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	ent, err := d.resolveLocked(id)
	if err != nil {
		return Entity{}, err
	}
	// No-op rename: same name returns the existing entity unchanged
	// (no UpdatedAt bump). Saves a KV write on idempotent retries.
	if ent.DisplayName == newDisplayName {
		return ent, nil
	}
	if takenID, taken := d.byName[ent.Kind][nameKey(newDisplayName)]; taken && takenID != id {
		return Entity{}, fmt.Errorf("%w: %q", ErrDisplayNameInUse, newDisplayName)
	}

	oldName := ent.DisplayName
	ent.DisplayName = newDisplayName
	ent.UpdatedAt = d.clock().UTC()

	// Order matters: write the entity first (with the new display
	// name) so a crash between the two writes leaves the entity
	// canonical and the name index merely stale by one rename. The
	// next List() rebuilds the name index from the canonical entity
	// records. The reverse ordering would leave an orphan name index
	// pointing at the renamed entity.
	if err := d.persistLocked(ent); err != nil {
		// On persist failure, restore in-memory state so a retry
		// sees the original display name.
		ent.DisplayName = oldName
		return Entity{}, err
	}
	// Remove the stale name index entry. persistLocked already added
	// the new one.
	if err := d.kv.Delete(nameIndexKey(ent.Kind, oldName)); err != nil {
		// Non-fatal: stale name index will be cleaned up on next
		// List for this kind. Log via the error so the caller can
		// surface it if needed.
		return ent, fmt.Errorf("identity: delete stale name index for %q: %w", oldName, err)
	}
	delete(d.byName[ent.Kind], nameKey(oldName))
	return ent, nil
}

// List implements [Dispatcher].
func (d *dispatcherImpl) List(kind Kind) []Entity {
	if !ValidKind(kind) {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ensureKindLoadedLocked(kind); err != nil {
		return nil
	}
	out := make([]Entity, 0, len(d.byName[kind]))
	for _, id := range d.byName[kind] {
		if ent, ok := d.byID[id]; ok {
			out = append(out, ent)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		// Stable: primary key is case-insensitive display name; tie-
		// break by ID so the order is deterministic for tests + UI.
		if li, lj := strings.ToLower(out[i].DisplayName), strings.ToLower(out[j].DisplayName); li != lj {
			return li < lj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Delete implements [Dispatcher]. v1 deletes unconditionally — the
// package comment documents this weakness. Callers must externally
// coordinate that no other subsystem holds a reference.
func (d *dispatcherImpl) Delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidID
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	ent, err := d.resolveLocked(id)
	if err != nil {
		return err
	}

	// Order: drop the name index first (so a concurrent reader can
	// no longer reach this entity by name), then the entity itself.
	if err := d.kv.Delete(nameIndexKey(ent.Kind, ent.DisplayName)); err != nil {
		return fmt.Errorf("identity: delete name index: %w", err)
	}
	if err := d.kv.Delete(entityKey(ent.Kind, ent.ID)); err != nil {
		return fmt.Errorf("identity: delete entity: %w", err)
	}
	delete(d.byID, ent.ID)
	delete(d.byName[ent.Kind], nameKey(ent.DisplayName))
	return nil
}

// persistLocked writes the entity and its name index to KV and updates
// the in-process cache. Must be called with d.mu held in write mode.
//
// Atomicity caveat: the underlying [store.Store] interface does not
// expose transactions, so the entity write and the name-index write
// are two distinct KV operations. A crash between them leaves the
// entity persisted but the name index missing — the next [List] for
// the kind rebuilds the missing index from the entity records (see
// ensureKindLoadedLocked). All in-process state mutations happen
// after a successful KV write so callers observe consistent state.
func (d *dispatcherImpl) persistLocked(ent Entity) error {
	raw, err := json.Marshal(ent)
	if err != nil {
		return fmt.Errorf("identity: encode entity: %w", err)
	}
	if err := d.kv.Set(entityKey(ent.Kind, ent.ID), raw); err != nil {
		return fmt.Errorf("identity: kv set entity: %w", err)
	}
	if err := d.kv.Set(nameIndexKey(ent.Kind, ent.DisplayName), []byte(ent.ID)); err != nil {
		return fmt.Errorf("identity: kv set name index: %w", err)
	}
	d.cacheLocked(ent)
	return nil
}

// cacheLocked updates the in-process cache. Must be called with d.mu
// held in write mode.
func (d *dispatcherImpl) cacheLocked(ent Entity) {
	d.byID[ent.ID] = ent
	if d.byName[ent.Kind] == nil {
		d.byName[ent.Kind] = make(map[string]string)
	}
	d.byName[ent.Kind][nameKey(ent.DisplayName)] = ent.ID
}

// ensureKindLoadedLocked performs a one-shot KV scan for the kind on
// first access so [List] and [ResolveByName] see every persisted
// entity, not just the ones touched in this process. Must be called
// with d.mu held in write mode.
func (d *dispatcherImpl) ensureKindLoadedLocked(kind Kind) error {
	if d.listed[kind] {
		return nil
	}
	prefix := entityPrefix(kind)
	keys, err := d.kv.List(prefix)
	if err != nil {
		return fmt.Errorf("identity: kv list: %w", err)
	}
	for _, key := range keys {
		raw, err := d.kv.Get(key)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("identity: kv get %q: %w", key, err)
		}
		var ent Entity
		if err := json.Unmarshal(raw, &ent); err != nil {
			// Skip malformed entries rather than refusing to load
			// the kind entirely; operators can repair via Delete.
			continue
		}
		// In-cache wins over KV when both are present (the cache is
		// always at least as fresh — writes go through persistLocked
		// which updates both).
		if _, cached := d.byID[ent.ID]; cached {
			continue
		}
		d.cacheLocked(ent)
	}
	d.listed[kind] = true
	return nil
}

// nameKey normalizes a display name for use as the byName index key.
// Lowercased so two display names that differ only in case still
// collide on uniqueness — operators rarely intend "Engineering" and
// "engineering" to refer to different groups.
func nameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// entityKey returns the KV key for an entity record.
func entityKey(kind Kind, id string) string {
	return entityKeyPrefix + string(kind) + "/" + id
}

// entityPrefix returns the KV List() prefix for all entities of a
// kind.
func entityPrefix(kind Kind) string {
	return entityKeyPrefix + string(kind) + "/"
}

// nameIndexKey returns the KV key for the name→id reverse index.
func nameIndexKey(kind Kind, name string) string {
	return nameKeyPrefix + string(kind) + "/" + nameKey(name)
}
