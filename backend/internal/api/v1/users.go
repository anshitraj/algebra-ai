package v1

import "net/http"

type createUserRequest struct {
	Email string `json:"email"`
}

type createUserResponse struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// createUser provisions an end user for a tenant integration (tenant bearer
// token required). People signing up for Algebra's own web app use
// POST /api/v1/auth/signup (or Google/GitHub) instead. With
// ALGEBRA_DEV_AUTH=true, an unauthenticated call still creates an unscoped
// user, for local scripts written before accounts existed.
func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	var tenantID string
	if t, err := a.resolveTenant(r); err == nil {
		tenantID = t.ID
	} else if !a.b.AuthConfig.DevHeaderAuth {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "a tenant token is required — people sign up via POST /api/v1/auth/signup"})
		return
	}
	user, err := a.b.Users.Create(r.Context(), req.Email, tenantID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createUserResponse{UserID: user.ID, Email: user.Email})
}
