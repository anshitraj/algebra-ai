package v1

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeLimiter is a minimal in-memory app.RateLimiter for testing the
// middleware's behavior without Redis — the fixed-window counting logic
// itself lives in internal/platform/redis and isn't what's under test here;
// this is about rateLimitMiddleware's request handling around whatever a
// RateLimiter answers.
type fakeLimiter struct {
	mu      sync.Mutex
	counts  map[string]int
	limit   int
	failErr error // if set, Allow always returns this error (tests fail-open)
}

func newFakeLimiter(limit int) *fakeLimiter {
	return &fakeLimiter{counts: map[string]int{}, limit: limit}
}

func (f *fakeLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (bool, time.Duration, error) {
	if f.failErr != nil {
		return false, 0, f.failErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts[key]++
	if f.counts[key] > f.limit {
		return false, 5 * time.Second, nil
	}
	return true, 0, nil
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
}

func TestRateLimitMiddleware_NilLimiterAlwaysAllows(t *testing.T) {
	api := &API{limiter: nil}
	handler := api.rateLimitMiddleware(okHandler())

	for i := 0; i < 200; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200 with no limiter configured, got %d", i, rec.Code)
		}
	}
}

func TestRateLimitMiddleware_BlocksAfterLimit(t *testing.T) {
	api := &API{limiter: newFakeLimiter(3)}
	handler := api.rateLimitMiddleware(okHandler())

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/merchants", nil)
		req.RemoteAddr = "10.0.0.1:5555"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d within limit: expected 200, got %d", i, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/merchants", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after exceeding limit, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected a Retry-After header on a 429 response")
	}
}

func TestRateLimitMiddleware_DifferentAgentsHaveIndependentBuckets(t *testing.T) {
	api := &API{limiter: newFakeLimiter(1)}
	handler := api.rateLimitMiddleware(okHandler())

	reqA := httptest.NewRequest(http.MethodGet, "/api/v1/intents/x", nil)
	reqA.Header.Set("Authorization", "Bearer token-agent-a")
	recA := httptest.NewRecorder()
	handler.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("agent A's first request: expected 200, got %d", recA.Code)
	}

	reqB := httptest.NewRequest(http.MethodGet, "/api/v1/intents/x", nil)
	reqB.Header.Set("Authorization", "Bearer token-agent-b")
	recB := httptest.NewRecorder()
	handler.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusOK {
		t.Fatalf("agent B's first request should not be limited by agent A's bucket, got %d", recB.Code)
	}

	// Agent A's second request should now be blocked (limit=1).
	reqA2 := httptest.NewRequest(http.MethodGet, "/api/v1/intents/x", nil)
	reqA2.Header.Set("Authorization", "Bearer token-agent-a")
	recA2 := httptest.NewRecorder()
	handler.ServeHTTP(recA2, reqA2)
	if recA2.Code != http.StatusTooManyRequests {
		t.Fatalf("agent A's second request: expected 429, got %d", recA2.Code)
	}
}

func TestRateLimitMiddleware_FailsOpenOnLimiterError(t *testing.T) {
	api := &API{limiter: &fakeLimiter{failErr: errors.New("redis: connection refused")}}
	handler := api.rateLimitMiddleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected a Redis error to fail OPEN (200), got %d", rec.Code)
	}
}

func TestRateLimitKey_PrefersAgentTokenOverIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:9999"
	req.Header.Set("Authorization", "Bearer sometoken")
	key := rateLimitKey(req)
	if key[:6] != "agent:" {
		t.Errorf("expected an agent: key when Authorization is present, got %q", key)
	}
	if key == "agent:sometoken" {
		t.Error("expected the raw token to be hashed, not embedded directly in the rate-limit key")
	}
}

func TestRateLimitKey_FallsBackToIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:9999"
	key := rateLimitKey(req)
	if key != "ip:1.2.3.4" {
		t.Errorf("expected ip:1.2.3.4, got %q", key)
	}
}

func TestRateLimitKey_SandboxProvidersHaveTheirOwnBucket(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandbox/x402/prices/beta", nil)
	req.RemoteAddr = "1.2.3.4:9999"
	if key := rateLimitKey(req); key != "sandbox-provider:1.2.3.4" {
		t.Errorf("the server's own calls to its sandbox providers must not share the anonymous bucket: %q", key)
	}
}
