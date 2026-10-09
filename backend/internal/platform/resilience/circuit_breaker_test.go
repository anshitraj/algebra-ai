package resilience

import (
	"testing"
	"time"
)

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	now := time.Now()
	for i := 0; i < 2; i++ {
		if !cb.Allow() {
			t.Fatalf("expected Allow before threshold, iteration %d", i)
		}
		cb.RecordFailure(now)
	}
	if cb.State() != Closed {
		t.Fatalf("expected still CLOSED after 2/3 failures, got %s", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("expected Allow on the 3rd attempt")
	}
	cb.RecordFailure(now)
	if cb.State() != Open {
		t.Fatalf("expected OPEN after 3 consecutive failures, got %s", cb.State())
	}
	if cb.Allow() {
		t.Fatal("expected Allow to be false while OPEN and within resetTimeout")
	}
}

func TestCircuitBreaker_HalfOpenAfterResetTimeout(t *testing.T) {
	cb := NewCircuitBreaker(1, 10*time.Millisecond)
	now := time.Now()
	cb.RecordFailure(now) // trips open immediately (threshold=1)
	if cb.State() != Open {
		t.Fatalf("expected OPEN, got %s", cb.State())
	}
	if cb.Allow() {
		t.Fatal("expected Allow false immediately after opening")
	}

	time.Sleep(15 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("expected Allow true (half-open trial) after resetTimeout elapsed")
	}
	if cb.State() != HalfOpen {
		t.Fatalf("expected HALF_OPEN after the trial Allow, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenFailureReopensImmediately(t *testing.T) {
	cb := NewCircuitBreaker(5, 10*time.Millisecond) // high threshold — must not matter in half-open
	now := time.Now()
	cb.RecordFailure(now)
	cb.RecordFailure(now)
	if cb.State() != Closed {
		t.Fatalf("expected CLOSED (below threshold), got %s", cb.State())
	}

	// Force it open via enough failures, then wait for half-open.
	cb2 := NewCircuitBreaker(1, 10*time.Millisecond)
	cb2.RecordFailure(now)
	time.Sleep(15 * time.Millisecond)
	if !cb2.Allow() {
		t.Fatal("expected half-open trial to be allowed")
	}
	cb2.RecordFailure(time.Now())
	if cb2.State() != Open {
		t.Fatalf("expected a half-open trial failure to reopen immediately, got %s", cb2.State())
	}
}

func TestCircuitBreaker_SuccessClosesAndResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	now := time.Now()
	cb.RecordFailure(now)
	cb.RecordFailure(now)
	cb.RecordSuccess()
	if cb.State() != Closed {
		t.Fatalf("expected CLOSED after success, got %s", cb.State())
	}
	// Failure count should have reset — two more failures should not trip it.
	cb.RecordFailure(now)
	cb.RecordFailure(now)
	if cb.State() != Closed {
		t.Fatalf("expected CLOSED (failure count reset by success), got %s", cb.State())
	}
}

func TestRegistry_ReturnsSameBreakerPerKey(t *testing.T) {
	r := NewRegistry(3, time.Minute)
	a1 := r.Get("zepto")
	a2 := r.Get("zepto")
	b := r.Get("amazon")
	if a1 != a2 {
		t.Error("expected the same *CircuitBreaker instance for the same key")
	}
	if a1 == b {
		t.Error("expected different *CircuitBreaker instances for different keys")
	}
}
