package paymentprovider

import "context"

// Provider is the interface every payment rail implements. Concrete
// adapters live under /providers — this package only defines the contract,
// the same "one interface, swappable backends" rule
// internal/domain/payment.CardVaultProvider and internal/domain/merchant.Connector
// already follow.
type Provider interface {
	Capabilities() Capabilities

	RegisterPaymentSource(ctx context.Context, req RegisterSourceRequest) (*SourceRegistration, error)
	CreateDelegatedAuthorization(ctx context.Context, req DelegatedAuthorizationRequest) (*DelegatedAuthorization, error)
	RequestAuthentication(ctx context.Context, req AuthenticationRequest) (*AuthenticationResult, error)
	CreateScopedCredential(ctx context.Context, req ScopedCredentialRequest) (*ScopedCredential, error)
	ExecutePayment(ctx context.Context, req ExecutePaymentRequest) (*PaymentResult, error)
	GetPaymentStatus(ctx context.Context, providerTransactionID string) (*PaymentResult, error)
	RevokeAuthorization(ctx context.Context, authorizationRef string) error

	// Refund may return shared.ErrNotImplemented for a rail that doesn't
	// support it — never a synthesized success.
	Refund(ctx context.Context, req RefundRequest) (*PaymentResult, error)
}
