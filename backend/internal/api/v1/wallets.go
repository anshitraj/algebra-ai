package v1

import "net/http"

// myWallets: GET /api/v1/me/wallets?network=solana-devnet[&refresh=1]. The
// signed-in person's own Solana wallets (the ones they signed in with, or the
// one Privy made for them) with what each holds on that cluster, so the
// console can show an address to fund and what has arrived. Only their own
// wallets are ever read: it is not a lookup for other addresses.
func (a *API) myWallets(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	if a.b.WalletBalances == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "wallet balances aren't available on this server"})
		return
	}
	network := r.URL.Query().Get("network")
	if network == "" {
		network = "solana-devnet"
	}
	view, err := a.b.WalletBalances.WalletsFor(r.Context(), sess.UserID, network, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
