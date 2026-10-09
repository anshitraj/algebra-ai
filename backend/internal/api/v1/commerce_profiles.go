package v1

import (
	"net/http"
	"time"

	"github.com/project-algebra/algebra/internal/domain/commerceprofile"
)

type commerceProfileResponse struct {
	UserID               string                    `json:"user_id"`
	DefaultShippingAlias string                    `json:"default_shipping_alias,omitempty"`
	DefaultPaymentAlias  string                    `json:"default_payment_alias,omitempty"`
	Preferences          map[string]map[string]any `json:"preferences"`
	UpdatedAt            string                    `json:"updated_at,omitempty"`
}

func toCommerceProfileResponse(p *commerceprofile.CommerceProfile) commerceProfileResponse {
	resp := commerceProfileResponse{
		UserID: p.UserID, DefaultShippingAlias: p.DefaultShippingAlias,
		DefaultPaymentAlias: p.DefaultPaymentAlias, Preferences: p.Preferences,
	}
	if !p.UpdatedAt.IsZero() {
		resp.UpdatedAt = p.UpdatedAt.Format(time.RFC3339)
	}
	return resp
}

// getCommerceProfile is GET /api/v1/commerce-profile — agent-authenticated
// (unlike GET /api/v1/profiles/shipping, which is human-only), since the
// whole point of a CommerceProfile is that the agent itself reads it.
func (a *API) getCommerceProfile(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	p, err := a.b.CommerceProfileSvc.GetOrEmpty(r.Context(), ag.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toCommerceProfileResponse(p))
}

type setPreferencesRequest struct {
	Attributes map[string]any `json:"attributes"`
}

// setCommerceProfilePreferences is PUT /api/v1/commerce-profile/preferences/{category}
// — merges attributes into one category, does not replace the whole profile.
func (a *API) setCommerceProfilePreferences(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req setPreferencesRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	p, err := a.b.CommerceProfileSvc.SetPreferences(r.Context(), ag.ID, r.PathValue("category"), req.Attributes)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toCommerceProfileResponse(p))
}

type setDefaultsRequest struct {
	ShippingAlias *string `json:"shipping_alias"`
	PaymentAlias  *string `json:"payment_alias"`
}

// setCommerceProfileDefaults is PUT /api/v1/commerce-profile/defaults —
// sets which existing shipping/payment alias should be treated as the
// user's default. Never touches the aliased address/payment data itself.
func (a *API) setCommerceProfileDefaults(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req setDefaultsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	p, err := a.b.CommerceProfileSvc.SetDefaultAliases(r.Context(), ag.ID, req.ShippingAlias, req.PaymentAlias)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toCommerceProfileResponse(p))
}
