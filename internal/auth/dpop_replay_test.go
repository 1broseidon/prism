package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestDPoPReplayCacheSeenExpiryAndCapacity(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewReplayCache(time.Minute, 2)
	if cache.Seen("a", now) {
		t.Fatal("first a should be unseen")
	}
	if !cache.Seen("a", now.Add(time.Second)) {
		t.Fatal("second a should be seen")
	}
	if !cache.Seen("a", now.Add(time.Minute)) {
		t.Fatal("a at exact expiry should still be seen")
	}
	if cache.Seen("a", now.Add(time.Minute+time.Nanosecond)) {
		t.Fatal("a after expiry should be unseen")
	}

	cache = NewReplayCache(time.Hour, 2)
	_ = cache.Seen("a", now)
	_ = cache.Seen("b", now)
	_ = cache.Seen("c", now)
	if !cache.Seen("b", now) || !cache.Seen("c", now) {
		t.Fatal("newer entries should remain")
	}
	if cache.Seen("a", now) {
		t.Fatal("oldest entry should have been evicted")
	}
}

func TestDPoPReplayCacheConcurrentSeen(t *testing.T) {
	cache := NewReplayCache(time.Minute, 20_000)
	now := time.Unix(100, 0)
	var wg sync.WaitGroup
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = cache.Seen(fmt.Sprintf("jti-%d", base*100+i), now)
			}
		}(g)
	}
	wg.Wait()
}
