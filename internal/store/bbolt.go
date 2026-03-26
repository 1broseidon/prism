package store

import (
	"fmt"
	"strings"

	bolt "go.etcd.io/bbolt"
)

var defaultBucket = []byte("prism")

// BoltStore is a bbolt-backed key-value store.
// Single file, crash-safe, read-optimized, zero config.
type BoltStore struct {
	db *bolt.DB
}

// NewBoltStore opens or creates a bbolt database at the given path.
func NewBoltStore(path string) (*BoltStore, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 1})
	if err != nil {
		return nil, fmt.Errorf("open bbolt: %w", err)
	}

	// Ensure the default bucket exists.
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(defaultBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create bucket: %w", err)
	}

	return &BoltStore{db: db}, nil
}

// Get implements Store.
func (s *BoltStore) Get(key string) ([]byte, error) {
	var val []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		v := b.Get([]byte(key))
		if v == nil {
			return ErrNotFound
		}
		val = make([]byte, len(v))
		copy(val, v)
		return nil
	})
	return val, err
}

// Set implements Store.
func (s *BoltStore) Set(key string, value []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(defaultBucket).Put([]byte(key), value)
	})
}

// Delete implements Store.
func (s *BoltStore) Delete(key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(defaultBucket).Delete([]byte(key))
	})
}

// List implements Store.
func (s *BoltStore) List(prefix string) ([]string, error) {
	var keys []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		c := b.Cursor()
		pfx := []byte(prefix)
		for k, _ := c.Seek(pfx); k != nil && strings.HasPrefix(string(k), prefix); k, _ = c.Next() {
			keys = append(keys, string(k))
		}
		return nil
	})
	return keys, err
}

// Close implements Store.
func (s *BoltStore) Close() error {
	return s.db.Close()
}
