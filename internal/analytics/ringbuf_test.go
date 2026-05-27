package analytics

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/1broseidon/prism/internal/auth"
)

func TestRingBufferEvictionAndOrder(t *testing.T) {
	r := NewRingBuffer(3)
	for i := 1; i <= 5; i++ {
		r.Add(auth.GrantEvent{RequestID: fmt.Sprintf("r-%d", i)})
	}
	got := r.Latest()
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, want := range []string{"r-3", "r-4", "r-5"} {
		if got[i].RequestID != want {
			t.Fatalf("got[%d] = %q, want %q", i, got[i].RequestID, want)
		}
	}
}

func TestRingBufferConcurrentEmit(t *testing.T) {
	r := NewRingBuffer(10_000)
	emitter := NewMultiEmitter(r, nil, 0, nil)
	var wg sync.WaitGroup
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func(group int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				emitter.Emit(context.Background(), auth.GrantEvent{
					Timestamp: time.Unix(int64(group*100+i), 0),
					RequestID: fmt.Sprintf("%d-%d", group, i),
				})
			}
		}(g)
	}
	wg.Wait()
	if got := r.Len(); got != 10_000 {
		t.Fatalf("ring len = %d, want 10000", got)
	}
}
