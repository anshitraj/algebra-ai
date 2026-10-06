package app

import (
	"context"
	"time"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// StoredExecution is one attempt's record: what happened and how good the
// result was.
type StoredExecution struct {
	Result  routing.ExecutionResult `json:"result"`
	Quality *routing.QualityResult  `json:"quality,omitempty"`
}

// ExecutionStore persists what happened to each routed attempt: the record
// the router learns from and the receipt draws on. It holds hashes,
// identifiers and statuses, never a response body.
type ExecutionStore interface {
	// SaveResult inserts the record for an attempt, or replaces it (by
	// result ID). principalID is the person the intent belongs to.
	SaveResult(ctx context.Context, principalID string, rec StoredExecution) error
	// ForIntent lists an intent's records, oldest first.
	ForIntent(ctx context.Context, intentID string) ([]StoredExecution, error)
	// ForReservation returns the record of one attempt, or shared.ErrNotFound.
	ForReservation(ctx context.Context, reservationID string) (*StoredExecution, error)
	// Recent returns, for each candidate ID, up to depth of its most recent
	// attempts that started at or after since, newest first. A candidate with
	// none is absent. It is what the router builds a provider's record from
	// (see SummarizeHistory): across every person's intents, because a
	// provider's reliability isn't anyone's secret.
	Recent(ctx context.Context, candidateIDs []string, since time.Time, depth int) (map[string][]StoredExecution, error)
}
