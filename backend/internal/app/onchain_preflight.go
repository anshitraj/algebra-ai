package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// Before a wallet is asked to sign the transaction that puts a pass on chain,
// the server checks that it can succeed: the owner holds the deposit and the
// SOL for rent and the fee on that cluster, and the node runs the transaction
// without error. A wallet shown a transaction that would revert says only
// "simulation failed"; this says why, and what to do.

// fundingFacts is what the cluster says about the owner's wallet.
type fundingFacts struct {
	Cluster      string // "devnet" or "mainnet"
	Owner        string
	Lamports     uint64
	USDCMinor    int64
	DepositMinor int64
	// Simulated reports whether the node ran the transaction; SimFailed
	// whether it failed there, with the program's log lines.
	Simulated bool
	SimFailed bool
	Logs      []string
}

// fundingProblem is a sentence a person can act on, or "" when the
// transaction should succeed.
func fundingProblem(f fundingFacts) string {
	wallet := f.Owner
	if len(wallet) > 12 {
		wallet = wallet[:4] + "…" + wallet[len(wallet)-4:]
	}
	where := "Solana " + f.Cluster
	faucet := func(what string) string {
		if f.Cluster != "devnet" {
			return ""
		}
		if what == "USDC" {
			return " Get devnet USDC at faucet.circle.com (choose Solana Devnet)."
		}
		return " Get devnet SOL at faucet.solana.com."
	}
	if f.DepositMinor > 0 && f.USDCMinor < f.DepositMinor {
		return fmt.Sprintf("wallet %s holds %s USDC on %s, and the deposit is %s USDC. Fund the wallet or lower the amount.%s",
			wallet, chain.FormatUnits(f.USDCMinor, chain.USDCDecimals), where, chain.FormatUnits(f.DepositMinor, chain.USDCDecimals), faucet("USDC"))
	}
	if f.Lamports == 0 {
		return fmt.Sprintf("wallet %s holds no SOL on %s. Making a pass needs about 0.01 SOL for its accounts' rent and the fee.%s", wallet, where, faucet("SOL"))
	}
	if f.Simulated && f.SimFailed {
		logs := strings.Join(f.Logs, "\n")
		if strings.Contains(logs, "insufficient lamports") || strings.Contains(logs, "insufficient funds for rent") {
			return fmt.Sprintf("wallet %s holds %s SOL on %s, which doesn't cover the rent of the pass's accounts and the fee (about 0.01 SOL).%s",
				wallet, chain.FormatUnits(int64(f.Lamports), 9), where, faucet("SOL"))
		}
		return fmt.Sprintf("this transaction would fail on %s: %s", where, lastProgramLog(f.Logs))
	}
	return ""
}

// lastProgramLog is the most telling line of a failed simulation.
func lastProgramLog(logs []string) string {
	for i := len(logs) - 1; i >= 0; i-- {
		l := strings.TrimSpace(logs[i])
		if strings.Contains(l, "Error") || strings.Contains(l, "failed") || strings.Contains(l, "insufficient") {
			if len(l) > 200 {
				l = l[:200]
			}
			return strings.TrimPrefix(l, "Program log: ")
		}
	}
	return "the node gave no reason"
}

// checkFunding reads the owner's balances and simulates tx on n. When the
// node can't be asked, it returns "": a flaky node must not stop a person
// whose wallet is fine, and the wallet simulates again before signing.
func (s *OnchainPassService) checkFunding(ctx context.Context, n *OnchainNetwork, owner solana.PublicKey, depositMinor int64, txBase64 string) string {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	f := fundingFacts{Cluster: n.Cluster, Owner: owner.String(), DepositMinor: depositMinor}
	lamports, err := n.RPC.GetBalance(ctx, owner, solana.Confirmed)
	if err != nil {
		return ""
	}
	f.Lamports = lamports
	if depositMinor > 0 {
		token, err := solana.AssociatedTokenAddress(owner, n.Mint, solana.TokenProgram)
		if err != nil {
			return ""
		}
		bal, err := n.RPC.GetTokenAccountBalance(ctx, token, solana.Confirmed)
		switch {
		case errors.Is(err, solana.ErrAccountNotFound):
			// No USDC account at all: it holds none.
		case err != nil:
			return ""
		default:
			f.USDCMinor = int64(bal.Amount)
		}
	}
	if sim, err := n.RPC.SimulateTransaction(ctx, txBase64, solana.Confirmed); err == nil && sim != nil {
		f.Simulated, f.SimFailed, f.Logs = true, sim.Err != nil, sim.Logs
	}
	return fundingProblem(f)
}
