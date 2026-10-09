package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// Provider health: the free probe's findings (internal/app/health_service.go).
//
//	GET  /classes/{id}/health   the latest probe of each provider of a class
//	POST /classes/{id}/probe    probe them all now (session only)
//
// A probe is an unpaid request: it never moves money.

const probeTimeout = 60 * time.Second

func (a *API) getClassHealth(w http.ResponseWriter, r *http.Request) {
	if a.b.Health == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "provider health isn't running on this server"})
		return
	}
	id := clip(r.PathValue("id"), 64)
	if _, ok := routing.ClassByID(id); !ok {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "no such class; GET /api/v1/classes lists them"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=15")
	writeJSON(w, http.StatusOK, map[string]any{"class": id, "health": a.b.Health.ForClass(r.Context(), id)})
}

func (a *API) probeClass(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireSession(w, r); !ok {
		return
	}
	if a.b.Health == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "provider health isn't running on this server"})
		return
	}
	id := clip(r.PathValue("id"), 64)
	if _, ok := routing.ClassByID(id); !ok {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "no such class; GET /api/v1/classes lists them"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	recs, err := a.b.Health.ProbeClass(ctx, id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	up := 0
	for _, h := range recs {
		if h.Status == "up" {
			up++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"class": id, "probed": len(recs), "up": up, "health": recs})
}
