package auth

import (
	"container/list"
	"sync"
	"time"
)

// ReplayCache is a thread-safe TTL/capacity-bounded JTI replay cache.
type ReplayCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	items    map[string]*list.Element
	order    *list.List
}

type replayEntry struct {
	jti     string
	expires time.Time
}

// NewReplayCache creates a replay cache. Non-positive capacity disables
// storage and therefore treats every JTI as unseen.
func NewReplayCache(ttl time.Duration, capacity int) *ReplayCache {
	return &ReplayCache{
		ttl:      ttl,
		capacity: capacity,
		items:    make(map[string]*list.Element),
		order:    list.New(),
	}
}

// Seen returns true when jti is already present and unexpired. Otherwise it
// marks the JTI as seen and returns false.
func (c *ReplayCache) Seen(jti string, now time.Time) bool {
	if c == nil || c.capacity <= 0 || c.ttl <= 0 || jti == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.evictExpired(now)
	if elem, ok := c.items[jti]; ok {
		ent := elem.Value.(replayEntry)
		if now.Before(ent.expires) || now.Equal(ent.expires) {
			return true
		}
		c.removeElement(elem)
	}
	for len(c.items) >= c.capacity {
		c.removeElement(c.order.Front())
	}
	ent := replayEntry{jti: jti, expires: now.Add(c.ttl)}
	c.items[jti] = c.order.PushBack(ent)
	return false
}

func (c *ReplayCache) evictExpired(now time.Time) {
	for elem := c.order.Front(); elem != nil; {
		next := elem.Next()
		ent := elem.Value.(replayEntry)
		if now.After(ent.expires) {
			c.removeElement(elem)
		}
		elem = next
	}
}

func (c *ReplayCache) removeElement(elem *list.Element) {
	if elem == nil {
		return
	}
	ent := elem.Value.(replayEntry)
	delete(c.items, ent.jti)
	c.order.Remove(elem)
}
