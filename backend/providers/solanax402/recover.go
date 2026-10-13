package solanax402

import (
	"context"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/spendpass"
	"github.com/project-algebra/algebra/providers/paychan"
)

var _ app.ChannelRecoverer = (*Rail)(nil)

// RecoverEscrow gets back the ceiling a usage-based payment escrowed when its
// provider never settled the channel. It takes the next step the program
// allows the payer (see paychan.NextRecovery): close the channel, seal it once
// the grace period is over, withdraw what was not settled. Each call takes at
// most one step and only acts on what the chain shows, so it can run on every
// sweep. It starts the clock (the close request) no earlier than notBefore:
// a provider that is going to settle does so within moments of answering.
func (r *Rail) RecoverEscrow(ctx context.Context, ev econ.Evidence, notBefore time.Time) (string, error) {
	if ev.Channel == "" {
		return "", nil
	}
	ch, err := solana.ParsePublicKey(ev.Channel)
	if err != nil {
		return "", fmt.Errorf("solanax402: the evidence's channel isn't an address: %w", err)
	}
	info, err := r.cfg.RPC.GetAccountInfo(ctx, ch, r.cfg.Commitment)
	if err != nil {
		return "", err
	}
	if info == nil || info.Owner != paychan.ProgramID {
		return "", nil // never opened, or closed out and reclaimed: nothing is escrowed
	}
	c, err := paychan.DecodeChannel(info.Data)
	if err != nil {
		return "", err
	}
	if c.Payer != r.payer {
		return "", fmt.Errorf("solanax402: channel %s was opened by %s, not by this wallet", ch, c.Payer)
	}
	now := r.now()
	step, _ := c.NextRecovery(now)
	switch {
	case step == paychan.StepNone:
		return "", nil
	case step == paychan.StepRequestClose && now.Before(notBefore):
		return "", nil
	}
	ix, err := paychan.RecoveryInstruction(step, r.payer, ch, r.mint, solana.TokenProgram)
	if err != nil {
		return "", err
	}
	signed, err := spendpass.Sign(ctx, r.cfg.RPC, r.cfg.Signer, nil, r.cfg.ComputePriceMicroLamports, ix)
	if err != nil {
		return "", err
	}
	if err := spendpass.Send(ctx, r.cfg.RPC, signed, r.cfg.Commitment); err != nil {
		return "", fmt.Errorf("solanax402: recovering channel %s (%s): %w", ch, step, err)
	}
	return fmt.Sprintf("%s (%s)", step, signed.Signature), nil
}
