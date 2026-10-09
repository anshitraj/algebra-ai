package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// OnchainPassRepo backs app.OnchainPassStore — see
// migrations/0020_onchain_passes.sql.
type OnchainPassRepo struct{ db *DB }

func NewOnchainPassRepo(db *DB) *OnchainPassRepo { return &OnchainPassRepo{db: db} }

var _ app.OnchainPassStore = (*OnchainPassRepo)(nil)

func (r *OnchainPassRepo) OnchainBinding(ctx context.Context, passID string) (*app.OnchainBinding, error) {
	var b app.OnchainBinding
	var number string
	err := r.db.Pool.QueryRow(ctx, `
		SELECT pass_id, user_id, network, program_id, address, owner_wallet, pass_number::text, linked_at
		FROM onchain_passes WHERE pass_id = $1`, passID).
		Scan(&b.PassID, &b.UserID, &b.Network, &b.ProgramID, &b.Address, &b.OwnerWallet, &number, &b.LinkedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: no on-chain pass for %s", shared.ErrNotFound, passID)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading on-chain pass: %w", err)
	}
	if b.PassNumber, err = strconv.ParseUint(number, 10, 64); err != nil {
		return nil, fmt.Errorf("postgres: on-chain pass number: %w", err)
	}
	b.LinkedAt = b.LinkedAt.UTC()
	return &b, nil
}

func (r *OnchainPassRepo) SaveOnchainBinding(ctx context.Context, b *app.OnchainBinding) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO onchain_passes (pass_id, user_id, network, program_id, address, owner_wallet, pass_number, linked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::numeric, $8)`,
		b.PassID, b.UserID, b.Network, b.ProgramID, b.Address, b.OwnerWallet, strconv.FormatUint(b.PassNumber, 10), b.LinkedAt.UTC())
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: that on-chain pass is already linked", shared.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("postgres: saving on-chain pass: %w", err)
	}
	return nil
}

func (r *OnchainPassRepo) DeleteOnchainBinding(ctx context.Context, passID string) error {
	if _, err := r.db.Pool.Exec(ctx, `DELETE FROM onchain_passes WHERE pass_id = $1`, passID); err != nil {
		return fmt.Errorf("postgres: unlinking on-chain pass: %w", err)
	}
	return nil
}

const pullCols = `reservation_id, intent_id, pass_id, network, pass_address, owner_wallet, amount_minor, pull_signature, pull_valid_until,
	state, used_minor, refund_minor, COALESCE(refund_signature, ''), COALESCE(refund_valid_until, 0), detail, created_at, updated_at`

func scanPull(row pgx.Row) (app.OnchainPull, error) {
	var p app.OnchainPull
	var pullValid, refundValid int64
	err := row.Scan(&p.ReservationID, &p.IntentID, &p.PassID, &p.Network, &p.PassAddress, &p.OwnerWallet, &p.AmountMinor, &p.PullSignature, &pullValid,
		&p.State, &p.UsedMinor, &p.RefundMinor, &p.RefundSignature, &refundValid, &p.Detail, &p.CreatedAt, &p.UpdatedAt)
	p.PullValidUntil, p.RefundValidUntil = uint64(pullValid), uint64(refundValid)
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, err
}

func (r *OnchainPassRepo) OnchainPull(ctx context.Context, reservationID string) (*app.OnchainPull, error) {
	p, err := scanPull(r.db.Pool.QueryRow(ctx, `SELECT `+pullCols+` FROM onchain_pulls WHERE reservation_id = $1`, reservationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: no pull for %s", shared.ErrNotFound, reservationID)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading pull: %w", err)
	}
	return &p, nil
}

func (r *OnchainPassRepo) InsertOnchainPull(ctx context.Context, p *app.OnchainPull) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO onchain_pulls (reservation_id, intent_id, pass_id, network, pass_address, owner_wallet, amount_minor,
		    pull_signature, pull_valid_until, state, detail, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		p.ReservationID, p.IntentID, p.PassID, p.Network, p.PassAddress, p.OwnerWallet, p.AmountMinor,
		p.PullSignature, int64(p.PullValidUntil), p.State, p.Detail, p.CreatedAt.UTC(), p.UpdatedAt.UTC())
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: this attempt already has a pull", shared.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("postgres: recording pull: %w", err)
	}
	return nil
}

// UpdateOnchainPull moves a pull on only if it is still in state from, so
// two processes can't both act on it (both send a refund, say).
func (r *OnchainPassRepo) UpdateOnchainPull(ctx context.Context, p *app.OnchainPull, from string) error {
	var refundSig, refundValid any
	if p.RefundSignature != "" {
		refundSig, refundValid = p.RefundSignature, int64(p.RefundValidUntil)
	}
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE onchain_pulls SET state = $2, used_minor = $3, refund_minor = $4, refund_signature = $5, refund_valid_until = $6,
		    detail = $7, updated_at = $8
		WHERE reservation_id = $1 AND state = $9`,
		p.ReservationID, p.State, p.UsedMinor, p.RefundMinor, refundSig, refundValid, p.Detail, p.UpdatedAt.UTC(), from)
	if err != nil {
		return fmt.Errorf("postgres: updating pull: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: pull %s is no longer %s", shared.ErrConflict, p.ReservationID, from)
	}
	return nil
}

func (r *OnchainPassRepo) OpenOnchainPulls(ctx context.Context, limit int) ([]app.OnchainPull, error) {
	return r.pulls(ctx, `SELECT `+pullCols+` FROM onchain_pulls WHERE state IN ('SENDING', 'PULLED', 'REFUNDING')
		ORDER BY updated_at LIMIT $1`, limit)
}

func (r *OnchainPassRepo) OnchainPullsForPass(ctx context.Context, passID string, limit int) ([]app.OnchainPull, error) {
	return r.pulls(ctx, `SELECT `+pullCols+` FROM onchain_pulls WHERE pass_id = $2 ORDER BY created_at DESC LIMIT $1`, limit, passID)
}

func (r *OnchainPassRepo) pulls(ctx context.Context, q string, args ...any) ([]app.OnchainPull, error) {
	rows, err := r.db.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing pulls: %w", err)
	}
	defer rows.Close()
	out := []app.OnchainPull{}
	for rows.Next() {
		p, err := scanPull(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning pull: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
