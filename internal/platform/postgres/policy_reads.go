package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/app"
)

// The policy dry run's reads (app.PolicyReader): the numbers the coordinator
// checks under a pass's lock, read without one. A dry run answers "would this
// pass right now", so a read that races a real attempt is fine.

var _ app.PolicyReader = (*EconRepo)(nil)

func (r *EconRepo) PassAttemptsRecent(ctx context.Context, passID, provider string, since time.Time) (int, error) {
	var n int
	err := r.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM economic_reservations
		WHERE executor_pass_id = $1 AND created_at >= $2 AND ($3 = '' OR provider_id = $3)`, passID, since, provider).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: counting pass attempts: %w", err)
	}
	return n, nil
}

func (r *EconRepo) ProviderPaidBy(ctx context.Context, principalID, provider string) (bool, error) {
	var paid bool
	err := r.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM economic_reservations r JOIN economic_intents i ON i.id = r.intent_id
		               WHERE r.provider_id = $2 AND r.state = 'COMMITTED' AND i.principal_id = $1)`, principalID, provider).Scan(&paid)
	if err != nil {
		return false, fmt.Errorf("postgres: checking whether a provider was paid: %w", err)
	}
	return paid, nil
}
