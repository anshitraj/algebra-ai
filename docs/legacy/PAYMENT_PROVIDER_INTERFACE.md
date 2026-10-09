# Payment Provider Interface

`backend/internal/domain/paymentprovider.Provider` is the interface every payment rail plugs into Algebra through. Before this existed, nothing in Algebra could actually move money on an agent's behalf — `backend/internal/domain/merchant.Connector.ExecuteCheckout` (the commerce-flow's own execution path) never receives a credential, only a cart handle, Algebra's own approval ID, and a shipping/billing address. This is the piece that closes that gap for the B2B agentic-payments product. See `docs/legacy/PROVIDER_STATUS.md` for which rails are actually wired today (short answer: one, `backend/providers/paymentdemo`, deterministic and in-memory).

## The interface

```go
type Provider interface {
	Capabilities() Capabilities

	RegisterPaymentSource(ctx context.Context, req RegisterSourceRequest) (*SourceRegistration, error)
	CreateDelegatedAuthorization(ctx context.Context, req DelegatedAuthorizationRequest) (*DelegatedAuthorization, error)
	RequestAuthentication(ctx context.Context, req AuthenticationRequest) (*AuthenticationResult, error)
	CreateScopedCredential(ctx context.Context, req ScopedCredentialRequest) (*ScopedCredential, error)
	ExecutePayment(ctx context.Context, req ExecutePaymentRequest) (*PaymentResult, error)
	GetPaymentStatus(ctx context.Context, providerTransactionID string) (*PaymentResult, error)
	RevokeAuthorization(ctx context.Context, authorizationRef string) error
	Refund(ctx context.Context, req RefundRequest) (*PaymentResult, error)
}
```

`backend/internal/app.PaymentIntentService.Execute` walks these in order: `RegisterPaymentSource` → `CreateDelegatedAuthorization` → `RequestAuthentication` → `CreateScopedCredential` → `ExecutePayment`. No method on this interface ever receives or returns a raw PAN, CVV, wallet private key, or seed phrase. `ScopedCredential.Value` is the one place a real secret may flow, and it's wrapped in `shared.SensitiveValue` (custom `String()`/`MarshalJSON()` that always redact) specifically so it can't accidentally end up in a log line, an audit event, or an API response built the ordinary way.

## Capabilities — never advertise what you can't do

```go
type Capabilities struct {
	Mode Mode // ISSUER_NATIVE | NETWORK_TOKEN | PROCESSOR_AGENTIC | STABLECOIN | MACHINE_PAYMENT | MERCHANT_MANAGED | HANDOFF | DEMO

	CanDelegate, CanIssueScopedCredential, CanExecute, CanRefund bool
	RequiresUserPresence, SupportsPasskey                       bool
	SupportsCard, SupportsUPI, SupportsStablecoin                bool
	SupportsRecurring, SupportsMerchantBinding, SupportsAmountBinding bool
}
```

`backend/internal/app.PaymentCapabilityResolver` reads this to answer "which rails could handle this request" without ever silently picking one that can't — same discipline `backend/internal/domain/merchant.Connector`'s doc comment already states for commerce connectors ("a connector must never claim a capability it doesn't have"), applied here to payment execution.

## Writing a new adapter

1. New package under `backend/providers/` (e.g. `backend/providers/visaintelligentcommerce`).
2. Implement all eight methods. A method the rail genuinely can't do returns `shared.ErrNotImplemented` — never a synthesized success (`Refund` is the common one to leave unimplemented for a rail that doesn't support it).
3. `Capabilities()` must be truthful — if `Refund` returns `ErrNotImplemented`, `CanRefund` must be `false`.
4. Register it in `backend/internal/platform/wiring.Build` (currently `demoProvider` is the sole registration) and add it to `backend/internal/app.PaymentCapabilityResolver`'s provider list.
5. Add a row to `docs/legacy/PROVIDER_STATUS.md` with its real status (SANDBOX until credentialed against production).
6. If it needs configuration (API keys, a cluster endpoint), fail closed at startup with a clear error if that configuration is missing — the same pattern `backend/internal/app/webhook_service.go` already uses for `WEBHOOK_SECRET_<PROVIDER>` (an unconfigured secret rejects every call rather than silently accepting one unverified).

## `backend/providers/paymentdemo` — the reference implementation

The only provider wired live in this build. Fully in-memory, deterministic, real (not stubbed) implementations of every method:

- **Scenarios** — `ExecutePaymentRequest.Metadata["demo_scenario"]` selects one deterministically: `decline`, `insufficient_funds`, `outage`, `authentication_required`. An absent or unrecognized value succeeds.
- **Credential expiry** — `CreateScopedCredential` issues a credential with a 5-minute lifetime; `ExecutePayment` checks it and fails cleanly if expired.
- **Idempotency** — a retried `ExecutePayment` call with the same `IdempotencyKey` replays the exact first outcome rather than re-deciding, independent of and in addition to `backend/internal/app.RunIdempotent` at the service layer. This is what a real payment gateway's own idempotency guarantee looks like — Algebra's app-layer idempotency and a provider's own idempotency are two separate, layered protections.
- Read the full implementation at `backend/providers/paymentdemo/demo.go` — every scenario above has a matching test in `backend/providers/paymentdemo/demo_test.go`.
