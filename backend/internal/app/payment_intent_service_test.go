package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/paymentintent"
	"github.com/project-algebra/algebra/policy"
	"github.com/project-algebra/algebra/providers/paymentdemo"
)

// paymentHarness wires PaymentIntentService (and the services it depends
// on) against in-memory fakes plus the REAL DemoProvider — the same
// "exercise real orchestration logic without Postgres" approach
// orchestration_test.go's harness uses for the commerce flow.
type paymentHarness struct {
	agents         *fakeAgentStore
	users          *fakeUserStore
	paymentIntents *fakePaymentIntentStore
	policySets     *fakePolicySetStore
	approvals      *fakeApprovalStore
	idempotency    *fakeIdempotencyStore
	audit          *fakeAuditLogger

	PolicySetSvc     *PolicySetService
	PaymentIntentSvc *PaymentIntentService
	Approvals        *ApprovalService
}

func newPaymentHarness() *paymentHarness {
	h := &paymentHarness{
		agents: newFakeAgentStore(), users: newFakeUserStore(), paymentIntents: newFakePaymentIntentStore(),
		policySets: newFakePolicySetStore(), approvals: newFakeApprovalStore(), idempotency: newFakeIdempotencyStore(),
		audit: newFakeAuditLogger(),
	}
	h.PolicySetSvc = NewPolicySetService(h.policySets)
	h.PaymentIntentSvc = NewPaymentIntentService(h.paymentIntents, h.agents, h.users, h.approvals, h.PolicySetSvc, paymentdemo.New(), h.audit, 15*time.Minute)
	h.Approvals = NewApprovalService(newFakeIntentStore(), h.paymentIntents, h.approvals, nil, h.audit)
	return h
}

// seedTenantAgent creates a tenant-owned user + agent (with both payment
// permissions) and returns the agent's ID. The seeded user's ID is always
// "user_"+tenantID, so tests that need it (approving on the user's behalf)
// can derive it without a round trip.
func (h *paymentHarness) seedTenantAgent(t *testing.T, tenantID string) string {
	t.Helper()
	userID := "user_" + tenantID
	if err := h.users.Create(context.Background(), userID, userID+"@example.test", tenantID, time.Now()); err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	ag := &agent.Identity{
		ID: "agent_" + tenantID, UserID: userID, ClientID: "test-client", Name: "test-agent",
		Permissions: []agent.Permission{agent.PermPaymentsCreateIntent, agent.PermPaymentsExecute, agent.PermPaymentsRequest},
		CreatedAt:   time.Now(),
	}
	h.agents.put(ag)
	return ag.ID
}

func generousRules() policy.Rules {
	return policy.Rules{
		MaxPerTransactionMinorUnits: 1000000,
		MaxPerDayMinorUnits:         10000000,
		ApprovalThresholdMinorUnits: 500000,
		Currency:                    "USD",
	}
}

func TestPaymentIntentService_Create_AllowAutoAuthorizes(t *testing.T) {
	h := newPaymentHarness()
	agentID := h.seedTenantAgent(t, "tenant_1")
	if _, err := h.PolicySetSvc.SetPolicy(context.Background(), "tenant_1", nil, generousRules()); err != nil {
		t.Fatalf("setting policy: %v", err)
	}

	result, err := h.PaymentIntentSvc.Create(context.Background(), CreatePaymentIntentInput{
		AgentID: agentID, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD", PaymentSourceAlias: "payment:personal",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Decision.Decision != policy.Allow {
		t.Fatalf("expected ALLOW, got %s (%v)", result.Decision.Decision, result.Decision.ReasonCodes)
	}
	if result.PaymentIntent.Status != paymentintent.StateAuthorized {
		t.Fatalf("expected AUTHORIZED, got %s", result.PaymentIntent.Status)
	}
}

func TestPaymentIntentService_Create_Deny(t *testing.T) {
	h := newPaymentHarness()
	agentID := h.seedTenantAgent(t, "tenant_2")
	rules := generousRules()
	rules.BlockedCategories = []string{"gambling"}
	if _, err := h.PolicySetSvc.SetPolicy(context.Background(), "tenant_2", nil, rules); err != nil {
		t.Fatalf("setting policy: %v", err)
	}

	result, err := h.PaymentIntentSvc.Create(context.Background(), CreatePaymentIntentInput{
		AgentID: agentID, Merchant: "Casino Co", Category: "gambling", AmountMinorUnits: 5000, Currency: "USD", PaymentSourceAlias: "payment:personal",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Decision.Decision != policy.Deny {
		t.Fatalf("expected DENY, got %s", result.Decision.Decision)
	}
	if result.PaymentIntent.Status != paymentintent.StateDenied {
		t.Fatalf("expected DENIED, got %s", result.PaymentIntent.Status)
	}
}

func TestPaymentIntentService_Create_NoTenantFails(t *testing.T) {
	h := newPaymentHarness()
	// An agent whose user has NO tenant — the first-party reference app's
	// shape. AgenticPaymentIntent must refuse it, not silently proceed.
	ag := &agent.Identity{
		ID: "agent_first_party", UserID: "user_no_tenant", ClientID: "c", Name: "n",
		Permissions: []agent.Permission{agent.PermPaymentsCreateIntent}, CreatedAt: time.Now(),
	}
	h.agents.put(ag)
	if err := h.users.Create(context.Background(), "user_no_tenant", "u@example.test", "", time.Now()); err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	_, err := h.PaymentIntentSvc.Create(context.Background(), CreatePaymentIntentInput{
		AgentID: ag.ID, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD", PaymentSourceAlias: "payment:personal",
	})
	if err == nil {
		t.Fatal("expected an error for an agent whose user has no tenant")
	}
}

func TestPaymentIntentService_Create_NoPolicyConfigured(t *testing.T) {
	h := newPaymentHarness()
	agentID := h.seedTenantAgent(t, "tenant_3")
	// Deliberately never call SetPolicy.

	_, err := h.PaymentIntentSvc.Create(context.Background(), CreatePaymentIntentInput{
		AgentID: agentID, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD", PaymentSourceAlias: "payment:personal",
	})
	if err == nil {
		t.Fatal("expected an error when the tenant has never configured a policy")
	}
	if !strings.Contains(err.Error(), "has not configured a policy") {
		t.Errorf("expected a clear fail-closed message, got: %v", err)
	}
}

func TestPaymentIntentService_ApprovalRequired_ThenApproveThenExecute(t *testing.T) {
	h := newPaymentHarness()
	agentID := h.seedTenantAgent(t, "tenant_4")
	rules := generousRules()
	rules.ApprovalThresholdMinorUnits = 1000 // ₹10-equivalent — the test amount below exceeds it
	if _, err := h.PolicySetSvc.SetPolicy(context.Background(), "tenant_4", nil, rules); err != nil {
		t.Fatalf("setting policy: %v", err)
	}

	result, err := h.PaymentIntentSvc.Create(context.Background(), CreatePaymentIntentInput{
		AgentID: agentID, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD", PaymentSourceAlias: "payment:personal",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.PaymentIntent.Status != paymentintent.StateApprovalRequired {
		t.Fatalf("expected APPROVAL_REQUIRED, got %s", result.PaymentIntent.Status)
	}

	userID := "user_tenant_4"
	appr, err := h.Approvals.GetByPaymentIntent(context.Background(), userID, result.PaymentIntent.ID)
	if err != nil {
		t.Fatalf("GetByPaymentIntent: %v", err)
	}
	if _, err := h.Approvals.Approve(context.Background(), userID, appr.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	authorized, err := h.PaymentIntentSvc.Get(context.Background(), agentID, result.PaymentIntent.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if authorized.Status != paymentintent.StateAuthorized {
		t.Fatalf("expected AUTHORIZED after approval, got %s", authorized.Status)
	}

	outcome, err := h.PaymentIntentSvc.Execute(context.Background(), h.idempotency, "exec-key-1", agentID, result.PaymentIntent.ID)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.Status != paymentintent.StateSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s (%s)", outcome.Status, outcome.Reason)
	}
}

func TestPaymentIntentService_Execute_IdempotentReplay(t *testing.T) {
	h := newPaymentHarness()
	agentID := h.seedTenantAgent(t, "tenant_5")
	if _, err := h.PolicySetSvc.SetPolicy(context.Background(), "tenant_5", nil, generousRules()); err != nil {
		t.Fatalf("setting policy: %v", err)
	}
	result, err := h.PaymentIntentSvc.Create(context.Background(), CreatePaymentIntentInput{
		AgentID: agentID, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD", PaymentSourceAlias: "payment:personal",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	first, err := h.PaymentIntentSvc.Execute(context.Background(), h.idempotency, "same-exec-key", agentID, result.PaymentIntent.ID)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	second, err := h.PaymentIntentSvc.Execute(context.Background(), h.idempotency, "same-exec-key", agentID, result.PaymentIntent.ID)
	if err != nil {
		t.Fatalf("second Execute (replay): %v", err)
	}
	if first.Status != second.Status {
		t.Fatalf("expected a replayed idempotent call to return the same outcome, got %s vs %s", first.Status, second.Status)
	}
}
