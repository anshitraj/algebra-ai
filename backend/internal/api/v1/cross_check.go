package v1

import "net/http"

// crossCheck: POST /api/v1/cross-check {"intent_ids": ["eint_...", "eint_..."]}.
// Compares the answers the person already paid for, from different providers, to
// one question, and says whether they agree and where they don't. It moves no
// money. Between 2 and 5 intents; an answer no longer kept, two questions, or two
// answers from one provider are refused with the reason.
func (a *API) crossCheck(w http.ResponseWriter, r *http.Request) {
	if !a.executionEnabled(w) {
		return
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		IntentIDs []string `json:"intent_ids"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	rep, err := a.b.Execution.CrossCheck(r.Context(), ag.UserID, req.IntentIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	// Built from the providers' answers: not for a shared cache.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, rep)
}
