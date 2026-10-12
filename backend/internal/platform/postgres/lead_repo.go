package postgres

import (
	"context"
	"fmt"

	"github.com/project-algebra/algebra/internal/app"
)

// LeadRepo backs app.LeadStore — see migrations/0023_demo_requests.sql.
type LeadRepo struct{ db *DB }

func NewLeadRepo(db *DB) *LeadRepo { return &LeadRepo{db: db} }

var _ app.LeadStore = (*LeadRepo)(nil)

func (r *LeadRepo) CreateLead(ctx context.Context, l *app.Lead) error {
	if _, err := r.db.Pool.Exec(ctx, `
		INSERT INTO demo_requests (id, business_name, work_email, website, use_case, estimated_usage, message, user_id, source_ip, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,$11)`,
		l.ID, l.BusinessName, l.WorkEmail, l.Website, l.UseCase, l.EstimatedUsage, l.Message, l.UserID, l.SourceIP, l.Status, l.CreatedAt); err != nil {
		return fmt.Errorf("postgres: inserting demo request: %w", err)
	}
	return nil
}

func (r *LeadRepo) Leads(ctx context.Context, limit int) ([]app.Lead, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, business_name, work_email, website, use_case, estimated_usage, message, COALESCE(user_id,''), status, created_at
		FROM demo_requests ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing demo requests: %w", err)
	}
	defer rows.Close()
	out := []app.Lead{}
	for rows.Next() {
		var l app.Lead
		if err := rows.Scan(&l.ID, &l.BusinessName, &l.WorkEmail, &l.Website, &l.UseCase, &l.EstimatedUsage, &l.Message, &l.UserID, &l.Status, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning demo request: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
