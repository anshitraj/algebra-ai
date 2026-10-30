package v1

import (
	"net/http"

	"github.com/project-algebra/algebra/internal/app"
)

func (a *API) toolKeys(w http.ResponseWriter) bool {
	if a.b.ToolKeys == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "web tool keys are not enabled on this server"})
		return false
	}
	return true
}

// listToolKeys: GET /api/v1/me/tool-keys. The web tools a person can use with
// their own API key, and which keys are saved. A key is never returned: only
// its last four characters.
func (a *API) listToolKeys(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok || !a.toolKeys(w) {
		return
	}
	out, err := a.b.ToolKeys.List(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Tools []app.ToolKeyView `json:"tools"`
	}{out})
}

// saveToolKey: PUT /api/v1/me/tool-keys/{vendor} with {"key": "..."}. The key
// is sealed at rest, and goes only to that vendor's own address when the
// person's agents use it.
func (a *API) saveToolKey(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok || !a.toolKeys(w) {
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	if err := a.b.ToolKeys.Save(r.Context(), sess.UserID, r.PathValue("vendor"), req.Key); err != nil {
		writeError(w, err)
		return
	}
	a.listToolKeys(w, r)
}

// deleteToolKey: DELETE /api/v1/me/tool-keys/{vendor}.
func (a *API) deleteToolKey(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok || !a.toolKeys(w) {
		return
	}
	if err := a.b.ToolKeys.Remove(r.Context(), sess.UserID, r.PathValue("vendor")); err != nil {
		writeError(w, err)
		return
	}
	a.listToolKeys(w, r)
}
