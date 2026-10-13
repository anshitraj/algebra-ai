package v1

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/project-algebra/algebra/internal/domain/intel"
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
	out := providerListing{Listing: l}
	if a.b.Intel != nil && len(l.Providers) > 0 {
		ids := make([]string, len(l.Providers))
		for i, p := range l.Providers {
			ids[i] = p.ID
		}
		// The registry's word is added when it can be read; a listing
		// without it still shows (and says nothing about trust).
		if t, err := a.b.Intel.ProviderTrust(r.Context(), ids); err == nil {
			out.Trust = t
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, out)
}

// providerListing is a catalog listing with what Algebra's own registry knows
// about each listed provider, by provider ID: how many of its endpoints were
// quoted, payment tested, identity verified or blocked. The counts are
// separate facts; a provider missing from Trust is one the registry has no
// record of. A listing itself vouches for nothing.
type providerListing struct {
	*catalog.Listing
	Trust map[string]intel.ProviderTrust `json:"trust"`
}

// providerDetail is one provider with the registry's facts about it, and per
// endpoint (by capability) the facts for each network it can be paid on.
type providerDetail struct {
	*catalog.Detail
	Trust         intel.ProviderTrust        `json:"trust"`
	EndpointTrust map[string][]endpointTrust `json:"endpoint_trust"`
}

// endpointTrust is what the registry knows about one endpoint on one network.
// Quoted, PaymentTested and Verified are separate: a payment that worked says
// nothing about who the seller is; only a person's review verifies.
type endpointTrust struct {
	Network       string `json:"network,omitempty"`
	Known         bool   `json:"known"`
	Trust         string `json:"trust,omitempty"`
	Quoted        bool   `json:"quoted"`
	PaymentTested bool   `json:"payment_tested"`
	Verified      bool   `json:"verified"`
	PayeePinned   bool   `json:"payee_pinned"`
	Executable    bool   `json:"executable"`
}

// trustOf reads a provider's endpoints from the registry, by the IDs the
// registry gives them (provider, URL template and network).
func (a *API) trustOf(r *http.Request, d *catalog.Detail) (intel.ProviderTrust, map[string][]endpointTrust) {
	type key struct{ capability, network string }
	ids := map[string]key{}
	var all []string
	for _, e := range d.Endpoints {
		nets := []string{e.Network}
		if len(e.Payments) > 0 {
			nets = nets[:0]
			for _, p := range e.Payments {
				nets = append(nets, p.Network)
			}
		}
		for _, n := range nets {
			id := intel.ID(d.ID, e.URL, n)
			if _, ok := ids[id]; !ok {
				ids[id] = key{e.Capability, n}
				all = append(all, id)
			}
		}
	}
	per := map[string][]endpointTrust{}
	sum := intel.ProviderTrust{}
	if a.b.Intel == nil {
		return sum, per
	}
	rows, err := a.b.Intel.EndpointsByID(r.Context(), all)
	if err != nil {
		return sum, per
	}
	for _, id := range all {
		k := ids[id]
		et := endpointTrust{Network: k.network}
		if e, ok := rows[id]; ok {
			lv := e.Levels()
			et = endpointTrust{Network: k.network, Known: true, Trust: string(e.Trust), Quoted: lv.Quoted, PaymentTested: lv.Tested,
				Verified: lv.Verified, PayeePinned: e.PinnedPayTo != "", Executable: lv.Executable}
			if e.Status != intel.StatusRemoved {
				sum.Endpoints++
				sum.Quoted += b2i(lv.Quoted)
				sum.Tested += b2i(lv.Tested)
				sum.Verified += b2i(lv.Verified)
				sum.Blocked += b2i(e.Trust == intel.TrustBlocked)
				sum.PayeePinned += b2i(e.PinnedPayTo != "")
			}
		}
		per[k.capability] = append(per[k.capability], et)
	}
	return sum, per
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
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
	out := providerDetail{Detail: d}
	out.Trust, out.EndpointTrust = a.trustOf(r, d)
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, out)
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
