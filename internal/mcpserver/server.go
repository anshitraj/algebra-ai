// Package mcpserver exposes Algebra's deliberately limited MCP tool
// surface (mandate §6) on top of the official
// github.com/modelcontextprotocol/go-sdk, targeting MCP spec 2026-07-28.
//
// Every handler in this package is thin: resolve the calling agent, call
// exactly one internal/app service method, map the result to a
// response DTO that cannot structurally carry a secret, and return. No
// commerce logic is duplicated here — see internal/app's package doc for
// why that rule is load-bearing, not stylistic.
//
// Agent identity: MCP's transport-level auth (OAuth 2.1 bearer tokens over
// streamable HTTP, per the current spec) is the production target, but this
// build runs the default stdio transport for local/dev use, which carries
// no HTTP headers. Every mutating tool's input therefore includes an
// explicit agent_token field, resolved here via resolveAgent — this is
// call-site-explicit rather than implicit-from-transport, and is exactly
// what mandate §6/§7 asks for either way: "every MCP request capable of
// mutating commerce state must identify user, agent, application/client."
// Over streamable HTTP, an agent may instead send its token as
// "Authorization: Bearer <token>" (bearerMiddleware) and leave agent_token
// empty — how a Spend Pass connects Claude, ChatGPT or any MCP client without
// a secret ever appearing in the conversation.
package mcpserver

import (
	"context"
	"fmt"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/app"
	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	integratorpkg "github.com/project-algebra/algebra/internal/domain/integrator"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/quote"
)

// Server holds every dependency the tool handlers need. It has no state of
// its own beyond these references — all commerce state lives in Postgres,
// which is what keeps this server horizontally scalable and stateless at
// the transport layer (mandate §5).
type Server struct {
	Agents            app.AgentStore
	Intents           *app.IntentService
	Discovery         *app.DiscoveryService
	Quotes            *app.QuoteService
	Policy            *app.PolicyService
	Orders            *app.OrderService
	Payments          *app.PaymentService
	Privacy           *privacy.Resolver
	Connectors        *app.ConnectorRegistry
	Idempotency       app.IdempotencyStore
	Integrators       app.IntegratorStore
	TransactionPolicy *app.TransactionPolicyService

	// PaymentIntents backs the payments.create_intent/get_intent/execute/
	// get_status tools — the B2B agentic-payments surface. See
	// internal/domain/paymentintent's package doc.
	PaymentIntents *app.PaymentIntentService

	// CommerceProfiles backs profiles.get_commerce_profile/
	// update_commerce_preferences — see internal/domain/commerceprofile.
	CommerceProfiles *app.CommerceProfileService

	// SpendPasses backs algebra.spend_pass: an agent reading its own limits.
	SpendPasses *app.SpendPassService

	// Limiter is optional (mandate §35/§49) — nil means no MCP-level rate
	// limiting, which is fine for local stdio development and not fine for
	// a production streamable-HTTP deployment. See rateLimitMiddleware.
	Limiter app.RateLimiter
}

func (srv *Server) resolveAgent(ctx context.Context, token string) (*agentpkg.Identity, error) {
	if token == "" {
		token = bearerFromContext(ctx)
	}
	if token == "" {
		return nil, fmt.Errorf("agent_token is required (or send Authorization: Bearer <token>)")
	}
	ag, err := srv.Agents.GetByTokenHash(ctx, agentpkg.HashToken(token))
	if err != nil {
		return nil, fmt.Errorf("invalid agent_token: %w", err)
	}
	if ag.IsRevoked() {
		return nil, fmt.Errorf("agent token has been revoked")
	}
	return ag, nil
}

// resolveIntegrator is resolveAgent's counterpart for
// policy.evaluate_transaction — an integrator token, never interchangeable
// with an agent_token (an integrator has no shopping permissions).
func (srv *Server) resolveIntegrator(ctx context.Context, token string) (*integratorpkg.Integrator, error) {
	if token == "" {
		return nil, fmt.Errorf("integrator_token is required")
	}
	integ, err := srv.Integrators.GetByTokenHash(ctx, agentpkg.HashToken(token))
	if err != nil {
		return nil, fmt.Errorf("invalid integrator_token: %w", err)
	}
	if integ.IsRevoked() {
		return nil, fmt.Errorf("integrator token has been revoked")
	}
	return integ, nil
}

// stripInternal returns a copy of q safe for an agent to see: CartID is a
// direct handle into a merchant connector's cart, and letting an agent hold
// it would let it manipulate the cart outside Algebra's
// discover/quote/policy/approve/execute flow entirely.
func stripInternal(q *quote.CheckoutQuote) quote.CheckoutQuote {
	public := *q
	public.CartID = ""
	return public
}

// NewMCPServer builds the go-sdk server and registers every tool against
// srv. Callers run it with server.Run(ctx, &gomcp.StdioTransport{}) for
// local/dev, or gomcp.NewStreamableHTTPHandler for a production HTTP
// deployment (see cmd/mcp).
func NewMCPServer(srv *Server) *gomcp.Server {
	s := gomcp.NewServer(&gomcp.Implementation{Name: "project-algebra", Version: "0.1.0"}, nil)
	s.AddReceivingMiddleware(bearerMiddleware, srv.rateLimitMiddleware)
	srv.registerCommerceTools(s)
	srv.registerSpendPassTools(s)
	srv.registerPaymentsTools(s)
	srv.registerProfilesTools(s)
	srv.registerPolicyTools(s)
	srv.registerPaymentIntentTools(s)
	return s
}
