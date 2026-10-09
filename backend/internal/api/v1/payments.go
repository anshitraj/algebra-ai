package v1

import (
	"net/http"

	"github.com/project-algebra/algebra/internal/domain/payment"
)

type paymentSourceView struct {
	ID           string               `json:"id"`
	Alias        string               `json:"alias"`
	Type         string               `json:"type"`
	Network      string               `json:"network,omitempty"`
	Last4        string               `json:"last4,omitempty"`
	Nickname     string               `json:"nickname,omitempty"`
	Capabilities payment.Capabilities `json:"capabilities"`
	Revoked      bool                 `json:"revoked"`
}

func toPaymentSourceView(s payment.PaymentSource) paymentSourceView {
	return paymentSourceView{ID: s.ID, Alias: s.Alias, Type: string(s.Type), Network: s.Network, Last4: s.Last4, Nickname: s.Nickname, Capabilities: s.Capabilities, Revoked: s.IsRevoked()}
}

func (a *API) listPaymentSources(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sources, err := a.b.Payments.ListSources(r.Context(), ag.ID, ag.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]paymentSourceView, len(sources))
	for i, s := range sources {
		out[i] = toPaymentSourceView(s)
	}
	writeJSON(w, http.StatusOK, out)
}

type addPaymentSourceRequest struct {
	ProviderNonce string `json:"provider_nonce"` // from the vault's hosted-fields client SDK; never a raw PAN
	Alias         string `json:"alias"`
	Nickname      string `json:"nickname,omitempty"`
}

// addPaymentSource is the server side of "Add Card": the browser has
// already exchanged the raw card details for a nonce via the vault
// provider's OWN hosted fields (mandate §21/§42) before this request is
// ever made. Algebra's backend never sees a PAN here or anywhere else.
func (a *API) addPaymentSource(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req addPaymentSourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	source, err := a.b.Payments.AddCard(r.Context(), payment.TokenizeRequest{
		UserID: userID, ProviderNonce: req.ProviderNonce, Alias: req.Alias, Nickname: req.Nickname,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toPaymentSourceView(*source))
}

func (a *API) revokePaymentSource(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := a.b.Payments.RevokeSource(r.Context(), userID, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}
