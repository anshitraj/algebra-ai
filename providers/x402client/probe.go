package x402client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/providers/x402"
)

var _ app.Prober = (*Runner)(nil)

// Probe makes the unpaid request a quote makes and reads what the provider
// asks, on any Solana network, whether or not this server has a rail to pay
// it: a probe never pays, so whether Algebra could pay is a separate
// question (Payable).
func (r *Runner) Probe(ctx context.Context, c routing.Candidate, input json.RawMessage) (app.ProbeResult, error) {
	spec, err := buildSpec(c.Endpoint, c.Method, input)
	if err != nil {
		return app.ProbeResult{}, err
	}
	resp, err := r.send(ctx, spec, nil)
	if err != nil {
		return app.ProbeResult{}, err
	}
	res := app.ProbeResult{Status: resp.Status}
	if resp.Status != http.StatusPaymentRequired {
		return res, nil
	}
	ch, err := x402.ParseChallenge(resp.Header, resp.Body)
	if err != nil {
		res.Detail = err.Error()
		return res, nil
	}
	want := chain.NormalizeNetwork(c.Network)
	for _, req := range ch.Accepts {
		n := chain.NormalizeNetwork(req.Network)
		if !strings.HasPrefix(n, chain.Solana) || (want != "" && n != want) || !acceptableAsset(n, req.Asset) {
			continue
		}
		scheme := strings.ToLower(req.Scheme)
		if scheme != "exact" && scheme != schemeUpto {
			continue
		}
		amount, err := req.AmountMinor()
		if err != nil {
			continue
		}
		if !res.Priced || amount < res.PriceMinor {
			res.Priced, res.PriceMinor, res.Network, res.Scheme = true, amount, n, scheme
			res.Payable = r.networks[n] != ""
		}
	}
	if !res.Priced {
		res.Detail = "no USDC-on-Solana option Algebra can pay"
	}
	return res, nil
}
