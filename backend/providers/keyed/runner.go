package keyed

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
	"strings"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
)

// HTTPDoer is the HTTP client the runner uses. *safehttp.Client satisfies it.
type HTTPDoer interface {
	Do(req *http.Request) (*safehttp.Response, error)
}

// KeyReader gives a person's key for a vendor, or an error wrapping
// shared.ErrNotFound when they have saved none (app.ToolKeys).
type KeyReader interface {
	Key(ctx context.Context, userID, vendor string) (string, error)
}

// Config builds a Runner.
type Config struct {
	HTTP   HTTPDoer
	Keys   KeyReader
	Passes app.PassOwners
	Tools  *Tools
}

// Runner is the http_api execution type: it calls a vendor's tool with the key
// of the person whose Spend Pass is making the call. It satisfies
// app.StepRunner.
type Runner struct {
	http   HTTPDoer
	keys   KeyReader
	passes app.PassOwners
	tools  *Tools
}

var _ app.StepRunner = (*Runner)(nil)

// NewRunner builds the runner.
func NewRunner(c Config) *Runner {
	if c.Tools == nil {
		c.Tools = NewTools(nil)
	}
	return &Runner{http: c.HTTP, keys: c.Keys, passes: c.Passes, tools: c.Tools}
}

func (*Runner) Type() routing.ExecutionType { return routing.ExecHTTPAPI }

// Rail: every keyed call settles through the one "keyed" rail.
func (*Runner) Rail(routing.Quote) (string, error) { return RailName, nil }

// Quote prices a call at the vendor's list price. Nothing is sent: the key is
// not needed to price, and a vendor's free tier is not asked about.
func (r *Runner) Quote(_ context.Context, c routing.Candidate, input json.RawMessage) (routing.Quote, error) {
	t, ok := r.tools.find(c.Provider, c.Capability)
	if !ok || c.Endpoint != t.endpoint {
		return routing.Quote{}, fmt.Errorf("keyed: %s is not a tool Algebra runs with a key", c.Provider)
	}
	if _, err := parseInput(t.class, input); err != nil {
		return routing.Quote{}, fmt.Errorf("keyed: %s: %w", c.Provider, err)
	}
	return routing.Quote{
		Cost:  routing.Cost{ProviderMinor: t.quoteMinor()},
		Asset: "USDC", Network: "keyed", PayTo: t.vendor,
		Semantics:          econ.SemanticsMeteredCapture,
		EstimatedLatencyMS: t.latencyMS,
	}, nil
}

// Run makes the call. Payment authority is asked for once, after the key is
// found and the request built, so a person with no key is refused before
// anything is recorded as spent.
func (r *Runner) Run(ctx context.Context, call app.StepCall) app.StepObservation {
	q := call.Quote
	var obs app.StepObservation
	fail := func(class routing.FailureClass, format string, args ...any) app.StepObservation {
		obs.Class, obs.Message = class, fmt.Sprintf(format, args...)
		return obs
	}

	// The key goes to the vendor's own address and nowhere else: the tool is
	// looked up by provider and class, and the quote must name its endpoint.
	t, ok := r.tools.find(q.Provider, q.Capability)
	if !ok || q.Endpoint != t.endpoint {
		return fail(routing.FailQuote, "%s is not a tool Algebra runs with a key", q.Provider)
	}
	in, err := parseInput(t.class, call.Input)
	if err != nil {
		return fail(routing.FailQuote, "%v", err)
	}
	pass, err := r.passes.Get(ctx, call.Reservation.ExecutorPassID)
	if err != nil {
		return fail(routing.FailPolicy, "the pass making this call can't be found")
	}
	key, err := r.keys.Key(ctx, pass.UserID, t.vendor)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return fail(routing.FailPolicy, "no %s API key is saved for this account", t.vendor)
		}
		return fail(routing.FailPolicy, "the saved %s API key can't be read", t.vendor)
	}
	query, body, err := t.build(in)
	if err != nil {
		return fail(routing.FailQuote, "can't build the request: %v", err)
	}
	target := t.endpoint
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	obs.RequestHash = requestHash(t.method, t.endpoint, query.Encode(), body)

	req, err := http.NewRequestWithContext(ctx, t.method, target, bytes.NewReader(body))
	if err != nil {
		return fail(routing.FailQuote, "can't build the request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if t.bearer {
		req.Header.Set(t.header, "Bearer "+key)
	} else {
		req.Header.Set(t.header, key)
	}

	if _, err := call.Pay(ctx, app.PaymentRequest{Resource: t.endpoint}); err != nil {
		return fail(routing.FailPayment, "payment authority refused: %v", err)
	}
	obs.AuthorityReleased = true

	resp, err := r.http.Do(req)
	if err != nil {
		class := routing.FailProvider
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
			class = routing.FailTimeout
		}
		obs.Class, obs.Message = class, "no usable answer from "+t.vendor+": "+scrub(err.Error(), key)
		return obs
	}
	obs.HTTPStatus = resp.Status
	out, charged, op, err := t.read(resp.Status, resp.Body)
	if err != nil {
		class := routing.FailProvider
		var vf vendorFailure
		if resp.Status/100 == 2 && !errors.As(err, &vf) {
			class = routing.FailInvalid
		}
		obs.Class, obs.Message = class, scrub(err.Error(), key)
		obs.ProviderOperationID = op
		return obs
	}
	if charged < 0 {
		charged = t.listMinor // the vendor reported no cost: its list price
	}
	if op == "" {
		op = call.Reservation.IdempotencyKey
	}
	obs.Delivered, obs.Body, obs.ContentType = true, out, "application/json"
	obs.ProviderOperationID, obs.ChargedMinor = op, charged
	obs.SettleTx = BilledPrefix + op
	return obs
}

// scrub keeps vendor text short, on one line and free of the key.
func scrub(s string, secrets ...string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "[redacted]")
		}
	}
	return cut(strings.Join(strings.Fields(s), " "), 160)
}

func requestHash(method, endpoint, query string, body []byte) string {
	sum := sha256.Sum256(append([]byte(query+"\x00"), body...))
	b, _ := json.Marshal(struct {
		Method   string `json:"method"`
		Endpoint string `json:"endpoint"`
		Input    string `json:"input_sha256"`
	}{method, endpoint, hex.EncodeToString(sum[:])})
	return econ.HashBytes(b)
}
