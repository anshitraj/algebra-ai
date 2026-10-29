package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
)

// RecentPurchases implements app.PurchaseMemory on EconRepo: a principal's
// delivered purchases of one capability with one input, committed since a
// time, newest first (migrations/0032_purchase_memory.sql).
func (r *EconRepo) RecentPurchases(ctx context.Context, principalID, capability, inputHash string, since time.Time, limit int) ([]app.RememberedPurchase, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT i.id, rv.id, COALESCE(rv.provider_id,''), COALESCE(i.project_id,''), i.committed_minor, i.constraints, rv.finished_at
		FROM economic_intents i
		JOIN economic_reservations rv ON rv.intent_id = i.id AND rv.state = 'COMMITTED'
		WHERE i.principal_id = $1 AND i.capability = $2 AND i.input_hash = $3
		  AND i.state = 'COMMITTED' AND i.fulfillment = 'FULFILLED' AND rv.finished_at >= $4
		ORDER BY rv.finished_at DESC LIMIT $5`, principalID, capability, inputHash, since, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading recent purchases: %w", err)
	}
	defer rows.Close()
	out := []app.RememberedPurchase{}
	for rows.Next() {
		var p app.RememberedPurchase
		var constraints []byte
		var at *time.Time
		if err := rows.Scan(&p.IntentID, &p.ReservationID, &p.ProviderID, &p.ProjectID, &p.CommittedMinor, &constraints, &at); err != nil {
			return nil, fmt.Errorf("postgres: scanning a recent purchase: %w", err)
		}
		var c econ.Constraints
		_ = json.Unmarshal(constraints, &c)
		p.Networks = c.AllowedNetworks
		if at != nil {
			p.At = at.UTC()
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

var _ app.PurchaseMemory = (*EconRepo)(nil)
