package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/project-algebra/algebra/internal/app"
)

// FaucetRepo remembers who has had their one devnet airdrop: one row per
// account and one per wallet (migrations/0027_devnet_faucet_drops.sql).
type FaucetRepo struct{ db *DB }

func NewFaucetRepo(db *DB) *FaucetRepo { return &FaucetRepo{db: db} }

var _ app.FaucetLedger = (*FaucetRepo)(nil)

// faucetStaleClaim is how long a claim nothing was sent for is honoured. A
// server that stopped between claiming and sending would otherwise leave the
// account without its airdrop for good.
const faucetStaleClaim = 10 * time.Minute

// receiptColumns are read the same way everywhere: the time of the transfer, or of the claim until then.
const faucetReceiptColumns = `address, sol_lamports, usdc_minor, signature, COALESCE(sent_at, created_at)`

func scanFaucetReceipt(row pgx.Row) (*app.FaucetReceipt, error) {
	var got app.FaucetReceipt
	var sol, usdc int64
	if err := row.Scan(&got.Address, &sol, &usdc, &got.Signature, &got.At); err != nil {
		return nil, err
	}
	got.SOLLamports, got.USDCMinor, got.At = uint64(sol), uint64(usdc), got.At.UTC()
	return &got, nil
}

// Received is what the account was given: a sent drop, or one being sent right
// now. A claim nothing was sent for long ago doesn't count.
func (r *FaucetRepo) Received(ctx context.Context, userID string) (*app.FaucetReceipt, error) {
	got, err := scanFaucetReceipt(r.db.Pool.QueryRow(ctx, `
		SELECT `+faucetReceiptColumns+` FROM devnet_faucet_drops
		WHERE user_id = $1 AND (signature <> '' OR created_at > now() - $2::interval)`,
		userID, fmt.Sprintf("%d seconds", int(faucetStaleClaim.Seconds()))))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a faucet drop: %w", err)
	}
	return got, nil
}

// Claim reserves the account's one airdrop for address. It answers
// *app.FaucetUsedError when the account has had one, or the wallet has, on any
// account. Two claims at once cannot both win: the table's keys decide.
func (r *FaucetRepo) Claim(ctx context.Context, userID, address string, at time.Time) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: claiming a faucet drop: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	// A claim nothing was sent for, made long ago, is given up.
	if _, err := tx.Exec(ctx, `
		DELETE FROM devnet_faucet_drops
		WHERE signature = '' AND created_at < $1 AND (user_id = $2 OR address = $3)`,
		at.Add(-faucetStaleClaim).UTC(), userID, address); err != nil {
		return fmt.Errorf("postgres: claiming a faucet drop: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO devnet_faucet_drops (user_id, address, created_at) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, userID, address, at.UTC())
	if err != nil {
		return fmt.Errorf("postgres: claiming a faucet drop: %w", err)
	}
	if tag.RowsAffected() == 1 {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres: claiming a faucet drop: %w", err)
		}
		return nil
	}

	// Someone has it: the account itself, or the wallet on another account.
	var owner string
	var sol, usdc int64
	got := &app.FaucetReceipt{}
	err = tx.QueryRow(ctx, `
		SELECT user_id, `+faucetReceiptColumns+` FROM devnet_faucet_drops
		WHERE user_id = $1 OR address = $2 ORDER BY (user_id = $1) DESC LIMIT 1`, userID, address).
		Scan(&owner, &got.Address, &sol, &usdc, &got.Signature, &got.At)
	if err != nil {
		return fmt.Errorf("postgres: claiming a faucet drop: %w", err)
	}
	got.SOLLamports, got.USDCMinor, got.At = uint64(sol), uint64(usdc), got.At.UTC()
	scope := "wallet"
	if owner == userID {
		scope = "account"
	}
	// The other account's drop is not this person's to see.
	if scope == "wallet" {
		got = nil
	}
	return &app.FaucetUsedError{Scope: scope, Received: got}
}

// Record completes a claim with what was sent.
func (r *FaucetRepo) Record(ctx context.Context, userID string, d app.FaucetDrop, at time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE devnet_faucet_drops SET signature = $2, sol_lamports = $3, usdc_minor = $4, sent_at = $5
		WHERE user_id = $1`, userID, d.Signature, int64(d.SOLLamports), int64(d.USDCMinor), at.UTC())
	if err != nil {
		return fmt.Errorf("postgres: recording a faucet drop: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: recording a faucet drop: %s holds no claim", userID)
	}
	return nil
}

// Release gives the account's claim back.
func (r *FaucetRepo) Release(ctx context.Context, userID string) error {
	if _, err := r.db.Pool.Exec(ctx, `DELETE FROM devnet_faucet_drops WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("postgres: releasing a faucet claim: %w", err)
	}
	return nil
}
