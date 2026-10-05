package v1

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
)

// --- Economic intents: the executor's side (agent token + Spend Pass) ---
//
// The lifecycle an executor drives, one call per step:
//
//	POST /economic-intents                                  create (idempotent by outcome)
//	POST /economic-intents/{id}/reservations                reserve: exclusive right to attempt
//	POST /economic-intents/{id}/reservations/{rid}/begin    policy recheck, cross the boundary
//	POST …/{rid}/authorize-payment                          single-use payment for the 402
//	POST …/{rid}/complete                                   report; Algebra verifies with the rail
//	POST …/{rid}/release                                    give up before beginning
//	POST /economic-intents/{id}/reconcile                   resolve an UNKNOWN outcome
//
// Approval is a person's: it lives under /me, session only.

type economicSpecRequest struct {
	Capability     string              `json:"capability"`
	Input          json.RawMessage     `json:"input"`
	Quantity       int                 `json:"quantity"`
	Window         string              `json:"window"`
	Currency       string              `json:"currency"`
	BudgetMaxMinor int64               `json:"budget_max_minor"`
	Constraints    econ.Constraints    `json:"constraints"`
	ProviderPolicy econ.ProviderPolicy `json:"provider_policy"`
	TTLSeconds     int                 `json:"ttl_seconds"`
	// PassID is used only by the session route (a person creating an
	// intent for one of their passes).
	PassID string `json:"spend_pass_id"`
}

func (req economicSpecRequest) spec() econ.Spec {
	return econ.Spec{
		Capability: req.Capability, Input: req.Input, Quantity: req.Quantity, Window: req.Window, Currency: req.Currency,
		BudgetMaxMinor: req.BudgetMaxMinor, Constraints: req.Constraints, ProviderPolicy: req.ProviderPolicy,
		TTL: time.Duration(req.TTLSeconds) * time.Second,
	}
}

// writeEconError adds the structured detail an executor needs to act on a
// refusal: why, and whether a duplicate commitment was prevented.
func writeEconError(w http.ResponseWriter, err error) {
	var rej *app.ReservationRejected
	var denied *app.AuthorityDenied
	switch {
	case errors.As(err, &rej):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": err.Error(), "reason": rej.Reason, "intent_state": rej.State,
			"duplicate_prevented": rej.Duplicate, "reason_codes": rej.ReasonCodes,
		})
	case errors.As(err, &denied):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error(), "reason_codes": denied.ReasonCodes})
	case errors.Is(err, app.ErrExecutionFrozen):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "reason": app.RejectUnknown})
	default:
		writeError(w, err)
	}
}

// econBadRequest reports validation errors from the domain as 400s.
func econBadRequest(w http.ResponseWriter, err error) bool {
	msg := err.Error()
	for _, p := range []string{"capability ", "quantity ", "window ", "budget ", "an intent ", "constraints ", "input: "} {
		if strings.HasPrefix(msg, p) {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: msg})
			return true
		}
	}
	return false
}

func (a *API) economic(w http.ResponseWriter) bool {
	if a.b.Economic == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "economic intents are not enabled on this server"})
		return false
	}
	return true
}

func (a *API) createEconomicIntent(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req economicSpecRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	v, created, err := a.b.Economic.CreateIntent(r.Context(), ag.ID, req.spec())
	if err != nil {
		if !econBadRequest(w, err) {
			writeEconError(w, err)
		}
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"intent": v, "created": created})
}

// agentView returns the intent if it belongs to the calling agent's principal.
func (a *API) agentView(w http.ResponseWriter, r *http.Request, withEvents bool) (*app.IntentView, string, bool) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return nil, "", false
	}
	v, err := a.b.Economic.ViewFor(r.Context(), ag.UserID, r.PathValue("id"), withEvents)
	if err != nil {
		writeError(w, err)
		return nil, "", false
	}
	return v, ag.ID, true
}

func (a *API) getEconomicIntent(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	v, _, ok := a.agentView(w, r, r.URL.Query().Get("events") == "true")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) reserveEconomicIntent(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		ProviderID string `json:"provider_id"`
		Rail       string `json:"rail"`
		QuoteMinor int64  `json:"quote_minor"`
		Semantics  string `json:"settlement_semantics"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	rv, err := a.b.Economic.Reserve(r.Context(), ag.ID, r.PathValue("id"), app.ReserveRequest{
		ProviderID: req.ProviderID, Rail: req.Rail, QuoteMinor: req.QuoteMinor, Semantics: econ.Semantics(strings.ToUpper(req.Semantics)),
	})
	if err != nil {
		writeEconError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rv)
}

func (a *API) beginEconomicAttempt(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rv, err := a.b.Economic.Begin(r.Context(), ag.ID, r.PathValue("id"), r.PathValue("rid"))
	if err != nil {
		writeEconError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rv)
}

// authorizeEconomicPayment returns a single-use payment for the provider's
// 402 challenge. The payment value is a bearer credential for that one
// payment: it's returned here and never stored or logged by Algebra.
func (a *API) authorizeEconomicPayment(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		// Requirements is the provider's 402 body, or one of its "accepts".
		Requirements json.RawMessage `json:"payment_requirements"`
		Resource     string          `json:"resource"`
	}
	if err := decodeJSON(r, &req); err != nil || len(req.Requirements) == 0 {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "send {\"payment_requirements\": <the provider's 402 response body>}"})
		return
	}
	if len(req.Requirements) > 16<<10 {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "payment requirements too large"})
		return
	}
	auth, err := a.b.Economic.AuthorizePayment(r.Context(), ag.ID, r.PathValue("id"), r.PathValue("rid"), app.PaymentRequest{Requirements: req.Requirements, Resource: req.Resource})
	if err != nil {
		writeEconError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"header": auth.Header, "value": auth.Value, "amount_minor": auth.AmountMinor,
		"payment_id": auth.Evidence.PaymentID, "network": auth.Evidence.Network, "test": auth.Evidence.Test,
	})
}

// completeEconomicAttempt takes the executor's report. Only identifiers and
// hashes are accepted from the executor: settlement facts come from the
// rail, never from the agent's word.
func (a *API) completeEconomicAttempt(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Outcome             string `json:"outcome"`
		Transaction         string `json:"transaction"`
		ResultHash          string `json:"result_hash"`
		RequestHash         string `json:"request_hash"`
		ProviderOperationID string `json:"provider_operation_id"`
		Detail              string `json:"detail"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	v, err := a.b.Economic.Complete(r.Context(), ag.ID, r.PathValue("id"), r.PathValue("rid"), app.CompletionReport{
		Outcome: econ.Outcome(strings.ToUpper(req.Outcome)), Detail: req.Detail,
		Evidence: econ.Evidence{Transaction: req.Transaction, ResultHash: req.ResultHash, RequestHash: req.RequestHash, ProviderOperationID: req.ProviderOperationID},
	})
	if err != nil {
		writeEconError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) releaseEconomicAttempt(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := a.b.Economic.Release(r.Context(), ag.ID, r.PathValue("id"), r.PathValue("rid")); err != nil {
		writeEconError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"released": true})
}

// reconcileEconomicIntent asks the rail and the provider what happened.
// Safe for either side to call: it only observes, and only evidence moves
// the intent.
func (a *API) reconcileEconomicIntent(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	if _, ok := a.econOwner(w, r); !ok {
		return
	}
	res, err := a.b.Economic.Reconcile(r.Context(), r.PathValue("id"))
	if err != nil {
		writeEconError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) getEconomicReceipt(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	v, ok := a.econOwner(w, r)
	if !ok {
		return
	}
	if v.Receipt == "" {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "no receipt yet: one is signed when the intent commits (state " + string(v.State) + ")"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"receipt": v.Receipt, "intent_id": v.ID, "state": v.State, "summary": v.Summary})
}

// econOwner authorizes either the principal's session or one of the
// principal's agents for the intent in the path.
func (a *API) econOwner(w http.ResponseWriter, r *http.Request) (*app.IntentView, bool) {
	principal := ""
	if r.Header.Get("Authorization") == "" {
		if userID, err := a.currentUserID(r); err == nil {
			principal = userID
		}
	}
	if principal == "" {
		ag, err := a.resolveAgent(r)
		if err != nil {
			writeError(w, err)
			return nil, false
		}
		principal = ag.UserID
	}
	v, err := a.b.Economic.ViewFor(r.Context(), principal, r.PathValue("id"), true)
	if err != nil {
		writeError(w, err)
		return nil, false
	}
	return v, true
}

// --- the person's side (session only) ---

func (a *API) listMyEconomicIntents(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := a.b.Economic.List(r.Context(), userID, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"intents": list})
}

func (a *API) getMyEconomicIntent(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	v, err := a.b.Economic.ViewFor(r.Context(), userID, r.PathValue("id"), true)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) myEconomicStats(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 365 {
		days = 30
	}
	st, err := a.b.Economic.Stats(r.Context(), userID, time.Now().AddDate(0, 0, -days))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "stats": st,
		"note": "duplicate_spend_prevented_minor is an upper bound: blocked attempts times each intent's budget ceiling."})
}

func (a *API) createMyEconomicIntent(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req economicSpecRequest
	if err := decodeJSON(r, &req); err != nil || req.PassID == "" {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "send the intent with \"spend_pass_id\""})
		return
	}
	v, created, err := a.b.Economic.CreateIntentForPass(r.Context(), userID, req.PassID, req.spec())
	if err != nil {
		if !econBadRequest(w, err) {
			writeEconError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"intent": v, "created": created})
}

// approveMyEconomicIntent is the human yes. An agent token can't reach it:
// only a signed-in person (or, in development, X-User-ID) resolves here.
func (a *API) approveMyEconomicIntent(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	if r.Header.Get("Authorization") != "" {
		writeJSON(w, http.StatusForbidden, errorBody{Error: "approval is a person's decision; agent tokens can't approve"})
		return
	}
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	v, err := a.b.Economic.Approve(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeEconError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) cancelMyEconomicIntent(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	if r.Header.Get("Authorization") != "" {
		writeJSON(w, http.StatusForbidden, errorBody{Error: "cancelling is done by the person, from the console"})
		return
	}
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	v, err := a.b.Economic.Cancel(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeEconError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
