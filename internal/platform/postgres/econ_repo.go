package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// EconRepo backs app.EconStore — see migrations/0014_economic_intents.sql.
//
// Correctness lives here, not in application memory: Atomically is one
// transaction holding row locks (Spend Pass first, then intent — always in
// that order, so two units can't deadlock), intent writes compare-and-swap
// on version, and the partial unique index uq_economic_reservations_one_live
// refuses a second live attempt even if every check above it raced.
type EconRepo struct{ db *DB }

func NewEconRepo(db *DB) *EconRepo { return &EconRepo{db: db} }

var _ app.EconStore = (*EconRepo)(nil)

// querier is what both the pool and a transaction offer.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const intentCols = `id, principal_id, COALESCE(pass_id,''), COALESCE(created_by_agent,''), capability, input_hash, input,
	quantity, validity_window, effect_key, intent_hash, currency, budget_max_minor, constraints, provider_policy,
	state, commitment, fulfillment, committed_minor, COALESCE(active_reservation_id,''), attempts, blocked_attempts,
	requires_approval, approved_at, version, created_at, updated_at, expires_at`

func scanEconIntent(row pgx.Row) (*econ.Intent, error) {
	var in econ.Intent
	var input, constraints, providerPolicy []byte
	var state, commitment, fulfillment string
	err := row.Scan(&in.ID, &in.PrincipalID, &in.PassID, &in.CreatedByAgent, &in.Capability, &in.InputHash, &input,
		&in.Quantity, &in.Window, &in.EffectKey, &in.IntentHash, &in.Currency, &in.BudgetMaxMinor, &constraints, &providerPolicy,
		&state, &commitment, &fulfillment, &in.CommittedMinor, &in.ActiveReservationID, &in.Attempts, &in.BlockedAttempts,
		&in.RequiresApproval, &in.ApprovedAt, &in.Version, &in.CreatedAt, &in.UpdatedAt, &in.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning economic intent: %w", err)
	}
	in.State, in.Commitment, in.Fulfillment = econ.State(state), econ.Commitment(commitment), econ.Fulfillment(fulfillment)
	in.Input = json.RawMessage(input)
	_ = json.Unmarshal(constraints, &in.Constraints)
	_ = json.Unmarshal(providerPolicy, &in.ProviderPolicy)
	return &in, nil
}

func (r *EconRepo) CreateIntent(ctx context.Context, in *econ.Intent) (*econ.Intent, bool, error) {
	constraints, err := json.Marshal(in.Constraints)
	if err != nil {
		return nil, false, err
	}
	providerPolicy, err := json.Marshal(in.ProviderPolicy)
	if err != nil {
		return nil, false, err
	}
	input := in.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	// ON CONFLICT DO NOTHING + re-read: many agents asking for the same
	// outcome at once all get the one intent that won the insert.
	tag, err := r.db.Pool.Exec(ctx, `
		INSERT INTO economic_intents (id, principal_id, pass_id, created_by_agent, capability, input_hash, input, quantity,
		    validity_window, effect_key, intent_hash, currency, budget_max_minor, constraints, provider_policy, state,
		    commitment, fulfillment, requires_approval, version, created_at, updated_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,$11,$12,$13,$14::jsonb,$15::jsonb,$16,$17,$18,$19,$20,$21,$22,$23)
		ON CONFLICT (principal_id, effect_key) DO NOTHING`,
		in.ID, in.PrincipalID, nullable(in.PassID), nullable(in.CreatedByAgent), in.Capability, in.InputHash, string(input), in.Quantity,
		in.Window, in.EffectKey, in.IntentHash, in.Currency, in.BudgetMaxMinor, string(constraints), string(providerPolicy), string(in.State),
		string(in.Commitment), string(in.Fulfillment), in.RequiresApproval, in.Version, in.CreatedAt, in.UpdatedAt, in.ExpiresAt)
	if err != nil {
		return nil, false, fmt.Errorf("postgres: inserting economic intent: %w", err)
	}
	created := tag.RowsAffected() == 1
	got, err := scanEconIntent(r.db.Pool.QueryRow(ctx, `SELECT `+intentCols+` FROM economic_intents WHERE principal_id = $1 AND effect_key = $2`,
		in.PrincipalID, in.EffectKey))
	if err != nil {
		return nil, false, err
	}
	return got, created, nil
}

func (r *EconRepo) GetIntent(ctx context.Context, id string) (*econ.Intent, error) {
	return scanEconIntent(r.db.Pool.QueryRow(ctx, `SELECT `+intentCols+` FROM economic_intents WHERE id = $1`, id))
}

func (r *EconRepo) ListIntents(ctx context.Context, principalID string, limit int) ([]econ.Intent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.Pool.Query(ctx, `SELECT `+intentCols+` FROM economic_intents WHERE principal_id = $1 ORDER BY created_at DESC LIMIT $2`,
		principalID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing economic intents: %w", err)
	}
	defer rows.Close()
	out := []econ.Intent{}
	for rows.Next() {
		in, err := scanEconIntent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *in)
	}
	return out, rows.Err()
}

const reservationCols = `id, intent_id, executor_agent_id, COALESCE(executor_pass_id,''), attempt, state, hold_minor,
	COALESCE(provider_id,''), COALESCE(rail,''), COALESCE(quote_minor,0), COALESCE(semantics,''), idempotency_key,
	COALESCE(policy_version,''), lease_expires_at, execution_deadline, evidence, COALESCE(outcome,''),
	COALESCE(release_reason,''), created_at, updated_at, finished_at`

func scanReservation(row pgx.Row) (*econ.Reservation, error) {
	var rv econ.Reservation
	var state, semantics, outcome string
	var evidence []byte
	err := row.Scan(&rv.ID, &rv.IntentID, &rv.ExecutorAgentID, &rv.ExecutorPassID, &rv.Attempt, &state, &rv.HoldMinor,
		&rv.ProviderID, &rv.Rail, &rv.QuoteMinor, &semantics, &rv.IdempotencyKey,
		&rv.PolicyVersion, &rv.LeaseExpiresAt, &rv.ExecutionDeadline, &evidence, &outcome,
		&rv.ReleaseReason, &rv.CreatedAt, &rv.UpdatedAt, &rv.FinishedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning economic reservation: %w", err)
	}
	rv.State, rv.Semantics, rv.Outcome = econ.ReservationState(state), econ.Semantics(semantics), econ.Outcome(outcome)
	_ = json.Unmarshal(evidence, &rv.Evidence)
	return &rv, nil
}

func queryReservations(ctx context.Context, q querier, where string, args ...any) ([]econ.Reservation, error) {
	rows, err := q.Query(ctx, `SELECT `+reservationCols+` FROM economic_reservations WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying economic reservations: %w", err)
	}
	defer rows.Close()
	out := []econ.Reservation{}
	for rows.Next() {
		rv, err := scanReservation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rv)
	}
	return out, rows.Err()
}

func (r *EconRepo) Reservations(ctx context.Context, intentID string) ([]econ.Reservation, error) {
	return queryReservations(ctx, r.db.Pool, `intent_id = $1 ORDER BY attempt`, intentID)
}

func (r *EconRepo) Events(ctx context.Context, intentID string) ([]app.EconEvent, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, intent_id, COALESCE(reservation_id,''), COALESCE(agent_id,''), event, COALESCE(attempt,0),
		       COALESCE(trace_id,''), data, created_at
		FROM economic_events WHERE intent_id = $1 ORDER BY seq`, intentID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing economic events: %w", err)
	}
	defer rows.Close()
	out := []app.EconEvent{}
	for rows.Next() {
		var e app.EconEvent
		var data []byte
		if err := rows.Scan(&e.ID, &e.IntentID, &e.ReservationID, &e.AgentID, &e.Event, &e.Attempt, &e.TraceID, &data, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning economic event: %w", err)
		}
		if len(data) > 2 {
			_ = json.Unmarshal(data, &e.Data)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Atomically runs fn as one transaction: lock the Spend Pass row (when
// given), then the intent row, run fn, and commit only if it returned nil.
func (r *EconRepo) Atomically(ctx context.Context, intentID, lockPassID string, fn func(u app.EconUnit) error) error {
	tx, err := r.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("postgres: beginning economic unit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if lockPassID != "" {
		// Serializes every exposure check on this pass: two intents can't
		// both fit into the same remaining budget.
		var id string
		if err := tx.QueryRow(ctx, `SELECT id FROM spend_passes WHERE id = $1 FOR UPDATE`, lockPassID).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return shared.ErrNotFound
			}
			return fmt.Errorf("postgres: locking spend pass: %w", err)
		}
	}
	in, err := scanEconIntent(tx.QueryRow(ctx, `SELECT `+intentCols+` FROM economic_intents WHERE id = $1 FOR UPDATE`, intentID))
	if err != nil {
		return err
	}
	u := &econUnit{tx: tx, ctx: ctx, intent: in, version: in.Version}
	if err := fn(u); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: committing economic unit: %w", err)
	}
	return nil
}

type econUnit struct {
	tx      pgx.Tx
	ctx     context.Context
	intent  *econ.Intent
	version int64
}

func (u *econUnit) Intent() *econ.Intent { return u.intent }

func (u *econUnit) SaveIntent(in *econ.Intent) error {
	var next int64
	err := u.tx.QueryRow(u.ctx, `
		UPDATE economic_intents SET
		    state = $3, commitment = $4, fulfillment = $5, committed_minor = $6, active_reservation_id = $7,
		    attempts = $8, blocked_attempts = $9, requires_approval = $10, approved_at = $11,
		    approved_by = CASE WHEN $11::timestamptz IS NULL THEN NULL ELSE principal_id END,
		    updated_at = $12, version = version + 1
		WHERE id = $1 AND version = $2
		RETURNING version`,
		in.ID, u.version, string(in.State), string(in.Commitment), string(in.Fulfillment), in.CommittedMinor, nullable(in.ActiveReservationID),
		in.Attempts, in.BlockedAttempts, in.RequiresApproval, in.ApprovedAt, in.UpdatedAt).Scan(&next)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: intent %s changed underneath this unit", shared.ErrConflict, in.ID)
		}
		return fmt.Errorf("postgres: saving economic intent: %w", err)
	}
	u.version, in.Version = next, next
	u.intent = in
	return nil
}

func (u *econUnit) Reservation(id string) (*econ.Reservation, error) {
	rv, err := scanReservation(u.tx.QueryRow(u.ctx, `SELECT `+reservationCols+` FROM economic_reservations WHERE id = $1 AND intent_id = $2`,
		id, u.intent.ID))
	if err != nil {
		return nil, err
	}
	return rv, nil
}

func (u *econUnit) InsertReservation(rv *econ.Reservation) error {
	evidence, err := json.Marshal(rv.Evidence)
	if err != nil {
		return err
	}
	// A savepoint, so a refused insert leaves the unit usable (to count the
	// blocked attempt) instead of aborting the whole transaction.
	sp, err := u.tx.Begin(u.ctx)
	if err != nil {
		return fmt.Errorf("postgres: savepoint: %w", err)
	}
	_, err = sp.Exec(u.ctx, `
		INSERT INTO economic_reservations (id, intent_id, executor_agent_id, executor_pass_id, attempt, state, hold_minor, currency,
		    provider_id, rail, quote_minor, semantics, idempotency_key, policy_version, lease_expires_at, execution_deadline,
		    evidence, outcome, release_reason, created_at, updated_at, finished_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17::jsonb,$18,$19,$20,$21,$22)`,
		rv.ID, rv.IntentID, rv.ExecutorAgentID, nullable(rv.ExecutorPassID), rv.Attempt, string(rv.State), rv.HoldMinor, u.intent.Currency,
		nullable(rv.ProviderID), nullable(rv.Rail), rv.QuoteMinor, nullable(string(rv.Semantics)), rv.IdempotencyKey, nullable(rv.PolicyVersion),
		rv.LeaseExpiresAt, rv.ExecutionDeadline, string(evidence), nullable(string(rv.Outcome)), nullable(rv.ReleaseReason),
		rv.CreatedAt, rv.UpdatedAt, rv.FinishedAt)
	if err != nil {
		_ = sp.Rollback(u.ctx)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			(pgErr.ConstraintName == "uq_economic_reservations_one_live" || pgErr.ConstraintName == "economic_reservations_intent_id_attempt_key") {
			return app.ErrLiveReservationExists
		}
		return fmt.Errorf("postgres: inserting economic reservation: %w", err)
	}
	return sp.Commit(u.ctx)
}

func (u *econUnit) SaveReservation(rv *econ.Reservation) error {
	evidence, err := json.Marshal(rv.Evidence)
	if err != nil {
		return err
	}
	tag, err := u.tx.Exec(u.ctx, `
		UPDATE economic_reservations SET
		    state = $3, hold_minor = $4, provider_id = $5, rail = $6, quote_minor = $7, semantics = $8, policy_version = $9,
		    lease_expires_at = $10, execution_deadline = $11, evidence = $12::jsonb, outcome = $13, release_reason = $14,
		    updated_at = $15, finished_at = $16
		WHERE id = $1 AND intent_id = $2`,
		rv.ID, u.intent.ID, string(rv.State), rv.HoldMinor, nullable(rv.ProviderID), nullable(rv.Rail), rv.QuoteMinor,
		nullable(string(rv.Semantics)), nullable(rv.PolicyVersion), rv.LeaseExpiresAt, rv.ExecutionDeadline, string(evidence),
		nullable(string(rv.Outcome)), nullable(rv.ReleaseReason), rv.UpdatedAt, rv.FinishedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: a second commitment for intent %s was refused", shared.ErrConflict, u.intent.ID)
		}
		return fmt.Errorf("postgres: saving economic reservation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return shared.ErrNotFound
	}
	return nil
}

// passExposureSQL is what a pass has used through economic intents: holds
// that may still turn into payments, plus payments committed since a time.
const passExposureSQL = `
	SELECT COALESCE(SUM(hold_minor), 0) FROM economic_reservations
	WHERE executor_pass_id = $1
	  AND (state IN ('RESERVED', 'EXECUTING', 'UNKNOWN', 'RECONCILING') OR (state = 'COMMITTED' AND finished_at >= $2))`

func (u *econUnit) PassExposure(passID string, since time.Time) (int64, error) {
	var total int64
	if err := u.tx.QueryRow(u.ctx, passExposureSQL, passID, since).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: summing pass exposure: %w", err)
	}
	return total, nil
}

// PassSpend implements app.PassEconomicSpend: the same number the
// coordinator checks under the pass's row lock, read on its own for the
// pass's view and the shopping check, which read orders the same way. Grants
// of reservations stay serialized by the coordinator, not by this read.
func (r *EconRepo) PassSpend(ctx context.Context, passID string, since time.Time) (int64, error) {
	var total int64
	if err := r.db.Pool.QueryRow(ctx, passExposureSQL, passID, since).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: summing pass exposure: %w", err)
	}
	return total, nil
}

var _ app.PassEconomicSpend = (*EconRepo)(nil)

func (u *econUnit) AppendEvent(e app.EconEvent) error {
	if err := app.ValidateEvent(e); err != nil {
		return err
	}
	return insertEvent(u.ctx, u.tx, e)
}

func insertEvent(ctx context.Context, q querier, e app.EconEvent) error {
	data := []byte(`{}`)
	if len(e.Data) > 0 {
		var err error
		if data, err = json.Marshal(e.Data); err != nil {
			return err
		}
	}
	_, err := q.Exec(ctx, `
		INSERT INTO economic_events (id, intent_id, reservation_id, agent_id, event, attempt, trace_id, data, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)`,
		e.ID, e.IntentID, nullable(e.ReservationID), nullable(e.AgentID), e.Event, e.Attempt, nullable(e.TraceID), string(data), e.CreatedAt)
	if err != nil {
		return fmt.Errorf("postgres: appending economic event: %w", err)
	}
	return nil
}

func (r *EconRepo) ExpiredLeases(ctx context.Context, now time.Time, limit int) ([]econ.Reservation, error) {
	return queryReservations(ctx, r.db.Pool, `state = 'RESERVED' AND lease_expires_at <= $1 ORDER BY lease_expires_at LIMIT $2`, now, sweepLimit(limit))
}

func (r *EconRepo) OverdueExecutions(ctx context.Context, now time.Time, limit int) ([]econ.Reservation, error) {
	return queryReservations(ctx, r.db.Pool, `state = 'EXECUTING' AND execution_deadline <= $1 ORDER BY execution_deadline LIMIT $2`, now, sweepLimit(limit))
}

func (r *EconRepo) Unresolved(ctx context.Context, limit int) ([]econ.Reservation, error) {
	return queryReservations(ctx, r.db.Pool, `state IN ('UNKNOWN', 'RECONCILING') ORDER BY updated_at LIMIT $1`, sweepLimit(limit))
}

func (r *EconRepo) ExpiredIntents(ctx context.Context, now time.Time, limit int) ([]string, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id FROM economic_intents WHERE state IN ('OPEN', 'AWAITING_APPROVAL') AND expires_at <= $1
		ORDER BY expires_at LIMIT $2`, now, sweepLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("postgres: listing expired intents: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// nullable stores "" as NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func sweepLimit(n int) int {
	if n <= 0 || n > 500 {
		return 100
	}
	return n
}

func (r *EconRepo) SaveReceipt(ctx context.Context, intentID, id, jws string, at time.Time) error {
	// One receipt per intent: a second signing attempt (two reconcilers
	// racing) keeps the first.
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO economic_receipts (id, intent_id, jws, created_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (intent_id) DO NOTHING`, id, intentID, jws, at)
	if err != nil {
		return fmt.Errorf("postgres: saving intent receipt: %w", err)
	}
	return nil
}

func (r *EconRepo) GetReceipt(ctx context.Context, intentID string) (string, error) {
	var jws string
	err := r.db.Pool.QueryRow(ctx, `SELECT jws FROM economic_receipts WHERE intent_id = $1`, intentID).Scan(&jws)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", shared.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("postgres: reading intent receipt: %w", err)
	}
	return jws, nil
}

func (r *EconRepo) Stats(ctx context.Context, principalID string, since time.Time) (*app.EconStats, error) {
	st := &app.EconStats{}
	err := r.db.Pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE state = 'COMMITTED'),
		       count(*) FILTER (WHERE state IN ('OPEN', 'AWAITING_APPROVAL', 'RESERVED', 'EXECUTING')),
		       count(*) FILTER (WHERE state IN ('UNKNOWN', 'RECONCILING')),
		       COALESCE(SUM(attempts), 0), COALESCE(SUM(blocked_attempts), 0),
		       COALESCE(SUM(committed_minor), 0),
		       COALESCE(SUM(blocked_attempts::bigint * budget_max_minor), 0)
		FROM economic_intents WHERE principal_id = $1 AND created_at >= $2`, principalID, since).
		Scan(&st.Intents, &st.Committed, &st.Open, &st.Unresolved, &st.Attempts, &st.DuplicateAttemptsBlock,
			&st.SpentMinor, &st.DuplicateSpendPreventedMinor)
	if err != nil {
		return nil, fmt.Errorf("postgres: economic stats: %w", err)
	}
	err = r.db.Pool.QueryRow(ctx, `
		SELECT count(DISTINCT ev.intent_id) FILTER (WHERE ev.event = 'execution.timeout'),
		       count(DISTINCT ev.intent_id) FILTER (WHERE ev.event = 'reconciliation.started' AND i.state NOT IN ('UNKNOWN', 'RECONCILING')),
		       COALESCE(SUM((ev.data->>'amount_minor')::bigint) FILTER (WHERE ev.event = 'payment.authorized'), 0)
		FROM economic_events ev JOIN economic_intents i ON i.id = ev.intent_id
		WHERE i.principal_id = $1 AND i.created_at >= $2`, principalID, since).
		Scan(&st.WentUnknown, &st.Reconciled, &st.AuthorizedMinor)
	if err != nil {
		return nil, fmt.Errorf("postgres: economic event stats: %w", err)
	}
	return st, nil
}
