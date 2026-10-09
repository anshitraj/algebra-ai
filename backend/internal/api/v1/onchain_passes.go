package v1

import (
	"net/http"

	"github.com/project-algebra/algebra/internal/app"
)

// --- Spend Passes on Solana: the person's side (session only) ---
//
//	GET    /onchain/networks                   where passes can be made, and what they must name
//	POST   /me/passes/{id}/onchain/prepare     unsigned tx: make (and fund) the on-chain pass
//	POST   /me/passes/{id}/onchain/link        {"network","address"}: check it on chain and link it
//	GET    /me/passes/{id}/onchain             the pass as the chain has it, and its recent pulls
//	POST   /me/passes/{id}/onchain/tx          unsigned tx: freeze, unfreeze, deposit, withdraw, revoke, close
//	DELETE /me/passes/{id}/onchain             forget the link
//
// Every transaction comes back unsigned: the person's wallet signs and sends
// it. Algebra never holds the owner's key.

func (a *API) onchainService(w http.ResponseWriter) (*app.OnchainPassService, bool) {
	if a.b.OnchainPasses == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "on-chain Spend Passes aren't available here: no Solana rail is running"})
		return nil, false
	}
	return a.b.OnchainPasses, true
}

func (a *API) onchainNetworks(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.onchainService(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": svc.Networks()})
}

func (a *API) prepareOnchainPass(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	svc, ok := a.onchainService(w)
	if !ok {
		return
	}
	var req app.PrepareOptions
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: `send {"network":"solana-devnet","owner":"<wallet>","deposit_minor":1000000}`})
		return
	}
	out, err := svc.Prepare(r.Context(), sess.UserID, r.PathValue("id"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) linkOnchainPass(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	svc, ok := a.onchainService(w)
	if !ok {
		return
	}
	var req struct {
		Network string `json:"network"`
		Address string `json:"address"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Address == "" {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: `send {"network":"solana-devnet","address":"<pass address>"}`})
		return
	}
	b, err := svc.Link(r.Context(), sess.UserID, r.PathValue("id"), req.Network, req.Address)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (a *API) getOnchainPass(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	svc, ok := a.onchainService(w)
	if !ok {
		return
	}
	v, err := svc.State(r.Context(), sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) onchainPassTx(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	svc, ok := a.onchainService(w)
	if !ok {
		return
	}
	var req struct {
		Action      string `json:"action"`
		AmountMinor int64  `json:"amount_minor"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: `send {"action":"freeze"} (or unfreeze, deposit, withdraw, revoke, close; deposit and withdraw take "amount_minor")`})
		return
	}
	tx, err := svc.OwnerTransaction(r.Context(), sess.UserID, r.PathValue("id"), req.Action, req.AmountMinor)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tx)
}

func (a *API) unlinkOnchainPass(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	svc, ok := a.onchainService(w)
	if !ok {
		return
	}
	if err := svc.Unlink(r.Context(), sess.UserID, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
