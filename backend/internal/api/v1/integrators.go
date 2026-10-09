package v1

import (
	"fmt"
	"net/http"

	"github.com/project-algebra/algebra/internal/domain/shared"
)

type createIntegratorRequest struct {
	Name string `json:"name"`
}

type createIntegratorResponse struct {
	IntegratorID string `json:"integrator_id"`
	Token        string `json:"token"` // shown exactly once
}

// createIntegrator mints a new Integrator bearer token — the B2B
// counterpart to createAgent, for a third-party app (a wallet, a checkout
// provider) that wants to call POST /policy/evaluate-transaction. Only the
// operator can register one (requireOperator); open in development.
func (a *API) createIntegrator(w http.ResponseWriter, r *http.Request) {
	if err := a.requireOperator(r); err != nil {
		writeError(w, err)
		return
	}
	var req createIntegratorRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	token, integ, err := a.b.IntegratorSvc.CreateIntegrator(r.Context(), req.Name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createIntegratorResponse{IntegratorID: integ.ID, Token: token})
}

// revokeIntegrator is callable by the integrator itself (its own token, for
// its own ID) or by the operator — never by anyone who merely knows the ID.
func (a *API) revokeIntegrator(w http.ResponseWriter, r *http.Request) {
	if !a.isOperator(r) {
		integ, err := a.resolveIntegrator(r)
		if err != nil {
			writeError(w, err)
			return
		}
		if integ.ID != r.PathValue("id") {
			writeError(w, fmt.Errorf("%w: token does not match integrator %s", shared.ErrUnauthorized, r.PathValue("id")))
			return
		}
	}
	if err := a.b.IntegratorSvc.Revoke(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}
