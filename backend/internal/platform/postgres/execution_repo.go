package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// ExecutionRepo backs app.ExecutionStore — see migrations/0015_route_executions.sql.
// It holds hashes, identifiers and statuses, never a response body.
type ExecutionRepo struct{ db *DB }

func NewExecutionRepo(db *DB) *ExecutionRepo { return &ExecutionRepo{db: db} }

var _ app.ExecutionStore = (*ExecutionRepo)(nil)

const executionCols = `id, intent_id, COALESCE(reservation_id,''), COALESCE(attempt,0), COALESCE(plan_id,''), COALESCE(plan_rank,0),
	COALESCE(mode,''), COALESCE(plan_hash,''), COALESCE(quote_hash,''),
	candidate_id, COALESCE(quote_id,''), provider, capability, execution_type,
	quoted_cost_minor, actual_cost_minor, started_at, completed_at, latency_ms, COALESCE(http_status,0),
	payment_status, delivery_status, COALESCE(transaction_ref,''), COALESCE(network,''), COALESCE(asset,''),
	COALESCE(request_hash,''), COALESCE(response_hash,''), COALESCE(failure_class,''), COALESCE(failure_message,''),
	test, quality`

func scanExecution(row pgx.Row) (*app.StoredExecution, error) {
	var x routing.ExecutionResult
	var mode, execType, payment, delivery, failClass, failMsg string
	var completed *time.Time
	var quality []byte
	err := row.Scan(&x.ID, &x.IntentID, &x.ReservationID, &x.Attempt, &x.PlanID, &x.PlanRank,
		&mode, &x.PlanHash, &x.QuoteHash,
		&x.CandidateID, &x.QuoteID, &x.Provider, &x.Capability, &execType,
		&x.QuotedCostMinor, &x.ActualCostMinor, &x.StartedAt, &completed, &x.LatencyMS, &x.HTTPStatus,
		&payment, &delivery, &x.Transaction, &x.Network, &x.Asset,
		&x.RequestHash, &x.ResponseHash, &failClass, &failMsg,
		&x.Test, &quality)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning execution: %w", err)
	}
	x.Mode, x.ExecutionType = routing.Mode(mode), routing.ExecutionType(execType)
	x.Payment, x.Delivery = routing.PaymentStatus(payment), econ.Fulfillment(delivery)
	if completed != nil {
		x.CompletedAt = completed.UTC()
	}
	x.StartedAt = x.StartedAt.UTC()
	if failClass != "" {
		x.Failure = &routing.Failure{Class: routing.FailureClass(failClass), Message: failMsg}
	}
	rec := &app.StoredExecution{Result: x}
	if len(quality) > 0 {
		var q routing.QualityResult
		if err := json.Unmarshal(quality, &q); err == nil {
			rec.Quality = &q
		}
	}
	return rec, nil
}

// SaveResult inserts or replaces the record for an attempt. Replacing is how a
// record is brought up to date when reconciliation resolves an ambiguous
// attempt.
func (r *ExecutionRepo) SaveResult(ctx context.Context, principalID string, rec app.StoredExecution) error {
	x := rec.Result
	var quality []byte
	var final *float64
	if rec.Quality != nil {
		var err error
		if quality, err = json.Marshal(rec.Quality); err != nil {
			return err
		}
		final = rec.Quality.FinalQuality
	}
	var completed *time.Time
	if !x.CompletedAt.IsZero() {
		c := x.CompletedAt.UTC()
		completed = &c
	}
	var failClass, failMsg any
	if x.Failure != nil {
		failClass, failMsg = nullable(string(x.Failure.Class)), nullable(x.Failure.Message)
	}
	var attempt, rank, status any
	if x.Attempt > 0 {
		attempt = x.Attempt
	}
	if x.PlanRank > 0 {
		rank = x.PlanRank
	}
	if x.HTTPStatus > 0 {
		status = x.HTTPStatus
	}
	var qualityArg any
	if quality != nil {
		qualityArg = string(quality)
	}
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO route_executions (id, principal_id, intent_id, reservation_id, attempt, plan_id, plan_rank, mode, plan_hash, quote_hash,
		    candidate_id, quote_id, provider, capability, execution_type, quoted_cost_minor, actual_cost_minor, started_at, completed_at,
		    latency_ms, http_status, payment_status, delivery_status, transaction_ref, network, asset, request_hash, response_hash,
		    failure_class, failure_message, test, quality, final_quality)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32::jsonb,$33)
		ON CONFLICT (id) DO UPDATE SET
		    actual_cost_minor = EXCLUDED.actual_cost_minor, completed_at = EXCLUDED.completed_at, latency_ms = EXCLUDED.latency_ms,
		    http_status = EXCLUDED.http_status, payment_status = EXCLUDED.payment_status, delivery_status = EXCLUDED.delivery_status,
		    transaction_ref = EXCLUDED.transaction_ref, network = EXCLUDED.network, asset = EXCLUDED.asset,
		    request_hash = EXCLUDED.request_hash, response_hash = EXCLUDED.response_hash,
		    failure_class = EXCLUDED.failure_class, failure_message = EXCLUDED.failure_message, test = EXCLUDED.test,
		    quality = EXCLUDED.quality, final_quality = EXCLUDED.final_quality, updated_at = now()`,
		x.ID, principalID, x.IntentID, nullable(x.ReservationID), attempt, nullable(x.PlanID), rank, nullable(string(x.Mode)),
		nullable(x.PlanHash), nullable(x.QuoteHash),
		x.CandidateID, nullable(x.QuoteID), x.Provider, x.Capability, string(x.ExecutionType), x.QuotedCostMinor, x.ActualCostMinor,
		x.StartedAt.UTC(), completed,
		x.LatencyMS, status, string(x.Payment), string(x.Delivery), nullable(x.Transaction), nullable(x.Network), nullable(x.Asset),
		nullable(x.RequestHash), nullable(x.ResponseHash),
		failClass, failMsg, x.Test, qualityArg, final)
	if err != nil {
		return fmt.Errorf("postgres: saving execution result: %w", err)
	}
	return nil
}

func (r *ExecutionRepo) ForIntent(ctx context.Context, intentID string) ([]app.StoredExecution, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT `+executionCols+` FROM route_executions WHERE intent_id = $1 ORDER BY started_at, attempt`, intentID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing executions: %w", err)
	}
	defer rows.Close()
	out := []app.StoredExecution{}
	for rows.Next() {
		rec, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// Recent returns each candidate's most recent attempts, newest first: the
// rows the router's record of a provider is built from. It reads across every
// person's intents on purpose (a provider's reliability is shared), and only
// the columns an attempt record holds: never a response body.
func (r *ExecutionRepo) Recent(ctx context.Context, candidateIDs []string, since time.Time, depth int) (map[string][]app.StoredExecution, error) {
	out := map[string][]app.StoredExecution{}
	if len(candidateIDs) == 0 || depth <= 0 {
		return out, nil
	}
	rows, err := r.db.Pool.Query(ctx, `
		WITH recent AS (
		    SELECT id AS recent_id, row_number() OVER (PARTITION BY candidate_id ORDER BY started_at DESC) AS rn
		    FROM route_executions
		    WHERE candidate_id = ANY($1) AND started_at >= $2
		)
		SELECT `+executionCols+`
		FROM route_executions JOIN recent ON recent.recent_id = route_executions.id
		WHERE recent.rn <= $3
		ORDER BY candidate_id, started_at DESC`, candidateIDs, since.UTC(), depth)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading recent executions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		rec, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		out[rec.Result.CandidateID] = append(out[rec.Result.CandidateID], *rec)
	}
	return out, rows.Err()
}

func (r *ExecutionRepo) ForReservation(ctx context.Context, reservationID string) (*app.StoredExecution, error) {
	return scanExecution(r.db.Pool.QueryRow(ctx, `SELECT `+executionCols+` FROM route_executions WHERE reservation_id = $1`, reservationID))
}
