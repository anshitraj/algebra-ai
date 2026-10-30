package app

import (
	"context"
	"slices"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// CatalogSource is the registries of providers an agent can name that Algebra
// reads instead of being configured with (Pay.sh, Circle's Agent
// Marketplace). A catalog lists what exists; it never vouches for it. Its
// candidates carry the catalog's source and are priced and checked like any
// other before anything is paid.
type CatalogSource interface {
	// Owns reports whether a provider name is a catalog's ("paysh:…", "circle:…").
	Owns(provider string) bool
	// CandidatesFor returns one provider's candidates that do the capability.
	CandidatesFor(ctx context.Context, provider, capability string) ([]routing.Candidate, error)
	// ForCapability returns the candidates for a capability when no provider
	// was named: a catalog capability says whose it is. It returns nil and no
	// error when the capability isn't one the catalog knows.
	ForCapability(ctx context.Context, capability string) ([]routing.Candidate, error)
}

// CandidateResolver turns what an agent asked for into candidates. It draws
// on the providers the operator configured, a catalog if there is one, and
// the endpoints the agent supplies itself.
type CandidateResolver struct {
	Configured map[string][]routing.Candidate
	// Catalog may be nil.
	Catalog CatalogSource
	// Classes may be nil. With it, a capability that names a class
	// ("token.price") is every provider of that class, not one endpoint.
	Classes ClassSource
	// Keyed may be nil. With it, ResolveFor adds the tools a person has saved
	// an API key for (providers/keyed) to that person's own candidates.
	Keyed KeyedSource
}

// KeyedSource gives the tools a person can run with their own API keys.
type KeyedSource interface {
	CandidatesFor(ctx context.Context, userID, capability string) []routing.Candidate
}

// ResolveFor is Resolve for one person: their keyed tools compete with the
// rest when nothing was named, and are used when named ("keyed:exa"). Endpoints
// an agent supplies are never keyed, and nobody sees another person's tools.
func (r CandidateResolver) ResolveFor(ctx context.Context, userID, capability string, named []string, supplied []CandidateInput) ([]routing.Candidate, []routing.Rejection) {
	out, rejected := r.Resolve(ctx, capability, named, supplied)
	if r.Keyed == nil || userID == "" {
		return out, rejected
	}
	mine := r.Keyed.CandidatesFor(ctx, userID, capability)
	if len(named) > 0 {
		kept := mine[:0]
		for _, c := range mine {
			if slices.ContainsFunc(named, func(n string) bool { return strings.EqualFold(strings.TrimSpace(n), c.Provider) }) {
				kept = append(kept, c)
			}
		}
		mine = kept
	} else if len(supplied) > 0 {
		mine = nil
	}
	if len(mine) == 0 {
		return out, rejected
	}
	// Named keyed tools were not found by Resolve, which doesn't know them.
	rejected = slices.DeleteFunc(rejected, func(rj routing.Rejection) bool {
		return slices.ContainsFunc(mine, func(c routing.Candidate) bool { return c.Provider == strings.ToLower(strings.TrimSpace(rj.Provider)) })
	})
	// They need no network to be priced, so they go first: the router quotes
	// only the first few candidates.
	have := make(map[string]bool, len(mine))
	for _, c := range mine {
		have[c.ID] = true
	}
	for _, c := range out {
		if !have[c.ID] {
			mine = append(mine, c)
		}
	}
	return mine, rejected
}

// ClassSource gives the candidates of a class of work across every catalog
// (see routing.Class), each with the input adapter that fits its provider.
type ClassSource interface {
	ClassCandidates(ctx context.Context, classID string) ([]routing.Candidate, error)
}

// Resolve is ResolveCandidates plus the catalog:
//
//   - a named provider a catalog owns is looked up in the catalog, and the
//     rest are looked up among the configured ones;
//   - with nothing named or supplied, the configured providers that do the
//     capability are used, and only if there are none is the catalog asked,
//     so a request for a capability the operator configured never waits on a
//     catalog;
//   - endpoints the agent supplies are, as always, unverified web finds.
func (r CandidateResolver) Resolve(ctx context.Context, capability string, named []string, supplied []CandidateInput) ([]routing.Candidate, []routing.Rejection) {
	out, rejected := r.resolve(ctx, capability, named, supplied)
	// A chain whose rail can't pay right now (its wallet is empty) is left
	// out here, for every caller, rather than tried and failed.
	kept := out[:0]
	for _, c := range out {
		if c.Network != "" && !chain.Payable(c.Network) {
			rejected = append(rejected, routing.Rejection{CandidateID: c.ID, Provider: c.Provider, Code: RejectNetworkNotReady,
				Detail: "Algebra's wallet on " + c.Network + " can't pay yet"})
			continue
		}
		kept = append(kept, c)
	}
	return kept, rejected
}

// RejectNetworkNotReady: the candidate sells on a chain whose rail can't pay now.
const RejectNetworkNotReady = "network_not_ready"

func (r CandidateResolver) resolve(ctx context.Context, capability string, named []string, supplied []CandidateInput) ([]routing.Candidate, []routing.Rejection) {
	if class, ok := routing.ClassByID(capability); ok && r.Classes != nil {
		return r.resolveClass(ctx, class, named, supplied)
	}
	if r.Catalog == nil {
		return ResolveCandidates(capability, named, supplied, r.Configured)
	}
	var configuredNames, catalogNames []string
	for _, n := range named {
		if r.Catalog.Owns(n) {
			catalogNames = append(catalogNames, n)
		} else {
			configuredNames = append(configuredNames, n)
		}
	}

	var out []routing.Candidate
	var rejected []routing.Rejection
	// ResolveCandidates treats "nothing named and nothing supplied" as "every
	// configured provider", so it must not be asked when only catalog
	// providers were named.
	if len(named) == 0 || len(configuredNames) > 0 || len(supplied) > 0 {
		out, rejected = ResolveCandidates(capability, configuredNames, supplied, r.Configured)
	}
	for _, n := range catalogNames {
		cands, err := r.Catalog.CandidatesFor(ctx, n, capability)
		switch {
		case err != nil:
			rejected = append(rejected, routing.Rejection{Provider: n, Code: routing.RejectUnquotable, Detail: "the catalog couldn't give that provider's endpoints: " + err.Error()})
		case len(cands) == 0:
			rejected = append(rejected, routing.Rejection{Provider: n, Code: routing.RejectUnquotable, Detail: "no endpoint of that provider does " + capability})
		default:
			out = append(out, cands...)
		}
	}
	if len(named) == 0 && len(supplied) == 0 && len(out) == 0 {
		cands, err := r.Catalog.ForCapability(ctx, capability)
		if err != nil {
			rejected = append(rejected, routing.Rejection{Provider: "catalog", Code: routing.RejectUnquotable, Detail: "the catalog couldn't be read: " + err.Error()})
		}
		out = append(out, cands...)
	}
	return out, rejected
}
