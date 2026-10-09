package v1

import (
	"net/http"

	"github.com/project-algebra/algebra/internal/domain/agent"
)

type createAgentRequest struct {
	UserID      string   `json:"user_id"`
	ClientID    string   `json:"client_id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type createAgentResponse struct {
	AgentID string `json:"agent_id"`
	Token   string `json:"token"` // shown exactly once
}

// createAgent mints a new AgentIdentity. A human/account-management action,
// not exposed to MCP — an agent cannot create another agent.
//
// Who may mint an agent for which user: a signed-in human for themselves
// (user_id may be omitted), or a tenant for one of its own end users.
// Anything else is rejected — an unauthenticated mint endpoint would let
// anyone create a spending agent for any user_id.
func (a *API) createAgent(w http.ResponseWriter, r *http.Request) {
	var req createAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	switch {
	case sessionFromContext(r.Context()) != nil:
		sessUser, _ := a.sessionUserID(r)
		if req.UserID != "" && req.UserID != sessUser {
			writeJSON(w, http.StatusForbidden, errorBody{Error: "you can only create agents for your own account"})
			return
		}
		req.UserID = sessUser
	case r.Header.Get("Authorization") != "":
		t, err := a.resolveTenant(r)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: err.Error()})
			return
		}
		u, err := a.b.Users.Get(r.Context(), req.UserID)
		if err != nil || u.TenantID != t.ID {
			writeJSON(w, http.StatusForbidden, errorBody{Error: "user does not belong to this tenant"})
			return
		}
	case a.b.AuthConfig.DevHeaderAuth:
		// ALGEBRA_DEV_AUTH: legacy local-development behavior.
	default:
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "sign in (or use a tenant token) to create an agent"})
		return
	}
	if req.ClientID == "" {
		req.ClientID = "external"
	}
	if req.Name == "" {
		req.Name = "Agent"
	}
	perms := make([]agent.Permission, len(req.Permissions))
	for i, p := range req.Permissions {
		perms[i] = agent.Permission(p)
	}
	token, identity, err := a.b.AgentSvc.CreateAgent(r.Context(), req.UserID, req.ClientID, req.Name, perms)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createAgentResponse{AgentID: identity.ID, Token: token})
}

func (a *API) revokeAgent(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := a.b.AgentSvc.Revoke(r.Context(), userID, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

type okResponse struct {
	OK bool `json:"ok"`
}
