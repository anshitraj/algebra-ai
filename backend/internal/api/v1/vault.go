package v1

import (
	"errors"
	"fmt"
	"math/big"
	"net/http"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/shared"
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

// allowanceOwner is vaultOwner where agent allowances run.
func (a *API) allowanceOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := a.vaultOwner(w, r)
	if !ok {
		return "", false
	}
	if a.b.VaultAllowances == nil && a.b.EVMAllowances == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "agents can't spend from vaults on this server"})
		return "", false
	}
	return userID, true
}

// vaultAllowances: GET /api/v1/me/vault/allowances. What agents may spend
// from the vault on each network: the owner's choice and what the chain says
// is left of it.
func (a *API) vaultAllowances(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.allowanceOwner(w, r)
	if !ok {
		return
	}
	out := []app.VaultAllowanceView{}
	if a.b.VaultAllowances != nil {
		sol, err := a.b.VaultAllowances.Allowances(r.Context(), userID)
		if err != nil {
			writeError(w, err)
			return
		}
		out = append(out, sol...)
	}
	if a.b.EVMAllowances != nil {
		// Base and Arc are read where the vault has an EVM address; a node
		// that doesn't answer leaves its row unread, not the page broken.
		if evmViews, err := a.b.EVMAllowances.Allowances(r.Context(), userID); err == nil {
			out = append(out, evmViews...)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"allowances": out})
}

// vaultAllowancePrepare: POST /api/v1/me/vault/allowance/prepare {network}.
// What the browser needs to build an approve or a revoke itself.
func (a *API) vaultAllowancePrepare(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.allowanceOwner(w, r)
	if !ok {
		return
	}
	var req struct {
		Network string `json:"network"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if a.b.EVMAllowances.Has(req.Network) {
		p, err := a.b.EVMAllowances.Prepare(r.Context(), userID, req.Network)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"kind": "permit", "permit": p})
		return
	}
	if a.b.VaultAllowances == nil {
		writeError(w, fmt.Errorf("%w: agents can't spend from a vault on %s here", shared.ErrNotFound, req.Network))
		return
	}
	p, err := a.b.VaultAllowances.Prepare(r.Context(), userID, req.Network)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// vaultAllowanceSubmit: POST /api/v1/me/vault/allowance {network,
// transaction}. An approve or revoke the vault signed; Algebra checks it,
// pays its fee and sends it.
func (a *API) vaultAllowanceSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.allowanceOwner(w, r)
	if !ok {
		return
	}
	var req struct {
		Network     string `json:"network"`
		Transaction string `json:"transaction"`
		// A permit, on Base and Arc: the limit, its deadline and the vault's signature.
		ValueMinor int64  `json:"value_minor"`
		Deadline   int64  `json:"deadline"`
		Signature  string `json:"signature"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if a.b.EVMAllowances.Has(req.Network) {
		out, err := a.b.EVMAllowances.Submit(r.Context(), userID, app.PermitSubmit{Network: req.Network, ValueMinor: req.ValueMinor, Deadline: req.Deadline, Signature: req.Signature})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	if a.b.VaultAllowances == nil {
		writeError(w, fmt.Errorf("%w: agents can't spend from a vault on %s here", shared.ErrNotFound, req.Network))
		return
	}
	out, err := a.b.VaultAllowances.Submit(r.Context(), userID, req.Network, req.Transaction)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
