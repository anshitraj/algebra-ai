// Package redis wraps go-redis for exactly the two things Algebra's own
// mandate scopes Redis to beyond simple caching: rate limiting and
// short-lived distributed locks (mandate §35: "cache, rate limiting,
// short-lived locks, idempotency optimization, quote caching, distributed
// coordination — Postgres remains authoritative for critical commerce
// state"). Nothing here is ever the system of record — every caller has a
// Postgres-backed fallback path that works correctly if Redis is
// unavailable, per that same principle applied consistently (the way
// Arcium is optional, Redis is optional too).
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

type Client struct {
	rdb *goredis.Client
}

// Connect opens a connection and verifies it with a ping. Callers should
// treat a failure here as "run without Redis," not as fatal — see
// wiring.Build.
func Connect(ctx context.Context, addr string) (*Client, error) {
	rdb := goredis.NewClient(&goredis.Options{Addr: addr})
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis: ping failed: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

func (c *Client) Close() error { return c.rdb.Close() }

// Ping checks the connection, for readiness probes.
func (c *Client) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }

// GetJSON reads a cached JSON value into out. Returns false when the key is
// absent or unreadable — a cache is never a source of truth.
func (c *Client) GetJSON(ctx context.Context, key string, out any) bool {
	raw, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, out) == nil
}

// SetJSON caches a value for ttl. Failures are ignored: a cache write that
// doesn't land must never fail the request it belongs to.
func (c *Client) SetJSON(ctx context.Context, key string, v any, ttl time.Duration) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = c.rdb.Set(ctx, key, raw, ttl).Err()
}

// Allow implements a fixed-window counter rate limiter: at most limit calls
// per window for the given key. Returns whether this call is allowed and,
// if not, how long until the window resets.
func (c *Client) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	count, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, 0, fmt.Errorf("redis: incrementing rate limit counter: %w", err)
	}
	if count == 1 {
		if err := c.rdb.Expire(ctx, key, window).Err(); err != nil {
			return false, 0, fmt.Errorf("redis: setting rate limit window: %w", err)
		}
	}
	if count > int64(limit) {
		ttl, err := c.rdb.TTL(ctx, key).Result()
		if err != nil || ttl < 0 {
			ttl = window
		}
		return false, ttl, nil
	}
	return true, 0, nil
}

// releaseScript deletes key only if it still holds the token this caller
// set — a compare-and-delete, so releasing a lock can never remove a
// different caller's lock acquired after this one expired. This is a
// single-instance lock (no Redlock multi-node quorum) — sufficient for
// Algebra's use (an efficiency/fast-fail optimization layered on top of
// Postgres's real correctness guarantee, e.g. approvals.MarkConsumed's
// atomic compare-and-swap), never the sole safety mechanism for a
// money-moving decision.
const releaseScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
else
	return 0
end
`

// Lock attempts to acquire a short-lived lock. If acquired, the returned
// release function must be called to free it early (it also expires
// naturally after ttl if never released, so a crashed holder can't wedge
// the lock forever).
func (c *Client) Lock(ctx context.Context, key string, ttl time.Duration) (release func(context.Context), acquired bool, err error) {
	token := uuid.NewString()
	ok, err := c.rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, false, fmt.Errorf("redis: acquiring lock %q: %w", key, err)
	}
	if !ok {
		return nil, false, nil
	}
	release = func(releaseCtx context.Context) {
		_ = c.rdb.Eval(releaseCtx, releaseScript, []string{key}, token).Err()
	}
	return release, true, nil
}
