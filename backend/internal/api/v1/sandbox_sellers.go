package v1

import (
	"net/http"

	"github.com/project-algebra/algebra/providers/sandboxpay"
)

// sandboxSellers is GET /api/v1/sandbox/sellers: every seller a demo account
// can buy from, paid in simulated USDC through the sandbox rail. Public: it
// names no one and moves nothing.
func (a *API) sandboxSellers(w http.ResponseWriter, r *http.Request) {
	sellers := a.b.SandboxPersonas.Sellers()
	if a.b.SandboxProvider != nil {
		sellers = append(sellers, sandboxpay.Seller{
			ID: sandboxpay.ProviderID, Name: "Token risk check (sandbox)", Capability: "solana.token-risk", PriceMinor: 3_000,
			Behaviour: "honest", ExampleTask: "Is the BONK token risky?",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sellers": sellers,
		"note":    "Demo sellers: simulated data, paid in simulated USDC. No money moves.",
	})
}
