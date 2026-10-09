package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/account"
)

// Everything under /api/v1/me is session-only: the signed-in human, never
// an agent token.

func (a *API) getMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	u, err := a.b.Accounts.User(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.toUserResponse(r.Context(), u))
}

type updateMeRequest struct {
	Name string `json:"name"`
}

func (a *API) updateMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	var req updateMeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	u, err := a.b.Accounts.UpdateName(r.Context(), sess.UserID, req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a.toUserResponse(r.Context(), u))
}

type sessionListItem struct {
	ID         string `json:"id"`
	UserAgent  string `json:"user_agent"`
	IP         string `json:"ip"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at"`
	Current    bool   `json:"current"`
}

func (a *API) listMySessions(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	sessions, err := a.b.Accounts.ListSessions(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]sessionListItem, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionListItem{
			ID: s.ID, UserAgent: s.UserAgent, IP: s.IP,
			CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339), LastSeenAt: s.LastSeenAt.UTC().Format(time.RFC3339),
			Current: s.ID == sess.ID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) revokeMySession(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	if err := a.b.Accounts.RevokeSession(r.Context(), sess.UserID, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	if r.PathValue("id") == sess.ID {
		a.clearSessionCookie(w)
	}
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

type guardrailsResponse struct {
	account.Guardrails
	IsDefault         bool     `json:"is_default"`
	KnownCategories   []string `json:"known_categories"`
	PlatformMaxPerDay int64    `json:"platform_max_per_day_minor_units"`
}

func (a *API) getMyGuardrails(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	g, isDefault, err := a.b.Accounts.Guardrails(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, guardrailsResponse{
		Guardrails: g, IsDefault: isDefault, KnownCategories: account.KnownCategories,
		PlatformMaxPerDay: account.PlatformMaxPerDayMinorUnits,
	})
}

func (a *API) setMyGuardrails(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	var req account.Guardrails
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	g, err := a.b.Accounts.SetGuardrails(r.Context(), sess.UserID, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, guardrailsResponse{
		Guardrails: g, KnownCategories: account.KnownCategories,
		PlatformMaxPerDay: account.PlatformMaxPerDayMinorUnits,
	})
}

func (a *API) completeOnboarding(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	var req app.OnboardingAnswers
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	if err := a.b.Onboarding.Complete(r.Context(), sess, req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
		return
	}
	u, err := a.b.Accounts.User(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.toUserResponse(r.Context(), u))
}

func queryLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

func (a *API) getMyOverview(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	g, _, err := a.b.Accounts.Guardrails(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	ov, err := a.b.Activity.Overview(r.Context(), sess.UserID, g.Currency)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"spent_today":       ov.SpentToday,
		"pending_approvals": ov.PendingApprovals,
		"orders_total":      ov.OrdersTotal,
		"guardrails":        g,
	})
}

func (a *API) listMyIntents(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	out, err := a.b.Activity.Intents(r.Context(), sess.UserID, queryLimit(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) listMyApprovals(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	out, err := a.b.Activity.PendingApprovals(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// getMyOrder is one of the signed-in user's orders with its timeline and
// delivery address — the console's order page, invoice and map.
func (a *API) getMyOrder(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	d, err := a.b.Orders.OrderForUser(r.Context(), sess.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, d)
}

func (a *API) listMyOrders(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	out, err := a.b.Activity.Orders(r.Context(), sess.UserID, queryLimit(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
