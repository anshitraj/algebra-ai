package v1

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// --- Spend Passes: the person's side (session only) ---

type createPassRequest struct {
	Label                    string   `json:"label"`
	AgentKind                string   `json:"agent_kind"`
	Currency                 string   `json:"currency"`
	BudgetMinorUnits         int64    `json:"budget_minor_units"`
	BudgetPeriod             string   `json:"budget_period"`
	MaxPerPurchaseMinorUnits *int64   `json:"max_per_purchase_minor_units"`
	ApproveAboveMinorUnits   *int64   `json:"approve_above_minor_units"`
	AllowedCategories        []string `json:"allowed_categories"`
	AllowedMerchants         []string `json:"allowed_merchants"`
	ExpiresInDays            int      `json:"expires_in_days"`
}

// Connect is how the new pass's agent reaches Algebra.
type passConnect struct {
	APIBase string `json:"api_base"`
	MCPURL  string `json:"mcp_url,omitempty"`
}

type issuedPassResponse struct {
	*app.IssuedPass
	Connect passConnect `json:"connect"`
}

func (a *API) listMyPasses(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	passes, err := a.b.SpendPasses.List(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passes": passes, "connect": a.passConnect()})
}

// createMyPass issues a pass and its agent token. The token appears in this
// response only — Algebra keeps just its hash.
func (a *API) createMyPass(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	var req createPassRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	days := req.ExpiresInDays
	if days <= 0 {
		days = 30
	}
	issued, err := a.b.SpendPasses.Create(r.Context(), sess.UserID, spendpass.Pass{
		Label: req.Label, AgentKind: spendpass.AgentKind(strings.ToLower(req.AgentKind)), Currency: req.Currency,
		BudgetMinorUnits: req.BudgetMinorUnits, BudgetPeriod: spendpass.Period(strings.ToLower(req.BudgetPeriod)),
		MaxPerPurchaseMinorUnits: req.MaxPerPurchaseMinorUnits, ApproveAboveMinorUnits: req.ApproveAboveMinorUnits,
		AllowedCategories: req.AllowedCategories, AllowedMerchants: req.AllowedMerchants,
		ExpiresAt: time.Now().Add(time.Duration(days) * 24 * time.Hour),
	})
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) || errors.Is(err, shared.ErrUnauthorized) {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, issuedPassResponse{IssuedPass: issued, Connect: a.passConnect()})
}

func (a *API) revokeMyPass(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	if err := a.b.SpendPasses.Revoke(r.Context(), sess.UserID, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) passConnect() passConnect {
	return passConnect{APIBase: a.b.AuthConfig.PublicWebURL + "/api/v1", MCPURL: a.b.MCPPublicURL}
}

// --- the agent's side ---

// getMyAgentPass lets an agent read its own Spend Pass: its limits and what
// budget is left — so it can plan within them instead of guessing.
func (a *API) getMyAgentPass(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	p, err := a.b.SpendPasses.ForAgent(r.Context(), ag.ID)
	if errors.Is(err, shared.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "this agent has no Spend Pass — it spends under the person's guardrails only"})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// --- receipts: public ---

// jwks publishes the key that verifies every Algebra spend receipt.
func (a *API) jwks(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, a.b.Receipts.JWKS())
}

// verifyReceipt answers "did this person really authorize this?" for anyone
// holding a receipt: a store, a payment company, a dispute team. A receipt
// carries no personal data, so this needs no sign-in.
func (a *API) verifyReceipt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Receipt string `json:"receipt"`
	}
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Receipt) == "" {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "send {\"receipt\": \"<the receipt text>\"}"})
		return
	}
	if len(req.Receipt) > 16<<10 {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "that's too long to be a receipt"})
		return
	}
	if receipt.Kind(req.Receipt) == receipt.IntentType && a.b.IntentReceipts != nil {
		writeJSON(w, http.StatusOK, a.b.IntentReceipts.Verify(r.Context(), req.Receipt))
		return
	}
	writeJSON(w, http.StatusOK, a.b.Receipts.Verify(r.Context(), req.Receipt))
}
