package app

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// resolveClass resolves a capability that names a class of work: the
// operator's own providers for it, then every catalog provider of it, each
// with the adapter that turns the class's input into its own. Named providers
// narrow the field to themselves; endpoints the agent supplies join it as
// unverified web finds that take the class's own field names.
//
// The order is a first cut, not the ranking: the router prices only the first
// MaxQuotedCandidates, so the providers most likely to be worth a price come
// first (the operator's, then the cheapest listed). The ranking proper is done
// on live quotes.
func (r CandidateResolver) resolveClass(ctx context.Context, class routing.Class, named []string, supplied []CandidateInput) ([]routing.Candidate, []routing.Rejection) {
	var out []routing.Candidate
	var rejected []routing.Rejection
	for _, cs := range r.Configured {
		for _, c := range cs {
			if c.Capability == class.ID {
				out = append(out, c)
			}
		}
	}
	cands, err := r.Classes.ClassCandidates(ctx, class.ID)
	if err != nil {
		rejected = append(rejected, routing.Rejection{Provider: "catalog", Code: routing.RejectUnquotable, Detail: "the catalogs couldn't be read: " + err.Error()})
	}
	out = append(out, cands...)

	if len(named) > 0 {
		want := map[string]bool{}
		for _, n := range named {
			want[strings.ToLower(strings.TrimSpace(n))] = true
		}
		found := map[string]bool{}
		out = slices.DeleteFunc(out, func(c routing.Candidate) bool {
			if want[c.Provider] {
				found[c.Provider] = true
				return false
			}
			return true
		})
		for n := range want {
			if !found[n] {
				rejected = append(rejected, routing.Rejection{Provider: n, Code: routing.RejectUnquotable, Detail: "no provider by that name does " + class.ID})
			}
		}
	}
	for _, in := range supplied {
		provider := strings.TrimSpace(in.Provider)
		if provider == "" {
			if u, err := url.Parse(strings.TrimSpace(in.Endpoint)); err == nil {
				provider = u.Hostname()
			}
		}
		out = append(out, routing.Candidate{
			Capability: class.ID, Provider: provider, Name: in.Name, ExecutionType: routing.ExecX402,
			Endpoint: in.Endpoint, Method: in.Method, Network: in.Network, Sources: []routing.DiscoverySource{routing.SourceWeb},
		})
	}
	orderForClass(out)
	return out, rejected
}

// orderForClass puts the operator's providers first, then catalog providers by
// listed price (an unstated price after every stated one), then by ID so the
// order never depends on map iteration.
func orderForClass(cs []routing.Candidate) {
	trusted := func(c routing.Candidate) int {
		for _, s := range c.Sources {
			if s == routing.SourceNative || s == routing.SourceConfigured {
				return 0
			}
		}
		return 1
	}
	price := func(c routing.Candidate) int64 {
		if c.PriceMinor <= 0 {
			return 1 << 62
		}
		return c.PriceMinor
	}
	slices.SortStableFunc(cs, func(a, b routing.Candidate) int {
		return cmp.Or(cmp.Compare(trusted(a), trusted(b)), cmp.Compare(price(a), price(b)), strings.Compare(a.ID, b.ID))
	})
}

// ClassCandidates is every candidate of a class, the operator's providers and
// the catalogs', in the resolver's order: what the health probe sweeps.
func (r CandidateResolver) ClassCandidates(ctx context.Context, classID string) ([]routing.Candidate, error) {
	class, ok := routing.ClassByID(classID)
	if !ok {
		return nil, fmt.Errorf("unknown class %q", classID)
	}
	if r.Classes == nil {
		var out []routing.Candidate
		for _, cs := range r.Configured {
			for _, c := range cs {
				if c.Capability == class.ID {
					out = append(out, c)
				}
			}
		}
		orderForClass(out)
		return out, nil
	}
	out, _ := r.resolveClass(ctx, class, nil, nil)
	return out, nil
}
