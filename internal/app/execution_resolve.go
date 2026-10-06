package app

import (
	"context"

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
