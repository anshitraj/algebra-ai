package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/money"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/payment"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

type OrderService struct {
	intents     IntentStore
	agents      AgentStore
	approvals   ApprovalStore
	orders      OrderStore
	quoteSvc    *QuoteService
	connectors  *ConnectorRegistry
	provider    policy.Provider
	ledger      SpendLedger
	audit       audit.Logger
	now         func() time.Time
	toleranceMU int64
	locker      Locker          // optional — see Locker's doc comment in interfaces.go
	privacy     PrivacyResolver // optional — see resolveFulfillment
	userRules   UserRulesSource // optional — see PolicyService.SetUserRules
	gate        ExecutionGate   // optional — plan limits, see SetExecutionGate
	passes      PassGate        // optional — see SetSpendPasses
	receipts    ReceiptIssuer   // optional — see SetReceipts
}

// SetSpendPasses re-checks an agent's Spend Pass right before paying: an
// approval can't outlive a revoked pass, and two purchases approved side by
// side can't both spend the same remaining budget.
func (s *OrderService) SetSpendPasses(g PassGate) { s.passes = g }

// SetReceipts signs a spend receipt for every order placed.
func (s *OrderService) SetReceipts(r ReceiptIssuer) { s.receipts = r }

// ExecutionGate decides whether a user's plan allows one more order this
// month. BillingService implements it.
type ExecutionGate interface {
	CheckExecution(ctx context.Context, userID string) error
}

// SetExecutionGate enforces plan limits before any order is placed. Checked
// before the approval is claimed, so hitting the limit leaves the intent
// APPROVED and executable after an upgrade.
func (s *OrderService) SetExecutionGate(g ExecutionGate) { s.gate = g }

func NewOrderService(intents IntentStore, agents AgentStore, approvals ApprovalStore, orders OrderStore, quoteSvc *QuoteService, connectors *ConnectorRegistry, provider policy.Provider, ledger SpendLedger, auditLogger audit.Logger, toleranceMinorUnits int64) *OrderService {
	return &OrderService{
		intents: intents, agents: agents, approvals: approvals, orders: orders,
		quoteSvc: quoteSvc, connectors: connectors, provider: provider, ledger: ledger,
		audit: auditLogger, now: time.Now, toleranceMU: toleranceMinorUnits,
	}
}

// SetLocker attaches an optional distributed lock (see Locker's doc
// comment). Called once during wiring if Redis is configured and reachable;
// left nil (the zero value) otherwise, which is a fully correct — just
// not fast-failing — configuration.
func (s *OrderService) SetLocker(l Locker) { s.locker = l }

// SetPrivacyResolver attaches the resolver used to turn the intent's
// shipping/payment ALIASES into the real addresses a merchant needs, for
// the duration of one checkout call. Wired once at startup.
func (s *OrderService) SetPrivacyResolver(r PrivacyResolver) { s.privacy = r }

// SetUserRules makes the payment-time re-check use the same per-user
// guardrails PolicyService evaluated with — otherwise a user who raised
// their cap would pass policy and then be denied at execution.
func (s *OrderService) SetUserRules(src UserRulesSource) { s.userRules = src }

// resolveFulfillment materializes the real shipping (and, if one exists,
// billing) address immediately before a checkout call — the single point in
// the entire system where an alias becomes a real value on the merchant
// path (mandate §24/§27). The result is passed straight into
// connector.ExecuteCheckout and is never persisted, logged, returned to an
// agent, or held anywhere else.
//
// A missing profile is not an error: an intent may legitimately carry no
// delivery profile (a digital purchase), and a payment alias may have no
// billing address on file. A resolver failure for a profile that DOES
// exist is a hard error — better to stop than to ship to nowhere.
func (s *OrderService) resolveFulfillment(ctx context.Context, pi *intent.PurchaseIntent, a *approval.Approval) (merchant.Fulfillment, error) {
	var f merchant.Fulfillment
	if s.privacy == nil {
		return f, nil
	}
	authz := privacy.ResolveAuthorization{
		Purpose:     "checkout_execution",
		IntentID:    pi.ID,
		RequestedBy: pi.AgentID,
	}

	if alias := pi.Constraints.DeliveryProfile; alias != "" {
		shipping, err := s.privacy.ResolveShipping(ctx, pi.UserID, alias, authz)
		switch {
		case err == nil:
			f.Shipping = &merchant.ShippingAddress{
				RecipientName: shipping.RecipientName, Line1: shipping.Line1, Line2: shipping.Line2,
				City: shipping.City, State: shipping.State, PostalCode: shipping.PostalCode,
				Country: shipping.Country, Phone: shipping.Phone,
			}
		case errors.Is(err, shared.ErrNotFound):
			// No such profile on file — leave it nil and let the connector
			// decide whether it can proceed without one.
		default:
			return f, fmt.Errorf("app: resolving shipping profile %q: %w", alias, err)
		}
	}

	if alias := a.PaymentSourceAlias; alias != "" {
		billing, err := s.privacy.ResolveBilling(ctx, pi.UserID, alias, authz)
		switch {
		case err == nil:
			f.Billing = &merchant.BillingAddress{
				Name: billing.Name, Line1: billing.Line1, Line2: billing.Line2,
				City: billing.City, State: billing.State, PostalCode: billing.PostalCode,
				Country: billing.Country,
			}
		case errors.Is(err, shared.ErrNotFound):
			// Most payment sources have no separate billing address on file.
		default:
			return f, fmt.Errorf("app: resolving billing profile %q: %w", alias, err)
		}
	}

	return f, nil
}

// ExecuteOutcome is the result of Execute — exactly the union of terminal
// and in-flight states the intent may land in, so callers (MCP/REST) can
// render the right next step without inspecting error strings.
type ExecuteOutcome struct {
	IntentStatus intent.State
	Order        *order.Order
	Challenge    *payment.AuthorizationChallenge
	Reason       string
	// Receipt is the signed spend receipt (compact JWS) for a placed order.
	Receipt string
}

// Execute is commerce.request_purchase's final step once an approval is
// granted: refresh the quote (never trust a cached price at authorization
// time — mandate §18), re-run the payment-time policy check, atomically
// claim the approval, and call the merchant connector.
func (s *OrderService) Execute(ctx context.Context, idem IdempotencyStore, idemKey, agentID, intentID string) (*ExecuteOutcome, error) {
	return RunIdempotent(ctx, idem, idemKey, "execute_"+intentID, func(ctx context.Context) (*ExecuteOutcome, error) {
		if s.locker != nil {
			release, acquired, err := s.locker.Lock(ctx, "algebra:execute:"+intentID, 15*time.Second)
			if err != nil {
				return nil, fmt.Errorf("app: acquiring execution lock: %w", err)
			}
			if !acquired {
				return nil, fmt.Errorf("%w: intent %s is already being executed by a concurrent request", shared.ErrConflict, intentID)
			}
			defer release(context.Background())
		}

		_, pi, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, intentID, agentpkg.PermShoppingExecute)
		if err != nil {
			return nil, err
		}
		if pi.Status != intent.StateApproved {
			return nil, fmt.Errorf("%w: intent %s is in state %s, not APPROVED", shared.ErrConflict, intentID, pi.Status)
		}
		if s.gate != nil {
			if err := s.gate.CheckExecution(ctx, pi.UserID); err != nil {
				return nil, err
			}
		}
		a, err := s.approvals.GetByIntent(ctx, intentID)
		if err != nil {
			return nil, err
		}
		if !a.Executable(s.now()) {
			return nil, fmt.Errorf("%w: approval %s is not executable (status=%s)", shared.ErrConflict, a.ID, a.Status)
		}

		refreshed, err := s.quoteSvc.RefreshQuote(ctx, a.QuoteID)
		if err != nil {
			return nil, fmt.Errorf("app: refreshing quote before authorization: %w", err)
		}
		itemsHash := approvalHash(refreshed, a.PaymentSourceAlias)
		tolerance := money.Amount{MinorUnits: s.toleranceMU, Currency: refreshed.FinalPayable.Currency}

		if !a.Matches(refreshed.Merchant, itemsHash, refreshed.FinalPayable, a.PaymentSourceAlias, tolerance) {
			a.Status = approval.StatusReapprovalRequired
			if err := s.approvals.Update(ctx, a); err != nil {
				return nil, fmt.Errorf("app: persisting reapproval-required: %w", err)
			}
			if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateReapprovalRequired, "ReapprovalRequired", "merchant quote drifted beyond tolerance since approval"); err != nil {
				return nil, err
			}
			return &ExecuteOutcome{IntentStatus: pi.Status, Reason: "merchant price changed beyond tolerance; user must reapprove"}, nil
		}

		spendToday, err := s.ledger.SpendToday(ctx, pi.UserID, refreshed.FinalPayable.Currency, s.now())
		if err != nil {
			return nil, fmt.Errorf("app: reading spend ledger: %w", err)
		}
		provider, err := providerFor(ctx, s.userRules, s.provider, pi.UserID)
		if err != nil {
			return nil, err
		}
		paymentDecision, err := provider.EvaluatePayment(ctx, policy.Input{
			UserID: pi.UserID, AgentID: pi.AgentID, Merchant: refreshed.Merchant,
			AmountMinorUnits: refreshed.FinalPayable.MinorUnits, Currency: refreshed.FinalPayable.Currency,
			PaymentProfile: a.PaymentSourceAlias, SpendTodayMinorUnits: spendToday,
		})
		if err != nil {
			return nil, fmt.Errorf("app: payment-time policy evaluation failed: %w", err)
		}
		// RequireApproval here (not just Deny) matters: a refreshed amount
		// can drift within Matches' tolerance yet still cross the approval
		// threshold (e.g. a fee bump of a few paise pushes a ₹998 order to
		// ₹1,001). Letting anything other than a fresh Allow fall through to
		// execution would silently spend without the human approval policy
		// just said this amount needs — "policy enforcement > AI judgment."
		// Deny is terminal (a hard cap that re-approving the same terms
		// cannot fix); RequireApproval is recoverable (the user can look at
		// the current terms and approve them), so each gets a different
		// outcome. Both stop before EXECUTING is entered and before the
		// approval is consumed.
		switch paymentDecision.Decision {
		case policy.Deny:
			// APPROVED has no direct edge to FAILED — EXECUTING is the
			// legal path through, and "we started, then a hard policy cap
			// stopped us" is an accurate description of what happened.
			if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateExecuting, "ExecutionStarted", ""); err != nil {
				return nil, err
			}
			reason := fmt.Sprintf("payment-time policy denied: %v", paymentDecision.ReasonCodes)
			if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateFailed, "IntentPolicyRejected", reason); err != nil {
				return nil, err
			}
			return &ExecuteOutcome{IntentStatus: intent.StateFailed, Reason: reason}, nil

		case policy.RequireApproval:
			a.Status = approval.StatusReapprovalRequired
			if err := s.approvals.Update(ctx, a); err != nil {
				return nil, fmt.Errorf("app: persisting reapproval-required: %w", err)
			}
			reason := fmt.Sprintf("payment-time policy re-check now requires approval: %v", paymentDecision.ReasonCodes)
			if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateReapprovalRequired, "ReapprovalRequired", reason); err != nil {
				return nil, err
			}
			return &ExecuteOutcome{IntentStatus: pi.Status, Reason: reason}, nil
		}

		// The Spend Pass, if the agent holds one, gets the last word before
		// money moves: revoked, expired or out of budget is terminal here.
		// (Its ask-me line was already honoured when the approval was made.)
		if s.passes != nil {
			passDecision, _, err := s.passes.CheckPurchase(ctx, pi.AgentID, refreshed.Merchant, pi.Constraints.Category, refreshed.FinalPayable)
			if err != nil {
				return nil, err
			}
			if passDecision != nil && passDecision.Decision == policy.Deny {
				if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateExecuting, "ExecutionStarted", ""); err != nil {
					return nil, err
				}
				reason := fmt.Sprintf("spend pass denied: %v", passDecision.ReasonCodes)
				if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateFailed, "IntentPolicyRejected", reason); err != nil {
					return nil, err
				}
				return &ExecuteOutcome{IntentStatus: intent.StateFailed, Reason: reason}, nil
			}
		}

		// Resolve aliases → real addresses BEFORE claiming the approval: a
		// resolution failure must leave the approval reusable, not burn it.
		fulfillment, err := s.resolveFulfillment(ctx, pi, a)
		if err != nil {
			return nil, err
		}

		claimed, err := s.approvals.MarkConsumed(ctx, a.ID)
		if err != nil {
			return nil, fmt.Errorf("app: claiming approval: %w", err)
		}
		if !claimed {
			return nil, fmt.Errorf("%w: approval %s was already consumed by a concurrent request", shared.ErrConflict, a.ID)
		}

		if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateExecuting, "ExecutionStarted", refreshed.Merchant); err != nil {
			return nil, err
		}

		connector, err := s.connectors.Get(refreshed.Merchant)
		if err != nil {
			_ = transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateFailed, "OrderFailed", err.Error())
			return nil, err
		}

		result, err := connector.ExecuteCheckout(ctx, refreshed.CartID, a.ID, fulfillment)
		if err != nil {
			_ = transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateFailed, "OrderFailed", err.Error())
			return &ExecuteOutcome{IntentStatus: intent.StateFailed, Reason: err.Error()}, nil
		}

		return s.handleExecutionResult(ctx, pi, a, connector, result)
	})
}

func (s *OrderService) handleExecutionResult(ctx context.Context, pi *intent.PurchaseIntent, a *approval.Approval, connector merchant.Connector, result *merchant.ExecutionResult) (*ExecuteOutcome, error) {
	switch result.Status {
	case merchant.ExecutionSucceeded:
		if result.Order == nil {
			return nil, fmt.Errorf("app: connector %s reported SUCCEEDED with no order", connector.Name())
		}
		ord := *result.Order
		ord.ID = newID("ord")
		ord.IntentID = pi.ID
		ord.ApprovalID = a.ID
		ord.ProviderMode = string(connector.Mode())
		if ord.PlacedAt.IsZero() {
			ord.PlacedAt = s.now()
		}
		if err := s.orders.Create(ctx, &ord); err != nil {
			return nil, fmt.Errorf("app: persisting order: %w", err)
		}
		_ = s.orders.AddEvent(ctx, order.Event{ID: newID("oev"), OrderID: ord.ID, Type: "OrderPlaced", CreatedAt: s.now()})
		if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateSucceeded, "OrderCompleted", ord.Merchant); err != nil {
			return nil, err
		}
		out := &ExecuteOutcome{IntentStatus: pi.Status, Order: &ord}
		// The order stands whether or not signing works; a missing receipt
		// is recorded, not fatal.
		if s.receipts != nil {
			if jws, err := s.receipts.Issue(ctx, &ord, pi, a); err == nil {
				out.Receipt = jws
			} else {
				evt := audit.NewEvent("ReceiptFailed", s.now())
				evt.UserID, evt.AgentID, evt.IntentID, evt.Result = pi.UserID, pi.AgentID, pi.ID, err.Error()
				_ = s.audit.Record(ctx, evt)
			}
		}
		return out, nil

	case merchant.ExecutionAuthenticationRequired:
		if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateAuthenticationRequired, "PaymentChallengeCreated", result.Reason); err != nil {
			return nil, err
		}
		return &ExecuteOutcome{IntentStatus: pi.Status, Challenge: result.Challenge, Reason: result.Reason}, nil

	case merchant.ExecutionMerchantInterventionNeeded:
		if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateMerchantInterventionRequired, "OrderFailed", result.Reason); err != nil {
			return nil, err
		}
		return &ExecuteOutcome{IntentStatus: pi.Status, Reason: result.Reason}, nil

	case merchant.ExecutionUserInterventionNeeded:
		if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateUserInterventionRequired, "OrderFailed", result.Reason); err != nil {
			return nil, err
		}
		return &ExecuteOutcome{IntentStatus: pi.Status, Reason: result.Reason}, nil

	default: // merchant.ExecutionFailed or anything unrecognized
		if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateFailed, "OrderFailed", result.Reason); err != nil {
			return nil, err
		}
		return &ExecuteOutcome{IntentStatus: pi.Status, Reason: result.Reason}, nil
	}
}

// CompleteAuthentication resumes an intent stuck in AUTHENTICATION_REQUIRED
// once the user has completed 3DS/OTP/UPI on their own device. Algebra
// never sees the OTP/challenge secret itself — only the outcome and, on
// success, the merchant's own confirmed order (fetched via GetOrder, not
// fabricated locally).
func (s *OrderService) CompleteAuthentication(ctx context.Context, agentID, intentID, merchantOrderID string, success bool) (*ExecuteOutcome, error) {
	_, pi, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, intentID, agentpkg.PermShoppingExecute)
	if err != nil {
		return nil, err
	}
	if pi.Status != intent.StateAuthenticationRequired {
		return nil, fmt.Errorf("%w: intent %s is in state %s, not AUTHENTICATION_REQUIRED", shared.ErrConflict, intentID, pi.Status)
	}
	a, err := s.approvals.GetByIntent(ctx, intentID)
	if err != nil {
		return nil, err
	}

	if !success {
		if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateFailed, "OrderFailed", "authentication challenge failed or was abandoned"); err != nil {
			return nil, err
		}
		return &ExecuteOutcome{IntentStatus: pi.Status, Reason: "authentication failed"}, nil
	}

	connector, err := s.connectors.Get(a.Merchant)
	if err != nil {
		return nil, err
	}
	confirmedOrder, err := connector.GetOrder(ctx, merchantOrderID)
	if err != nil {
		_ = transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateFailed, "OrderFailed", err.Error())
		return nil, fmt.Errorf("app: fetching confirmed order after authentication: %w", err)
	}
	ord := *confirmedOrder
	ord.ID = newID("ord")
	ord.IntentID = pi.ID
	ord.ApprovalID = a.ID
	ord.ProviderMode = string(connector.Mode())
	if err := s.orders.Create(ctx, &ord); err != nil {
		return nil, fmt.Errorf("app: persisting order: %w", err)
	}
	if err := transitionIntent(ctx, s.intents, s.audit, s.now, pi, intent.StateSucceeded, "OrderCompleted", ord.Merchant); err != nil {
		return nil, err
	}
	return &ExecuteOutcome{IntentStatus: pi.Status, Order: &ord}, nil
}

// CancelOrder asks the merchant to cancel a placed order, then records the
// outcome. Algebra never marks an order cancelled on its own say-so — if
// the merchant refuses (already shipped, past its cancellation window), the
// error surfaces and the order stays exactly as the merchant reports it
// (mandate §69: real integration over fake completeness).
func (s *OrderService) CancelOrder(ctx context.Context, agentID, intentID string) (*order.Order, error) {
	if _, _, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, intentID, agentpkg.PermShoppingExecute); err != nil {
		return nil, err
	}
	ord, err := s.orders.GetByIntent(ctx, intentID)
	if err != nil {
		return nil, err
	}
	if ord.Status == order.StatusCancelled {
		return ord, nil // already cancelled — idempotent
	}

	connector, err := s.connectors.Get(ord.Merchant)
	if err != nil {
		return nil, err
	}
	if err := connector.CancelOrder(ctx, ord.MerchantOrderID); err != nil {
		return nil, fmt.Errorf("app: merchant %s refused to cancel order %s: %w", ord.Merchant, ord.MerchantOrderID, err)
	}

	ord.Status = order.StatusCancelled
	if err := s.orders.UpdateStatus(ctx, ord.ID, order.StatusCancelled); err != nil {
		return nil, fmt.Errorf("app: persisting cancelled order: %w", err)
	}
	_ = s.orders.AddEvent(ctx, order.Event{ID: newID("oev"), OrderID: ord.ID, Type: "OrderCancelled", CreatedAt: s.now()})

	evt := audit.NewEvent("OrderCancelled", s.now())
	evt.IntentID = intentID
	evt.AgentID = agentID
	evt.Merchant = ord.Merchant
	evt.Result = ord.MerchantOrderID
	_ = s.audit.Record(ctx, evt)

	return ord, nil
}

func (s *OrderService) GetOrderStatus(ctx context.Context, agentID, intentID string) (*order.Order, error) {
	if _, _, err := requireOwnedIntent(ctx, s.agents, s.intents, agentID, intentID, agentpkg.PermOrdersRead); err != nil {
		return nil, err
	}
	return s.orders.GetByIntent(ctx, intentID)
}

// GetReceipt returns the same Order record — it carries no sensitive
// fields (no PAN, no address), so it's safe to hand back through MCP as-is.
func (s *OrderService) GetReceipt(ctx context.Context, agentID, intentID string) (*order.Order, error) {
	return s.GetOrderStatus(ctx, agentID, intentID)
}

// approvalHash rebuilds the items-hash from a freshly refreshed quote, using
// exactly the same construction policy_service.createApproval used
// originally — this is what lets Approval.Matches compare like for like.
func approvalHash(q *quote.CheckoutQuote, paymentAlias string) string {
	items := make([]approval.HashableItem, len(q.Items))
	for i, it := range q.Items {
		items[i] = approval.HashableItem{MerchantProductID: it.MerchantProductID, Quantity: it.Quantity, UnitPriceMinorUnits: it.UnitPrice.MinorUnits}
	}
	return approval.CanonicalHash(q.Merchant, items, q.FinalPayable.Currency, paymentAlias)
}
