package v1

import (
	"net/http"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/quote"
)

type itemRequest struct {
	Query    string `json:"query"`
	Quantity int    `json:"quantity"`
}

type constraintsRequest struct {
	MaxTotalMinorUnits int64    `json:"max_total_minor_units"`
	Currency           string   `json:"currency"`
	DeliveryProfile    string   `json:"delivery_profile,omitempty"`
	PaymentProfile     string   `json:"payment_profile,omitempty"`
	PreferredMerchants []string `json:"preferred_merchants,omitempty"`
	ExcludedMerchants  []string `json:"excluded_merchants,omitempty"`
	Category           string   `json:"category,omitempty"`
}

type createIntentRequest struct {
	Items       []itemRequest      `json:"items"`
	Constraints constraintsRequest `json:"constraints"`
}

// intentResponse carries what the owner needs to render an intent — never
// user/agent IDs or metadata.
type intentResponse struct {
	IntentID        string        `json:"intent_id"`
	Status          string        `json:"status"`
	SelectedQuoteID string        `json:"selected_quote_id,omitempty"`
	Items           []intent.Item `json:"items"`
	Category        string        `json:"category,omitempty"`
	MaxTotalMinor   int64         `json:"max_total_minor_units,omitempty"`
	Currency        string        `json:"currency,omitempty"`
	CreatedAt       string        `json:"created_at"`
}

func toIntentResponse(pi *intent.PurchaseIntent) intentResponse {
	return intentResponse{
		IntentID: pi.ID, Status: string(pi.Status), SelectedQuoteID: pi.SelectedQuoteID,
		Items: pi.Items, Category: pi.Constraints.Category, MaxTotalMinor: pi.Constraints.MaxTotalMinorUnits,
		Currency: pi.Constraints.Currency, CreatedAt: pi.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (a *API) createIntent(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req createIntentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	items := make([]intent.Item, len(req.Items))
	for i, it := range req.Items {
		items[i] = intent.Item{Query: it.Query, Quantity: it.Quantity}
	}
	pi, err := a.b.Intents.CreateIntent(r.Context(), a.b.Idempotency, r.Header.Get("Idempotency-Key"), app.CreateIntentInput{
		UserID: ag.UserID, AgentID: ag.ID, Items: items,
		Constraints: intent.Constraints{
			MaxTotalMinorUnits: req.Constraints.MaxTotalMinorUnits, Currency: req.Constraints.Currency,
			DeliveryProfile: req.Constraints.DeliveryProfile, PaymentProfile: req.Constraints.PaymentProfile,
			PreferredMerchants: req.Constraints.PreferredMerchants, ExcludedMerchants: req.Constraints.ExcludedMerchants,
			Category: req.Constraints.Category,
		},
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toIntentResponse(pi))
}

func (a *API) getIntent(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	pi, err := a.b.Intents.GetIntent(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toIntentResponse(pi))
}

func (a *API) cancelIntent(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	pi, err := a.b.Intents.CancelIntent(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toIntentResponse(pi))
}

func (a *API) discover(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	quotes, err := a.b.Discovery.Discover(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, quotesResponse{Quotes: stripAll(quotes)})
}

type quotesResponse struct {
	Quotes []quote.CheckoutQuote `json:"quotes"`
}

func stripAll(quotes []*quote.CheckoutQuote) []quote.CheckoutQuote {
	out := make([]quote.CheckoutQuote, len(quotes))
	for i, q := range quotes {
		cp := *q
		cp.CartID = "" // never expose the merchant cart handle over a transport boundary
		out[i] = cp
	}
	return out
}

func (a *API) getQuotes(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	quotes, err := a.b.Quotes.GetQuotes(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, quotesResponse{Quotes: stripAll(quotes)})
}

type selectQuoteRequest struct {
	QuoteID string `json:"quote_id"`
}

func (a *API) selectQuote(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req selectQuoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := a.b.Quotes.SelectQuote(r.Context(), ag.ID, r.PathValue("id"), req.QuoteID); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

type decisionResponse struct {
	Decision      string   `json:"decision"`
	ReasonCodes   []string `json:"reason_codes"`
	PolicyVersion string   `json:"policy_version"`
}

func (a *API) requestPurchase(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	dec, err := a.b.Policy.EvaluateAndTransition(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decisionResponse{Decision: string(dec.Decision), ReasonCodes: dec.ReasonCodes, PolicyVersion: dec.PolicyVersion})
}

func (a *API) policyPreview(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	dec, err := a.b.Policy.PreviewDecision(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decisionResponse{Decision: string(dec.Decision), ReasonCodes: dec.ReasonCodes, PolicyVersion: dec.PolicyVersion})
}

func (a *API) policyExplain(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	dec, err := a.b.Policy.ExplainDecision(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decisionResponse{Decision: string(dec.Decision), ReasonCodes: dec.ReasonCodes, PolicyVersion: dec.PolicyVersion})
}

type executeResponse struct {
	IntentStatus string      `json:"intent_status"`
	Order        interface{} `json:"order,omitempty"`
	Reason       string      `json:"reason,omitempty"`
	// Receipt is the signed spend receipt (compact JWS) for a placed order:
	// verifiable by anyone at /.well-known/jwks.json or POST /receipts/verify.
	Receipt string `json:"receipt,omitempty"`
}

func (a *API) execute(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	outcome, err := a.b.Orders.Execute(r.Context(), a.b.Idempotency, r.Header.Get("Idempotency-Key"), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, executeResponse{IntentStatus: string(outcome.IntentStatus), Order: outcome.Order, Reason: outcome.Reason, Receipt: outcome.Receipt})
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	ord, err := a.b.Orders.GetOrderStatus(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ord)
}

func (a *API) getReceipt(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	ord, err := a.b.Orders.GetReceipt(r.Context(), ag.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ord)
}
