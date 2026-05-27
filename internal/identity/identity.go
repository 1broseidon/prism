// Package identity provides Prism's central identity dispatcher.
//
// Every identity-bearing entity in Prism — agents, groups, roles, and
// backends/servers — is promoted to a stable opaque ID as its working-set
// key, with a mutable display name as the operator-facing label. The
// dispatcher mints IDs, owns the kind→{id, display_name} registry, and
// serves resolution.
//
// IDs are ULIDs (26-char Crockford base32, time-ordered, URL-safe). They
// are immutable; display names are mutable and unique per kind.
//
// Storage layout (KV-backed):
//
//	identity/{kind}/{id}            → Entity JSON
//	identity_by_name/{kind}/{name}  → id  (refreshed on rename)
//
// Reads hit an in-process cache populated lazily by [Resolve],
// [ResolveByName], and [List]; writes update both the KV store and the
// cache while holding the dispatcher's write lock so callers never
// observe a partial state.
//
// # v1 limitations
//
// Delete is unconditional — there is no ref-count or safe-delete in v1.
// Callers that share an entity ID across subsystems must coordinate
// removal externally until ref counting lands in a later epic-5 task.
//
// Backwards compatibility hook: [Dispatcher.AllocateWithID] accepts a
// caller-supplied ID for migrations (e.g. registering existing
// `prism_id`s as KindAgent entities without re-minting). The dispatcher
// validates the caller-supplied ID for non-emptiness and uniqueness
// within the kind, but does not enforce ULID format on it — agents'
// pre-existing IDs are 32-byte base64url, not ULID.
package identity

import (
	"errors"
	"regexp"
	"time"
)

// Kind enumerates the kinds of identity-bearing entities in Prism.
type Kind string

const (
	KindAgent   Kind = "agent"
	KindGroup   Kind = "group"
	KindRole    Kind = "role"
	KindBackend Kind = "backend"
)

// ValidKind reports whether k is one of the supported entity kinds.
func ValidKind(k Kind) bool {
	switch k {
	case KindAgent, KindGroup, KindRole, KindBackend:
		return true
	}
	return false
}

// Entity is the registry record for an identity-bearing thing in Prism.
// ID is immutable; DisplayName is the operator-facing label and may be
// renamed via [Dispatcher.Rename].
type Entity struct {
	ID          string    `json:"id"`
	Kind        Kind      `json:"kind"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	CreatedBy   string    `json:"created_by,omitempty"`
}

// Dispatcher is the central identity registry.
//
// Implementations must enforce display-name uniqueness per kind at
// [Dispatcher.Allocate] and [Dispatcher.Rename], surface sentinel errors
// (defined in this package) for caller routing via [errors.Is], and be
// safe for concurrent use.
type Dispatcher interface {
	// Allocate mints a new ULID for the given kind and registers it
	// under the supplied display name. Returns [ErrInvalidDisplayName]
	// when the name fails validation and [ErrDisplayNameInUse] when
	// the kind already has an entity with that name.
	Allocate(kind Kind, displayName string) (Entity, error)

	// AllocateWithID registers an entity under a caller-supplied ID
	// rather than minting a new ULID. Used by migrations (e.g. when
	// registering existing prism_ids under KindAgent). Returns
	// [ErrInvalidID] when id is empty, [ErrInvalidDisplayName] when
	// the name fails validation, and [ErrDisplayNameInUse] when the
	// kind already has an entity with that name. The supplied id need
	// not be a ULID; only the kind+id pair must be unique.
	AllocateWithID(kind Kind, id, displayName string) (Entity, error)

	// Resolve returns the entity for the given ID. Returns
	// [ErrNotFound] when no entity is registered under id.
	Resolve(id string) (Entity, error)

	// ResolveByName returns the entity for the given kind and display
	// name. Used by compat shims (and the test suite). Returns
	// [ErrNotFound] when no entity matches.
	ResolveByName(kind Kind, name string) (Entity, error)

	// Rename changes an entity's display name. The new name is
	// validated and checked for uniqueness within the kind. Returns
	// [ErrNotFound] when id is unknown, [ErrInvalidDisplayName] when
	// validation fails, and [ErrDisplayNameInUse] when the name is
	// already taken by another entity of the same kind.
	Rename(id, newDisplayName string) (Entity, error)

	// List returns every registered entity of the given kind, sorted
	// by display name (case-insensitive). Returns an empty slice when
	// no entities are registered.
	List(kind Kind) []Entity

	// Delete removes an entity by ID. v1 succeeds unconditionally
	// even when the entity is still referenced elsewhere; ref-count
	// safety is deferred to a later task. Returns [ErrNotFound] when
	// id is unknown.
	Delete(id string) error
}

// Sentinel errors. Handlers use [errors.Is] to route them to HTTP
// status codes — see internal/admin/identity.go for the mapping.
var (
	// ErrNotFound is returned when an ID or {kind,name} lookup
	// references an entity that is not registered.
	ErrNotFound = errors.New("identity: not found")

	// ErrDisplayNameInUse is returned when an Allocate or Rename
	// would create a duplicate display name within a kind.
	ErrDisplayNameInUse = errors.New("identity: display name in use")

	// ErrInvalidDisplayName is returned when a display name fails the
	// validation regex (empty, too long, or contains disallowed
	// characters).
	ErrInvalidDisplayName = errors.New("identity: invalid display name")

	// ErrInvalidID is returned when an empty or otherwise malformed
	// ID is supplied to AllocateWithID or other ID-keyed methods.
	ErrInvalidID = errors.New("identity: invalid id")

	// ErrKindMismatch is returned when a caller-supplied kind doesn't
	// match the kind under which an entity is registered (e.g. a
	// Rename on a different-kind ID).
	ErrKindMismatch = errors.New("identity: kind mismatch")
)

// displayNameRe enforces the validation rule from
// docs/superpowers/specs/2026-05-19-prism-identity-unification.md §3.4:
//   - 1..63 characters
//   - alphanumeric + space, underscore, dot, dash
//   - leading character must be alphanumeric (no leading punctuation)
var displayNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _.-]{0,62}$`)

// validateDisplayName returns [ErrInvalidDisplayName] wrapped with a
// human-readable cause when name fails the spec's regex check.
func validateDisplayName(name string) error {
	if !displayNameRe.MatchString(name) {
		return ErrInvalidDisplayName
	}
	return nil
}
