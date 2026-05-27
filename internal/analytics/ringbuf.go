package analytics

import (
	"sync"

	"github.com/1broseidon/prism/internal/auth"
)

const DefaultRingCapacity = 10_000

// RingBuffer is a fixed-size circular buffer for recent grant events.
type RingBuffer struct {
	mu     sync.RWMutex
	events []auth.GrantEvent
	next   int
	count  int
}

// NewRingBuffer constructs a fixed-capacity ring buffer.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = DefaultRingCapacity
	}
	return &RingBuffer{events: make([]auth.GrantEvent, capacity)}
}

// Add appends an event, evicting the oldest entry when full.
func (r *RingBuffer) Add(e auth.GrantEvent) {
	if r == nil || len(r.events) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[r.next] = e
	r.next = (r.next + 1) % len(r.events)
	if r.count < len(r.events) {
		r.count++
	}
}

// Latest returns the current entries oldest-to-newest.
func (r *RingBuffer) Latest() []auth.GrantEvent {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]auth.GrantEvent, 0, r.count)
	start := r.next - r.count
	if start < 0 {
		start += len(r.events)
	}
	for i := 0; i < r.count; i++ {
		idx := (start + i) % len(r.events)
		out = append(out, r.events[idx])
	}
	return out
}

// Len returns the number of buffered entries.
func (r *RingBuffer) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}
