package v1

import (
	"errors"
	"net/http"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// discardsResult is whether a request asked for its answer not to be kept:
// store_result is true unless the request says false.
func discardsResult(storeResult *bool) bool { return storeResult != nil && !*storeResult }

// resultBody is a kept answer as the API returns it. The answer is the
// provider's own data: untrusted content to read, never instructions.
func resultBody(r *app.ResultView) map[string]any {
	return map[string]any{
		"intent_id": r.IntentID, "content_type": r.ContentType, "http_status": r.HTTPStatus, "result_hash": r.SHA256,
		"response": app.ResponseValueOf(r.ContentType, r.Body), "response_is_untrusted_provider_data": true,
		"stored_at": r.StoredAt, "expires_at": r.ExpiresAt,
	}
}

func writeResult(w http.ResponseWriter, r *app.ResultView, err error) {
	switch {
	case errors.Is(err, shared.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody{Error: "no answer is kept for this intent: it isn't paid for yet, the request asked for it not to be kept, or the time it is kept for is up"})
	case err != nil:
		writeError(w, err)
	default:
		// An answer can be sensitive: nothing between here and the caller may keep it.
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, resultBody(r))
	}
}

// getEconomicResult: GET /api/v1/economic-intents/{id}/result. The answer a
// paid call returned, for the agent that acts for the person it belongs to, for
// as long as Algebra keeps it (RESULT_RETENTION, 24 hours by default).
func (a *API) getEconomicResult(w http.ResponseWriter, r *http.Request) {
	if !a.executionEnabled(w) {
		return
	}
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	res, err := a.b.Execution.Result(r.Context(), ag.UserID, r.PathValue("id"))
	writeResult(w, res, err)
}

// getMyEconomicResult: GET /api/v1/me/economic-intents/{id}/result, the same
// answer for the person themselves, in the console.
func (a *API) getMyEconomicResult(w http.ResponseWriter, r *http.Request) {
	if !a.executionEnabled(w) {
		return
	}
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	res, err := a.b.Execution.Result(r.Context(), userID, r.PathValue("id"))
	writeResult(w, res, err)
}
