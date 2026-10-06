package v1

import (
	"errors"
	"net/http"

	"github.com/project-algebra/algebra/providers/catalog"
)

// Classes of work (internal/domain/routing.Class): every catalog endpoint
// grouped by what it does, so an agent can ask for "token.price" and be routed
// across every provider of it, and a person can compare them.
//
//	GET /classes        every class, with how many providers do it
//	GET /classes/{id}   one class with its providers, listed prices and health
//
// Public like the provider listings: it is a read-only view of public catalogs.

func (a *API) listClasses(w http.ResponseWriter, r *http.Request) {
	if a.b.Classes == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "the provider catalogs are turned off on this server"})
		return
	}
	sums, built, err := a.b.Classes.Summaries(r.Context())
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	type classRow struct {
		ID               string `json:"id"`
		Title            string `json:"title"`
		Description      string `json:"description"`
		Kind             string `json:"kind"`
		Fields           any    `json:"fields"`
		Sample           any    `json:"sample"`
		Providers        int    `json:"providers"`
		Routable         int    `json:"routable"`
		MedianPriceMinor int64  `json:"median_price_minor"`
	}
	out := make([]classRow, 0, len(sums))
	for _, s := range sums {
		out = append(out, classRow{
			ID: s.ID, Title: s.Title, Description: s.Description, Kind: string(s.Kind), Fields: s.Fields, Sample: s.Sample,
			Providers: len(s.Members), Routable: s.Routable, MedianPriceMinor: s.MedianPriceMinor,
		})
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, map[string]any{"classes": out, "built_at": built})
}

func (a *API) getClass(w http.ResponseWriter, r *http.Request) {
	if a.b.Classes == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "the provider catalogs are turned off on this server"})
		return
	}
	s, err := a.b.Classes.Summary(r.Context(), clip(r.PathValue("id"), 64))
	if errors.Is(err, catalog.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "no such class; GET /api/v1/classes lists them"})
		return
	}
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	body := map[string]any{"class": s}
	if a.b.Health != nil {
		body["health"] = a.b.Health.ForClass(r.Context(), s.ID)
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, body)
}
