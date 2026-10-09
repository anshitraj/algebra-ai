package mcpserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/app"
)

// fakeLimiter is a minimal in-memory app.RateLimiter, mirroring
// internal/api/v1's own test fake — this package can't import that
// unexported type, and duplicating ~15 lines is cheaper than exporting it
// just for tests.
type fakeLimiter struct {
	mu      sync.Mutex
	counts  map[string]int
	limit   int
	failErr error
}

func newFakeLimiter(limit int) *fakeLimiter {
	return &fakeLimiter{counts: map[string]int{}, limit: limit}
}

func (f *fakeLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, time.Duration, error) {
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

type echoInput struct {
	AgentToken string `json:"agent_token"`
}
type echoOutput struct {
	OK bool `json:"ok"`
}

// buildTestServer wires the rate-limit middleware onto a bare go-sdk server
// with one trivial echo tool, and connects a client to it over the SDK's
// own in-memory transport — this exercises the real middleware chain
// (AddReceivingMiddleware → tools/call → our rateLimitMiddleware), not a
// hand-rolled substitute for it.
// limiter is typed as the app.RateLimiter interface, not *fakeLimiter — so
// that passing a literal nil produces a genuinely nil interface. A nil
// *fakeLimiter wrapped into the interface would NOT compare equal to nil
// inside rateLimitMiddleware (the classic Go typed-nil-in-interface trap),
// which is exactly the bug this signature avoids.
func buildTestServer(t *testing.T, limiter app.RateLimiter) *gomcp.ClientSession {
	t.Helper()
	srv := &Server{Limiter: limiter}
	s := gomcp.NewServer(&gomcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	s.AddReceivingMiddleware(srv.rateLimitMiddleware)
	gomcp.AddTool(s, &gomcp.Tool{Name: "test.echo", Description: "echo"}, func(_ context.Context, _ *gomcp.CallToolRequest, in echoInput) (*gomcp.CallToolResult, echoOutput, error) {
		return nil, echoOutput{OK: true}, nil
	})

	serverTransport, clientTransport := gomcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect failed: %v", err)
	}
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect failed: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callEcho(t *testing.T, cs *gomcp.ClientSession, agentToken string) error {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      "test.echo",
		Arguments: map[string]any{"agent_token": agentToken},
	})
	if err != nil {
		return err
	}
	if res.IsError {
		return errors.New("tool reported an error result")
	}
	return nil
}

func TestMCPRateLimit_NilLimiterAlwaysAllows(t *testing.T) {
	cs := buildTestServer(t, nil)
	for i := 0; i < 10; i++ {
		if err := callEcho(t, cs, "tok"); err != nil {
			t.Fatalf("call %d: unexpected error with no limiter configured: %v", i, err)
		}
	}
}

func TestMCPRateLimit_BlocksAfterLimitPerAgent(t *testing.T) {
	limiter := newFakeLimiter(2)
	cs := buildTestServer(t, limiter)

	for i := 0; i < 2; i++ {
		if err := callEcho(t, cs, "agent-a"); err != nil {
			t.Fatalf("call %d within limit: unexpected error: %v", i, err)
		}
	}
	if err := callEcho(t, cs, "agent-a"); err == nil {
		t.Fatal("expected the 3rd call from the same agent to be rate-limited")
	}

	// A different agent has its own bucket.
	if err := callEcho(t, cs, "agent-b"); err != nil {
		t.Fatalf("a different agent's first call should not be limited by agent-a's bucket: %v", err)
	}
}

func TestMCPRateLimit_FailsOpenOnLimiterError(t *testing.T) {
	limiter := &fakeLimiter{failErr: errors.New("redis down")}
	cs := buildTestServer(t, limiter)
	if err := callEcho(t, cs, "agent-a"); err != nil {
		t.Fatalf("expected a limiter error to fail open, got error: %v", err)
	}
}
