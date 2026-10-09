package v1

import (
	"net/http"

	"github.com/project-algebra/algebra/internal/app"
)

// listMerchants is the backend for the "Connected Merchants" console screen
// (mandate §44) — capability and readiness data come straight from each
// connector, never hand-typed, so the UI cannot imply support that doesn't
// exist.
func (a *API) listMerchants(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, app.DescribeMerchants(a.b.Connectors))
}
