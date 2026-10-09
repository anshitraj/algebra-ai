package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

// EconStore persists economic intents, reservations, lifecycle events and
// receipts (migrations/0014_economic_intents.sql).
//
// Every state change goes through Atomically, which runs fn with the intent
// row locked — and, when lockPassID is set, the executor's Spend Pass row
// locked first — and commits only if fn returns nil. That's what makes
// "check the state, then write the reservation" one indivisible step: two
// executors racing for the same intent are serialized by the database, and
// a partial unique index backs the rule up even if a check were wrong.
type EconStore interface {
	// CreateIntent inserts an intent, or returns the existing one with the
	// same (principal, effect key) and created=false.
	CreateIntent(ctx context.Context, in *econ.Intent) (*econ.Intent, bool, error)
	GetIntent(ctx context.Context, id string) (*econ.Intent, error)
	ListIntents(ctx context.Context, principalID string, limit int) ([]econ.Intent, error)
	Reservations(ctx context.Context, intentID string) ([]econ.Reservation, error)
	Events(ctx context.Context, intentID string) ([]EconEvent, error)

	Atomically(ctx context.Context, intentID, lockPassID string, fn func(u EconUnit) error) error

	// Sweeper queries.
	ExpiredLeases(ctx context.Context, now time.Time, limit int) ([]econ.Reservation, error)
	OverdueExecutions(ctx context.Context, now time.Time, limit int) ([]econ.Reservation, error)
	Unresolved(ctx context.Context, limit int) ([]econ.Reservation, error)
	ExpiredIntents(ctx context.Context, now time.Time, limit int) ([]string, error)

	SaveReceipt(ctx context.Context, intentID, id, jws string, at time.Time) error
	GetReceipt(ctx context.Context, intentID string) (string, error)
	Stats(ctx context.Context, principalID string, since time.Time) (*EconStats, error)
}

// EconUnit is one serialized unit of work on an intent.
type EconUnit interface {
	// Intent is the locked intent; mutate it and SaveIntent to persist.
	Intent() *econ.Intent
	// SaveIntent writes the intent, compare-and-swap on the version read
	// when the lock was taken.
	SaveIntent(in *econ.Intent) error
	Reservation(id string) (*econ.Reservation, error)
	// InsertReservation fails with ErrLiveReservationExists when the intent
	// already has a live reservation (the partial unique index).
	InsertReservation(r *econ.Reservation) error
	SaveReservation(r *econ.Reservation) error
	// PassExposure is what the pass has committed to economic intents in
	// its window plus every live hold: money that is spent or may be.
	PassExposure(passID string, since time.Time) (int64, error)
	// PassAttemptsSince counts the reservations a pass was granted since a
	// time, to one provider or (provider "") to any: the velocity limit.
	PassAttemptsSince(passID, provider string, since time.Time) (int, error)
	// ProviderPaid reports whether the principal has ever committed money to
	// the provider: the new-provider gate.
	ProviderPaid(principalID, provider string) (bool, error)
	AppendEvent(e EconEvent) error
}

// ErrLiveReservationExists: the one-live-reservation index refused a
// second attempt.
var ErrLiveReservationExists = errors.New("app: intent already has a live reservation")

// EconEvent is one append-only lifecycle event.
type EconEvent struct {
	ID            string         `json:"id"`
	IntentID      string         `json:"intent_id"`
	ReservationID string         `json:"reservation_id,omitempty"`
	AgentID       string         `json:"agent_id,omitempty"`
	Event         string         `json:"event"`
	Attempt       int            `json:"attempt,omitempty"`
	TraceID       string         `json:"trace_id,omitempty"`
	Data          map[string]any `json:"data,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

// forbiddenEventKeys mirrors the audit log's deny-list: telemetry is
// append-only, so a secret written here could never be scrubbed.
var forbiddenEventKeys = []string{"token", "secret", "private_key", "seed", "password", "otp", "cvv", "pan", "card_number", "authorization", "cookie"}

// ValidateEvent refuses event data whose key names look like secrets.
func ValidateEvent(e EconEvent) error {
	for k := range e.Data {
		lower := strings.ToLower(k)
		for _, f := range forbiddenEventKeys {
			if strings.Contains(lower, f) {
				return fmt.Errorf("app: event data key %q looks like a secret", k)
			}
		}
	}
	return nil
}

// EconStats are a principal's coordination numbers for a period — the
// dashboard's and the billing meter's source.
type EconStats struct {
	Intents                int   `json:"intents"`
	Committed              int   `json:"committed"`
	Open                   int   `json:"open"`
	Unresolved             int   `json:"unresolved"`
	Attempts               int   `json:"execution_attempts"`
	DuplicateAttemptsBlock int   `json:"duplicate_commit_attempts_blocked"`
	WentUnknown            int   `json:"went_unknown"`
	Reconciled             int   `json:"reconciled"`
	AuthorizedMinor        int64 `json:"authorized_minor"`
	SpentMinor             int64 `json:"spent_minor"`
	// DuplicateSpendPreventedMinor: blocked attempts × their intent's
	// budget ceiling — an upper bound, labelled as such in the UI.
	DuplicateSpendPreventedMinor int64 `json:"duplicate_spend_prevented_minor"`
}
