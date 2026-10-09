package v1

import (
	"errors"
	"io"
	"net/http"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Billing for Algebra's own plans. Everything but the webhook is
// session-only: a user manages their own plan, never an agent.

// getBillingPlans is GET /api/v1/billing/plans — public: the pricing page's
// numbers, straight from billing config.
func (a *API) getBillingPlans(w http.ResponseWriter, _ *http.Request) {
	if a.b.Billing == nil {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "billing is not configured"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, a.b.Billing.Plans())
}

func (a *API) getBilling(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	st, err := a.b.Billing.Status(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *API) startCheckout(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	co, err := a.b.Billing.StartCheckout(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	u, err := a.b.Accounts.User(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"key_id": co.KeyID, "subscription_id": co.SubscriptionID, "test_mode": co.TestMode,
		"prefill": map[string]string{"name": u.Name, "email": u.Email},
	})
}

type confirmCheckoutRequest struct {
	PaymentID      string `json:"razorpay_payment_id"`
	SubscriptionID string `json:"razorpay_subscription_id"`
	Signature      string `json:"razorpay_signature"`
}

func (a *API) confirmCheckout(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	var req confirmCheckoutRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	sub, err := a.b.Billing.ConfirmCheckout(r.Context(), sess.UserID, req.PaymentID, req.SubscriptionID, req.Signature)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (a *API) cancelSubscription(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	sub, err := a.b.Billing.Cancel(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

// razorpayWebhook is called by Razorpay, not a browser: authenticated only
// by the HMAC signature over the raw body. 2xx tells Razorpay to stop
// retrying, so a bad signature gets 400 and a transient failure 500.
func (a *API) razorpayWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "unreadable body"})
		return
	}
	err = a.b.Billing.HandleWebhook(r.Context(), body, r.Header.Get("X-Razorpay-Signature"), r.Header.Get("X-Razorpay-Event-Id"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, okResponse{OK: true})
	case errors.Is(err, app.ErrBillingNotConfigured):
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "billing not configured"})
	default:
		status := http.StatusInternalServerError
		if errors.Is(err, shared.ErrUnauthorized) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, errorBody{Error: "webhook rejected"})
	}
}
