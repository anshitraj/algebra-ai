package app

import (
	"context"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

// EvaluateTransactionInput is a self-contained transaction description from
// a third-party integrator — no Algebra PurchaseIntent, Quote, or user
// account involved. UserRef and PaymentRef are opaque identifiers the
// integrator assigns; Algebra never resolves or stores what they mean.
// SpendTodayMinorUnits is caller-supplied because Algebra has no ledger for
// transactions it never processes — the same non-custodial principle
// applied to data, not just money.
type EvaluateTransactionInput struct {
	UserRef              string
	Category             string
	Merchant             string
	AmountMinorUnits     int64
	Currency             string
	SpendTodayMinorUnits int64
	PaymentRef           string
	International        bool
	Rules                policy.Rules
}

// TransactionPolicyService is the standalone counterpart to PolicyService:
// PolicyService evaluates policy for an intent already persisted inside
// Algebra's own commerce flow; this evaluates a transaction an external
// integrator describes entirely in the request, deterministically, and
// returns without creating an Approval or transitioning anything — there is
// nothing of Algebra's to transition. A REQUIRE_APPROVAL/DENY decision is
// the integrator's own app's job to act on.
type TransactionPolicyService struct {
	integrators IntegratorStore
	audit       audit.Logger
	now         func() time.Time
}

func NewTransactionPolicyService(integrators IntegratorStore, auditLogger audit.Logger) *TransactionPolicyService {
	return &TransactionPolicyService{integrators: integrators, audit: auditLogger, now: time.Now}
}

// EvaluateTransaction is POST /api/v1/policy/evaluate-transaction and MCP's
// policy.evaluate_transaction. in.Rules is required — Algebra does not
// presume to know an integrator's own budget policy, and falling back to
// DefaultRules() (INR-specific hard caps) would silently mismatch a
// non-INR caller.
func (s *TransactionPolicyService) EvaluateTransaction(ctx context.Context, integratorID string, in EvaluateTransactionInput) (*policy.PolicyDecision, error) {
	integ, err := s.integrators.Get(ctx, integratorID)
	if err != nil {
		return nil, err
	}
	if integ.IsRevoked() {
		return nil, fmt.Errorf("%w: integrator %s has been revoked", shared.ErrUnauthorized, integratorID)
	}

	// Version labels the evaluation path for audit trails — it identifies
	// "an integrator's own inline conditions", not a specific ruleset the
	// caller is expected to name. Whether conditions were actually supplied
	// at all is validated by the transport layer (the handler requires the
	// request's "conditions" field to be present) — by the time a request
	// reaches here, in.Rules is the integrator's real, intended ruleset,
	// even if it's deliberately wide open.
	in.Rules.Version = "external-inline-v1"

	provider := policy.NewLocalProvider(in.Rules)
	input := policy.Input{
		UserID:               in.UserRef,
		Merchant:             in.Merchant,
		Category:             in.Category,
		AmountMinorUnits:     in.AmountMinorUnits,
		Currency:             in.Currency,
		PaymentProfile:       in.PaymentRef,
		International:        in.International,
		SpendTodayMinorUnits: in.SpendTodayMinorUnits,
	}
	decision, err := provider.EvaluatePurchaseIntent(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("app: evaluating transaction: %w", err)
	}

	evt := audit.NewEvent("TransactionEvaluated", s.now())
	evt.Merchant = in.Merchant
	evt.PolicyDecision = string(decision.Decision)
	evt.Result = fmt.Sprintf("%v", decision.ReasonCodes)
	evt.Metadata = map[string]any{"integrator_id": integratorID, "user_ref": in.UserRef}
	_ = s.audit.Record(ctx, evt)

	return decision, nil
}
