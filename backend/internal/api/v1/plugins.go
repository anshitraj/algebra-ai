package v1

import (
	"net/http"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/plugin"
)

// listMyPlugins is GET /api/v1/me/plugins: the plugin catalog with the
// signed-in person's switches and settings. Session only.
func (a *API) listMyPlugins(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	list, err := a.b.Plugins.List(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": list})
}

type setPluginRequest struct {
	Enabled bool           `json:"enabled"`
	Config  *plugin.Config `json:"config,omitempty"`
}

// setMyPlugin is PUT /api/v1/me/plugins/{id}: switch a plugin on or off,
// and set the Reddit plugin's subreddits. Session only — an agent can't
// change which sources it reads.
func (a *API) setMyPlugin(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	var req setPluginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	st, err := a.b.Plugins.Set(r.Context(), sess.UserID, r.PathValue("id"), req.Enabled, req.Config)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// communityDeals is GET /api/v1/community-deals?q=whey+protein — recent
// posts about a product from the community plugins the agent's person has
// on (Reddit, DesiDime). Unverified tips, never prices.
func (a *API) communityDeals(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	res, err := a.b.Discovery.CommunityTips(r.Context(), ag.ID, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
