package mcpserver

import (
	"context"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/domain/payment"
)

type listSourcesInput struct {
	AgentToken string `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
}

type sourceView struct {
	ID           string               `json:"id"`
	Alias        string               `json:"alias"`
	Type         string               `json:"type"`
	Network      string               `json:"network,omitempty"`
	Last4        string               `json:"last4,omitempty"`
	Nickname     string               `json:"nickname,omitempty"`
	Capabilities payment.Capabilities `json:"capabilities"`
}

type listSourcesOutput struct {
	Sources []sourceView `json:"sources"`
}

type capabilityInput struct {
	AgentToken string `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
	Alias      string `json:"alias" jsonschema:"payment source alias, e.g. payment:personal"`
}

type capabilityOutput struct {
	Capabilities payment.Capabilities `json:"capabilities"`
}

// registerPaymentsTools wires payments.list_sources,
// payments.get_source_capabilities, and payments.get_spending_capability.
// The latter two answer the same underlying question (what can this
// payment source do) from the two angles the mandate names separately —
// both share one handler rather than pretending to compute something
// different twice.
func (srv *Server) registerPaymentsTools(s *gomcp.Server) {
	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "payments.list_sources",
		Description: "List the calling user's payment source aliases and their safe metadata (never a card number or provider token).",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in listSourcesInput) (*gomcp.CallToolResult, listSourcesOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, listSourcesOutput{}, err
		}
		sources, err := srv.Payments.ListSources(ctx, ag.ID, ag.UserID)
		if err != nil {
			return nil, listSourcesOutput{}, err
		}
		out := make([]sourceView, len(sources))
		for i, src := range sources {
			out[i] = sourceView{ID: src.ID, Alias: src.Alias, Type: string(src.Type), Network: src.Network, Last4: src.Last4, Nickname: src.Nickname, Capabilities: src.Capabilities}
		}
		return nil, listSourcesOutput{Sources: out}, nil
	})

	capabilityHandler := func(ctx context.Context, _ *gomcp.CallToolRequest, in capabilityInput) (*gomcp.CallToolResult, capabilityOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, capabilityOutput{}, err
		}
		caps, err := srv.Payments.GetSpendingCapability(ctx, ag.ID, ag.UserID, in.Alias)
		if err != nil {
			return nil, capabilityOutput{}, err
		}
		return nil, capabilityOutput{Capabilities: *caps}, nil
	}

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "payments.get_source_capabilities",
		Description: "Get the capability matrix (limits, currencies, auth requirements) for one payment source alias.",
	}, capabilityHandler)

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "payments.get_spending_capability",
		Description: "Alias of payments.get_source_capabilities, for agents that think in terms of 'how much can I spend' rather than 'source capabilities'.",
	}, capabilityHandler)
}
