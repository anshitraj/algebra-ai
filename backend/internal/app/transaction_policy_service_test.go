package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/integrator"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

func TestTransactionPolicyService_Allow(t *testing.T) {
	store := newFakeIntegratorStore()
	store.put(&integrator.Integrator{ID: "integrator_1", Name: "Kite"})
	svc := NewTransactionPolicyService(store, newFakeAuditLogger())

	dec, err := svc.EvaluateTransaction(context.Background(), "integrator_1", EvaluateTransactionInput{
		UserRef: "kite-user-1", Merchant: "Target", Category: "retail",
		AmountMinorUnits: 5000, Currency: "USD", PaymentRef: "kite-card-1",
		Rules: policy.Rules{MaxPerTransactionMinorUnits: 10000, ApprovalThresholdMinorUnits: 8000},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != policy.Allow {
		t.Errorf("expected ALLOW, got %s (%v)", dec.Decision, dec.ReasonCodes)
	}
}

func TestTransactionPolicyService_RequireApproval(t *testing.T) {
	store := newFakeIntegratorStore()
	store.put(&integrator.Integrator{ID: "integrator_1", Name: "Kite"})
	svc := NewTransactionPolicyService(store, newFakeAuditLogger())

	dec, err := svc.EvaluateTransaction(context.Background(), "integrator_1", EvaluateTransactionInput{
		UserRef: "kite-user-1", Merchant: "Target", AmountMinorUnits: 9000, Currency: "USD",
		Rules: policy.Rules{MaxPerTransactionMinorUnits: 10000, ApprovalThresholdMinorUnits: 8000},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != policy.RequireApproval {
		t.Errorf("expected REQUIRE_APPROVAL, got %s", dec.Decision)
	}
}

func TestTransactionPolicyService_Deny_BlockedCategory(t *testing.T) {
	store := newFakeIntegratorStore()
	store.put(&integrator.Integrator{ID: "integrator_1", Name: "Kite"})
	svc := NewTransactionPolicyService(store, newFakeAuditLogger())

	dec, err := svc.EvaluateTransaction(context.Background(), "integrator_1", EvaluateTransactionInput{
		UserRef: "kite-user-1", Merchant: "Casino Co", Category: "gambling", AmountMinorUnits: 100, Currency: "USD",
		Rules: policy.Rules{BlockedCategories: []string{"gambling"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != policy.Deny {
		t.Errorf("expected DENY, got %s", dec.Decision)
	}
}

func TestTransactionPolicyService_RevokedIntegratorRejected(t *testing.T) {
	store := newFakeIntegratorStore()
	revoked := &integrator.Integrator{ID: "integrator_1", Name: "Kite"}
	store.put(revoked)
	if err := store.Revoke(context.Background(), "integrator_1", time.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc := NewTransactionPolicyService(store, newFakeAuditLogger())

	_, err := svc.EvaluateTransaction(context.Background(), "integrator_1", EvaluateTransactionInput{
		UserRef: "kite-user-1", Merchant: "Target", AmountMinorUnits: 100, Currency: "USD",
		Rules: policy.Rules{},
	})
	if !errors.Is(err, shared.ErrUnauthorized) {
		t.Errorf("expected shared.ErrUnauthorized, got %v", err)
	}
}

func TestTransactionPolicyService_UnknownIntegratorNotFound(t *testing.T) {
	store := newFakeIntegratorStore()
	svc := NewTransactionPolicyService(store, newFakeAuditLogger())

	_, err := svc.EvaluateTransaction(context.Background(), "does-not-exist", EvaluateTransactionInput{Rules: policy.Rules{}})
	if !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("expected shared.ErrNotFound, got %v", err)
	}
}
