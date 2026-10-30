package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

type fakeCreditSpend struct{ used int64 }

func (f *fakeCreditSpend) TestCreditSpend(_ context.Context, userID string, rails []string) (int64, error) {
	if userID != "user_1" || !slices.Equal(rails, []string{"solana-devnet"}) {
		return 0, errors.New("wrong question")
	}
	return f.used, nil
}

type onePass struct{ p spendpass.Pass }

func (o onePass) Get(context.Context, string) (*spendpass.Pass, error) { return &o.p, nil }

func TestTestCredits_RefusesWhatTheCreditsCantCover(t *testing.T) {
	spend := &fakeCreditSpend{}
	c := NewTestCredits([]string{"solana-devnet"}, "solana-devnet", onePass{spendpass.Pass{ID: "pass_1", UserID: "user_1"}}, spend, 10_000_000, 5_000_000_000)
	ctx := context.Background()

	// 9.99 USDC used before, plus this attempt's hold of 0.05: the asked 0.01 fits.
	spend.used = 9_990_000 + 50_000
	rv := &econ.Reservation{Rail: "solana-devnet", HoldMinor: 50_000}
	if _, err := c.Fund(ctx, "pass_1", rv, 10_000); err != nil {
		t.Fatalf("0.01 of the last 0.01 is fine: %v", err)
	}
	// Asking 0.02 does not.
	var denied *AuthorityDenied
	if _, err := c.Fund(ctx, "pass_1", rv, 20_000); !errors.As(err, &denied) || denied.ReasonCodes[0] != DenyTestCreditsExhausted {
		t.Fatalf("over the credits: %v", err)
	}
	// Another rail (mainnet, the sandbox) is not this funder's business.
	if _, err := c.Fund(ctx, "pass_1", &econ.Reservation{Rail: "solana", HoldMinor: 1}, 1); err != nil {
		t.Fatalf("other rails pass through: %v", err)
	}
	bal, err := c.Balance(ctx, "user_1")
	if err != nil || bal.USDCGrantedMinor != 10_000_000 || bal.USDCLeftMinor != 0 || bal.SOLLamports != 5_000_000_000 || bal.Network != "solana-devnet" {
		t.Fatalf("balance: %+v %v", bal, err)
	}
	spend.used = 2_500_000
	if bal, _ := c.Balance(ctx, "user_1"); bal.USDCLeftMinor != 7_500_000 || bal.USDCUsedMinor != 2_500_000 {
		t.Fatalf("7.5 left: %+v", bal)
	}
}
