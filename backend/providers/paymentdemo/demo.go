// Package paymentdemo implements a deterministic, fully in-memory
// internal/domain/paymentprovider.Provider — the only payment rail actually
// wired live in this build (Phase 1). It never touches a network and never
// receives a real credential; it exists so the whole tenant → policy →
// AgenticPaymentIntent → approval → execute → webhook → audit loop is
// demonstrable and testable before any real card-network/processor
// partnership exists. Mode() always reports ModeDemo — a demo result can
// never be mistaken for a real one downstream.
package paymentdemo

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/project-algebra/algebra/internal/domain/payment"
	"github.com/project-algebra/algebra/internal/domain/paymentprovider"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type sourceRecord struct {
	providerRef string
	userID      string
	alias       string
}

type authRecord struct {
	authorizationRef string
	sourceRef        string
	userID, agentID  string
	merchant         string
	merchantDomain   string
	maxAmount        int64
	currency         string
	expiresAt        time.Time
	revoked          bool
}

type credentialRecord struct {
	credentialRef    string
	authorizationRef string
	amount           int64
	currency         string
	expiresAt        time.Time
	consumed         bool
}

// Provider is a real, functioning paymentprovider.Provider for local
// development and tests — see the package doc. Every method is
// deterministic and side-effect-free beyond its own in-memory state; no
// method ever blocks on or fails due to an external dependency.
type Provider struct {
	mu sync.Mutex

	sourcesByKey map[string]sourceRecord // "userID|alias" -> record, so re-registering is idempotent
	sources      map[string]sourceRecord // providerRef -> record

	authorizations map[string]authRecord
	credentials    map[string]credentialRecord

	// results is keyed by ProviderTransactionID for GetPaymentStatus, and
	// separately mirrored by idempotency key so a retried ExecutePayment
	// call replays the exact same outcome rather than moving money twice —
	// real payment gateways require exactly this from callers, independent
	// of Algebra's own app-level idempotency (internal/app.RunIdempotent).
	results          map[string]paymentprovider.PaymentResult
	byIdempotencyKey map[string]paymentprovider.PaymentResult

	now func() time.Time
}

func New() *Provider {
	return &Provider{
		sourcesByKey:     map[string]sourceRecord{},
		sources:          map[string]sourceRecord{},
		authorizations:   map[string]authRecord{},
		credentials:      map[string]credentialRecord{},
		results:          map[string]paymentprovider.PaymentResult{},
		byIdempotencyKey: map[string]paymentprovider.PaymentResult{},
		now:              time.Now,
	}
}

func (p *Provider) Capabilities() paymentprovider.Capabilities {
	return paymentprovider.Capabilities{
		Mode:                     paymentprovider.ModeDemo,
		CanDelegate:              true,
		CanIssueScopedCredential: true,
		CanExecute:               true,
		CanRefund:                true,
		RequiresUserPresence:     false,
		SupportsPasskey:          false,
		SupportsCard:             true,
		SupportsUPI:              true,
		SupportsStablecoin:       false,
		SupportsRecurring:        false,
		SupportsMerchantBinding:  true,
		SupportsAmountBinding:    true,
	}
}

func (p *Provider) RegisterPaymentSource(_ context.Context, req paymentprovider.RegisterSourceRequest) (*paymentprovider.SourceRegistration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := req.UserID + "|" + req.Alias
	if existing, ok := p.sourcesByKey[key]; ok {
		return &paymentprovider.SourceRegistration{ProviderRef: existing.providerRef, Capabilities: p.Capabilities()}, nil
	}

	rec := sourceRecord{providerRef: "demo_src_" + uuid.NewString(), userID: req.UserID, alias: req.Alias}
	p.sourcesByKey[key] = rec
	p.sources[rec.providerRef] = rec
	return &paymentprovider.SourceRegistration{ProviderRef: rec.providerRef, Capabilities: p.Capabilities()}, nil
}

func (p *Provider) CreateDelegatedAuthorization(_ context.Context, req paymentprovider.DelegatedAuthorizationRequest) (*paymentprovider.DelegatedAuthorization, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.sources[req.SourceRef]; !ok {
		return nil, fmt.Errorf("%w: demo payment source %s", shared.ErrNotFound, req.SourceRef)
	}
	rec := authRecord{
		authorizationRef: "demo_auth_" + uuid.NewString(),
		sourceRef:        req.SourceRef,
		userID:           req.UserID, agentID: req.AgentID,
		merchant: req.Merchant, merchantDomain: req.MerchantDomain,
		maxAmount: req.MaxAmountMinorUnits, currency: req.Currency,
		expiresAt: req.ExpiresAt,
	}
	p.authorizations[rec.authorizationRef] = rec
	return &paymentprovider.DelegatedAuthorization{AuthorizationRef: rec.authorizationRef, ExpiresAt: rec.expiresAt}, nil
}

// RequestAuthentication always reports Satisfied — DemoProvider does not
// require real user presence (Capabilities().RequiresUserPresence is
// false). The AUTHENTICATION_REQUIRED scenario is instead exercised at
// ExecutePayment time via Metadata["demo_scenario"], matching how the
// existing commerce flow's AUTHENTICATION_REQUIRED state is only ever
// entered from an execution attempt, never a separate pre-check.
func (p *Provider) RequestAuthentication(_ context.Context, _ paymentprovider.AuthenticationRequest) (*paymentprovider.AuthenticationResult, error) {
	return &paymentprovider.AuthenticationResult{Satisfied: true}, nil
}

func (p *Provider) CreateScopedCredential(_ context.Context, req paymentprovider.ScopedCredentialRequest) (*paymentprovider.ScopedCredential, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	auth, ok := p.authorizations[req.AuthorizationRef]
	if !ok {
		return nil, fmt.Errorf("%w: demo authorization %s", shared.ErrNotFound, req.AuthorizationRef)
	}
	if auth.revoked {
		return nil, fmt.Errorf("%w: demo authorization %s has been revoked", shared.ErrConflict, req.AuthorizationRef)
	}
	if p.now().After(auth.expiresAt) {
		return nil, fmt.Errorf("%w: demo authorization %s has expired", shared.ErrConflict, req.AuthorizationRef)
	}
	if req.AmountMinorUnits > auth.maxAmount {
		return nil, fmt.Errorf("%w: requested amount exceeds the authorized maximum for %s", shared.ErrConflict, req.AuthorizationRef)
	}

	rec := credentialRecord{
		credentialRef:    "demo_cred_" + uuid.NewString(),
		authorizationRef: req.AuthorizationRef,
		amount:           req.AmountMinorUnits, currency: req.Currency,
		// Short-lived by design — a scoped credential is meant to be used
		// immediately, not held. Real rails set their own; this is a
		// deliberately tight default so the "credential expiry" scenario
		// (mandate §31) is easy to exercise in a test.
		expiresAt: p.now().Add(5 * time.Minute),
	}
	p.credentials[rec.credentialRef] = rec

	return &paymentprovider.ScopedCredential{
		CredentialRef: rec.credentialRef,
		Value:         shared.NewSensitiveValue("demo_cred_value_" + uuid.NewString()),
		ExpiresAt:     rec.expiresAt,
	}, nil
}

// ExecutePayment is where every §31 demo scenario lives. Metadata["demo_scenario"]
// selects one deterministically; an absent or unrecognized value succeeds.
// A retried call with the same IdempotencyKey always replays the exact
// first outcome rather than re-deciding — this is DemoProvider's OWN
// idempotency guarantee, independent of and in addition to
// internal/app.RunIdempotent at the service layer.
func (p *Provider) ExecutePayment(_ context.Context, req paymentprovider.ExecutePaymentRequest) (*paymentprovider.PaymentResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if req.IdempotencyKey != "" {
		if prior, ok := p.byIdempotencyKey[req.IdempotencyKey]; ok {
			return &prior, nil
		}
	}

	cred, ok := p.credentials[req.CredentialRef]
	if !ok {
		return nil, fmt.Errorf("%w: demo credential %s", shared.ErrNotFound, req.CredentialRef)
	}

	now := p.now()
	var result paymentprovider.PaymentResult

	switch {
	case now.After(cred.expiresAt):
		result = paymentprovider.PaymentResult{Status: paymentprovider.PaymentFailed, Reason: "scoped credential expired", OccurredAt: now}
	case cred.consumed:
		result = paymentprovider.PaymentResult{Status: paymentprovider.PaymentFailed, Reason: "scoped credential already used", OccurredAt: now}
	default:
		switch req.Metadata["demo_scenario"] {
		case "decline":
			result = paymentprovider.PaymentResult{Status: paymentprovider.PaymentDeclined, Reason: "card declined by issuer (demo)", OccurredAt: now}
		case "insufficient_funds":
			result = paymentprovider.PaymentResult{Status: paymentprovider.PaymentDeclined, Reason: "insufficient funds (demo)", OccurredAt: now}
		case "outage":
			result = paymentprovider.PaymentResult{Status: paymentprovider.PaymentProviderUnavailable, Reason: "provider temporarily unavailable (demo)", OccurredAt: now}
		case "authentication_required":
			result = paymentprovider.PaymentResult{
				Status: paymentprovider.PaymentAuthenticationRequired,
				Reason: "strong customer authentication required (demo)",
				Challenge: &payment.AuthorizationChallenge{
					ChallengeID: "demo_chal_" + uuid.NewString(),
					Mechanism:   payment.ChallengeOTP,
					ExpiresAt:   now.Add(10 * time.Minute),
					Status:      "PENDING",
				},
				OccurredAt: now,
			}
		default:
			rec := p.credentials[req.CredentialRef]
			rec.consumed = true
			p.credentials[req.CredentialRef] = rec
			result = paymentprovider.PaymentResult{
				Status: paymentprovider.PaymentSucceeded, ProviderTransactionID: "demo_txn_" + uuid.NewString(),
				FinalAmountMinorUnits: req.AmountMinorUnits, FinalCurrency: req.Currency, OccurredAt: now,
			}
		}
	}

	if result.ProviderTransactionID != "" {
		p.results[result.ProviderTransactionID] = result
	}
	if req.IdempotencyKey != "" {
		p.byIdempotencyKey[req.IdempotencyKey] = result
	}
	return &result, nil
}

func (p *Provider) GetPaymentStatus(_ context.Context, providerTransactionID string) (*paymentprovider.PaymentResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result, ok := p.results[providerTransactionID]
	if !ok {
		return nil, fmt.Errorf("%w: demo transaction %s", shared.ErrNotFound, providerTransactionID)
	}
	return &result, nil
}

func (p *Provider) RevokeAuthorization(_ context.Context, authorizationRef string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec, ok := p.authorizations[authorizationRef]
	if !ok {
		return fmt.Errorf("%w: demo authorization %s", shared.ErrNotFound, authorizationRef)
	}
	rec.revoked = true
	p.authorizations[authorizationRef] = rec
	return nil
}

func (p *Provider) Refund(_ context.Context, req paymentprovider.RefundRequest) (*paymentprovider.PaymentResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	original, ok := p.results[req.ProviderTransactionID]
	if !ok {
		return nil, fmt.Errorf("%w: demo transaction %s", shared.ErrNotFound, req.ProviderTransactionID)
	}
	if original.Status != paymentprovider.PaymentSucceeded {
		return nil, fmt.Errorf("%w: demo transaction %s did not succeed, nothing to refund", shared.ErrConflict, req.ProviderTransactionID)
	}
	if req.AmountMinorUnits > original.FinalAmountMinorUnits {
		return nil, fmt.Errorf("%w: refund amount exceeds the original transaction", shared.ErrConflict)
	}

	now := p.now()
	result := paymentprovider.PaymentResult{
		Status: paymentprovider.PaymentSucceeded, ProviderTransactionID: "demo_refund_" + uuid.NewString(),
		FinalAmountMinorUnits: req.AmountMinorUnits, FinalCurrency: req.Currency,
		Reason: "refund of " + req.ProviderTransactionID, OccurredAt: now,
	}
	p.results[result.ProviderTransactionID] = result
	return &result, nil
}

var _ paymentprovider.Provider = (*Provider)(nil)
