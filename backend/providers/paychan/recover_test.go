package paychan

import (
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

// The payer's way out of a channel its provider never settles, as the
// program's own state machine allows it.
func TestNextRecovery(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	const grace = 60
	cases := []struct {
		name string
		c    Channel
		want RecoveryStep
		wait bool
	}{
		{"open: ask for the close", Channel{Status: StatusOpen, Deposit: 50_000}, StepRequestClose, false},
		{"closing, grace period running: wait", Channel{Status: StatusClosing, Deposit: 50_000, GracePeriod: grace, ClosureStartedAt: now.Unix() - 30}, StepNone, true},
		{"closing, grace over but within the margin: wait", Channel{Status: StatusClosing, Deposit: 50_000, GracePeriod: grace, ClosureStartedAt: now.Unix() - grace - 3}, StepNone, true},
		{"closing, grace over: seal it", Channel{Status: StatusClosing, Deposit: 50_000, GracePeriod: grace, ClosureStartedAt: now.Unix() - grace - 60}, StepSeal, false},
		{"sealed, remainder still in the escrow: withdraw", Channel{Status: StatusSealed, Deposit: 50_000, Settled: 0}, StepWithdraw, false},
		{"sealed with part settled: withdraw the rest", Channel{Status: StatusSealed, Deposit: 50_000, Settled: 2_000}, StepWithdraw, false},
		{"sealed and already withdrawn: done", Channel{Status: StatusSealed, Deposit: 50_000, PayerWithdrawnAt: 1_999_000}, StepNone, false},
		{"sealed with nothing left to withdraw: done", Channel{Status: StatusSealed, Deposit: 50_000, Settled: 50_000}, StepNone, false},
		{"distributed: the provider refunded the payer itself", Channel{Status: StatusDistributed, Deposit: 50_000, Settled: 2_000}, StepNone, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step, wait := tc.c.NextRecovery(now)
			if step != tc.want {
				t.Fatalf("step = %v, want %v", step, tc.want)
			}
			if (wait > 0) != tc.wait {
				t.Fatalf("wait = %v, want waiting: %v", wait, tc.wait)
			}
		})
	}
}

func TestRecoveryInstructionsAreThePayersOwn(t *testing.T) {
	payer, _ := solana.NewKeypair()
	ch, _ := solana.NewKeypair()
	mint := solana.MustPublicKey("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU")
	for step, want := range map[RecoveryStep]byte{StepRequestClose: ixRequestClose, StepSeal: ixSeal, StepWithdraw: ixWithdrawPayer} {
		ix, err := RecoveryInstruction(step, payer.PublicKey(), ch.PublicKey(), mint, solana.TokenProgram)
		if err != nil {
			t.Fatal(err)
		}
		if ix.ProgramID != ProgramID || len(ix.Data) == 0 || ix.Data[0] != want {
			t.Fatalf("%v: %+v", step, ix)
		}
	}
	if _, err := RecoveryInstruction(StepNone, payer.PublicKey(), ch.PublicKey(), mint, solana.TokenProgram); err == nil {
		t.Fatal("there is no instruction for 'nothing to do'")
	}
	// Closing and withdrawing are the payer's to sign; sealing is anyone's.
	for _, step := range []RecoveryStep{StepRequestClose, StepWithdraw} {
		ix, _ := RecoveryInstruction(step, payer.PublicKey(), ch.PublicKey(), mint, solana.TokenProgram)
		if !ix.Accounts[0].IsSigner || ix.Accounts[0].Pubkey != payer.PublicKey() {
			t.Fatalf("%v must be signed by the payer: %+v", step, ix.Accounts[0])
		}
	}
}
