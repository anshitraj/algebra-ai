package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/plugin"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/websearch"
)

// CommunitySearcher reads recent community deal posts about a product
// from the given sites (connectors/websearch.Gemini or .Tavily).
type CommunitySearcher interface {
	SearchCommunity(ctx context.Context, query string, sites []websearch.CommunitySite, limit int) ([]websearch.Tip, error)
}

// TipResults are community tips plus where they were looked for.
type TipResults struct {
	Tips []websearch.Tip `json:"tips"`
	// Searched names the places read, e.g. ["r/dealsforindia", "DesiDime"].
	Searched []string `json:"searched"`
}

// communityCacheTTL is short: community deals often end within hours.
const communityCacheTTL = 5 * time.Minute

// SetCommunitySearcher attaches the community tips source.
func (s *DiscoveryService) SetCommunitySearcher(c CommunitySearcher) { s.community = c }

// SetPlugins attaches the per-person plugin switches that gate community
// tips and each deal source.
func (s *DiscoveryService) SetPlugins(p *PluginService) { s.plugins = p }

// CommunityTips reads what people are posting about a product, from the
// community plugins the agent's person switched on — never from any other.
// Tips are unverified leads, not prices: nothing here enters a quote.
func (s *DiscoveryService) CommunityTips(ctx context.Context, agentID, query string) (*TipResults, error) {
	if _, err := requirePermission(ctx, s.agents, agentID, agentpkg.PermShoppingRead); err != nil {
		return nil, err
	}
	if s.community == nil || s.plugins == nil {
		return nil, fmt.Errorf("%w: community deals aren't set up on this server", shared.ErrNotImplemented)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: say what you're looking for", shared.ErrConflict)
	}
	if len(query) > maxDealQueryLen {
		query = query[:maxDealQueryLen]
	}
	on, err := s.plugins.ForAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	var sites []websearch.CommunitySite
	var names []string
	for _, p := range plugin.Catalog {
		st, ok := on[p.ID]
		if !ok || p.Purpose != plugin.PurposeCommunity {
			continue
		}
		for _, site := range plugin.SitesFor(p, st.Config) {
			sites = append(sites, site)
			if site.Path != "" {
				names = append(names, strings.TrimPrefix(site.Path, "/"))
			} else {
				names = append(names, p.Name)
			}
		}
	}
	if len(sites) == 0 {
		return nil, fmt.Errorf("%w: no community plugin is on — the user can turn one on under Plugins", shared.ErrUnauthorized)
	}

	key := fmt.Sprintf("algebra:community:v1:%s:%s", strings.ToLower(strings.Join(names, ",")), strings.ToLower(strings.Join(strings.Fields(query), " ")))
	var tips []websearch.Tip
	if s.searchCache != nil && s.searchCache.GetJSON(ctx, key, &tips) {
		return &TipResults{Tips: tips, Searched: names}, nil
	}
	tips, err = s.community.SearchCommunity(ctx, query, sites, 6)
	if err != nil {
		return nil, err
	}
	if tips == nil {
		tips = []websearch.Tip{}
	}
	if s.searchCache != nil && len(tips) > 0 {
		s.searchCache.SetJSON(ctx, key, tips, communityCacheTTL)
	}
	return &TipResults{Tips: tips, Searched: names}, nil
}

// dealPluginsFor is the agent's person's switched-on plugins, and false when
// plugins aren't wired (tests) — then every deal source is on.
func (s *DiscoveryService) dealPluginsFor(ctx context.Context, agentID string) (map[string]PluginState, bool) {
	if s.plugins == nil {
		return nil, false
	}
	on, err := s.plugins.ForAgent(ctx, agentID)
	if err != nil {
		return nil, false
	}
	return on, true
}
