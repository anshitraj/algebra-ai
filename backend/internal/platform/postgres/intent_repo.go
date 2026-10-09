package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type IntentRepo struct{ db *DB }

func NewIntentRepo(db *DB) *IntentRepo { return &IntentRepo{db: db} }

func (r *IntentRepo) Create(ctx context.Context, pi *intent.PurchaseIntent) error {
	items, err := json.Marshal(pi.Items)
	if err != nil {
		return fmt.Errorf("postgres: marshaling intent items: %w", err)
	}
	constraints, err := json.Marshal(pi.Constraints)
	if err != nil {
		return fmt.Errorf("postgres: marshaling intent constraints: %w", err)
	}
	metadata, err := marshalOrNull(pi.Metadata)
	if err != nil {
		return fmt.Errorf("postgres: marshaling intent metadata: %w", err)
	}

	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO purchase_intents (id, user_id, agent_id, status, items, constraints, selected_quote_id, metadata, created_at, updated_at, expires_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, NULLIF($7, ''), $8::jsonb, $9, $10, $11)`,
		pi.ID, pi.UserID, pi.AgentID, string(pi.Status), string(items), string(constraints),
		pi.SelectedQuoteID, metadata, pi.CreatedAt, pi.UpdatedAt, pi.ExpiresAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting intent: %w", err)
	}
	return nil
}

func (r *IntentRepo) Get(ctx context.Context, id string) (*intent.PurchaseIntent, error) {
	row := r.db.Pool.QueryRow(ctx, `
		SELECT id, user_id, agent_id, status, items, constraints, COALESCE(selected_quote_id, ''), metadata, created_at, updated_at, expires_at
		FROM purchase_intents WHERE id = $1`, id)
	return scanIntent(row)
}

func (r *IntentRepo) Update(ctx context.Context, pi *intent.PurchaseIntent) error {
	items, err := json.Marshal(pi.Items)
	if err != nil {
		return fmt.Errorf("postgres: marshaling intent items: %w", err)
	}
	constraints, err := json.Marshal(pi.Constraints)
	if err != nil {
		return fmt.Errorf("postgres: marshaling intent constraints: %w", err)
	}
	metadata, err := marshalOrNull(pi.Metadata)
	if err != nil {
		return fmt.Errorf("postgres: marshaling intent metadata: %w", err)
	}

	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE purchase_intents
		SET status = $2, items = $3::jsonb, constraints = $4::jsonb, selected_quote_id = NULLIF($5, ''),
		    metadata = $6::jsonb, updated_at = $7, expires_at = $8
		WHERE id = $1`,
		pi.ID, string(pi.Status), string(items), string(constraints), pi.SelectedQuoteID,
		metadata, pi.UpdatedAt, pi.ExpiresAt)
	if err != nil {
		return fmt.Errorf("postgres: updating intent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: intent %s", shared.ErrNotFound, pi.ID)
	}
	return nil
}

func scanIntent(row pgx.Row) (*intent.PurchaseIntent, error) {
	var pi intent.PurchaseIntent
	var itemsRaw, constraintsRaw, metadataRaw []byte
	var status string

	err := row.Scan(&pi.ID, &pi.UserID, &pi.AgentID, &status, &itemsRaw, &constraintsRaw,
		&pi.SelectedQuoteID, &metadataRaw, &pi.CreatedAt, &pi.UpdatedAt, &pi.ExpiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning intent: %w", err)
	}
	pi.Status = intent.State(status)
	if err := json.Unmarshal(itemsRaw, &pi.Items); err != nil {
		return nil, fmt.Errorf("postgres: decoding intent items: %w", err)
	}
	if err := json.Unmarshal(constraintsRaw, &pi.Constraints); err != nil {
		return nil, fmt.Errorf("postgres: decoding intent constraints: %w", err)
	}
	if len(metadataRaw) > 0 {
		if err := json.Unmarshal(metadataRaw, &pi.Metadata); err != nil {
			return nil, fmt.Errorf("postgres: decoding intent metadata: %w", err)
		}
	}
	return &pi, nil
}

func marshalOrNull(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}
