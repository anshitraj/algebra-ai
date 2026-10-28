package v1

import (
	"errors"
	"math/big"
	"net/http"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/account"
)

// The owner's vault (docs/WALLET.md). Every route is the signed-in person's
// own vault; demo accounts have none. Nothing here ever receives a key.

// vaultOwner is the signed-in person, if they may have a vault.
func (a *API) vaultOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return "", false
	}
	if a.b.OwnerVaults == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "this server runs no vault"})
		return "", false
	}
	if a.b.Accounts != nil {
		if mode, err := a.b.Accounts.UserMode(r.Context(), sess.UserID); err == nil && mode == account.ModeDemo {
			writeJSON(w, http.StatusForbidden, errorBody{Error: "demo accounts practise with simulated USDC and have no vault; sign up to make one"})
			return "", false
		}
	}
	return sess.UserID, true
}

// vaultGet: GET /api/v1/me/vault. The account's vault addresses (null when
// it has none yet) and the chains this server runs.
func (a *API) vaultGet(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.vaultOwner(w, r)
	if !ok {
		return
	}
	v, err := a.b.OwnerVaults.Get(r.Context(), userID)
	if err != nil && !errors.Is(err, app.ErrVaultNone) {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vault": v, "chains": a.b.OwnerVaults.Chains()})
}

// vaultRegister: POST /api/v1/me/vault, an ownership proof. Records the
// addresses after both signatures check out.
func (a *API) vaultRegister(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.vaultOwner(w, r)
	if !ok {
		return
	}
	var p app.OwnershipProof
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, err)
		return
	}
	v, err := a.b.OwnerVaults.Register(r.Context(), userID, p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vault": v})
}

// vaultBalances: GET /api/v1/me/vault/balances?network=testnet|mainnet.
func (a *API) vaultBalances(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.vaultOwner(w, r)
	if !ok {
		return
	}
	test := r.URL.Query().Get("network") != "mainnet"
	out, err := a.b.OwnerVaults.Balances(r.Context(), userID, test, r.URL.Query().Get("force") == "1")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"balances": out})
}

// vaultPrepare: POST /api/v1/me/vault/withdraw/prepare {chain, to,
// amount_minor}. What the browser needs to build the withdrawal itself.
func (a *API) vaultPrepare(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.vaultOwner(w, r)
	if !ok {
		return
	}
	var req struct {
		Chain       string `json:"chain"`
		To          string `json:"to"`
		AmountMinor string `json:"amount_minor"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	amount, ok := new(big.Int).SetString(req.AmountMinor, 10)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "amount_minor must be a whole number of minor units"})
		return
	}
	p, err := a.b.OwnerVaults.PrepareWithdraw(r.Context(), userID, req.Chain, req.To, amount)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// vaultBroadcast: POST /api/v1/me/vault/broadcast {chain, raw}. Relays a
// transaction the browser signed with this account's vault.
func (a *API) vaultBroadcast(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.vaultOwner(w, r)
	if !ok {
		return
	}
	var req struct {
		Chain string `json:"chain"`
		Raw   string `json:"raw"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	hash, err := a.b.OwnerVaults.Broadcast(r.Context(), userID, req.Chain, req.Raw)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hash": hash})
}
