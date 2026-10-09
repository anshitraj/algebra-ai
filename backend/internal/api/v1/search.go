package v1

import (
	"net/http"
	"strconv"
	"strings"
)

// searchProducts is commerce.search_products over REST: a free-text query
// fanned out to every searchable merchant, without creating an intent.
// Merchants Algebra can't search come back with a handoff_url instead.
func (a *API) searchProducts(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "q is required"})
		return
	}
	results, err := a.b.Discovery.SearchProducts(r.Context(), ag.ID, q, queryLimit(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// webSearch is commerce.web_search over REST: the last-resort general web
// search (links only — never a price, cart or order). 501 when no
// web-search fallback is configured.
func (a *API) webSearch(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "q is required"})
		return
	}
	var maxPrice int64
	if v := r.URL.Query().Get("max_price"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: "max_price must be a non-negative integer (minor units)"})
			return
		}
		maxPrice = n
	}
	results, err := a.b.Discovery.SearchWebWithin(r.Context(), ag.ID, q, queryLimit(r), maxPrice)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
