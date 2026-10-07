package jupiter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// fakeJupiter serves /order and /execute, scripted per test.
type fakeJupiter struct {
	mu      sync.Mutex
	srv     *httptest.Server
	orders  []map[string]string // the query of each /order
	keys    []string            // the x-api-key of each request
	execs   []map[string]any    // the body of each /execute
	order   func(q map[string]string) (int, any)
	execute func(body map[string]any) (int, any)
}

func newFakeJupiter(t *testing.T, wallet solana.PublicKey) *fakeJupiter {
	f := &fakeJupiter{}
	// Out is 0.4 of a token per USDC atom for small swaps, a little worse for large ones.
	f.order = func(q map[string]string) (int, any) {
		var amount uint64
		fmt.Sscan(q["amount"], &amount)
		out := amount * 4 / 10
		if amount > 3_000_000 {
			out = amount * 36 / 100 // 10% worse rate: price impact
		}
		impact := -0.04 // percent, as Jupiter reports it: negative when the swap loses value
		if amount > 3_000_000 {
			impact = -10
		}
		var slippage uint64
		fmt.Sscan(q["slippageBps"], &slippage)
		o := map[string]any{
			"requestId": "req-123", "inAmount": q["amount"], "outAmount": fmt.Sprint(out), "router": "metis", "transactionVersion": 0, "lastValidBlockHeight": 1_150,
			"priceImpact": impact, "otherAmountThreshold": fmt.Sprint(out - out*slippage/10_000),
		}
		if q["taker"] != "" {
			o["transaction"] = swapTx(t, wallet)
		} else {
			o["transaction"] = nil
		}
		return 200, o
	}
	f.execute = func(body map[string]any) (int, any) {
		return 200, map[string]any{"status": "Success", "signature": "5sigsigsig", "code": 0, "totalInputAmount": "2500000", "inputAmountResult": "2497500", "outputAmountResult": "1000000", "totalOutputAmount": "999000"}
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.keys = append(f.keys, r.Header.Get("x-api-key"))
		var status int
		var body any
		switch r.URL.Path {
		case "/order":
			q := map[string]string{}
			for k := range r.URL.Query() {
				q[k] = r.URL.Query().Get(k)
			}
			f.orders = append(f.orders, q)
			status, body = f.order(q)
		case "/execute":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.execs = append(f.execs, in)
			status, body = f.execute(in)
		default:
			status, body = 404, map[string]any{"error": "no such route"}
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJupiter) client(key string) *Client {
	return NewClient(f.srv.URL, key, safehttp.New(safehttp.Options{AllowLoopback: true}))
}

func testRunner(t *testing.T) (*Runner, *fakeJupiter, solana.PublicKey) {
	t.Helper()
	wallet, _ := solana.NewKeypair()
	f := newFakeJupiter(t, wallet.PublicKey())
	r := NewRunner(RunnerConfig{Client: f.client("key-1"), Wallet: wallet.PublicKey(), Now: func() time.Time { return time.Unix(1_800_000_000, 0) }})
	return r, f, wallet.PublicKey()
}

func input(amount string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"output_mint":%q,"amount_usdc":%q}`, boughtMint.String(), amount))
}

func TestParseInput(t *testing.T) {
	good, err := ParseInput(input("2.50"), 0)
	if err != nil || good.OutputMint != boughtMint.String() || good.AmountMinor != 2_500_000 || good.SlippageBps != DefaultSlippageBps {
		t.Fatalf("%+v %v", good, err)
	}
	if s, err := ParseInput(json.RawMessage(fmt.Sprintf(`{"output_mint":%q,"amount_usdc":1,"max_slippage_bps":30}`, boughtMint)), 0); err != nil || s.AmountMinor != 1_000_000 || s.SlippageBps != 30 {
		t.Errorf("a number and a tolerance: %+v %v", s, err)
	}
	for name, tc := range map[string]struct{ in, want string }{
		"nothing":               {``, "output_mint and amount_usdc"},
		"not an object":         {`[1]`, "must be an object"},
		"a typo":                {fmt.Sprintf(`{"output_mint":%q,"amount":"2"}`, boughtMint), "unknown field"},
		"no mint":               {`{"amount_usdc":"2"}`, "must be a token's mint address"},
		"a mint that isn't":     {`{"output_mint":"hello","amount_usdc":"2"}`, "must be a token's mint address"},
		"USDC":                  {fmt.Sprintf(`{"output_mint":%q,"amount_usdc":"2"}`, usdcMint()), "is USDC"},
		"SOL":                   {fmt.Sprintf(`{"output_mint":%q,"amount_usdc":"2"}`, wrappedSOL), "into SOL isn't supported"},
		"no amount":             {fmt.Sprintf(`{"output_mint":%q}`, boughtMint), "positive amount of USDC"},
		"a negative amount":     {fmt.Sprintf(`{"output_mint":%q,"amount_usdc":"-1"}`, boughtMint), "positive amount of USDC"},
		"too many decimals":     {fmt.Sprintf(`{"output_mint":%q,"amount_usdc":"1.0000001"}`, boughtMint), "positive amount of USDC"},
		"scientific notation":   {fmt.Sprintf(`{"output_mint":%q,"amount_usdc":1e-3}`, boughtMint), "positive amount of USDC"},
		"slippage of nothing":   {fmt.Sprintf(`{"output_mint":%q,"amount_usdc":"2","max_slippage_bps":0}`, boughtMint), "between 1 and 100"},
		"slippage over the cap": {fmt.Sprintf(`{"output_mint":%q,"amount_usdc":"2","max_slippage_bps":101}`, boughtMint), "between 1 and 100"},
	} {
		if _, err := ParseInput(json.RawMessage(tc.in), 0); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want %q, got %v", name, tc.want, err)
		}
	}
}

func TestMinOutRoundsInTheWalletsFavour(t *testing.T) {
	for _, tc := range []struct {
		out  uint64
		bps  int
		want uint64
	}{
		{1_000_000, 50, 995_000},
		{999, 50, 995}, // 999*0.005 = 4.995: slippage rounds down to 4, so the minimum is 995
		{1, 50, 1},     // never a minimum of zero from a tiny quote
		{1_000_000, 0, 1_000_000},
		{1_000_000, 10_000, 0},
		{1 << 63, 100, (1 << 63) - (1<<63)/100},
		{^uint64(0), 300, ^uint64(0) - ^uint64(0)/10_000*300 - (^uint64(0)%10_000)*300/10_000},
	} {
		if got := MinOut(tc.out, tc.bps); got != tc.want {
			t.Errorf("MinOut(%d, %d) = %d, want %d", tc.out, tc.bps, got, tc.want)
		}
	}
}

func TestQuotePricesTheSwapAndEstimatesItsImpact(t *testing.T) {
	r, f, _ := testRunner(t)
	cand, err := Candidate()
	if err != nil {
		t.Fatal(err)
	}
	q, err := r.Quote(context.Background(), cand, input("2"))
	if err != nil {
		t.Fatal(err)
	}
	if q.Cost.Total() != 2_000_000 || q.Asset != "USDC" || q.Network != "solana" || q.ExpectedOutput != "800000" || q.SlippageBps != 50 || q.PriceImpactBps != 4 {
		t.Errorf("Jupiter's 0.04%% is 4 basis points: %+v", q)
	}
	if rail, err := r.Rail(q); err != nil || rail != RailName {
		t.Errorf("rail: %q %v", rail, err)
	}
	if !q.ValidUntil.Equal(time.Unix(1_800_000_000, 0).Add(20 * time.Second)) {
		t.Errorf("a quote is good for 20 seconds: %v", q.ValidUntil)
	}
	if len(f.orders) != 1 || f.orders[0]["taker"] != "" || f.orders[0]["inputMint"] != usdcMint() || f.orders[0]["outputMint"] != boughtMint.String() || f.orders[0]["slippageBps"] != "50" || f.orders[0]["maxSupportedTransactionVersion"] != "0" {
		t.Errorf("one quote-only order, version 0: %v", f.orders)
	}
	if f.keys[0] != "key-1" {
		t.Errorf("the API key is sent: %v", f.keys)
	}

	// A large swap in a thin pool: the fake reports a ten percent loss above $3.
	f.orders = nil
	q, err = r.Quote(context.Background(), cand, input("4"))
	if err != nil || q.PriceImpactBps != 1_000 || q.ExpectedOutput != "1440000" {
		t.Errorf("a ten percent loss is 1000 bps: %+v %v", q, err)
	}
	if len(f.orders) != 1 {
		t.Errorf("one request prices it: %v", f.orders)
	}
	// A swap that gains value (Jupiter reports it as positive) has no impact.
	f.order = func(map[string]string) (int, any) {
		return 200, map[string]any{"requestId": "r", "inAmount": "2000000", "outAmount": "800000", "priceImpact": 0.5}
	}
	if q, err := r.Quote(context.Background(), cand, input("2")); err != nil || q.PriceImpactBps != 0 {
		t.Errorf("a gain is no impact: %+v %v", q, err)
	}
	// And one that doesn't say has none recorded.
	f.order = func(map[string]string) (int, any) {
		return 200, map[string]any{"requestId": "r", "inAmount": "2000000", "outAmount": "800000"}
	}
	if q, err := r.Quote(context.Background(), cand, input("2")); err != nil || q.PriceImpactBps != 0 {
		t.Errorf("no figure: %+v %v", q, err)
	}
}

func TestQuoteSaysWhyItCant(t *testing.T) {
	r, f, _ := testRunner(t)
	cand, _ := Candidate()
	ask := func(amount string) error { _, err := r.Quote(context.Background(), cand, input(amount)); return err }

	if err := ask("5.01"); err == nil || !strings.Contains(err.Error(), "more than the most one swap may spend, 5") {
		t.Errorf("the runner's own ceiling: %v", err)
	}
	if _, err := r.Quote(context.Background(), cand, json.RawMessage(`{}`)); err == nil {
		t.Error("bad input")
	}
	for status, want := range map[int]string{401: "rejected the API key", 403: "rejected the API key", 429: "rate limit", 404: "no route", 422: "no route", 500: "answered HTTP 500"} {
		f.order = func(map[string]string) (int, any) { return status, map[string]any{"error": "x"} }
		if err := ask("2"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("HTTP %d: want %q, got %v", status, want, err)
		}
	}
	f.order = func(map[string]string) (int, any) {
		return 200, map[string]any{"requestId": "r", "inAmount": "2000000", "outAmount": "0", "errorMessage": "Could not find a route"}
	}
	if err := ask("2"); err == nil || !strings.Contains(err.Error(), "Could not find a route") {
		t.Errorf("a route-less answer: %v", err)
	}
	f.order = func(map[string]string) (int, any) { return 200, map[string]any{"outAmount": "lots"} }
	if err := ask("2"); err == nil || !strings.Contains(err.Error(), "no route") {
		t.Errorf("a nonsense answer: %v", err)
	}
}

// run drives Run with a Pay that records what it was asked and signs nothing.
func run(t *testing.T, r *Runner, q routing.Quote, in json.RawMessage, pay func(app.PaymentRequest) (*app.PaymentAuthority, error)) (app.StepObservation, []app.PaymentRequest) {
	t.Helper()
	var asked []app.PaymentRequest
	obs := r.Run(context.Background(), app.StepCall{Quote: q, Input: in, Pay: func(_ context.Context, pr app.PaymentRequest) (*app.PaymentAuthority, error) {
		asked = append(asked, pr)
		return pay(pr)
	}})
	return obs, asked
}

func okPay(pr app.PaymentRequest) (*app.PaymentAuthority, error) {
	return &app.PaymentAuthority{Value: "c2lnbmVk", AmountMinor: 2_500_000}, nil
}

func quoteFor(t *testing.T, r *Runner, amount string) routing.Quote {
	t.Helper()
	cand, _ := Candidate()
	q, err := r.Quote(context.Background(), cand, input(amount))
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestRunBuildsAsksTheRailToSignAndHasJupiterLandIt(t *testing.T) {
	r, f, wallet := testRunner(t)
	q := quoteFor(t, r, "2.5")
	obs, asked := run(t, r, q, input("2.5"), okPay)

	if !obs.Delivered || !obs.AuthorityReleased || obs.Class != "" || obs.SettleTx != "5sigsigsig" || obs.HTTPStatus != 200 || obs.RequestHash == "" || obs.ContentType != "application/json" {
		t.Fatalf("observation: %+v", obs)
	}
	var body map[string]any
	if err := json.Unmarshal(obs.Body, &body); err != nil || body["signature"] != "5sigsigsig" || body["output_mint"] != boughtMint.String() || body["status"] != "success" || body["output_amount"] != "999000" {
		t.Errorf("the answer says what was spent and received: %s", obs.Body)
	}

	// The order was built for the wallet.
	last := f.orders[len(f.orders)-1]
	if last["taker"] != wallet.String() || last["amount"] != "2500000" {
		t.Errorf("order: %v", last)
	}
	// The rail was given the terms and the unsigned transaction, once.
	if len(asked) != 1 {
		t.Fatalf("Pay is asked once: %d", len(asked))
	}
	var terms Terms
	if err := json.Unmarshal(asked[0].Requirements, &terms); err != nil || terms.AmountMinor != 2_500_000 || terms.OutputMint != boughtMint.String() || terms.InputMint != usdcMint() ||
		terms.SlippageBps != 50 || terms.OutAmount != "1000000" || terms.RequestID != "req-123" || terms.LastValidBlockHeight != 1_150 || terms.Transaction == "" {
		t.Errorf("terms: %+v", terms)
	}
	// What the rail signed is what Jupiter is asked to land.
	if len(f.execs) != 1 || f.execs[0]["signedTransaction"] != "c2lnbmVk" || f.execs[0]["requestId"] != "req-123" || f.execs[0]["lastValidBlockHeight"] != float64(1_150) {
		t.Errorf("execute: %v", f.execs)
	}
}

func TestRunStopsBeforeAnythingIsSignedWhenTheOrderIsNotTheOneQuoted(t *testing.T) {
	for name, tc := range map[string]struct {
		order func(q map[string]string) (int, any)
		want  string
	}{
		"no transaction": {func(q map[string]string) (int, any) {
			return 200, map[string]any{"requestId": "r", "inAmount": q["amount"], "outAmount": "1000000", "transaction": "", "errorMessage": "Insufficient funds", "lastValidBlockHeight": 1}
		}, "couldn't build it (Insufficient funds)"},
		"a different amount": {func(q map[string]string) (int, any) {
			return 200, map[string]any{"requestId": "r", "inAmount": "9000000", "outAmount": "1000000", "transaction": swapTx(t, solana.PublicKey{1}), "lastValidBlockHeight": 1}
		}, "spends 9000000, not the 2500000"},
		"a version 1 transaction": {func(q map[string]string) (int, any) {
			return 200, map[string]any{"requestId": "r", "inAmount": q["amount"], "outAmount": "1000000", "transaction": "AA==", "transactionVersion": 1, "lastValidBlockHeight": 1}
		}, "version 1 transaction"},
		"no request id": {func(q map[string]string) (int, any) {
			return 200, map[string]any{"inAmount": q["amount"], "outAmount": "1000000", "transaction": "AA==", "lastValidBlockHeight": 1}
		}, "no request id or expiry"},
		"no expiry": {func(q map[string]string) (int, any) {
			return 200, map[string]any{"requestId": "r", "inAmount": q["amount"], "outAmount": "1000000", "transaction": "AA=="}
		}, "no request id or expiry"},
		"the price moved by more than the slippage": {func(q map[string]string) (int, any) {
			return 200, map[string]any{"requestId": "r", "inAmount": q["amount"], "outAmount": "994999", "transaction": "AA==", "lastValidBlockHeight": 1}
		}, "the price moved"},
		"Jupiter is down": {func(q map[string]string) (int, any) { return 503, map[string]any{} }, "answered HTTP 503"},
	} {
		r, f, _ := testRunner(t)
		q := quoteFor(t, r, "2.5") // priced at 1,000,000
		call := 0
		good := f.order
		f.order = func(qq map[string]string) (int, any) {
			call++
			if qq["taker"] == "" {
				return good(qq)
			}
			return tc.order(qq)
		}
		obs, asked := run(t, r, q, input("2.5"), okPay)
		if obs.Delivered || obs.AuthorityReleased || obs.Class != routing.FailQuote || !strings.Contains(obs.Message, tc.want) || len(asked) != 0 || len(f.execs) != 0 {
			t.Errorf("%s: want a quote failure with %q and nothing signed: %+v (asked %d, executed %d)", name, tc.want, obs, len(asked), len(f.execs))
		}
	}
}

func TestRunRefusesAnOrderWhoseOwnMinimumIsLooserThanTheSlippageAllowed(t *testing.T) {
	for floor, ok := range map[string]bool{
		"995000": true,  // exactly the quote less 0.5%
		"994999": true,  // Jupiter rounds down: one atom of slack
		"994998": false, // looser than the agent allowed
		"900000": false,
		"0":      false,
		"junk":   false,
	} {
		r, f, _ := testRunner(t)
		q := quoteFor(t, r, "2.5")
		good := f.order
		f.order = func(qq map[string]string) (int, any) {
			status, o := good(qq)
			if qq["taker"] != "" {
				o.(map[string]any)["otherAmountThreshold"] = floor
			}
			return status, o
		}
		obs, asked := run(t, r, q, input("2.5"), okPay)
		if ok && !obs.Delivered || !ok && (obs.Delivered || obs.Class != routing.FailQuote || !strings.Contains(obs.Message, "looser than") || len(asked) != 0) {
			t.Errorf("floor %q: accepted=%v, got %+v", floor, ok, obs)
		}
	}
	// An order that states no floor is judged by the simulation alone.
	r, f, _ := testRunner(t)
	q := quoteFor(t, r, "2.5")
	good := f.order
	f.order = func(qq map[string]string) (int, any) {
		status, o := good(qq)
		delete(o.(map[string]any), "otherAmountThreshold")
		return status, o
	}
	if obs, _ := run(t, r, q, input("2.5"), okPay); !obs.Delivered {
		t.Errorf("no stated floor: %+v", obs)
	}
}

func TestRunAcceptsAPriceThatMovedWithinTheSlippage(t *testing.T) {
	r, f, _ := testRunner(t)
	q := quoteFor(t, r, "2.5") // 1,000,000 quoted; 995,000 is the least
	good := f.order
	f.order = func(qq map[string]string) (int, any) {
		status, o := good(qq)
		if qq["taker"] != "" {
			o.(map[string]any)["outAmount"] = "995000"
		}
		return status, o
	}
	if obs, _ := run(t, r, q, input("2.5"), okPay); !obs.Delivered {
		t.Errorf("0.5%% worse is what the agent allowed: %+v", obs)
	}
}

func TestRunRefusesAnInputThatIsNotTheOnePriced(t *testing.T) {
	r, _, _ := testRunner(t)
	q := quoteFor(t, r, "2.5")
	if obs, asked := run(t, r, q, input("3"), okPay); obs.Class != routing.FailQuote || !strings.Contains(obs.Message, "isn't the one that was priced") || len(asked) != 0 {
		t.Errorf("a different amount: %+v", obs)
	}
	other := json.RawMessage(fmt.Sprintf(`{"output_mint":%q,"amount_usdc":"2.5","max_slippage_bps":90}`, boughtMint))
	if obs, _ := run(t, r, q, other, okPay); obs.Class != routing.FailQuote {
		t.Errorf("a different tolerance: %+v", obs)
	}
	if obs, _ := run(t, r, q, json.RawMessage(`{}`), okPay); obs.Class != routing.FailQuote {
		t.Errorf("bad input: %+v", obs)
	}
}

func TestRunWhenTheRailRefusesNothingIsAuthorized(t *testing.T) {
	r, f, _ := testRunner(t)
	q := quoteFor(t, r, "2.5")
	obs, _ := run(t, r, q, input("2.5"), func(app.PaymentRequest) (*app.PaymentAuthority, error) {
		return nil, errors.New("the swap rail refused to sign: the swap would add USDC to the wallet instead of spending it")
	})
	if obs.AuthorityReleased || obs.Delivered || obs.Class != routing.FailPayment || !strings.Contains(obs.Message, "refused to sign") || len(f.execs) != 0 {
		t.Errorf("%+v", obs)
	}
}

func TestRunWhenJupiterFailsOrGoesQuiet(t *testing.T) {
	r, f, _ := testRunner(t)
	q := quoteFor(t, r, "2.5")

	// Jupiter says it failed: authority was released, so the coordinator asks the rail.
	f.execute = func(map[string]any) (int, any) {
		return 200, map[string]any{"status": "Failed", "code": -1001, "error": "Slippage tolerance exceeded"}
	}
	obs, _ := run(t, r, q, input("2.5"), okPay)
	if obs.Delivered || !obs.AuthorityReleased || obs.Class != routing.FailProvider || !strings.Contains(obs.Message, "Slippage tolerance exceeded") || obs.SettleTx != "" {
		t.Errorf("failed: %+v", obs)
	}

	// Jupiter answers nothing usable: the transaction is signed and may have
	// landed, which only the chain can say.
	f.execute = func(map[string]any) (int, any) { return 502, map[string]any{} }
	obs, _ = run(t, r, q, input("2.5"), okPay)
	if obs.Delivered || !obs.AuthorityReleased || obs.Class != routing.FailAmbiguous || !strings.Contains(obs.Message, "couldn't tell whether") {
		t.Errorf("ambiguous: %+v", obs)
	}
}

func TestRunnerBasics(t *testing.T) {
	r, _, _ := testRunner(t)
	if r.Type() != routing.ExecSolanaSwap {
		t.Error("type")
	}
	if _, err := r.Rail(routing.Quote{Network: "base"}); err == nil {
		t.Error("swaps are on Solana mainnet only")
	}
	c, err := Candidate()
	if err != nil || c.Provider != "jupiter" || c.Capability != "solana.swap" || c.Endpoint != "" || len(c.Sources) != 1 || c.Sources[0] != routing.SourceNative || c.Network != "solana" {
		t.Errorf("candidate: %+v %v", c, err)
	}
	if info, err := CapabilityInfo().Normalize(); err != nil || info.Kind != routing.KindTrade {
		t.Errorf("capability: %+v %v", info, err)
	}
}
