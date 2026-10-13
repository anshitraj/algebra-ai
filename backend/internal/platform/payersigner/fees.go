package payersigner

import (
	"context"
	"log/slog"
	"time"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

const (
	// FeeFloorLamports (0.02 SOL) is what the payer needs to be ready: pulls
	// and refunds cost a few thousand lamports each.
	FeeFloorLamports = 20_000_000
	// topUpBelowLamports (0.05 SOL) is when the faucet is asked for more.
	topUpBelowLamports = 50_000_000
	airdropLamports    = 1_000_000_000
	// TopUpEvery is how often a generated key's balance is checked.
	TopUpEvery = 30 * time.Minute
)

// chainAPI is the part of the node client fee top-ups use.
type chainAPI interface {
	GetBalance(ctx context.Context, pk solana.PublicKey, commitment string) (uint64, error)
	RequestAirdrop(ctx context.Context, to solana.PublicKey, lamports uint64) (string, error)
	ConfirmSignature(ctx context.Context, sig, commitment string, interval time.Duration) (*solana.SignatureStatus, error)
}

// Ready reports the payer's SOL and whether it covers fees.
func Ready(ctx context.Context, rpc interface {
	GetBalance(ctx context.Context, pk solana.PublicKey, commitment string) (uint64, error)
}, key solana.PublicKey) (lamports uint64, ok bool, err error) {
	lamports, err = rpc.GetBalance(ctx, key, solana.Confirmed)
	return lamports, err == nil && lamports >= FeeFloorLamports, err
}

// TopUp asks the devnet faucet for SOL when the key is low, and says what
// happened. The public faucet often refuses (rate limits); that is reported,
// never fatal, and the address is in the message so a person can fund it at
// https://faucet.solana.com.
func TopUp(ctx context.Context, rpc chainAPI, key solana.PublicKey, log *slog.Logger) {
	bal, err := rpc.GetBalance(ctx, key, solana.Confirmed)
	if err != nil {
		log.Warn("payer signer: couldn't read the balance", "payer", key.String(), "err", err)
		return
	}
	if bal >= topUpBelowLamports {
		return
	}
	sig, err := rpc.RequestAirdrop(ctx, key, airdropLamports)
	if err != nil {
		log.Warn("payer signer: low on devnet SOL and the faucet refused; fund the address at https://faucet.solana.com",
			"payer", key.String(), "lamports", bal, "err", err)
		return
	}
	if _, err := rpc.ConfirmSignature(ctx, sig, solana.Confirmed, 0); err != nil {
		log.Warn("payer signer: the devnet airdrop was not confirmed", "payer", key.String(), "err", err)
		return
	}
	log.Info("payer signer: topped up from the devnet faucet", "payer", key.String(), "signature", sig)
}

// KeepFunded tops the key up now and then until ctx ends. For a generated
// devnet key only.
func KeepFunded(ctx context.Context, rpc chainAPI, key solana.PublicKey, log *slog.Logger, every time.Duration) {
	run := func() {
		c, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		TopUp(c, rpc, key, log)
	}
	run()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
