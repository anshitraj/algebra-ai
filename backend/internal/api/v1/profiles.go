package v1

import (
	"net/http"
	"time"

	"github.com/project-algebra/algebra/internal/domain/privacy"
)

type createShippingProfileRequest struct {
	Alias   string                  `json:"alias"` // e.g. "shipping:home"
	Profile privacy.ShippingProfile `json:"profile"`
}

// createShippingProfile is a user action (never exposed via MCP — an agent
// only ever sees the alias, per PrivacyResolver's whole reason for
// existing). The plaintext address in the request body is encrypted before
// it touches storage and is never logged (mandate §25/§50).
func (a *API) createShippingProfile(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req createShippingProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := a.b.Privacy.StoreShipping(r.Context(), newProfileID(), userID, req.Alias, req.Profile, time.Now()); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, okResponse{OK: true})
}

type createBillingProfileRequest struct {
	Alias   string                 `json:"alias"` // e.g. "payment:personal"
	Profile privacy.BillingProfile `json:"profile"`
}

func (a *API) createBillingProfile(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req createBillingProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := a.b.Privacy.StoreBilling(r.Context(), newProfileID(), userID, req.Alias, req.Profile, time.Now()); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, okResponse{OK: true})
}

func (a *API) listShippingAliases(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	aliases, err := a.b.Privacy.ListAliases(r.Context(), userID, privacy.ProfileShipping)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, aliases)
}
