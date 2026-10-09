package v1

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/project-algebra/algebra/internal/app"
)

// findDeals is commerce.find_deals over REST: each merchant's own published
// deals for a product (Flipkart offers, Amazon savings and deals) plus the
// curated bank/card offers that apply. Informational only — no coupon code
// is ever returned, because neither merchant's API publishes one.
//
//	GET /api/v1/deals?q=wireless+mouse&merchant=amazon,flipkart&price=119500&banks=HDFC,SBI&limit=5
func (a *API) findDeals(w http.ResponseWriter, r *http.Request) {
	ag, err := a.resolveAgent(r)
	if err != nil {
		writeError(w, err)
		return
	}
	qs := r.URL.Query()
	var price int64
	if v := qs.Get("price"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: "price must be a non-negative integer (minor units)"})
			return
		}
		price = n
	}
	res, err := a.b.Discovery.FindDeals(r.Context(), ag.ID, app.DealQuery{
		Query:           strings.TrimSpace(qs.Get("q")),
		Merchants:       csvParam(qs.Get("merchant")),
		PriceMinorUnits: price,
		Banks:           csvParam(qs.Get("banks")),
		Limit:           queryLimit(r),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// csvParam splits a comma-separated query parameter, dropping blanks and
// capping the count so a caller can't make one request fan out forever.
func csvParam(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" && len(p) <= 60 {
			out = append(out, p)
		}
		if len(out) == 10 {
			break
		}
	}
	return out
}
