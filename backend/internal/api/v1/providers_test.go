package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/internal/platform/wiring"
	"github.com/project-algebra/algebra/providers/catalog"
	"github.com/project-algebra/algebra/providers/paysh"
)

// fakePaySh serves a tiny Pay.sh: a catalog and one provider page.
func fakePaySh(t *testing.T) *catalog.Multi {
	t.Helper()
	catalogDoc, err := os.ReadFile("../../../providers/paysh/testdata/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile("../../../providers/paysh/testdata/birdeye-data.md")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/catalog":
			_, _ = w.Write(catalogDoc)
		case "/api/birdeye/data/index.md":
			_, _ = w.Write(page)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return catalog.NewMulti(paysh.New(paysh.Config{
		CatalogURL: srv.URL + "/api/catalog", DocsURL: srv.URL + "/api",
		HTTP: safehttp.New(safehttp.Options{AllowLoopback: true}),
	}))
}

func TestProvidersAreOffWhenTheCatalogIsOff(t *testing.T) {
	for _, path := range []string{"/api/v1/providers", "/api/v1/providers/birdeye/data"} {
		if rec := serve(t, &wiring.Bundle{}, "GET", path, ""); rec.Code != http.StatusNotImplemented {
			t.Errorf("GET %s with no catalog: %d, want 501", path, rec.Code)
		}
	}
}

func TestListProviders(t *testing.T) {
	b := &wiring.Bundle{Directory: fakePaySh(t)}

	rec := serve(t, b, "GET", "/api/v1/providers", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Cache-Control"), "max-age") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	var l catalog.Listing
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	if len(l.Sources) != 1 || l.Sources[0].Name != "pay.sh" || l.Total != 9 || len(l.Providers) != 9 || len(l.Categories) == 0 {
		t.Errorf("listing: %+v", l)
	}

	rec = serve(t, b, "GET", "/api/v1/providers?q=birdeye&limit=5", "")
	l = catalog.Listing{}
	_ = json.Unmarshal(rec.Body.Bytes(), &l)
	if rec.Code != http.StatusOK || l.Total != 1 || l.Providers[0].ID != "paysh:birdeye.data" {
		t.Errorf("a query: %d %+v", rec.Code, l)
	}

	// An absurd query is clipped, not an error.
	if rec := serve(t, b, "GET", "/api/v1/providers?q="+strings.Repeat("x", 5000), ""); rec.Code != http.StatusOK {
		t.Errorf("long query: %d", rec.Code)
	}
}

func TestGetProvider(t *testing.T) {
	b := &wiring.Bundle{Directory: fakePaySh(t)}

	// By FQN, with a slash in the path; and by provider ID.
	for _, id := range []string{"birdeye/data", "paysh:birdeye.data"} {
		rec := serve(t, b, "GET", "/api/v1/providers/"+id, "")
		var d catalog.Detail
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %v %s", id, rec.Code, err, rec.Body)
		}
		if d.ID != "paysh:birdeye.data" || len(d.Endpoints) != 11 || d.Endpoints[0].Capability != "birdeye.data.get.x402-defi-historical_price_unix" || !d.Endpoints[0].Callable {
			t.Errorf("%s: %+v", id, d)
		}
	}

	if rec := serve(t, b, "GET", "/api/v1/providers/nobody/here", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown provider: %d, want 404", rec.Code)
	}
}

func TestProvidersWhenPaySHCantBeReached(t *testing.T) {
	// A catalog address nobody listens on, through a client that refuses loopback anyway.
	dead := catalog.NewMulti(paysh.New(paysh.Config{CatalogURL: "https://127.0.0.1:1/catalog", HTTP: safehttp.New(safehttp.Options{})}))
	rec := serve(t, &wiring.Bundle{Directory: dead}, "GET", "/api/v1/providers", "")
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "127.0.0.1") {
		t.Errorf("an unreachable catalog is a 502 that doesn't leak where it looked: %d %s", rec.Code, rec.Body)
	}
}
