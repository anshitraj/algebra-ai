package wiring

import "github.com/project-algebra/algebra/internal/mcpserver"

// MCP is what the MCP tool server runs on: the bundle's own services. The API
// serves it at /mcp and cmd/mcp serves it over stdio, so both offer the same
// tools over the same state, and a tool added to one is on the other.
//
// Serving it from the API matters for more than convenience: the sandbox
// provider and the loopback ports the HTTP client may reach exist in the
// process that hosts the sandbox, so execution through the sandbox rail only
// works there.
func (b *Bundle) MCP() *mcpserver.Server {
	return &mcpserver.Server{
		Agents: b.Agents, Intents: b.Intents, Discovery: b.Discovery, Quotes: b.Quotes,
		Policy: b.Policy, Orders: b.Orders, Payments: b.Payments, Privacy: b.Privacy,
		Connectors: b.Connectors, Idempotency: b.Idempotency, Limiter: b.Limiter,
		Integrators: b.Integrators, TransactionPolicy: b.TransactionPolicy,
		PaymentIntents: b.PaymentIntentSvc, CommerceProfiles: b.CommerceProfileSvc, SpendPasses: b.SpendPasses,
		Economic: b.Economic, Execution: b.Execution, Candidates: b.Candidates, Directory: b.Directory,
		Classes: b.Classes, Health: b.Health,
	}
}
