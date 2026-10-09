package merchant

import (
	"context"

	"github.com/project-algebra/algebra/internal/domain/deal"
)

// IntegrationKind names HOW a connector reaches its merchant, following the
// mandate's §10 priority order (official API > official MCP > partnership
// API > user-authorized session > browser-assisted). It is descriptive
// metadata for operators and agents choosing between merchants — it never
// grants anything. Only Capabilities() says what a connector can do.
type IntegrationKind string

const (
	IntegrationMock            IntegrationKind = "mock"
	IntegrationOfficialMCP     IntegrationKind = "official_mcp"
	IntegrationOfficialAPI     IntegrationKind = "official_api"
	IntegrationAffiliateAPI    IntegrationKind = "affiliate_api"
	IntegrationDeepLinkHandoff IntegrationKind = "deep_link_handoff"
	IntegrationNotImplemented  IntegrationKind = "not_implemented"
)

// Status is a connector's self-reported readiness, surfaced by
// GET /api/v1/merchants and commerce.list_merchants so a user can see WHY a
// merchant option is or isn't usable ("link your Zepto account", "set
// FLIPKART_AFFILIATE_TOKEN") instead of a bare row of false flags.
type Status struct {
	Integration IntegrationKind `json:"integration"`
	// Ready is true only when what the connector offers can be exercised
	// right now: credentials present, account linked, live tool contract
	// validated. A handoff-only connector is Ready — its link always works —
	// while still reporting every capability false.
	Ready bool `json:"ready"`
	// Detail is a plain-language explanation of what the connector does or
	// what is missing. It must never contain a credential, token, or
	// personal data.
	Detail string `json:"detail"`
	// Source is the public documentation the integration is built against.
	Source string `json:"source,omitempty"`
}

// StatusReporter is optionally implemented by connectors whose readiness
// depends on external setup.
type StatusReporter interface {
	Status() Status
}

// HandoffLinker is implemented by connectors that cannot search or buy on
// the user's behalf but can give the user a link to continue on the
// merchant's own site or app, themselves. Following that link is always a
// human action — nothing in Algebra ever opens, fetches, or automates it.
type HandoffLinker interface {
	HandoffURL(query string) string
}

// DealFinder is implemented by connectors whose merchant publishes deals
// or offers through an official API (Flipkart's affiliate offers feed,
// Amazon's Creators API savings and deal details). It is independent of
// Capabilities().Coupons: nothing here is applied to a cart — deals are
// shown to the user, who gets them on the merchant's own site. A connector
// that isn't configured returns shared.ErrNotImplemented with a plain
// explanation of what's missing.
type DealFinder interface {
	FindDeals(ctx context.Context, query string, limit int) ([]deal.Deal, error)
}

// Warmer is implemented by connectors whose capabilities depend on a network
// check that shouldn't run inside Capabilities() (which has no context and
// is called on hot paths) — e.g. listing a remote MCP server's tools and
// validating them against the published contract. Wiring calls Warm once in
// the background at startup.
type Warmer interface {
	Warm(ctx context.Context) error
}
