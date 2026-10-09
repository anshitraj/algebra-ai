// Package shared holds the handful of sentinel errors used across domain
// packages, so callers can errors.Is against one definition instead of each
// package inventing its own "not implemented" error.
package shared

import "errors"

// ErrNotImplemented marks a provider/connector method that exists for
// architectural completeness (interface satisfied, plumbing in place) but
// has no real backing integration yet — typically because external
// credentials, a signed partnership API, or a published wire contract are
// not available in this environment. It must never be swallowed into a
// synthesized success; callers surface it as a failure state
// (USER_INTERVENTION_REQUIRED, NOT_YET_IMPLEMENTED, etc.), never a fake
// receipt or order.
var ErrNotImplemented = errors.New("algebra: not implemented — external integration not configured")

// ErrUnauthorized indicates a caller (agent/user) lacked the permission or
// authentication required for the requested operation.
var ErrUnauthorized = errors.New("algebra: unauthorized")

// ErrNotFound indicates the requested entity does not exist or is not
// visible to the caller.
var ErrNotFound = errors.New("algebra: not found")

// ErrConflict indicates a concurrency/state conflict — e.g. an idempotency
// key reused with a different payload, or a state transition attempted from
// a stale in-memory copy.
var ErrConflict = errors.New("algebra: conflict")
