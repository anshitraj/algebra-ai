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
	"github.com/project-algebra/algebra/internal/domain/jobpass"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// JobPassRepo backs app.JobPassStore (migrations/0031_job_passes.sql).
//
// What a job may still spend is never stored: it is summed from the
// reservations made under the job and the budgets of its live sub-jobs, under
// the Spend Pass's row lock, by the same SQL the coordinator reserves with.
type JobPassRepo struct{ db *DB }

func NewJobPassRepo(db *DB) *JobPassRepo { return &JobPassRepo{db: db} }

var _ app.JobPassStore = (*JobPassRepo)(nil)

const jobCols = `id, user_id, pass_id, COALESCE(parent_id,''), depth, label, currency, budget_minor, state,
	created_by, COALESCE(token_hash,''), close_reason, created_at, expires_at, closed_at, value_minor`

func scanJob(row pgx.Row) (*jobpass.Job, error) {
	var j jobpass.Job
	var state string
	err := row.Scan(&j.ID, &j.UserID, &j.PassID, &j.ParentID, &j.Depth, &j.Label, &j.Currency, &j.BudgetMinor, &state,
		&j.CreatedBy, &j.TokenHash, &j.CloseReason, &j.CreatedAt, &j.ExpiresAt, &j.ClosedAt, &j.ValueMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: scanning job pass: %w", err)
	}
	j.State = jobpass.State(state)
	j.CreatedAt, j.ExpiresAt = j.CreatedAt.UTC(), j.ExpiresAt.UTC()
	return &j, nil
}

func queryJobs(ctx context.Context, q querier, sql string, args ...any) ([]jobpass.Job, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying job passes: %w", err)
	}
	defer rows.Close()
	out := []jobpass.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func getJob(ctx context.Context, q querier, id string) (*jobpass.Job, error) {
	return scanJob(q.QueryRow(ctx, `SELECT `+jobCols+` FROM job_passes WHERE id = $1`, id))
}

// passExposureSQL is what a pass has used through economic intents: holds
// that may still turn into payments, plus payments committed since $2. A job
// live at $3 counts with its whole budget instead, and so does everything
// beneath it (the "covered" jobs: a live job and all its descendants), so a
// job's spend is never counted twice. Only the top-most live jobs' budgets
// are added: a live sub-job's budget is inside its parent's.
const passExposureSQL = `
	WITH RECURSIVE covered(id) AS (
	    SELECT id FROM job_passes WHERE pass_id = $1 AND state = 'ACTIVE' AND expires_at > $3
	  UNION
	    SELECT j.id FROM job_passes j JOIN covered c ON j.parent_id = c.id
	)
	SELECT
	  COALESCE((SELECT SUM(r.hold_minor) FROM economic_reservations r
	            WHERE r.executor_pass_id = $1
	              AND (r.state IN ('RESERVED', 'EXECUTING', 'UNKNOWN', 'RECONCILING') OR (r.state = 'COMMITTED' AND r.finished_at >= $2))
	              AND (r.job_id IS NULL OR r.job_id NOT IN (SELECT id FROM covered))), 0)
	+ COALESCE((SELECT SUM(j.budget_minor) FROM job_passes j
	            WHERE j.pass_id = $1 AND j.state = 'ACTIVE' AND j.expires_at > $3
	              AND (j.parent_id IS NULL OR j.parent_id NOT IN (SELECT id FROM covered))), 0)`

// jobExposureSQL is what job $1 has used or set aside at $2: every hold and
// payment under it or under a sub-job that has ended (at any time: a job's
// budget is for its whole life), plus the whole budget of each top-most live
// sub-job, whose own spend is inside that budget.
const jobExposureSQL = `
	WITH RECURSIVE sub(id) AS (
	    SELECT $1::text
	  UNION
	    SELECT j.id FROM job_passes j JOIN sub s ON j.parent_id = s.id
	),
	covered(id) AS (
	    SELECT j.id FROM job_passes j
	    WHERE j.id IN (SELECT id FROM sub) AND j.id <> $1 AND j.state = 'ACTIVE' AND j.expires_at > $2
	  UNION
	    SELECT j.id FROM job_passes j JOIN covered c ON j.parent_id = c.id
	)
	SELECT
	  COALESCE((SELECT SUM(r.hold_minor) FROM economic_reservations r
	            WHERE r.job_id IN (SELECT id FROM sub) AND r.job_id NOT IN (SELECT id FROM covered)
	              AND r.state IN ('RESERVED', 'EXECUTING', 'UNKNOWN', 'RECONCILING', 'COMMITTED')), 0)
	+ COALESCE((SELECT SUM(j.budget_minor) FROM job_passes j
	            WHERE j.id IN (SELECT id FROM covered) AND j.state = 'ACTIVE' AND j.expires_at > $2
	              AND j.parent_id NOT IN (SELECT id FROM covered)), 0)`

func passExposure(ctx context.Context, q querier, passID string, since, now time.Time) (int64, error) {
	var total int64
	if err := q.QueryRow(ctx, passExposureSQL, passID, since, now).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: summing pass exposure: %w", err)
	}
	return total, nil
}

func jobExposure(ctx context.Context, q querier, jobID string, now time.Time) (int64, error) {
	var total int64
	if err := q.QueryRow(ctx, jobExposureSQL, jobID, now).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: summing job exposure: %w", err)
	}
	return total, nil
}

// PassSetAside implements app.PassSetAside on EconRepo: for each top-most
// live job of the pass, its budget less what the payments under it, at any
// depth, have paid or still hold. A live sub-job's unused budget is part of
// what its top job holds, so it is counted here too.
func (r *EconRepo) PassSetAside(ctx context.Context, passID string, now time.Time) (int64, error) {
	rows, err := r.db.Pool.Query(ctx, `
		WITH RECURSIVE covered(id) AS (
		    SELECT id FROM job_passes WHERE pass_id = $1 AND state = 'ACTIVE' AND expires_at > $2
		  UNION
		    SELECT j.id FROM job_passes j JOIN covered c ON j.parent_id = c.id
		)
		SELECT j.id, j.budget_minor FROM job_passes j
		WHERE j.pass_id = $1 AND j.state = 'ACTIVE' AND j.expires_at > $2
		  AND (j.parent_id IS NULL OR j.parent_id NOT IN (SELECT id FROM covered))`, passID, now)
	if err != nil {
		return 0, fmt.Errorf("postgres: reading live jobs: %w", err)
	}
	type top struct {
		id     string
		budget int64
	}
	var tops []top
	for rows.Next() {
		var t top
		if err := rows.Scan(&t.id, &t.budget); err != nil {
			rows.Close()
			return 0, fmt.Errorf("postgres: scanning live job: %w", err)
		}
		tops = append(tops, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var total int64
	for _, t := range tops {
		var used int64
		err := r.db.Pool.QueryRow(ctx, `
			WITH RECURSIVE sub(id) AS (
			    SELECT $1::text
			  UNION
			    SELECT j.id FROM job_passes j JOIN sub s ON j.parent_id = s.id
			)
			SELECT COALESCE(SUM(hold_minor), 0) FROM economic_reservations
			WHERE job_id IN (SELECT id FROM sub) AND state IN ('RESERVED', 'EXECUTING', 'UNKNOWN', 'RECONCILING', 'COMMITTED')`, t.id).Scan(&used)
		if err != nil {
			return 0, fmt.Errorf("postgres: summing a job tree's spend: %w", err)
		}
		total += max(t.budget-used, 0)
	}
	return total, nil
}

var _ app.PassSetAside = (*EconRepo)(nil)

func (r *JobPassRepo) WithPass(ctx context.Context, passID string, fn func(u app.JobUnit) error) error {
	tx, err := r.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("postgres: beginning job unit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The lock EconRepo.Atomically takes first for every reservation under
	// the pass: a job and a payment can't both fit into the same budget.
	var id string
	if err := tx.QueryRow(ctx, `SELECT id FROM spend_passes WHERE id = $1 FOR UPDATE`, passID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return shared.ErrNotFound
		}
		return fmt.Errorf("postgres: locking spend pass: %w", err)
	}
	if err := fn(&jobUnit{tx: tx, ctx: ctx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: committing job unit: %w", err)
	}
	return nil
}

type jobUnit struct {
	tx  pgx.Tx
	ctx context.Context
}

func (u *jobUnit) PassExposure(passID string, since, now time.Time) (int64, error) {
	return passExposure(u.ctx, u.tx, passID, since, now)
}

func (u *jobUnit) JobExposure(jobID string, now time.Time) (int64, error) {
	return jobExposure(u.ctx, u.tx, jobID, now)
}

func (u *jobUnit) Job(id string) (*jobpass.Job, error) { return getJob(u.ctx, u.tx, id) }

func (u *jobUnit) LiveJobs(passID string, now time.Time) (int, error) {
	var n int
	err := u.tx.QueryRow(u.ctx, `SELECT COUNT(*) FROM job_passes WHERE pass_id = $1 AND state = 'ACTIVE' AND expires_at > $2`, passID, now).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: counting live jobs: %w", err)
	}
	return n, nil
}

func (u *jobUnit) InsertJob(j *jobpass.Job) error {
	_, err := u.tx.Exec(u.ctx, `
		INSERT INTO job_passes (id, user_id, pass_id, parent_id, depth, label, currency, budget_minor, state,
		    created_by, token_hash, close_reason, created_at, expires_at, closed_at, value_minor)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		j.ID, j.UserID, j.PassID, nullable(j.ParentID), j.Depth, j.Label, j.Currency, j.BudgetMinor, string(j.State),
		j.CreatedBy, nullable(j.TokenHash), j.CloseReason, j.CreatedAt.UTC(), j.ExpiresAt.UTC(), j.ClosedAt, j.ValueMinor)
	if err != nil {
		return fmt.Errorf("postgres: inserting job pass: %w", err)
	}
	return nil
}

func (u *jobUnit) EndJobs(rootID string, state jobpass.State, reason string, now time.Time) (int, error) {
	tag, err := u.tx.Exec(u.ctx, `
		WITH RECURSIVE sub(id) AS (
		    SELECT $1::text
		  UNION
		    SELECT j.id FROM job_passes j JOIN sub s ON j.parent_id = s.id
		)
		UPDATE job_passes SET state = $2, close_reason = $3, closed_at = $4
		WHERE id IN (SELECT id FROM sub) AND state = 'ACTIVE' AND expires_at > $4`,
		rootID, string(state), reason, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: ending job passes: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *JobPassRepo) Job(ctx context.Context, id string) (*jobpass.Job, error) {
	return getJob(ctx, r.db.Pool, id)
}

func (r *JobPassRepo) JobByTokenHash(ctx context.Context, hash string) (*jobpass.Job, error) {
	return scanJob(r.db.Pool.QueryRow(ctx, `SELECT `+jobCols+` FROM job_passes WHERE token_hash = $1`, hash))
}

func (r *JobPassRepo) Jobs(ctx context.Context, userID, passID string, limit int) ([]jobpass.Job, error) {
	return queryJobs(ctx, r.db.Pool, `SELECT `+jobCols+` FROM job_passes
		WHERE user_id = $1 AND parent_id IS NULL AND ($2 = '' OR pass_id = $2)
		ORDER BY created_at DESC LIMIT $3`, userID, passID, limit)
}

func (r *JobPassRepo) Subtree(ctx context.Context, rootID string) ([]jobpass.Job, error) {
	return queryJobs(ctx, r.db.Pool, `
		WITH RECURSIVE sub(id) AS (
		    SELECT $1::text
		  UNION
		    SELECT j.id FROM job_passes j JOIN sub s ON j.parent_id = s.id
		)
		SELECT `+jobCols+` FROM job_passes WHERE id IN (SELECT id FROM sub) ORDER BY depth, created_at`, rootID)
}

func (r *JobPassRepo) JobAttempts(ctx context.Context, jobIDs []string, limit int) ([]app.JobAttempt, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT r.id, r.intent_id, r.job_id, i.capability, COALESCE(r.provider_id,''), COALESCE(r.rail,''), r.state,
		       r.hold_minor, r.evidence, i.fulfillment, COALESCE(r.outcome,''), r.created_at, r.finished_at, r.executor_agent_id
		FROM economic_reservations r JOIN economic_intents i ON i.id = r.intent_id
		WHERE r.job_id = ANY($1)
		ORDER BY r.created_at DESC LIMIT $2`, jobIDs, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading job attempts: %w", err)
	}
	defer rows.Close()
	out := []app.JobAttempt{}
	for rows.Next() {
		var a app.JobAttempt
		var state, fulfillment, outcome string
		var evidence []byte
		if err := rows.Scan(&a.ReservationID, &a.IntentID, &a.JobID, &a.Capability, &a.ProviderID, &a.Rail, &state,
			&a.HoldMinor, &evidence, &fulfillment, &outcome, &a.CreatedAt, &a.FinishedAt, &a.ExecutorAgent); err != nil {
			return nil, fmt.Errorf("postgres: scanning job attempt: %w", err)
		}
		a.State, a.Fulfillment, a.Outcome = econ.ReservationState(state), econ.Fulfillment(fulfillment), econ.Outcome(outcome)
		var ev econ.Evidence
		_ = json.Unmarshal(evidence, &ev)
		a.Transaction, a.Network = ev.Transaction, ev.Network
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *JobPassRepo) SetJobValue(ctx context.Context, id string, value *int64) error {
	tag, err := r.db.Pool.Exec(ctx, `UPDATE job_passes SET value_minor = $2 WHERE id = $1`, id, value)
	if err != nil {
		return fmt.Errorf("postgres: setting a task's value: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return shared.ErrNotFound
	}
	return nil
}

// JobSavings sums the reuse events that name each job (purchase memory).
func (r *JobPassRepo) JobSavings(ctx context.Context, jobIDs []string) (map[string]app.Saving, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT data->>'job_pass_id', COALESCE(SUM((data->>'saved_minor')::bigint), 0), COUNT(*)
		FROM economic_events
		WHERE event = 'result.reused' AND data->>'job_pass_id' = ANY($1)
		GROUP BY 1`, jobIDs)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading job savings: %w", err)
	}
	defer rows.Close()
	out := map[string]app.Saving{}
	for rows.Next() {
		var id string
		var s app.Saving
		if err := rows.Scan(&id, &s.Minor, &s.Count); err != nil {
			return nil, fmt.Errorf("postgres: scanning job savings: %w", err)
		}
		out[id] = s
	}
	return out, rows.Err()
}

// PassPnL implements app.PnLStore on EconRepo: one pass's money since a time.
func (r *EconRepo) PassPnL(ctx context.Context, passID string, since time.Time) (*app.PassMoney, error) {
	var m app.PassMoney
	err := r.db.Pool.QueryRow(ctx, `
		SELECT
		  COALESCE(SUM(rv.hold_minor) FILTER (WHERE rv.state = 'COMMITTED'), 0),
		  COALESCE(SUM(rv.hold_minor) FILTER (WHERE rv.state = 'COMMITTED' AND i.fulfillment = 'NOT_FULFILLED'), 0),
		  COUNT(*) FILTER (WHERE rv.state = 'COMMITTED' AND i.fulfillment = 'FULFILLED'),
		  COUNT(*) FILTER (WHERE rv.state IN ('RELEASED', 'EXPIRED'))
		FROM economic_reservations rv JOIN economic_intents i ON i.id = rv.intent_id
		WHERE rv.executor_pass_id = $1 AND rv.created_at >= $2`, passID, since).Scan(&m.PaidMinor, &m.WastedMinor, &m.Accepted, &m.FailedFree)
	if err != nil {
		return nil, fmt.Errorf("postgres: summing a pass's payments: %w", err)
	}
	err = r.db.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM((data->>'saved_minor')::bigint), 0), COUNT(*)
		FROM economic_events WHERE event = 'result.reused' AND data->>'spend_pass_id' = $1 AND created_at >= $2`, passID, since).Scan(&m.SavedMinor, &m.Reused)
	if err != nil {
		return nil, fmt.Errorf("postgres: summing a pass's reuse: %w", err)
	}
	err = r.db.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(blocked_attempts), 0), COALESCE(SUM(blocked_attempts::bigint * budget_max_minor), 0)
		FROM economic_intents WHERE pass_id = $1 AND created_at >= $2`, passID, since).Scan(&m.DuplicatesBlocked, &m.DuplicatePreventedMinor)
	if err != nil {
		return nil, fmt.Errorf("postgres: summing a pass's blocked duplicates: %w", err)
	}
	return &m, nil
}

var _ app.PnLStore = (*EconRepo)(nil)

func (r *JobPassRepo) ExpireJobs(ctx context.Context, now time.Time, limit int) (int, error) {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE job_passes SET state = 'EXPIRED', closed_at = expires_at, close_reason = 'expired'
		WHERE id IN (SELECT id FROM job_passes WHERE state = 'ACTIVE' AND expires_at <= $1 LIMIT $2)`, now.UTC(), sweepLimit(limit))
	if err != nil {
		return 0, fmt.Errorf("postgres: expiring job passes: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
