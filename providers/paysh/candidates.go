package paysh

import (
	"context"
	"slices"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/providers/catalog"
)

// candidatesOf turns a provider's callable endpoints into candidates found
// on Pay.sh (see catalog.Candidates).
func candidatesOf(d *Detail) ([]routing.Candidate, []routing.Dropped) {
	return catalog.Candidates(d, routing.SourcePaySh, nil)
}

// CandidatesFor returns the candidates of one provider that do a capability.
// provider is a provider ID ("paysh:birdeye.data") or an FQN.
func (c *Client) CandidatesFor(ctx context.Context, provider, capability string) ([]routing.Candidate, error) {
	d, err := c.Detail(ctx, provider)
	if err != nil {
		return nil, err
	}
	cands, _ := candidatesOf(d)
	return slices.DeleteFunc(cands, func(x routing.Candidate) bool { return x.Capability != capability }), nil
}

// ForCapability finds the candidates for a capability when the caller named
// no provider. A Pay.sh capability starts with its provider's name
// ("birdeye.data.get.x402-defi-price"), so the capability alone says whose it
// is. The longest matching provider wins. A capability that belongs to no
// provider in the catalog is ErrNotFound.
func (c *Client) ForCapability(ctx context.Context, capability string) ([]routing.Candidate, error) {
	capability = strings.ToLower(strings.TrimSpace(capability))
	snap, _, _, err := c.catalogNow(ctx)
	if err != nil {
		return nil, err
	}
	best := ""
	for _, p := range snap.providers {
		if prefix := strings.ReplaceAll(p.FQN, "/", ".") + "."; strings.HasPrefix(capability, prefix) && len(p.ID) > len(best) {
			best = p.ID
		}
	}
	if best == "" {
		return nil, ErrNotFound
	}
	return c.CandidatesFor(ctx, best, capability)
}
