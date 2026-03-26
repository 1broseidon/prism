// Package store provides a pluggable key-value store interface for Prism.
//
// The default implementation uses bbolt (embedded, single file, zero config).
// Operators can switch to Redis for shared state across multiple instances.
package store

import "errors"

// ErrNotFound is returned when a key does not exist in the store.
var ErrNotFound = errors.New("key not found")

// Store is a simple key-value interface for persisting Prism state
// (DCR clients, refresh tokens, etc.).
type Store interface {
	// Get retrieves the value for a key. Returns ErrNotFound if the key doesn't exist.
	Get(key string) ([]byte, error)

	// Set stores a key-value pair. Overwrites any existing value.
	Set(key string, value []byte) error

	// Delete removes a key. No error if the key doesn't exist.
	Delete(key string) error

	// List returns all keys with the given prefix.
	List(prefix string) ([]string, error)

	// Close releases any resources held by the store.
	Close() error
}
