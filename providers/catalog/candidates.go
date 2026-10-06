package catalog

import (
	"fmt"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// Candidates turns a provider's callable endpoints into routing candidates
// found by source. They are listings (trust "listed"): the executor prices
// each with an unpaid request and checks the terms again at payment. extra,
// if set, adds what a catalog knows beyond the common fields, such as the
// payment terms it publishes.
//
// An endpoint that doesn't normalise (say its host is a private address) is
// reported in the second result and the rest are still returned.
func Candidates(d *Detail, source routing.DiscoverySource, extra func(*routing.Candidate, Endpoint)) ([]routing.Candidate, []routing.Dropped) {
	var out []routing.Candidate
	var dropped []routing.Dropped
	for _, e := range d.Endpoints {
		if !e.Callable {
			continue
		}
		c := routing.Candidate{
			Capability: e.Capability, Provider: d.ID, Name: d.Name, ExecutionType: routing.ExecX402,
			Endpoint: e.URL, Method: e.Method, PriceMinor: e.PriceMinor, Asset: Currency, Network: chain.Solana,
			Sources: []routing.DiscoverySource{source}, SourceRef: fmt.Sprintf("%s:%s#%s %s", d.Source, d.FQN, e.Method, e.Path),
			DiscoveredAt: d.FetchedAt,
		}
		if extra != nil {
			extra(&c, e)
		}
		n, err := c.Normalize()
		if err != nil {
			dropped = append(dropped, routing.Dropped{Provider: d.ID, Reason: fmt.Sprintf("%s %s: %v", e.Method, e.Path, err)})
			continue
		}
		out = append(out, n)
	}
	return out, dropped
}
