package catalog

import (
	"fmt"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// Candidates turns a provider's callable endpoints into routing candidates
// found by source: one per network the endpoint can be paid on (mainnet,
// devnet), each in that network's real USDC. They are listings (trust
// "listed"): the executor prices each with an unpaid request and checks the
// terms again at payment, and an intent's allowed networks pick between them.
// extra, if set, adds what a catalog knows beyond the common fields.
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
		pays := e.Payments
		if len(pays) == 0 {
			pays = []Payment{{Network: chain.Solana, PriceMinor: e.PriceMinor}}
		}
		for _, pay := range pays {
			c := routing.Candidate{
				Capability: e.Capability, Provider: d.ID, Name: d.Name, ExecutionType: routing.ExecX402,
				Endpoint: e.URL, Method: e.Method, PriceMinor: pay.PriceMinor, Asset: Currency, Network: pay.Network,
				Sources: []routing.DiscoverySource{source}, SourceRef: fmt.Sprintf("%s:%s#%s %s", d.Source, d.FQN, e.Method, e.Path),
				DiscoveredAt: d.FetchedAt,
			}
			if addr, ok := chain.AssetAddress(pay.Network, Currency); ok {
				c.AssetAddress = addr
			}
			if extra != nil {
				extra(&c, e)
			}
			n, err := c.Normalize()
			if err != nil {
				dropped = append(dropped, routing.Dropped{Provider: d.ID, Reason: fmt.Sprintf("%s %s on %s: %v", e.Method, e.Path, pay.Network, err)})
				continue
			}
			out = append(out, n)
		}
	}
	return out, dropped
}
