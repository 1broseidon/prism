package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is a Redis-backed key-value store.
// Use for shared state across multiple Prism instances.
type RedisStore struct {
	client *redis.Client
	prefix string
}

// NewRedisStore connects to a Redis instance.
// All keys are prefixed with "prism:" to avoid collisions.
func NewRedisStore(url string) (*RedisStore, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return &RedisStore{client: client, prefix: "prism:"}, nil
}

// Get implements Store.
func (s *RedisStore) Get(key string) ([]byte, error) {
	ctx := context.Background()
	val, err := s.client.Get(ctx, s.prefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	return val, err
}

// Set implements Store.
func (s *RedisStore) Set(key string, value []byte) error {
	ctx := context.Background()
	return s.client.Set(ctx, s.prefix+key, value, 0).Err()
}

// Delete implements Store.
func (s *RedisStore) Delete(key string) error {
	ctx := context.Background()
	return s.client.Del(ctx, s.prefix+key).Err()
}

// List implements Store.
func (s *RedisStore) List(prefix string) ([]string, error) {
	ctx := context.Background()
	fullPrefix := s.prefix + prefix
	var keys []string
	iter := s.client.Scan(ctx, 0, fullPrefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		// Strip the store prefix to return clean keys.
		key := iter.Val()
		if len(key) > len(s.prefix) {
			keys = append(keys, key[len(s.prefix):])
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

// Close implements Store.
func (s *RedisStore) Close() error {
	return s.client.Close()
}
