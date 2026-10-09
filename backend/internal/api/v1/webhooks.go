package v1

import (
	"io"
	"net/http"
)

// receiveWebhook is a generic inbound webhook endpoint (mandate §14/§36:
// OrderCreated/PaymentChallengeCreated-style external events; §47: webhook
// spoofing must be rejected). {provider} names which shared secret to
// verify against (WEBHOOK_SECRET_<PROVIDER>, see
// internal/platform/wiring.envWebhookSecret) — no real provider is
// configured in this build, so every call is rejected with 401 until one
// is, which is the correct default (reject unverified, never accept).
func (a *API) receiveWebhook(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB cap — mandate §49 request size limits
	defer r.Body.Close()
	if err != nil {
		writeError(w, err)
		return
	}

	result, err := a.b.Webhooks.Receive(r.Context(), provider, r.Header.Get("X-Algebra-Signature"), body, r.Header.Get("X-Algebra-Event-ID"))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, okResponse{OK: !result.Duplicate})
}
