package app

import (
	"context"
	"fmt"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// AuditReader is the read side of the append-only audit log. It is
// deliberately separate from audit.Logger (the write side): nothing that
// can read the trail can also rewrite it, and audit.Logger still has no
// Update/Delete anywhere in the system.
type AuditReader interface {
	ListByIntent(ctx context.Context, intentID string, limit int) ([]audit.Event, error)
	ListByPaymentIntent(ctx context.Context, paymentIntentID string, limit int) ([]audit.Event, error)
}

// AuditService answers the question the mandate says the audit trail must
// always be able to answer (§33): which agent requested this purchase, what
// did the user approve, which policy allowed it, which merchant was
// selected, and what was actually charged.
type AuditService struct {
	reader AuditReader
	agents AgentStore
}

func NewAuditService(reader AuditReader, agents AgentStore) *AuditService {
	return &AuditService{reader: reader, agents: agents}
}

// ListForIntent returns the full ordered trail for one intent.
func (s *AuditService) ListForIntent(ctx context.Context, agentID, intentID string, limit int) ([]audit.Event, error) {
	ag, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermOrdersRead)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	events, err := s.reader.ListByIntent(ctx, intentID, limit)
	if err != nil {
		return nil, err
	}
	// Ownership: an intent's trail belongs to its own user only. Any event
	// attributed to someone else means this isn't the caller's intent.
	for _, e := range events {
		if e.UserID != "" && e.UserID != ag.UserID {
			return nil, fmt.Errorf("%w: intent %s", shared.ErrNotFound, intentID)
		}
	}
	return events, nil
}

// ListForPaymentIntent is ListForIntent's counterpart for
// AgenticPaymentIntent audit trails — gated on PermPaymentsRequest rather
// than PermOrdersRead, since a tenant's agent may hold payment permissions
// without holding commerce-flow Order permissions at all.
func (s *AuditService) ListForPaymentIntent(ctx context.Context, agentID, paymentIntentID string, limit int) ([]audit.Event, error) {
	if _, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermPaymentsRequest); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	return s.reader.ListByPaymentIntent(ctx, paymentIntentID, limit)
}
