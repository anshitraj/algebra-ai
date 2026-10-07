package v1

import (
	"net/http"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/privacy"
)

// consumeAgentTurn is POST /api/v1/me/agent-turns. The web app's chat route
// calls it once per message, before running the model, so one person can't
// run up unbounded LLM and web-search cost. Demo accounts — no signup —
// get a smaller allowance. Session only. Without Redis there is no counter
// (development), and a Redis error fails open like the rate limiter.
func (a *API) consumeAgentTurn(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	u, err := a.b.Accounts.User(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	limit := a.b.AuthConfig.AgentTurnsPerDay
	if u.IsDemo() {
		limit = a.b.AuthConfig.DemoAgentTurnsPerDay
	}
	if a.limiter == nil || limit <= 0 {
		writeJSON(w, http.StatusOK, agentTurnResponse{OK: true})
		return
	}
	allowed, retry, err := a.limiter.Allow(r.Context(), "quota:agent-turns:"+sess.UserID, limit, 24*time.Hour)
	if err != nil || allowed {
		writeJSON(w, http.StatusOK, agentTurnResponse{OK: true, Limit: limit})
		return
	}
	w.Header().Set("Retry-After", formatSeconds(retry))
	writeJSON(w, http.StatusTooManyRequests, agentTurnResponse{
		Error: "daily agent message limit reached", Limit: limit, Demo: u.IsDemo(), RetryAfterSeconds: int(retry.Seconds()),
	})
}

type agentTurnResponse struct {
	OK                bool   `json:"ok,omitempty"`
	Error             string `json:"error,omitempty"`
	Limit             int    `json:"limit,omitempty"`
	Demo              bool   `json:"demo,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

type deleteMeRequest struct {
	// Confirm must be the word DELETE — a typed confirmation, since this
	// can't be undone.
	Confirm string `json:"confirm"`
}

// deleteMe is DELETE /api/v1/me: the signed-in person erases their own
// account (see AccountService.DeleteAccount). Session only — never an agent.
// A paid plan that would still renew must be cancelled first, so nobody is
// charged for an account they deleted.
func (a *API) deleteMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	var req deleteMeRequest
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Confirm) != "DELETE" {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: `type DELETE to confirm`})
		return
	}
	if a.b.Billing != nil {
		st, err := a.b.Billing.Status(r.Context(), sess.UserID)
		if err != nil {
			writeError(w, err)
			return
		}
		if sub := st.Subscription; sub != nil && !sub.Status.Terminal() && !sub.CancelAtPeriodEnd {
			writeJSON(w, http.StatusConflict, errorBody{Error: "cancel your plan under Plan & billing first, then delete your account"})
			return
		}
	}
	if err := a.b.Accounts.DeleteAccount(r.Context(), sess.UserID); err != nil {
		writeError(w, err)
		return
	}
	a.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

// exportMe is GET /api/v1/me/export: everything Algebra holds about the
// signed-in person, as one JSON download (the DPDP Act's right to a summary
// of personal data). Saved addresses are listed by name only — their
// contents are encrypted and only released to a merchant for an approved
// order; they're visible on the Profile page.
func (a *API) exportMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	u, err := a.b.Accounts.User(ctx, sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := map[string]any{
		"exported_at": time.Now().UTC(),
		"account":     a.toUserResponse(ctx, u),
	}
	if g, _, err := a.b.Accounts.Guardrails(ctx, sess.UserID); err == nil {
		out["guardrails"] = g
	}
	if aliases, err := a.b.Privacy.ListAliases(ctx, sess.UserID, privacy.ProfileShipping); err == nil {
		out["saved_addresses"] = aliases
	}
	if sess.AgentID != "" {
		if p, err := a.b.CommerceProfileSvc.GetOrEmpty(ctx, sess.AgentID); err == nil {
			out["shopping_profile"] = p
		}
	}
	if intents, err := a.b.Activity.Intents(ctx, sess.UserID, 100); err == nil {
		out["purchase_requests"] = intents
	}
	if orders, err := a.b.Activity.Orders(ctx, sess.UserID, 100); err == nil {
		out["orders"] = orders
	}
	if sessions, err := a.b.Accounts.ListSessions(ctx, sess.UserID); err == nil {
		out["signed_in_devices"] = sessions
	}
	// What their agents did: the Spend Passes, and the requests made under them
	// with the input each carried. The answers kept for repeat requests are not
	// here; they have their own route while they are kept.
	if passes, err := a.b.SpendPasses.List(ctx, sess.UserID); err == nil {
		out["spend_passes"] = passes
	}
	if a.b.Economic != nil {
		if requests, err := a.b.Economic.List(ctx, sess.UserID, 200); err == nil {
			out["requests"] = requests
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="algebra-data-export.json"`)
	writeJSON(w, http.StatusOK, out)
}
