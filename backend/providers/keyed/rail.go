package keyed

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
)

// RailName is the rail every keyed call settles through.
const RailName = "keyed"

// BilledPrefix starts the "transaction" of a call its vendor billed: there is
// no chain transaction, only the vendor's operation id.
const BilledPrefix = "billed:"

// Rail is the payment rail for calls a vendor bills to the person's own
// account. It moves no money and signs nothing: it releases the authority to
// make the call, and later says what was charged. Whether anything was
// charged is the runner's record of what the vendor reported, kept in the
// attempt's evidence; a call that reported no charge is not settled.
type Rail struct{}

var (
	_ app.Rail              = Rail{}
	_ app.PaymentAuthorizer = Rail{}
)

// NewRail makes the rail.
func NewRail() Rail { return Rail{} }

func (Rail) Name() string { return RailName }

func (Rail) Authorize(_ context.Context, rv *econ.Reservation, _ app.PaymentRequest) (*app.PaymentAuthority, error) {
	id := "keyed_" + uuid.NewString()
	vendor := strings.TrimPrefix(rv.ProviderID, providerPrefix)
	return &app.PaymentAuthority{Value: id, Evidence: econ.Evidence{
		Rail: RailName, Protocol: "api-key", Scheme: "billed", Network: chain.Keyed, Asset: "USD",
		PaymentID: id, Payer: "your " + vendor + " account", PayTo: vendor,
	}}, nil
}

func (Rail) Settlement(_ context.Context, ev econ.Evidence) (app.Settlement, error) {
	if !strings.HasPrefix(ev.Transaction, BilledPrefix) {
		return app.Settlement{Status: app.SettlementNotSettled, Detail: "the vendor reported no charge for this call"}, nil
	}
	return app.Settlement{
		Status: app.SettlementSettled, AmountMinor: ev.AmountMinor, Transaction: ev.Transaction,
		Network: chain.Keyed, Asset: "USD", Payer: ev.Payer, PayTo: ev.PayTo,
		Detail: "billed by the vendor to the person's own account",
	}, nil
}
