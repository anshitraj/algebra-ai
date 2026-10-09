package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type QuoteRepo struct{ db *DB }

func NewQuoteRepo(db *DB) *QuoteRepo { return &QuoteRepo{db: db} }

func (r *QuoteRepo) Save(ctx context.Context, intentID string, q *quote.CheckoutQuote) error {
	payload, err := json.Marshal(q)
	if err != nil {
		return fmt.Errorf("postgres: marshaling quote: %w", err)
	}
	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO quotes (id, intent_id, merchant, payload, expires_at, retrieved_at)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6)
		ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload, expires_at = EXCLUDED.expires_at, retrieved_at = EXCLUDED.retrieved_at`,
		q.QuoteID, intentID, q.Merchant, string(payload), q.ExpiresAt, q.RetrievedAt)
	if err != nil {
		return fmt.Errorf("postgres: saving quote: %w", err)
	}
	return nil
}

func (r *QuoteRepo) Get(ctx context.Context, quoteID string) (*quote.CheckoutQuote, error) {
	row := r.db.Pool.QueryRow(ctx, `SELECT payload FROM quotes WHERE id = $1`, quoteID)
	return scanQuote(row)
}

func (r *QuoteRepo) ListByIntent(ctx context.Context, intentID string) ([]*quote.CheckoutQuote, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT payload FROM quotes WHERE intent_id = $1 ORDER BY created_at ASC`, intentID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing quotes: %w", err)
	}
	defer rows.Close()

	var out []*quote.CheckoutQuote
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: scanning quote: %w", err)
		}
		var q quote.CheckoutQuote
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, fmt.Errorf("postgres: decoding quote: %w", err)
		}
		out = append(out, &q)
	}
	return out, nil
}

func scanQuote(row pgx.Row) (*quote.CheckoutQuote, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning quote: %w", err)
	}
	var q quote.CheckoutQuote
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, fmt.Errorf("postgres: decoding quote: %w", err)
	}
	return &q, nil
}
