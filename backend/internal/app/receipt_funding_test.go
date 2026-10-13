package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// fundingFor serves the pulls of a vault service, by reservation.
type fundingFor map[string]*OnchainPull

func (f fundingFor) PullFor(_ context.Context, reservationID string) (*OnchainPull, error) {
	if p, ok := f[reservationID]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("%w: no pull", shared.ErrNotFound)
}

// commitWithFunding runs one intent to a committed, signed receipt; pull is the
// vault pull made for its attempt (nil: the payer's own money).
func commitWithFunding(t *testing.T, pull func(reservationID string) *OnchainPull) (*receipt.IntentClaims, *econRig) {
	t.Helper()
	ctx := context.Background()
	rig := newEconRig(t, 1, 10_000_000)
	in := rig.intent(t, "agent_1", 50_000)
	r := rig.fullRun(t, "agent_1", in.ID)
	rig.rail.land(r, 3_000)
	// The funding reader is attached before the intent finishes, as at start-up.
	f := fundingFor{}
	if p := pull(r.ID); p != nil {
		f[r.ID] = p
	}
	svc := NewIntentReceiptService(rig.signer, "https://algebra.test", rig.agents, rig.store)
	svc.SetFunding(f)
	rig.svc.SetReceipts(svc)
	v, err := rig.svc.Complete(ctx, "agent_1", in.ID, r.ID, CompletionReport{Outcome: econ.OutcomeFulfilled, Evidence: econ.Evidence{ResultHash: "sha256:abc"}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := receipt.VerifyIntent(v.Receipt, rig.signer.JWKS())
	if err != nil {
		t.Fatal(err)
	}
	return c, rig
}

// A payment a vault funded says which vault, whose, and the pull that moved the
// money: signed, so it can't be changed afterwards.
func TestReceiptNamesTheVaultThatFundedThePayment(t *testing.T) {
	c, _ := commitWithFunding(t, func(rsv string) *OnchainPull {
		return &OnchainPull{ReservationID: rsv, PassAddress: "FakeVaultAddress111", OwnerWallet: "FakeOwnerWallet222", PullSignature: "FakePullSignature333", AmountMinor: 3_000, State: PullPulled}
	})
	f := c.Settlement.Funding
	if f == nil {
		t.Fatal("a vault-funded payment's receipt names its funding")
	}
	if f.Source != "spend_pass_vault" || f.Vault != "FakeVaultAddress111" || f.Owner != "FakeOwnerWallet222" ||
		f.PullSignature != "FakePullSignature333" || f.Pulled.MinorUnits != 3_000 || f.Pulled.Currency != "USDC" {
		t.Fatalf("funding: %+v", f)
	}
}

func TestReceiptHasNoFundingWhenNoVaultPaid(t *testing.T) {
	c, _ := commitWithFunding(t, func(string) *OnchainPull { return nil })
	if c.Settlement == nil || c.Settlement.Funding != nil {
		t.Fatalf("no vault, no funding claim: %+v", c.Settlement)
	}
}

// A pull that never landed moved nothing: it is not a funding claim.
func TestReceiptIgnoresAPullThatNeverLanded(t *testing.T) {
	c, _ := commitWithFunding(t, func(rsv string) *OnchainPull {
		return &OnchainPull{ReservationID: rsv, PassAddress: "v", OwnerWallet: "o", PullSignature: "s", AmountMinor: 3_000, State: PullVoid}
	})
	if c.Settlement.Funding != nil {
		t.Fatalf("a void pull funded nothing: %+v", c.Settlement.Funding)
	}
}
