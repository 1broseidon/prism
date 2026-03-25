package middleware

import (
	"sync"
	"time"
)

// CBState represents the circuit breaker state.
type CBState int

// Circuit breaker states.
const (
	CBClosed   CBState = iota // Normal operation
	CBOpen                    // Rejecting calls
	CBHalfOpen                // Probing for recovery
)

func (s CBState) String() string {
	switch s {
	case CBOpen:
		return "open"
	case CBHalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

// CircuitBreakerConfig configures a CircuitBreaker.
type CircuitBreakerConfig struct {
	Threshold   int           // Consecutive failures to trip
	Timeout     time.Duration // Wait in Open before probing
	MaxHalfOpen int           // Max requests in HalfOpen
}

// CircuitBreaker implements the circuit breaker pattern for backend MCP servers.
type CircuitBreaker struct {
	mu            sync.Mutex
	state         CBState
	failures      int
	lastFailure   time.Time
	halfOpenCount int
	cfg           CircuitBreakerConfig
}

// NewCircuitBreaker creates a CircuitBreaker in the Closed state.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{cfg: cfg}
}

// Allow returns true if the request should proceed.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CBClosed:
		return true
	case CBOpen:
		if time.Since(cb.lastFailure) >= cb.cfg.Timeout {
			cb.state = CBHalfOpen
			cb.halfOpenCount = 0
			return true
		}
		return false
	case CBHalfOpen:
		limit := cb.cfg.MaxHalfOpen
		if limit <= 0 {
			limit = 1
		}
		if cb.halfOpenCount < limit {
			cb.halfOpenCount++
			return true
		}
		return false
	}
	return false
}

// RecordSuccess records a successful call.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CBHalfOpen:
		cb.state = CBClosed
		cb.failures = 0
	case CBClosed:
		cb.failures = 0
	case CBOpen:
		// no action
	}
}

// RecordFailure records a failed call.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailure = time.Now()

	switch cb.state {
	case CBClosed:
		cb.failures++
		if cb.cfg.Threshold > 0 && cb.failures >= cb.cfg.Threshold {
			cb.state = CBOpen
		}
	case CBHalfOpen:
		cb.state = CBOpen
		cb.halfOpenCount = 0
	}
}

// State returns the current state.
func (cb *CircuitBreaker) State() CBState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}
