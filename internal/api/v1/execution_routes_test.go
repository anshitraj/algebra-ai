package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/platform/wiring"
)

// Go's ServeMux panics at registration when two patterns conflict, which
// would only otherwise show up when the server starts.
func TestRouterRegistersWithoutConflicts(t *testing.T) {
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("route registration panicked: %v", p)
		}
	}()
	_ = NewRouter(&wiring.Bundle{}, nil, nil)
}

func serve(t *testing.T, b *wiring.Bundle, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewRouter(b, nil, nil)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestExecutionEndpointsAreOffUntilConfigured(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/execute"},
		{"POST", "/api/v1/economic-intents/eint_1/execute"},
		{"GET", "/api/v1/economic-intents/eint_1/executions"},
	} {
		if rec := serve(t, &wiring.Bundle{}, tc.method, tc.path, `{}`); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s with no executor configured: %d, want 501", tc.method, tc.path, rec.Code)
		}
	}
}

func TestExecutionEndpointsNeedAnAgent(t *testing.T) {
	econ := app.NewEconomicService(nil, nil, nil, nil)
	b := &wiring.Bundle{Economic: econ, Execution: app.NewExecutionService(econ, nil)}
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/execute"},
		{"POST", "/api/v1/economic-intents/eint_1/execute"},
	} {
		if rec := serve(t, b, tc.method, tc.path, `{"capability":"solana.token-risk"}`); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without credentials: %d, want 401: %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
}
