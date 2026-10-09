// Package resilience implements the per-provider circuit breaker the
// mandate names explicitly for merchant discovery fan-out (§38: "Use
// concurrent merchant discovery... with per-provider timeouts and circuit
// breakers"). It is deliberately small: a classic closed/open/half-open
// breaker, nothing framework-shaped.
package resilience

import (
	"sync"
	"time"
)

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Open:
		return "OPEN"
	case HalfOpen:
		return "HALF_OPEN"
	default:
		return "CLOSED"
	}
}

// CircuitBreaker trips to Open after failureThreshold consecutive failures,
// rejecting calls until resetTimeout has passed, then allows exactly one
// trial call (HalfOpen) — success closes it again, failure re-opens it.
type CircuitBreaker struct {
	mu               sync.Mutex
	failureThreshold int
	resetTimeout     time.Duration
	state            State
	consecutiveFails int
	openedAt         time.Time
}

func NewCircuitBreaker(failureThreshold int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{failureThreshold: failureThreshold, resetTimeout: resetTimeout}
}

// Allow reports whether a call may proceed right now. Every attempted call
// — including a HalfOpen trial — must be followed by exactly one of
// RecordSuccess/RecordFailure.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case Open:
		if time.Since(cb.openedAt) < cb.resetTimeout {
			return false
		}
		cb.state = HalfOpen
		return true
	default:
		return true
	}
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFails = 0
	cb.state = Closed
}

func (cb *CircuitBreaker) RecordFailure(now time.Time) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFails++
	if cb.state == HalfOpen || cb.consecutiveFails >= cb.failureThreshold {
		cb.state = Open
		cb.openedAt = now
	}
}

func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Registry hands out one CircuitBreaker per key (e.g. per merchant
// connector name), created lazily with shared thresholds.
type Registry struct {
	mu               sync.Mutex
	breakers         map[string]*CircuitBreaker
	failureThreshold int
	resetTimeout     time.Duration
}

func NewRegistry(failureThreshold int, resetTimeout time.Duration) *Registry {
	return &Registry{breakers: map[string]*CircuitBreaker{}, failureThreshold: failureThreshold, resetTimeout: resetTimeout}
}

func (r *Registry) Get(key string) *CircuitBreaker {
	r.mu.Lock()
	defer r.mu.Unlock()
	cb, ok := r.breakers[key]
	if !ok {
		cb = NewCircuitBreaker(r.failureThreshold, r.resetTimeout)
		r.breakers[key] = cb
	}
	return cb
}
