// Package x402client is the x402 execution runner. It prices an x402
// resource with an unpaid request and pays for it by replaying the request
// with a payment header that Algebra's own rail releases, so the agent never
// holds a key and the provider never gets paid more than it quoted.
//
// It is the client half of the protocol only: it speaks to whatever
// provider (and facilitator behind it) a candidate names, and pays through
// whichever rail is configured for the network the provider asks for.
//
// What it refuses, on purpose:
//
//   - an option in anything but USDC at Circle's real address on that
//     network: a token that calls itself USDC elsewhere is another token;
//   - a network no rail is configured for;
//   - paying when the provider's terms (payee, asset, network, or a higher
//     price) differ from the quote it was approved at;
//   - following redirects, or sending the payment header anywhere but the
//     endpoint it was priced at (the HTTP client never redirects).
package x402client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/providers/x402"
)

// HTTPDoer is the HTTP client the runner uses. *safehttp.Client satisfies it.
type HTTPDoer interface {
	Do(req *http.Request) (*safehttp.Response, error)
}

// Config configures a Runner.
type Config struct {
	HTTP HTTPDoer
	// Networks maps each network Algebra can pay on to the name of the rail
	// that pays it, e.g. {"sandbox": "sandbox", "solana": "x402-solana"}.
	Networks map[string]string
	// ReuseQuoteFor lets Run pay against a quote's own requirements, skipping
	// the re-check request, while the quote is younger than this. Zero always
	// re-checks. Reusing saves a round trip; re-checking catches a provider
	// that changed its terms.
	ReuseQuoteFor time.Duration
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Runner is the x402 app.StepRunner.
type Runner struct {
	http     HTTPDoer
	networks map[string]string
	reuse    time.Duration
	now      func() time.Time
}

var _ app.StepRunner = (*Runner)(nil)

// New builds a Runner.
func New(c Config) *Runner {
	nets := make(map[string]string, len(c.Networks))
	for n, rail := range c.Networks {
		nets[chain.NormalizeNetwork(n)] = rail
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	return &Runner{http: c.HTTP, networks: nets, reuse: c.ReuseQuoteFor, now: now}
}

func (r *Runner) Type() routing.ExecutionType { return routing.ExecX402 }

// Rail names the rail that pays a quote's network.
func (r *Runner) Rail(q routing.Quote) (string, error) {
	rail, ok := r.networks[chain.NormalizeNetwork(q.Network)]
	if !ok {
		return "", fmt.Errorf("no payment rail is configured for network %q", q.Network)
	}
	return rail, nil
}

// option is one payment option Algebra can pay.
type option struct {
	index   int
	req     x402.Requirements
	network string
	amount  int64
}

// options returns the options in a challenge that Algebra will pay, and why
// it passed on the others. wantNetwork, when set, narrows to that network.
func (r *Runner) options(ch x402.Challenge, wantNetwork string) ([]option, []string) {
	var ok []option
	var why []string
	for i, req := range ch.Accepts {
		n := chain.NormalizeNetwork(req.Network)
		fail := func(reason string) {
			why = append(why, fmt.Sprintf("%s on %s: %s", orDash(req.Scheme), orDash(n), reason))
		}
		switch {
		case !strings.EqualFold(req.Scheme, "exact"):
			fail("only the exact scheme is supported")
			continue
		case r.networks[n] == "":
			fail("no payment rail for this network")
			continue
		case wantNetwork != "" && n != chain.NormalizeNetwork(wantNetwork):
			fail("not the network requested")
			continue
		case strings.TrimSpace(req.PayTo) == "":
			fail("no payee")
			continue
		}
		amount, err := req.AmountMinor()
		if err != nil {
			fail("unreadable amount")
			continue
		}
		if !acceptableAsset(n, req.Asset) {
			fail("asset " + orDash(req.Asset) + " is not USDC")
			continue
		}
		ok = append(ok, option{index: i, req: req, network: n, amount: amount})
	}
	return ok, why
}

// acceptableAsset: USDC at Circle's real address. The sandbox has no chain,
// so it names the asset by symbol.
func acceptableAsset(network, asset string) bool {
	if network == chain.Sandbox {
		return strings.EqualFold(strings.TrimSpace(asset), "USDC")
	}
	want, ok := chain.AssetAddress(network, "USDC")
	return ok && chain.SameAddress(network, want, asset)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// cheapest picks the lowest-priced option, the provider's order breaking ties.
func cheapest(opts []option) option {
	best := opts[0]
	for _, o := range opts[1:] {
		if o.amount < best.amount {
			best = o
		}
	}
	return best
}

// Quote prices a candidate for one input with an unpaid request.
func (r *Runner) Quote(ctx context.Context, c routing.Candidate, input json.RawMessage) (routing.Quote, error) {
	spec, err := buildSpec(c.Endpoint, c.Method, input)
	if err != nil {
		return routing.Quote{}, err
	}
	resp, err := r.send(ctx, spec, nil)
	if err != nil {
		return routing.Quote{}, fmt.Errorf("x402: reaching %s: %w", c.Provider, err)
	}
	if resp.Status != http.StatusPaymentRequired {
		if resp.Status >= 200 && resp.Status < 300 {
			return routing.Quote{}, fmt.Errorf("x402: %s answered without asking for payment, so it isn't an x402 resource", c.Provider)
		}
		return routing.Quote{}, fmt.Errorf("x402: %s answered %d to an unpaid request", c.Provider, resp.Status)
	}
	ch, err := x402.ParseChallenge(resp.Header, resp.Body)
	if err != nil {
		return routing.Quote{}, fmt.Errorf("x402: %s: %w", c.Provider, err)
	}
	opts, why := r.options(ch, c.Network)
	if len(opts) == 0 {
		return routing.Quote{}, fmt.Errorf("x402: %s offers no payment option Algebra can pay (%s)", c.Provider, strings.Join(why, "; "))
	}
	best := cheapest(opts)
	reqs, err := ch.Single(best.index)
	if err != nil {
		return routing.Quote{}, err
	}
	q := routing.Quote{
		Method: spec.method, Cost: routing.Cost{ProviderMinor: best.amount}, Asset: "USDC", Network: best.network,
		PayTo: best.req.PayTo, Semantics: econ.SemanticsPrepaidExact, EstimatedLatencyMS: c.EstimatedLatencyMS,
		Requirements: reqs, Test: chain.IsTestNetwork(best.network),
	}
	if best.network != chain.Sandbox {
		q.AssetAddress = best.req.Asset
	}
	return q, nil
}

// Run pays for and makes the call. It returns what it saw; deciding what that
// means for the money is the coordinator's job.
func (r *Runner) Run(ctx context.Context, call app.StepCall) app.StepObservation {
	q := call.Quote
	var obs app.StepObservation
	fail := func(class routing.FailureClass, format string, args ...any) app.StepObservation {
		obs.Class, obs.Message = class, fmt.Sprintf(format, args...)
		return obs
	}

	spec, err := buildSpec(q.Endpoint, q.Method, call.Input)
	if err != nil {
		return fail(routing.FailQuote, "can't build the request: %v", err)
	}
	obs.RequestHash = spec.hash()

	reqs := q.Requirements
	if age := r.now().Sub(q.QuotedAt); r.reuse <= 0 || age < 0 || age >= r.reuse || len(reqs) == 0 {
		// Re-check the provider's terms against what was approved.
		resp, err := r.send(ctx, spec, nil)
		if err != nil {
			return fail(routing.FailQuote, "couldn't re-check the provider's terms: %v", err)
		}
		obs.HTTPStatus = resp.Status
		if resp.Status != http.StatusPaymentRequired {
			return fail(routing.FailQuote, "the provider answered %d when its terms were re-checked", resp.Status)
		}
		ch, err := x402.ParseChallenge(resp.Header, resp.Body)
		if err != nil {
			return fail(routing.FailQuote, "%v", err)
		}
		opt, ok := r.sameTerms(ch, q)
		if !ok {
			return fail(routing.FailQuote, "the provider changed its terms since it was quoted (payee, asset, network or a higher price)")
		}
		if reqs, err = ch.Single(opt.index); err != nil {
			return fail(routing.FailQuote, "%v", err)
		}
	}

	// The resource being paid for is the URL actually called, with its path
	// parameters filled in, not the template.
	auth, err := call.Pay(ctx, app.PaymentRequest{Requirements: reqs, Resource: spec.url})
	if err != nil {
		return fail(routing.FailPayment, "payment authority refused: %v", err)
	}
	obs.AuthorityReleased = true
	header := auth.Header
	if header == "" {
		header = x402.HeaderPayment
	}

	hdr := http.Header{}
	hdr.Set(header, auth.Value)
	hdr.Set("Idempotency-Key", call.Reservation.IdempotencyKey)
	resp, err := r.send(ctx, spec, hdr)
	if err != nil {
		class := classify(err)
		obs.Class, obs.Message = class, "no usable response after payment was released: "+scrub(err.Error(), auth.Value)
		// The payment may have been collected and the answer lost. Ask once
		// more with the same idempotency key and no payment: a provider that
		// stored the result will replay it for free.
		if got, ok := r.replay(ctx, spec, call.Reservation.IdempotencyKey); ok {
			return r.delivered(obs, got, "recovered by idempotent replay")
		}
		return obs
	}
	obs.HTTPStatus = resp.Status
	switch {
	case resp.Status >= 200 && resp.Status < 300:
		return r.delivered(obs, resp, "")
	case resp.Status == http.StatusPaymentRequired:
		obs.Class, obs.Message = routing.FailPayment, "the provider rejected the payment"
		if ch, err := x402.ParseChallenge(resp.Header, resp.Body); err == nil && ch.Error != "" {
			obs.Message += ": " + scrub(ch.Error, auth.Value)
		}
		return obs
	default:
		obs.Class, obs.Message = routing.FailProvider, fmt.Sprintf("the provider answered %d after payment was released", resp.Status)
		if resp.Status >= 500 {
			if got, ok := r.replay(ctx, spec, call.Reservation.IdempotencyKey); ok {
				return r.delivered(obs, got, "recovered by idempotent replay")
			}
		}
		return obs
	}
}

// delivered records a success response and any settle header it carried.
func (r *Runner) delivered(obs app.StepObservation, resp *safehttp.Response, note string) app.StepObservation {
	obs.Delivered, obs.HTTPStatus, obs.Body = true, resp.Status, resp.Body
	obs.ContentType = resp.Header.Get("Content-Type")
	obs.Class, obs.Message = "", note
	for _, h := range []string{x402.HeaderPaymentResponse, x402.HeaderResponseV2} {
		if v := resp.Header.Get(h); v != "" {
			if s, err := x402.DecodeSettle(v); err == nil && s.Success {
				obs.SettleTx = s.Transaction
				break
			}
		}
	}
	return obs
}

// replay repeats the request with the same idempotency key and no payment.
// It can only ever recover a result: with no payment header there is nothing
// for a provider to collect.
func (r *Runner) replay(ctx context.Context, s callSpec, key string) (*safehttp.Response, bool) {
	hdr := http.Header{}
	hdr.Set("Idempotency-Key", key)
	resp, err := r.send(ctx, s, hdr)
	if err != nil || resp.Status < 200 || resp.Status >= 300 {
		return nil, false
	}
	return resp, true
}

// sameTerms finds the option in a fresh challenge that still matches the
// quote: same network, payee and asset, and no higher a price.
func (r *Runner) sameTerms(ch x402.Challenge, q routing.Quote) (option, bool) {
	opts, _ := r.options(ch, q.Network)
	var match []option
	for _, o := range opts {
		if o.req.PayTo != q.PayTo || o.amount > q.Cost.ProviderMinor {
			continue
		}
		if q.AssetAddress != "" && !chain.SameAddress(o.network, o.req.Asset, q.AssetAddress) {
			continue
		}
		match = append(match, o)
	}
	if len(match) == 0 {
		return option{}, false
	}
	return cheapest(match), true
}

func (r *Runner) send(ctx context.Context, s callSpec, extra http.Header) (*safehttp.Response, error) {
	req, err := http.NewRequestWithContext(ctx, s.method, s.url, bytes.NewReader(s.body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if len(s.body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range extra {
		req.Header[http.CanonicalHeaderKey(k)] = slices.Clone(v)
	}
	return r.http.Do(req)
}

func classify(err error) routing.FailureClass {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return routing.FailTimeout
	}
	return routing.FailProvider
}

// scrub keeps provider-supplied text short, on one line and free of any
// secret before it goes into a record. Secrets are removed before the text
// is truncated, so cutting a long message can't leave half a credential.
func scrub(s string, secrets ...string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "[redacted]")
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// callSpec is one request, ready to send and to hash.
type callSpec struct {
	method string
	url    string
	body   []byte
}

func (s callSpec) hash() string {
	sum := sha256.Sum256(s.body)
	b, _ := json.Marshal(struct {
		Method string `json:"method"`
		URL    string `json:"url"`
		Body   string `json:"body_sha256"`
	}{s.method, s.url, hex.EncodeToString(sum[:])})
	return econ.HashBytes(b)
}

// buildSpec turns an intent's canonical input into a request. A templated
// endpoint (".../{chain}/tokens/{mint}") is first filled in from the input
// fields of the same names, which then leave the input (see expandPath). For
// GET and DELETE the input's remaining top-level scalar fields become query
// parameters; for POST, PUT and PATCH the remaining input is the JSON body.
// With no method, an empty input means GET and anything else POST.
func buildSpec(endpoint, method string, input json.RawMessage) (callSpec, error) {
	canon, err := econ.Canonicalize(input)
	if err != nil {
		return callSpec{}, err
	}
	if endpoint, canon, err = expandPath(endpoint, canon); err != nil {
		return callSpec{}, err
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodPost
		if string(canon) == "{}" {
			method = http.MethodGet
		}
	}
	switch method {
	case http.MethodGet, http.MethodDelete:
		u, err := url.Parse(endpoint)
		if err != nil {
			return callSpec{}, errors.New("the endpoint isn't a valid URL")
		}
		var fields map[string]any
		dec := json.NewDecoder(bytes.NewReader(canon))
		dec.UseNumber()
		if err := dec.Decode(&fields); err != nil {
			return callSpec{}, errors.New("a GET input must be a JSON object")
		}
		q := u.Query()
		for k, v := range fields {
			switch t := v.(type) {
			case string:
				q.Set(k, t)
			case json.Number:
				q.Set(k, t.String())
			case bool:
				q.Set(k, fmt.Sprint(t))
			default:
				return callSpec{}, fmt.Errorf("input field %q can't go in a query string; use a POST candidate", k)
			}
		}
		u.RawQuery = q.Encode()
		return callSpec{method: method, url: u.String()}, nil
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return callSpec{method: method, url: endpoint, body: canon}, nil
	}
	return callSpec{}, fmt.Errorf("method %s isn't supported", method)
}
