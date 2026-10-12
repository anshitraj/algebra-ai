package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/tenancy"
)

// Book a Demo, plans and the trial.
//
//	POST /api/v1/leads               an enquiry from the public form; stored, never forwarded
//	GET  /api/v1/leads               the enquiries, newest first (operator token)
//	GET  /api/v1/plans               the plan catalog, and whether plans can be bought here
//	POST /api/v1/orgs/{id}/trial     start the organization's seven-day Startup trial
//	GET  /api/v1/projects/{id}/usage a project's metered use this month

// leadLimit bounds enquiries from one address: the form is public.
const (
	leadLimit       = 5
	leadLimitWindow = time.Hour
)

func (a *API) createLead(w http.ResponseWriter, r *http.Request) {
	if a.b.Leads == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "demo requests are not enabled on this server"})
		return
	}
	if a.limiter != nil {
		if ok, retry, err := a.limiter.Allow(r.Context(), "quota:leads:"+clientIP(r), leadLimit, leadLimitWindow); err == nil && !ok {
			w.Header().Set("Retry-After", formatSeconds(retry))
			writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "too many requests from this address; try again later or email us"})
			return
		}
	}
	var req struct {
		BusinessName   string `json:"business_name"`
		WorkEmail      string `json:"work_email"`
		Website        string `json:"website"`
		UseCase        string `json:"use_case"`
		EstimatedUsage string `json:"estimated_usage"`
		Message        string `json:"message"`
		// Company is a field people never see; a filled one is a bot. It is
		// answered like a success and stored nowhere.
		Company string `json:"company"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	if req.Company != "" {
		writeJSON(w, http.StatusCreated, map[string]any{"received": true})
		return
	}
	in := app.Lead{BusinessName: req.BusinessName, WorkEmail: req.WorkEmail, Website: req.Website, UseCase: req.UseCase,
		EstimatedUsage: req.EstimatedUsage, Message: req.Message, SourceIP: clientIP(r)}
	if sess := sessionFromContext(r.Context()); sess != nil {
		in.UserID = sess.UserID
	}
	lead, err := a.b.Leads.Submit(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"received": true, "id": lead.ID})
}

// listLeads is the operator's. There is no open development fallback, so
// people's enquiries are never readable without the token.
func (a *API) listLeads(w http.ResponseWriter, r *http.Request) {
	if a.b.Leads == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "demo requests are not enabled on this server"})
		return
	}
	if !a.isOperator(r) {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "operator token required (" + operatorHeader + ")"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	leads, err := a.b.Leads.List(r.Context(), limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"leads": leads})
}

// listPlans is public: it is what the pricing page shows. billing_live is
// false until a payment processor for these plans is configured and tested,
// and the page must not offer to sell a plan while it is.
func (a *API) listPlans(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"plans":        tenancy.Plans,
		"trial_plan":   tenancy.TrialPlan,
		"trial_days":   int(tenancy.TrialLength / (24 * time.Hour)),
		"billing_live": false,
	})
}

func (a *API) startTrial(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	org, err := a.b.Tenancy.StartTrial(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, org)
}

func (a *API) projectUsage(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	usage, err := a.b.Tenancy.Usage(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, usage)
}
