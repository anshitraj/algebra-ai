package v1

import (
	"net/http"
	"strconv"

	"github.com/project-algebra/algebra/internal/app"
)

// Organizations, projects and API keys. Every route here is a person's
// decision: it needs a signed-in session, and an agent token or API key sent
// in Authorization is refused, so no key can mint or revoke keys.

func (a *API) tenancyEnabled(w http.ResponseWriter) bool {
	if a.b.Tenancy == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "projects are not enabled on this server"})
		return false
	}
	return true
}

// humanSession is requireSession that also refuses a bearer credential.
func (a *API) humanSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Header.Get("Authorization") != "" {
		writeJSON(w, http.StatusForbidden, errorBody{Error: "managing organizations, projects and keys is a person's decision; sign in to the console"})
		return "", false
	}
	sess, ok := a.requireSession(w, r)
	if !ok || !a.tenancyEnabled(w) {
		return "", false
	}
	return sess.UserID, true
}

// listOrgs: GET /api/v1/orgs. A person with no organization gets one (and a
// first project), so a new account can issue a key at once.
func (a *API) listOrgs(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	name := ""
	if a.b.Accounts != nil {
		if u, err := a.b.Accounts.User(r.Context(), userID); err == nil {
			name = u.Name
		}
	}
	orgs, err := a.b.Tenancy.Workspace(r.Context(), userID, name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"organizations": orgs})
}

func (a *API) createOrg(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	org, err := a.b.Tenancy.CreateOrganization(r.Context(), userID, req.Name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

func (a *API) listProjects(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	projects, err := a.b.Tenancy.Projects(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (a *API) createProject(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	p, err := a.b.Tenancy.CreateProject(r.Context(), userID, r.PathValue("id"), req.Name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (a *API) updateProject(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	var req struct {
		Name            *string `json:"name"`
		DefaultStrategy *string `json:"default_strategy"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	p, err := a.b.Tenancy.UpdateProject(r.Context(), userID, r.PathValue("id"), req.Name, req.DefaultStrategy)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) listKeys(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	keys, err := a.b.Tenancy.Keys(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys, "connect": a.passConnect()})
}

// createKey: POST /api/v1/projects/{id}/keys. The secret is in this response
// only; Algebra keeps its hash.
func (a *API) createKey(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	var in app.KeyInput
	if err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	issued, err := a.b.Tenancy.CreateKey(r.Context(), userID, r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"key": issued, "connect": a.passConnect()})
}

func (a *API) rotateKey(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	issued, err := a.b.Tenancy.RotateKey(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"key": issued, "connect": a.passConnect()})
}

func (a *API) revokeKey(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	if err := a.b.Tenancy.RevokeKey(r.Context(), userID, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) projectEvents(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := a.b.Tenancy.Events(r.Context(), userID, r.PathValue("id"), limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// projectPasses: GET /api/v1/projects/{id}/passes, a project's Spend Passes
// (optionally one environment).
func (a *API) projectPasses(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.humanSession(w, r)
	if !ok {
		return
	}
	if _, _, err := a.b.Tenancy.ProjectFor(r.Context(), userID, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	all, err := a.b.SpendPasses.List(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	env := r.URL.Query().Get("environment")
	out := []app.PassView{}
	for _, p := range all {
		if p.ProjectID == r.PathValue("id") && (env == "" || p.Environment == env) {
			out = append(out, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"passes": out})
}
