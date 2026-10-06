package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
)

// railView is one Solana cluster as the console shows it: whether Algebra can
// pay on it, from which wallet, and how much USDC that wallet holds.
type railView struct {
	Network    string `json:"network"`
	Configured bool   `json:"configured"`
	Rail       string `json:"rail,omitempty"`
	Address    string `json:"address,omitempty"`
	// USDCMinor is nil when the wallet has no USDC account yet.
	USDCMinor       *uint64 `json:"usdc_minor,omitempty"`
	MaxPaymentMinor int64   `json:"max_payment_minor,omitempty"`
	// Error says the node couldn't be read just now; payments still need it.
	Error string `json:"error,omitempty"`
}

// listRails: GET /api/v1/rails. Mainnet and devnet, each configured or not,
// with the wallet's balance read from the chain; and whether the sandbox
// (simulated money) is on. Never moves money.
func (a *API) listRails(w http.ResponseWriter, r *http.Request) {
	if _, err := a.currentUserID(r); err != nil {
		if _, aerr := a.resolveAgent(r); aerr != nil {
			writeError(w, err)
			return
		}
	}
	out := []railView{}
	for _, network := range []string{chain.Solana, chain.SolanaDevnet} {
		v := railView{Network: network}
		for _, rail := range a.b.SolanaRails {
			if rail.Network() != network {
				continue
			}
			v.Configured, v.Rail, v.Address = true, rail.Name(), rail.Address().String()
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			st, err := rail.Status(ctx)
			cancel()
			if err != nil {
				v.Error = "the Solana node couldn't be read just now"
			} else {
				v.USDCMinor, v.MaxPaymentMinor = st.USDCMinor, st.MaxPaymentMinor
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"rails": out, "sandbox": a.b.SandboxProvider != nil})
}
