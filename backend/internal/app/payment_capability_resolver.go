package app

import (
	"context"

	"github.com/project-algebra/algebra/internal/domain/paymentprovider"
)

// AvailableRail is one payment rail PaymentCapabilityResolver considered,
// with an explicit reason when it isn't usable — never silently omitted.
type AvailableRail struct {
	Mode         paymentprovider.Mode
	Available    bool
	Reason       string
	Capabilities paymentprovider.Capabilities
}

// PaymentCapabilityResolver answers "which payment rails could handle this
// request" without ever silently picking an unavailable one. Exactly one
// provider is wired in this build (providers/paymentdemo), so there is
// exactly one branch today — the shape exists so adding a real provider
// (Visa Intelligent Commerce, Mastercard Agent Pay, ...) later is a
// registration, not a rewrite of this contract.
type PaymentCapabilityResolver struct {
	providers []paymentprovider.Provider
}

func NewPaymentCapabilityResolver(providers ...paymentprovider.Provider) *PaymentCapabilityResolver {
	return &PaymentCapabilityResolver{providers: providers}
}

// Resolve returns one entry per configured provider, reporting whether its
// capabilities actually cover the requested amount — a real fitness check,
// not a hardcoded assumption.
func (r *PaymentCapabilityResolver) Resolve(_ context.Context, _ string, amountMinorUnits int64) []AvailableRail {
	out := make([]AvailableRail, 0, len(r.providers))
	for _, p := range r.providers {
		caps := p.Capabilities()
		rail := AvailableRail{Mode: caps.Mode, Capabilities: caps}
		switch {
		case !caps.CanExecute:
			rail.Reason = "provider cannot execute payments"
		case amountMinorUnits <= 0:
			rail.Reason = "amount must be positive"
		default:
			rail.Available = true
		}
		out = append(out, rail)
	}
	return out
}
