package v1

import "net/http"

type completeAuthenticationRequest struct {
	MerchantOrderID string `json:"merchant_order_id"`
	Success         bool   `json:"success"`
}

// completeAuthentication resumes an intent parked in
// AUTHENTICATION_REQUIRED once the user finished 3DS/OTP/UPI on their own
// device (mandate §23). Algebra never sees the challenge secret — only the
// outcome, and on success it fetches the merchant's OWN confirmed order
// rather than inventing one.
func (a *API) completeAuthentication(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req completeAuthenticationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	outcome, err := a.b.Orders.CompleteAuthentication(r.Context(), ag.ID, r.PathValue("id"), req.MerchantOrderID, req.Success)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, executeResponse{
		IntentStatus: string(outcome.IntentStatus), Order: outcome.Order, Reason: outcome.Reason,
	})
}

// cancelOrder asks the MERCHANT to cancel — it does not simply mark the
// order cancelled locally. A merchant refusal surfaces as an error.
func (a *API) cancelOrder(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	ord, err := a.b.Orders.CancelOrder(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ord)
}

// getAuditTrail is the read side of the append-only audit log (mandate
// §33/§45) — the endpoint that makes "which agent requested this, what did
// the user approve, which policy allowed it, what was charged" answerable.
func (a *API) getAuditTrail(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	events, err := a.b.AuditSvc.ListForIntent(r.Context(), ag.ID, r.PathValue("id"), 0)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}
