package paymentdemo

import (
	"context"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/paymentprovider"
)

// prepareCredential walks the same register -> delegate -> scope-credential
// chain internal/app.PaymentIntentService.prepareCredential does, for tests
// that only care about ExecutePayment's own behavior.
func prepareCredential(t *testing.T, p *Provider, amount int64) *paymentprovider.ScopedCredential {
	t.Helper()
	ctx := context.Background()
	reg, err := p.RegisterPaymentSource(ctx, paymentprovider.RegisterSourceRequest{UserID: "user_1", Alias: "payment:personal"})
	if err != nil {
		t.Fatalf("RegisterPaymentSource: %v", err)
	}
	auth, err := p.CreateDelegatedAuthorization(ctx, paymentprovider.DelegatedAuthorizationRequest{
		UserID: "user_1", AgentID: "agent_1", SourceRef: reg.ProviderRef,
		Merchant: "Acme Co", MaxAmountMinorUnits: amount, Currency: "USD", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateDelegatedAuthorization: %v", err)
	}
	cred, err := p.CreateScopedCredential(ctx, paymentprovider.ScopedCredentialRequest{
		AuthorizationRef: auth.AuthorizationRef, AmountMinorUnits: amount, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("CreateScopedCredential: %v", err)
	}
	return cred
}

func TestExecutePayment_Succeeds(t *testing.T) {
	p := New()
	cred := prepareCredential(t, p, 5000)

	result, err := p.ExecutePayment(context.Background(), paymentprovider.ExecutePaymentRequest{
		CredentialRef: cred.CredentialRef, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD", IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatalf("ExecutePayment: %v", err)
	}
	if result.Status != paymentprovider.PaymentSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s (%s)", result.Status, result.Reason)
	}
	if result.ProviderTransactionID == "" {
		t.Error("expected a provider transaction id on success")
	}
	if result.FinalAmountMinorUnits != 5000 || result.FinalCurrency != "USD" {
		t.Errorf("unexpected final amount/currency: %d %s", result.FinalAmountMinorUnits, result.FinalCurrency)
	}
}

func TestExecutePayment_DemoScenarios(t *testing.T) {
	cases := []struct {
		scenario string
		want     paymentprovider.PaymentStatus
	}{
		{"decline", paymentprovider.PaymentDeclined},
		{"insufficient_funds", paymentprovider.PaymentDeclined},
		{"outage", paymentprovider.PaymentProviderUnavailable},
		{"authentication_required", paymentprovider.PaymentAuthenticationRequired},
	}
	for _, c := range cases {
		t.Run(c.scenario, func(t *testing.T) {
			p := New()
			cred := prepareCredential(t, p, 5000)
			result, err := p.ExecutePayment(context.Background(), paymentprovider.ExecutePaymentRequest{
				CredentialRef: cred.CredentialRef, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD",
				IdempotencyKey: "key-" + c.scenario, Metadata: map[string]string{"demo_scenario": c.scenario},
			})
			if err != nil {
				t.Fatalf("ExecutePayment: %v", err)
			}
			if result.Status != c.want {
				t.Fatalf("scenario %q: expected %s, got %s", c.scenario, c.want, result.Status)
			}
		})
	}
}

func TestExecutePayment_AuthenticationRequiredCarriesChallenge(t *testing.T) {
	p := New()
	cred := prepareCredential(t, p, 5000)
	result, err := p.ExecutePayment(context.Background(), paymentprovider.ExecutePaymentRequest{
		CredentialRef: cred.CredentialRef, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD",
		Metadata: map[string]string{"demo_scenario": "authentication_required"},
	})
	if err != nil {
		t.Fatalf("ExecutePayment: %v", err)
	}
	if result.Challenge == nil {
		t.Fatal("expected a challenge on AUTHENTICATION_REQUIRED")
	}
}

func TestExecutePayment_CredentialExpiry(t *testing.T) {
	p := New()
	start := time.Now()
	p.now = func() time.Time { return start }
	cred := prepareCredential(t, p, 5000)

	// Advance the fake clock past the 5-minute scoped-credential lifetime.
	p.now = func() time.Time { return start.Add(10 * time.Minute) }

	result, err := p.ExecutePayment(context.Background(), paymentprovider.ExecutePaymentRequest{
		CredentialRef: cred.CredentialRef, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("ExecutePayment: %v", err)
	}
	if result.Status != paymentprovider.PaymentFailed {
		t.Fatalf("expected FAILED for an expired credential, got %s", result.Status)
	}
}

func TestExecutePayment_DuplicateIdempotencyKeyReplaysResult(t *testing.T) {
	p := New()
	cred := prepareCredential(t, p, 5000)
	req := paymentprovider.ExecutePaymentRequest{
		CredentialRef: cred.CredentialRef, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD", IdempotencyKey: "same-key",
	}

	first, err := p.ExecutePayment(context.Background(), req)
	if err != nil {
		t.Fatalf("first ExecutePayment: %v", err)
	}
	second, err := p.ExecutePayment(context.Background(), req)
	if err != nil {
		t.Fatalf("second ExecutePayment: %v", err)
	}
	if first.ProviderTransactionID != second.ProviderTransactionID {
		t.Fatalf("expected a retried call with the same idempotency key to replay the same transaction, got %s vs %s",
			first.ProviderTransactionID, second.ProviderTransactionID)
	}
}

func TestExecutePayment_UnknownCredentialIsAnError(t *testing.T) {
	p := New()
	if _, err := p.ExecutePayment(context.Background(), paymentprovider.ExecutePaymentRequest{CredentialRef: "does-not-exist", Merchant: "Acme Co", AmountMinorUnits: 100, Currency: "USD"}); err == nil {
		t.Fatal("expected an error for an unknown credential reference")
	}
}

func TestGetPaymentStatus_ReturnsRecordedResult(t *testing.T) {
	p := New()
	cred := prepareCredential(t, p, 5000)
	executed, err := p.ExecutePayment(context.Background(), paymentprovider.ExecutePaymentRequest{
		CredentialRef: cred.CredentialRef, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("ExecutePayment: %v", err)
	}
	status, err := p.GetPaymentStatus(context.Background(), executed.ProviderTransactionID)
	if err != nil {
		t.Fatalf("GetPaymentStatus: %v", err)
	}
	if status.Status != paymentprovider.PaymentSucceeded {
		t.Errorf("expected SUCCEEDED, got %s", status.Status)
	}
}

func TestRefund_OfSucceededTransaction(t *testing.T) {
	p := New()
	cred := prepareCredential(t, p, 5000)
	executed, err := p.ExecutePayment(context.Background(), paymentprovider.ExecutePaymentRequest{
		CredentialRef: cred.CredentialRef, Merchant: "Acme Co", AmountMinorUnits: 5000, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("ExecutePayment: %v", err)
	}
	refund, err := p.Refund(context.Background(), paymentprovider.RefundRequest{
		ProviderTransactionID: executed.ProviderTransactionID, AmountMinorUnits: 5000, Currency: "USD", Reason: "customer request",
	})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if refund.Status != paymentprovider.PaymentSucceeded {
		t.Errorf("expected refund to succeed, got %s", refund.Status)
	}
}

func TestRevokeAuthorization_UnknownRefIsAnError(t *testing.T) {
	p := New()
	if err := p.RevokeAuthorization(context.Background(), "does-not-exist"); err == nil {
		t.Fatal("expected an error revoking an unknown authorization")
	}
}

func TestCapabilities_ReportsDemoMode(t *testing.T) {
	p := New()
	caps := p.Capabilities()
	if caps.Mode != paymentprovider.ModeDemo {
		t.Errorf("expected DEMO mode, got %s", caps.Mode)
	}
	if !caps.CanExecute {
		t.Error("expected CanExecute to be true — this provider is the only one wired live")
	}
}
