package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/chain"
)

// railView is one Solana cluster as the console shows it: whether Algebra can
// pay on it and whether the wallet it pays from can. It names neither the
// wallet nor its balance: any signed-in account can read this, and what a
// person needs is whether payments can go through (a receipt names the wallet
// that paid; the operator reads the address from the server's own log or
// cmd/solana-wallet).
type railView struct {
	Network    string `json:"network"`
	Configured bool   `json:"configured"`
	Rail       string `json:"rail,omitempty"`
	// Funded: the wallet holds at least one payment's ceiling of USDC (any USDC,
	// when no ceiling is set). False when it has no USDC account yet.
	Funded          bool  `json:"funded"`
	MaxPaymentMinor int64 `json:"max_payment_minor,omitempty"`
	// Error says the node couldn't be read just now; payments still need it.
	Error string `json:"error,omitempty"`
}

// railFunded says whether a balance covers one payment of up to ceilingMinor.
// A nil balance is a wallet with no USDC account.
func railFunded(usdcMinor *uint64, ceilingMinor int64) bool {
	if usdcMinor == nil || *usdcMinor == 0 {
		return false
	}
	return ceilingMinor <= 0 || *usdcMinor >= uint64(ceilingMinor)
}

// listRails: GET /api/v1/rails. Mainnet and devnet, each configured or not,
// with the wallet's balance read from the chain; and whether the sandbox
// (simulated money) is on. Never moves money.
func (a *API) listRails(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		ag, aerr := a.resolveAgent(r)
		if aerr != nil {
			writeError(w, err)
			return
		}
		userID = ag.UserID
	}
	out := []railView{}
	// A demo account pays in the sandbox: the wallets behind the real rails, their
	// addresses and balances, are not shown to someone who signed in with one click.
	if a.b.Accounts != nil && userID != "" {
		mode, err := a.b.Accounts.UserMode(r.Context(), userID)
		if err != nil {
			writeError(w, err)
			return
		}
		if mode == account.ModeDemo {
			writeJSON(w, http.StatusOK, map[string]any{"rails": out, "sandbox": a.b.SandboxProvider != nil})
			return
		}
	}
	for _, network := range []string{chain.Solana, chain.SolanaDevnet} {
		v := railView{Network: network}
		for _, rail := range a.b.SolanaRails {
			if rail.Network() != network {
				continue
			}
			v.Configured, v.Rail = true, rail.Name()
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			st, err := rail.Status(ctx)
			cancel()
			if err != nil {
				v.Error = "the Solana node couldn't be read just now"
			} else {
				v.Funded, v.MaxPaymentMinor = railFunded(st.USDCMinor, st.MaxPaymentMinor), st.MaxPaymentMinor
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"rails": out, "sandbox": a.b.SandboxProvider != nil})
}
