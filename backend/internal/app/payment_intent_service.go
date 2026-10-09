package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/money"
	"github.com/project-algebra/algebra/internal/domain/paymentintent"
	"github.com/project-algebra/algebra/internal/domain/paymentprovider"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

// PaymentIntentService is the tenant-facing counterpart to PolicyService +
// OrderService combined — evaluating and executing an AgenticPaymentIntent
// instead of a commerce-flow PurchaseIntent. See
// internal/domain/paymentintent's package doc for why this is a separate
// object and state machine rather than a rename of PurchaseIntent.
type PaymentIntentService struct {
	intents     PaymentIntentStore
	agents      AgentStore
	users       UserStore
	approvals   ApprovalStore
	policySets  *PolicySetService
	provider    paymentprovider.Provider
	audit       audit.Logger
	now         func() time.Time
	approvalTTL time.Duration

	webhooks *WebhookDispatchService // optional — see SetWebhookDispatcher
}

func NewPaymentIntentService(intents PaymentIntentStore, agents AgentStore, users UserStore, approvals ApprovalStore, policySets *PolicySetService, provider paymentprovider.Provider, auditLogger audit.Logger, approvalTTL time.Duration) *PaymentIntentService {
	return &PaymentIntentService{
		intents: intents, agents: agents, users: users, approvals: approvals,
		policySets: policySets, provider: provider, audit: auditLogger,
		now: time.Now, approvalTTL: approvalTTL,
	}
}

// SetWebhookDispatcher attaches the outbound-webhook dispatcher — optional,
// same pattern as OrderService.SetLocker/SetPrivacyResolver. Without it,
// AgenticPaymentIntent transitions simply don't notify a tenant's endpoint;
// everything else (policy, approval, execution) still works.
func (s *PaymentIntentService) SetWebhookDispatcher(w *WebhookDispatchService) { s.webhooks = w }

type CreatePaymentIntentInput struct {
	AgentID string

	Purpose             string
	Merchant            string
	MerchantDomain      string
	Category            string
	International       bool
	AmountMinorUnits    int64
	Currency            string
	ToleranceMinorUnits int64
	ProductRef          string
	PaymentSourceAlias  string
	RequestedCapability string
}

type CreatePaymentIntentResult struct {
	PaymentIntent *paymentintent.AgenticPaymentIntent
	Decision      *policy.PolicyDecision
}

// Create builds an AgenticPaymentIntent and evaluates it against the
// tenant's persisted policy synchronously. Unlike PurchaseIntent (which has
// a discovery/quote step in between creation and policy evaluation), a
// tenant's agent already knows the merchant and amount up front — there is
// nothing to wait for between create and evaluate, so one call does both.
func (s *PaymentIntentService) Create(ctx context.Context, in CreatePaymentIntentInput) (*CreatePaymentIntentResult, error) {
	ag, err := requirePermission(ctx, s.agents, in.AgentID, agentpkg.PermPaymentsCreateIntent)
	if err != nil {
		return nil, err
	}
	user, err := s.users.Get(ctx, ag.UserID)
	if err != nil {
		return nil, err
	}
	if user.TenantID == "" {
		return nil, fmt.Errorf("%w: agentic payment intents require the calling agent's user to belong to a tenant — the first-party console uses PurchaseIntent instead", shared.ErrUnauthorized)
	}
	if in.Merchant == "" || in.AmountMinorUnits <= 0 || in.Currency == "" || in.PaymentSourceAlias == "" {
		return nil, fmt.Errorf("app: payment intent requires a merchant, a positive amount, a currency, and a payment_source_alias")
	}

	now := s.now()
	p := paymentintent.New(newID("pay"), user.TenantID, ag.UserID, ag.ID, in.Merchant, in.AmountMinorUnits, in.Currency, in.PaymentSourceAlias, now)
	p.Purpose = in.Purpose
	p.MerchantDomain = in.MerchantDomain
	p.Category = in.Category
	p.International = in.International
	p.ToleranceMinorUnits = in.ToleranceMinorUnits
	p.ProductRef = in.ProductRef
	p.RequestedCapability = in.RequestedCapability

	if err := s.intents.Create(ctx, p); err != nil {
		return nil, fmt.Errorf("app: persisting payment intent: %w", err)
	}
	recordPaymentIntentAudit(ctx, s.audit, s.now, p, "PaymentIntentCreated", "", string(p.Status), "created")
	s.fireWebhook(ctx, p, "payment_intent.created")

	decision, err := s.evaluate(ctx, p)
	if err != nil {
		return nil, err
	}
	return &CreatePaymentIntentResult{PaymentIntent: p, Decision: decision}, nil
}

func (s *PaymentIntentService) evaluate(ctx context.Context, p *paymentintent.AgenticPaymentIntent) (*policy.PolicyDecision, error) {
	if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StatePolicyEvaluating, "PolicyEvaluationStarted", "evaluating "+p.Merchant); err != nil {
		return nil, err
	}

	userID := p.UserID
	activePolicy, err := s.policySets.GetActive(ctx, p.TenantID, &userID)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return nil, fmt.Errorf("%w: tenant %s has not configured a policy — call POST /api/v1/tenants/{id}/policy-sets first", shared.ErrConflict, p.TenantID)
		}
		return nil, err
	}

	provider := policy.NewLocalProvider(activePolicy.Rules)
	decision, err := provider.EvaluatePurchaseIntent(ctx, policy.Input{
		UserID: p.UserID, AgentID: p.AgentID, Merchant: p.Merchant, Category: p.Category,
		AmountMinorUnits: p.AmountMinorUnits, Currency: p.Currency,
		PaymentProfile: p.PaymentSourceAlias, International: p.International,
	})
	if err != nil {
		return nil, fmt.Errorf("app: policy evaluation failed: %w", err)
	}
	p.PolicyVersion = decision.PolicyVersion

	evt := audit.NewEvent("PolicyEvaluated", s.now())
	evt.TenantID, evt.UserID, evt.AgentID, evt.AgenticPaymentIntentID = p.TenantID, p.UserID, p.AgentID, p.ID
	evt.Merchant = p.Merchant
	evt.PolicyDecision = string(decision.Decision)
	evt.Result = fmt.Sprintf("%v", decision.ReasonCodes)
	_ = s.audit.Record(ctx, evt)

	switch decision.Decision {
	case policy.Deny:
		if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateDenied, "PaymentIntentDenied", fmt.Sprintf("%v", decision.ReasonCodes)); err != nil {
			return nil, err
		}
		s.fireWebhook(ctx, p, "payment_intent.policy_denied")

	case policy.RequireApproval:
		if err := s.createApproval(ctx, p, approval.StatusPending); err != nil {
			return nil, err
		}
		if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateApprovalRequired, "ApprovalRequested", "policy requires explicit approval"); err != nil {
			return nil, err
		}
		s.fireWebhook(ctx, p, "payment_intent.approval_required")

	default: // policy.Allow
		if err := s.createApproval(ctx, p, approval.StatusApproved); err != nil {
			return nil, err
		}
		if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateAuthorized, "PaymentIntentAuthorized", "auto-approved under policy"); err != nil {
			return nil, err
		}
		s.fireWebhook(ctx, p, "payment_intent.policy_allowed")
	}

	return decision, nil
}

// createApproval reuses internal/domain/approval.Approval rather than
// building a second approval mechanism — see Approval's doc comment.
// ItemsHash is computed over an empty item list (there are no cart items
// for an AgenticPaymentIntent), still binding merchant+currency+payment-
// alias; QuoteID is left empty.
func (s *PaymentIntentService) createApproval(ctx context.Context, p *paymentintent.AgenticPaymentIntent, status approval.Status) error {
	hash := approval.CanonicalHash(p.Merchant, nil, p.Currency, p.PaymentSourceAlias)
	now := s.now()
	a := &approval.Approval{
		ID: newID("appr"), AgenticPaymentIntentID: p.ID,
		UserID: p.UserID, AgentID: p.AgentID,
		Merchant:           p.Merchant,
		Amount:             money.Amount{MinorUnits: p.AmountMinorUnits, Currency: p.Currency},
		PaymentSourceAlias: p.PaymentSourceAlias, ItemsHash: hash,
		Status: status, CreatedAt: now, ExpiresAt: now.Add(s.approvalTTL),
	}
	if status == approval.StatusApproved {
		a.DecidedAt = &now
	}
	if err := s.approvals.Create(ctx, a); err != nil {
		return fmt.Errorf("app: creating approval: %w", err)
	}
	return nil
}

func (s *PaymentIntentService) Get(ctx context.Context, agentID, id string) (*paymentintent.AgenticPaymentIntent, error) {
	ag, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermPaymentsRequest)
	if err != nil {
		return nil, err
	}
	p, err := s.intents.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.UserID != ag.UserID {
		return nil, fmt.Errorf("%w: payment intent %s", shared.ErrNotFound, id)
	}
	return p, nil
}

// ListForTenant is GET /api/v1/transactions — authenticated as the tenant
// itself, not an agent, so there is no requirePermission call here.
func (s *PaymentIntentService) ListForTenant(ctx context.Context, tenantID string, limit int) ([]paymentintent.AgenticPaymentIntent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.intents.ListByTenant(ctx, tenantID, limit)
}

// Revoke cancels a payment intent that hasn't completed yet.
func (s *PaymentIntentService) Revoke(ctx context.Context, agentID, id string) (*paymentintent.AgenticPaymentIntent, error) {
	ag, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermPaymentsExecute)
	if err != nil {
		return nil, err
	}
	p, err := s.intents.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.UserID != ag.UserID {
		return nil, fmt.Errorf("%w: payment intent %s", shared.ErrNotFound, id)
	}
	target := paymentintent.StateCancelled
	switch p.Status {
	case paymentintent.StateApprovalRequired, paymentintent.StateAuthorized, paymentintent.StateReadyToExecute:
		target = paymentintent.StateRevoked
	}
	if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, target, "PaymentIntentRevoked", "revoked by agent"); err != nil {
		return nil, err
	}
	return p, nil
}

type PaymentExecuteOutcome struct {
	Status paymentintent.State
	Result *paymentprovider.PaymentResult
	Reason string
}

// Execute prepares a scoped credential from the payment provider, atomically
// claims the approval, and calls ExecutePayment — the same
// idempotency-wrap → atomic-approval-claim → provider-call shape
// OrderService.Execute already uses for the commerce flow.
func (s *PaymentIntentService) Execute(ctx context.Context, idem IdempotencyStore, idemKey, agentID, paymentIntentID string) (*PaymentExecuteOutcome, error) {
	return RunIdempotent(ctx, idem, idemKey, "execute_payment_intent_"+paymentIntentID, func(ctx context.Context) (*PaymentExecuteOutcome, error) {
		ag, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermPaymentsExecute)
		if err != nil {
			return nil, err
		}
		p, err := s.intents.Get(ctx, paymentIntentID)
		if err != nil {
			return nil, err
		}
		if p.UserID != ag.UserID {
			return nil, fmt.Errorf("%w: payment intent %s", shared.ErrNotFound, paymentIntentID)
		}
		if p.Status != paymentintent.StateAuthorized {
			return nil, fmt.Errorf("%w: payment intent %s is in state %s, not AUTHORIZED", shared.ErrConflict, paymentIntentID, p.Status)
		}
		a, err := s.approvals.GetByPaymentIntent(ctx, paymentIntentID)
		if err != nil {
			return nil, err
		}
		if !a.Executable(s.now()) {
			return nil, fmt.Errorf("%w: approval %s is not executable (status=%s)", shared.ErrConflict, a.ID, a.Status)
		}

		if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateCredentialPreparing, "CredentialPreparationStarted", ""); err != nil {
			return nil, err
		}

		// Prepare the credential BEFORE claiming the approval — a failure
		// here must leave the approval reusable, not burn it (mirrors
		// OrderService.Execute resolving fulfillment before MarkConsumed).
		credential, prepErr := s.prepareCredential(ctx, p, a)
		if prepErr != nil {
			_ = transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateProviderUnavailable, "ProviderUnavailable", prepErr.Error())
			s.fireWebhook(ctx, p, "payment_intent.provider_unavailable")
			return &PaymentExecuteOutcome{Status: p.Status, Reason: prepErr.Error()}, nil
		}

		if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateReadyToExecute, "CredentialPrepared", ""); err != nil {
			return nil, err
		}

		claimed, err := s.approvals.MarkConsumed(ctx, a.ID)
		if err != nil {
			return nil, fmt.Errorf("app: claiming approval: %w", err)
		}
		if !claimed {
			return nil, fmt.Errorf("%w: approval %s was already consumed by a concurrent request", shared.ErrConflict, a.ID)
		}

		if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateProcessing, "ExecutionStarted", p.Merchant); err != nil {
			return nil, err
		}

		result, err := s.provider.ExecutePayment(ctx, paymentprovider.ExecutePaymentRequest{
			CredentialRef: credential.CredentialRef, Merchant: p.Merchant, MerchantDomain: p.MerchantDomain,
			AmountMinorUnits: p.AmountMinorUnits, Currency: p.Currency, IdempotencyKey: idemKey,
		})
		if err != nil {
			_ = transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateFailed, "PaymentFailed", err.Error())
			s.fireWebhook(ctx, p, "payment_intent.failed")
			return &PaymentExecuteOutcome{Status: p.Status, Reason: err.Error()}, nil
		}
		return s.handleExecutionResult(ctx, p, result)
	})
}

// prepareCredential walks a payment source through the full
// paymentprovider.Provider chain — register, delegate, authenticate, mint a
// scoped credential — never holding onto anything beyond the opaque
// references it returns.
func (s *PaymentIntentService) prepareCredential(ctx context.Context, p *paymentintent.AgenticPaymentIntent, a *approval.Approval) (*paymentprovider.ScopedCredential, error) {
	registration, err := s.provider.RegisterPaymentSource(ctx, paymentprovider.RegisterSourceRequest{UserID: p.UserID, Alias: p.PaymentSourceAlias})
	if err != nil {
		return nil, err
	}
	auth, err := s.provider.CreateDelegatedAuthorization(ctx, paymentprovider.DelegatedAuthorizationRequest{
		UserID: p.UserID, AgentID: p.AgentID, SourceRef: registration.ProviderRef,
		Merchant: p.Merchant, MerchantDomain: p.MerchantDomain,
		MaxAmountMinorUnits: p.AmountMinorUnits + p.ToleranceMinorUnits, Currency: p.Currency,
		ExpiresAt: a.ExpiresAt,
	})
	if err != nil {
		return nil, err
	}
	authResult, err := s.provider.RequestAuthentication(ctx, paymentprovider.AuthenticationRequest{AuthorizationRef: auth.AuthorizationRef})
	if err != nil {
		return nil, err
	}
	if !authResult.Satisfied {
		return nil, fmt.Errorf("app: provider authentication was not satisfied")
	}
	return s.provider.CreateScopedCredential(ctx, paymentprovider.ScopedCredentialRequest{
		AuthorizationRef: auth.AuthorizationRef, AmountMinorUnits: p.AmountMinorUnits, Currency: p.Currency, IdempotencyKey: "",
	})
}

func (s *PaymentIntentService) handleExecutionResult(ctx context.Context, p *paymentintent.AgenticPaymentIntent, result *paymentprovider.PaymentResult) (*PaymentExecuteOutcome, error) {
	p.ProviderStatus = string(result.Status)

	switch result.Status {
	case paymentprovider.PaymentSucceeded:
		p.ProviderTransactionID = result.ProviderTransactionID
		p.FinalAmountMinorUnits = result.FinalAmountMinorUnits
		p.FinalCurrency = result.FinalCurrency
		if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateSucceeded, "PaymentSucceeded", result.ProviderTransactionID); err != nil {
			return nil, err
		}
		s.fireWebhook(ctx, p, "payment_intent.succeeded")
		return &PaymentExecuteOutcome{Status: p.Status, Result: result}, nil

	case paymentprovider.PaymentAuthenticationRequired:
		if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateAuthenticationRequired, "AuthenticationRequired", result.Reason); err != nil {
			return nil, err
		}
		s.fireWebhook(ctx, p, "payment_intent.authentication_required")
		return &PaymentExecuteOutcome{Status: p.Status, Result: result, Reason: result.Reason}, nil

	case paymentprovider.PaymentProviderUnavailable:
		if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateProviderUnavailable, "ProviderUnavailable", result.Reason); err != nil {
			return nil, err
		}
		s.fireWebhook(ctx, p, "payment_intent.provider_unavailable")
		return &PaymentExecuteOutcome{Status: p.Status, Result: result, Reason: result.Reason}, nil

	default: // Declined or Failed
		if err := transitionPaymentIntent(ctx, s.intents, s.audit, s.now, p, paymentintent.StateFailed, "PaymentFailed", result.Reason); err != nil {
			return nil, err
		}
		s.fireWebhook(ctx, p, "payment_intent.failed")
		return &PaymentExecuteOutcome{Status: p.Status, Result: result, Reason: result.Reason}, nil
	}
}

type webhookPaymentIntentEvent struct {
	PaymentIntentID       string `json:"payment_intent_id"`
	Status                string `json:"status"`
	Merchant              string `json:"merchant"`
	AmountMinorUnits      int64  `json:"amount_minor_units"`
	Currency              string `json:"currency"`
	ProviderTransactionID string `json:"provider_transaction_id,omitempty"`
}

func (s *PaymentIntentService) fireWebhook(ctx context.Context, p *paymentintent.AgenticPaymentIntent, eventType string) {
	if s.webhooks == nil {
		return
	}
	_ = s.webhooks.Dispatch(ctx, p.TenantID, eventType, webhookPaymentIntentEvent{
		PaymentIntentID: p.ID, Status: string(p.Status), Merchant: p.Merchant,
		AmountMinorUnits: p.AmountMinorUnits, Currency: p.Currency,
		ProviderTransactionID: p.ProviderTransactionID,
	})
}
