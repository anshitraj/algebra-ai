package jupiter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/platform/safehttp"
)

// A real answer from Jupiter's Swap API v2 to a quote-only order, kept as a
// fixture so the parsing is checked against what Jupiter really sends.
func TestOrderReadsARealQuoteOnlyAnswer(t *testing.T) {
	body, err := os.ReadFile("testdata/order-quote.json")
	if err != nil {
		t.Fatal(err)
	}
	var seen *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", safehttp.New(safehttp.Options{AllowLoopback: true}))
	o, err := c.Order(context.Background(), OrderRequest{InputMint: usdcMint(), OutputMint: "JUPyiwrYJFskUPiHa7hkeR8VUtAeFoSYbKedZNsDvCN", Amount: 2_500_000, SlippageBps: 50})
	if err != nil {
		t.Fatal(err)
	}
	if o.InAmount != "2500000" || o.OutAmount != "7642385" || o.OtherAmountThreshold != "7604173" || o.Router != "metis" || o.RequestID == "" ||
		o.Transaction != "" || o.PriceImpactBps != 0 || o.FeeBps != 0 || o.TransactionVersion != 0 || o.ErrorCode != nil {
		t.Errorf("%+v", o)
	}
	// Jupiter's own floor is the quote less the slippage, rounded down: within one
	// atom of ours, which is what the runner allows.
	out, _ := atoms(o.OutAmount)
	floor, _ := atoms(o.OtherAmountThreshold)
	if floor+1 < MinOut(out, 50) || floor > MinOut(out, 50) {
		t.Errorf("the real floor %d against ours %d", floor, MinOut(out, 50))
	}
	if seen.Header.Get("x-api-key") != "" {
		t.Error("no key configured, none sent")
	}
	if got := seen.URL.Query(); got.Get("taker") != "" || got.Get("slippageBps") != "50" || got.Get("amount") != "2500000" {
		t.Errorf("query: %v", got)
	}
}

func TestClientSendsTheKeyAndSurfacesErrorsBounded(t *testing.T) {
	long := strings.Repeat("x", 500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(long))
	}))
	defer srv.Close()
	h := safehttp.New(safehttp.Options{AllowLoopback: true})
	if _, err := NewClient(srv.URL, "", h).Order(context.Background(), OrderRequest{Amount: 1}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("without the key: %v", err)
	}
	_, err := NewClient(srv.URL, "k", h).Order(context.Background(), OrderRequest{Amount: 1})
	var he *HTTPError
	if !errorsAs(err, &he) || he.Status != 400 || len(he.Body) != 200 {
		t.Errorf("Jupiter's words are kept, bounded: %v", err)
	}
	if _, err := NewClient(srv.URL, "k", h).Execute(context.Background(), "tx", "id", 1); err == nil {
		t.Error("execute surfaces errors too")
	}
}

func errorsAs(err error, target **HTTPError) bool {
	for err != nil {
		if he, ok := err.(*HTTPError); ok {
			*target = he
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
