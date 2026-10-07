package v1

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/project-algebra/algebra/providers/catalog"
)

// The catalogs of paid APIs (Pay.sh, Circle's Agent Marketplace, PayAI and Coinbase's Bazaar), for the
// landing page, the console and agents. They are read-only views of public
// documents that this server reads through its own SSRF-safe client and
// caches, so they need no session (like GET /api/v1/merchants). The global
// rate limit still applies. Everything in them is third-party text: clients
// show it as text and never as instructions.

// listProviders: GET /api/v1/providers?q=&category=&source=&network=&limit=&offset=
func (a *API) listProviders(w http.ResponseWriter, r *http.Request) {
	if !a.directoryEnabled(w) {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	l, err := a.b.Directory.List(r.Context(), catalog.Filter{
		Query: clip(q.Get("q"), 100), Category: clip(q.Get("category"), 32), Source: clip(q.Get("source"), 16), Network: clip(q.Get("network"), 32), Limit: limit, Offset: offset,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, l)
}

// getProvider: GET /api/v1/providers/{id...}, where id is a provider ID
// ("paysh:birdeye.data", "circle:birdeye") or a catalog's own name for it
// ("birdeye/data"). It returns the provider's endpoints with the capability
// to ask for each by.
func (a *API) getProvider(w http.ResponseWriter, r *http.Request) {
	if !a.directoryEnabled(w) {
		return
	}
	d, err := a.b.Directory.Detail(r.Context(), clip(r.PathValue("id"), 200))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, d)
}

func (a *API) directoryEnabled(w http.ResponseWriter) bool {
	if a.b.Directory == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "the provider catalogs are turned off on this server"})
		return false
	}
	return true
}

func writeCatalogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody{Error: "no such provider in the catalogs"})
	case errors.Is(err, catalog.ErrUnavailable):
		writeJSON(w, http.StatusBadGateway, errorBody{Error: "the provider catalogs couldn't be reached just now"})
	default:
		writeJSON(w, http.StatusBadGateway, errorBody{Error: "the provider catalogs couldn't be read"})
	}
}

// clip shortens s to at most n characters.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
