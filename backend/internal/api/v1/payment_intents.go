package v1

import (
	"net/http"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/paymentintent"
)

type createPaymentIntentRequest struct {
	Purpose             string `json:"purpose,omitempty"`
	Merchant            string `json:"merchant"`
	MerchantDomain      string `json:"merchant_domain,omitempty"`
	Category            string `json:"category,omitempty"`
	International       bool   `json:"international,omitempty"`
	AmountMinorUnits    int64  `json:"amount_minor_units"`
	Currency            string `json:"currency"`
	ToleranceMinorUnits int64  `json:"tolerance_minor_units,omitempty"`
	ProductRef          string `json:"product_ref,omitempty"`
	PaymentSourceAlias  string `json:"payment_source_alias"`
	RequestedCapability string `json:"requested_capability,omitempty"`
}

type paymentIntentResponse struct {
	PaymentIntentID       string `json:"payment_intent_id"`
	Status                string `json:"status"`
	Merchant              string `json:"merchant"`
	AmountMinorUnits      int64  `json:"amount_minor_units"`
	Currency              string `json:"currency"`
	PolicyVersion         string `json:"policy_version,omitempty"`
	ProviderTransactionID string `json:"provider_transaction_id,omitempty"`
	ProviderStatus        string `json:"provider_status,omitempty"`
}

func toPaymentIntentResponse(p *paymentintent.AgenticPaymentIntent) paymentIntentResponse {
	return paymentIntentResponse{
		PaymentIntentID: p.ID, Status: string(p.Status), Merchant: p.Merchant,
		AmountMinorUnits: p.AmountMinorUnits, Currency: p.Currency,
		PolicyVersion: p.PolicyVersion, ProviderTransactionID: p.ProviderTransactionID, ProviderStatus: p.ProviderStatus,
	}
}

type createPaymentIntentResponse struct {
	paymentIntentResponse
	Decision decisionResponse `json:"decision"`
}

// createPaymentIntent is POST /api/v1/payment-intents — builds an
// AgenticPaymentIntent and evaluates it against the tenant's policy in one
// call (see app.PaymentIntentService.Create's doc comment on why there is
// no separate evaluate step, unlike the commerce-flow's create-then-
// request-purchase split).
func (a *API) createPaymentIntent(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req createPaymentIntentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}

	result, err := a.b.PaymentIntentSvc.Create(r.Context(), app.CreatePaymentIntentInput{
		AgentID: ag.ID, Purpose: req.Purpose, Merchant: req.Merchant, MerchantDomain: req.MerchantDomain,
		Category: req.Category, International: req.International, AmountMinorUnits: req.AmountMinorUnits,
		Currency: req.Currency, ToleranceMinorUnits: req.ToleranceMinorUnits, ProductRef: req.ProductRef,
		PaymentSourceAlias: req.PaymentSourceAlias, RequestedCapability: req.RequestedCapability,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, createPaymentIntentResponse{
		paymentIntentResponse: toPaymentIntentResponse(result.PaymentIntent),
		Decision: decisionResponse{
			Decision: string(result.Decision.Decision), ReasonCodes: result.Decision.ReasonCodes, PolicyVersion: result.Decision.PolicyVersion,
		},
	})
}

// getPaymentIntent backs both GET /api/v1/payment-intents/{id} and
// GET /api/v1/payment-intents/{id}/status — the same resource either way.
func (a *API) getPaymentIntent(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	p, err := a.b.PaymentIntentSvc.Get(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPaymentIntentResponse(p))
}

func (a *API) getPaymentIntentAuditTrail(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	events, err := a.b.AuditSvc.ListForPaymentIntent(r.Context(), ag.ID, r.PathValue("id"), 0)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// approvePaymentIntent/rejectPaymentIntent take a payment_intent_id (not an
// approval_id, unlike /api/v1/approvals/{id}/approve) — they resolve the
// underlying approval first via ApprovalService.GetByPaymentIntent, then
// call the same generalized Approve/Reject a human clicking a button in a
// console would.
func (a *API) approvePaymentIntent(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	appr, err := a.b.Approvals.GetByPaymentIntent(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	approved, err := a.b.Approvals.Approve(r.Context(), userID, appr.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, approvalResponse{ApprovalID: approved.ID, Status: string(approved.Status)})
}

func (a *API) rejectPaymentIntent(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	appr, err := a.b.Approvals.GetByPaymentIntent(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	rejected, err := a.b.Approvals.Reject(r.Context(), userID, appr.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, approvalResponse{ApprovalID: rejected.ID, Status: string(rejected.Status)})
}

type paymentIntentExecuteResponse struct {
	Status                string `json:"status"`
	ProviderTransactionID string `json:"provider_transaction_id,omitempty"`
	Reason                string `json:"reason,omitempty"`
}

func (a *API) executePaymentIntent(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	outcome, err := a.b.PaymentIntentSvc.Execute(r.Context(), a.b.Idempotency, r.Header.Get("Idempotency-Key"), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	resp := paymentIntentExecuteResponse{Status: string(outcome.Status), Reason: outcome.Reason}
	if outcome.Result != nil {
		resp.ProviderTransactionID = outcome.Result.ProviderTransactionID
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) revokePaymentIntent(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	p, err := a.b.PaymentIntentSvc.Revoke(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPaymentIntentResponse(p))
}

type transactionsResponse struct {
	Transactions []paymentIntentResponse `json:"transactions"`
}

// listTransactions is GET /api/v1/transactions — tenant-authenticated (not
// agent-authenticated), listing everything under that tenant.
func (a *API) listTransactions(w http.ResponseWriter, r *http.Request) {
	t, err := a.resolveTenant(r)
	if err != nil {
		writeError(w, err)
		return
	}
	list, err := a.b.PaymentIntentSvc.ListForTenant(r.Context(), t.ID, 0)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]paymentIntentResponse, len(list))
	for i := range list {
		out[i] = toPaymentIntentResponse(&list[i])
	}
	writeJSON(w, http.StatusOK, transactionsResponse{Transactions: out})
}
