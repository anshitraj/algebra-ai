package app

import (
	"context"
	"encoding/json"
	"fmt"
)

// RunIdempotent executes fn at most once per (key, scope): a second call
// with the same key/scope replays the first call's result instead of
// re-executing fn. This is what makes retried create_intent/approve/
// execute/place_order/cancel_order calls safe (mandate §31) — the caller
// doesn't need to reason about retries, RunIdempotent does it once, here.
//
// An empty key disables idempotency for that call (some read-only or
// intentionally-repeatable operations don't need it) — fn just runs.
//
// fn's result is only recorded once it returns without error; an error
// leaves the key reservation open so a genuine retry can succeed.
func RunIdempotent[T any](ctx context.Context, store IdempotencyStore, key, scope string, fn func(ctx context.Context) (T, error)) (T, error) {
	var zero T
	if key == "" {
		return fn(ctx)
	}

	existing, alreadyDone, err := store.Begin(ctx, key, scope)
	if err != nil {
		return zero, fmt.Errorf("app: reserving idempotency key: %w", err)
	}
	if alreadyDone {
		var result T
		if err := json.Unmarshal(existing, &result); err != nil {
			return zero, fmt.Errorf("app: decoding idempotent replay for %s/%s: %w", scope, key, err)
		}
		return result, nil
	}

	result, err := fn(ctx)
	if err != nil {
		return zero, err
	}

	payload, err := json.Marshal(result)
	if err != nil {
		return zero, fmt.Errorf("app: encoding idempotent result: %w", err)
	}
	if err := store.Complete(ctx, key, scope, payload); err != nil {
		return zero, fmt.Errorf("app: recording idempotent result: %w", err)
	}
	return result, nil
}
