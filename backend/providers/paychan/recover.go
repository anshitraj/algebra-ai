package paychan

import (
	"errors"
	"time"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

// A provider that takes a payment channel's open and then never settles it
// leaves the payer's ceiling escrowed. The program gives the payer a way out
// that needs nobody's cooperation, and this file is that path:
//
//	open ──RequestClose (payer)──▶ closing ──grace period, then Seal (anyone)──▶ sealed ──WithdrawPayer (payer)──▶ money back
//
// While the channel is closing the provider can still settle what it earned;
// once sealed, what it did not settle is the payer's.

// RecoveryStep is the next thing a payer does to get an unsettled escrow back.
type RecoveryStep uint8

const (
	// StepNone: nothing to do now (finished, still within the grace period, or
	// the provider already settled and refunded the payer).
	StepNone RecoveryStep = iota
	// StepRequestClose starts the grace period.
	StepRequestClose
	// StepSeal locks the channel once the grace period is over.
	StepSeal
	// StepWithdraw returns the unsettled remainder to the payer.
	StepWithdraw
)

func (s RecoveryStep) String() string {
	switch s {
	case StepRequestClose:
		return "requested the channel's close"
	case StepSeal:
		return "sealed the channel"
	case StepWithdraw:
		return "withdrew the unsettled remainder"
	}
	return "nothing to do"
}

// sealSlack is a margin after the grace period, for clock and block-time
// differences: sealing a moment early is refused by the program, not harmful.
const sealSlack = 10 * time.Second

// NextRecovery reads the channel's own state and says what the payer does
// next. When the answer is StepNone because the grace period is still running,
// wait is how long until it is over.
func (c *Channel) NextRecovery(now time.Time) (step RecoveryStep, wait time.Duration) {
	switch c.Status {
	case StatusOpen:
		return StepRequestClose, 0
	case StatusClosing:
		ready := time.Unix(c.ClosureStartedAt, 0).Add(time.Duration(c.GracePeriod)*time.Second + sealSlack)
		if now.Before(ready) {
			return StepNone, ready.Sub(now)
		}
		return StepSeal, 0
	case StatusSealed:
		if c.PayerWithdrawnAt == 0 && c.Refundable() > 0 {
			return StepWithdraw, 0
		}
	}
	// Distributed: the provider paid itself and refunded the payer in one go.
	return StepNone, 0
}

var errNoStep = errors.New("paychan: there is no instruction for that recovery step")

// RecoveryInstruction is the instruction for a step. The payer signs it.
func RecoveryInstruction(step RecoveryStep, payer, channel, mint, tokenProgram solana.PublicKey) (solana.Instruction, error) {
	switch step {
	case StepRequestClose:
		return RequestClose(payer, channel), nil
	case StepSeal:
		return Seal(channel), nil
	case StepWithdraw:
		return WithdrawPayer(payer, channel, mint, tokenProgram)
	}
	return solana.Instruction{}, errNoStep
}
