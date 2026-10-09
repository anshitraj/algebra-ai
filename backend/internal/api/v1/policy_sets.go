package v1

import (
	"errors"
	"net/http"

	"github.com/project-algebra/algebra/policy"
)

type setPolicySetRequest struct {
	UserID     string             `json:"user_id,omitempty"` // omitted = the tenant's own default, applies to every end user
	Conditions *conditionsPayload `json:"conditions"`
}

type policySetResponse struct {
	PolicySetID string       `json:"policy_set_id"`
	Version     int          `json:"version"`
	Rules       policy.Rules `json:"rules,omitempty"`
}

// setPolicySet is POST /api/v1/tenants/{id}/policy-sets — persists a new
// version of the tenant's policy, superseding whatever was active. Reuses
// conditionsPayload (internal/api/v1/policy_transactions.go) — the same
// card/wallet-shaped policy.Rules subset an Integrator sets inline, now
// persisted instead.
func (a *API) setPolicySet(w http.ResponseWriter, r *http.Request) {
	t, err := a.resolveTenant(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := requireOwnTenant(t, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	var req setPolicySetRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.Conditions == nil {
		writeError(w, errors.New(`missing "conditions" — send at least an empty {} to explicitly allow everything`))
		return
	}
	var userID *string
	if req.UserID != "" {
		userID = &req.UserID
	}
	ps, err := a.b.PolicySetSvc.SetPolicy(r.Context(), t.ID, userID, req.Conditions.toRules())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, policySetResponse{PolicySetID: ps.ID, Version: ps.Version})
}

// getLatestPolicySet is GET /api/v1/tenants/{id}/policy-sets/latest —
// returns the tenant's own default (not any per-user override).
func (a *API) getLatestPolicySet(w http.ResponseWriter, r *http.Request) {
	t, err := a.resolveTenant(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := requireOwnTenant(t, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	ps, err := a.b.PolicySetSvc.GetActive(r.Context(), t.ID, nil)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, policySetResponse{PolicySetID: ps.ID, Version: ps.Version, Rules: ps.Rules})
}
