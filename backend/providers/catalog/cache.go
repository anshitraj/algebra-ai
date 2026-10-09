package catalog

import (
	"context"
	"sync"
	"time"
)

// Cached is one value read from a third party. It is served for TTL without
// asking again; after that a refresh is tried, shared by every caller that
// arrives meanwhile. When a refresh fails, the old value keeps being served,
// marked stale, for up to StaleFor, and the source isn't asked again for
// RetryAfter, so an outage neither empties the page nor turns every request
// into a slow failing fetch. Safe for concurrent use; the zero value needs only
// its durations.
type Cached[T any] struct {
	TTL, StaleFor, RetryAfter time.Duration
	// RefreshTimeout bounds one fetch; it isn't tied to any one caller.
	RefreshTimeout time.Duration
	Now            func() time.Time

	mu      sync.Mutex
	val     T
	have    bool
	fetched time.Time
	failed  time.Time
	flight  *flight
}

type flight struct {
	done chan struct{}
	err  error
}

func (c *Cached[T]) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Get returns the value, when it was fetched, and whether it is stale.
// Without a usable value it returns ErrUnavailable (or ctx's error).
func (c *Cached[T]) Get(ctx context.Context, fetch func(context.Context) (T, error)) (T, time.Time, bool, error) {
	now := c.now()
	c.mu.Lock()
	val, have, fetched, failed := c.val, c.have, c.fetched, c.failed
	c.mu.Unlock()

	if have && now.Sub(fetched) < c.TTL {
		return val, fetched, false, nil
	}
	recentlyFailed := !failed.IsZero() && now.Sub(failed) < c.RetryAfter
	if !recentlyFailed {
		if err := c.refresh(ctx, fetch); err == nil {
			c.mu.Lock()
			val, fetched = c.val, c.fetched
			c.mu.Unlock()
			return val, fetched, false, nil
		} else if ctx.Err() != nil {
			var zero T
			return zero, time.Time{}, false, ctx.Err()
		}
	}
	if have && now.Sub(fetched) < c.TTL+c.StaleFor {
		return val, fetched, true, nil
	}
	var zero T
	return zero, time.Time{}, false, ErrUnavailable
}

// refresh runs fetch once for everyone who needs it at the same time. The
// fetch isn't tied to any one caller's context, so a caller that gives up
// doesn't fail the others; each caller can still stop waiting.
func (c *Cached[T]) refresh(ctx context.Context, fetch func(context.Context) (T, error)) error {
	c.mu.Lock()
	f := c.flight
	if f == nil {
		f = &flight{done: make(chan struct{})}
		c.flight = f
		timeout := c.RefreshTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		go func() {
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
			defer cancel()
			v, err := fetch(fctx)
			c.mu.Lock()
			if err != nil {
				c.failed = c.now()
			} else {
				c.val, c.have, c.fetched, c.failed = v, true, c.now(), time.Time{}
			}
			c.flight = nil
			c.mu.Unlock()
			f.err = err
			close(f.done)
		}()
	}
	c.mu.Unlock()
	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
