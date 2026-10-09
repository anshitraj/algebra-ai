package mcpserver

import (
	"context"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/domain/commerceprofile"
	"github.com/project-algebra/algebra/internal/domain/privacy"
)

type profileAliasesOutput struct {
	Aliases []string `json:"aliases"`
}

// registerProfilesTools wires profiles.list_shipping_profiles and
// profiles.list_payment_profiles. Both return ALIASES ONLY — never a
// resolved address, phone, or payment credential. That resolution only
// ever happens inside privacy.Resolver, called from merchant-execution code
// paths that never serialize their result back through MCP (mandate §24).
func (srv *Server) registerProfilesTools(s *gomcp.Server) {
	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "profiles.list_shipping_profiles",
		Description: "List the calling user's shipping profile aliases (e.g. 'shipping:home'), never the resolved address.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in agentTokenOnlyInput) (*gomcp.CallToolResult, profileAliasesOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, profileAliasesOutput{}, err
		}
		aliases, err := srv.Privacy.ListAliases(ctx, ag.UserID, privacy.ProfileShipping)
		if err != nil {
			return nil, profileAliasesOutput{}, err
		}
		return nil, profileAliasesOutput{Aliases: aliases}, nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "profiles.list_payment_profiles",
		Description: "List the calling user's payment source aliases (e.g. 'payment:personal'), never card details.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in agentTokenOnlyInput) (*gomcp.CallToolResult, profileAliasesOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, profileAliasesOutput{}, err
		}
		sources, err := srv.Payments.ListSources(ctx, ag.ID, ag.UserID)
		if err != nil {
			return nil, profileAliasesOutput{}, err
		}
		aliases := make([]string, len(sources))
		for i, src := range sources {
			aliases[i] = src.Alias
		}
		return nil, profileAliasesOutput{Aliases: aliases}, nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name: "profiles.get_commerce_profile",
		Description: "Get the calling user's known shopping preferences (sizes, colors, style, dietary, ...) " +
			"and default shipping/payment aliases. Check this before asking a clarifying question you might " +
			"already know the answer to.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in agentTokenOnlyInput) (*gomcp.CallToolResult, commerceProfileOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, commerceProfileOutput{}, err
		}
		p, err := srv.CommerceProfiles.GetOrEmpty(ctx, ag.ID)
		if err != nil {
			return nil, commerceProfileOutput{}, err
		}
		return nil, toCommerceProfileOutput(p), nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name: "profiles.update_commerce_preferences",
		Description: "Save a stable preference the user just stated (e.g. their usual size or a color they " +
			"prefer) under one category, so it doesn't need to be asked again next time. Merges into whatever " +
			"is already known for that category — does not replace the whole profile.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in updateCommercePreferencesInput) (*gomcp.CallToolResult, commerceProfileOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, commerceProfileOutput{}, err
		}
		p, err := srv.CommerceProfiles.SetPreferences(ctx, ag.ID, in.Category, in.Attributes)
		if err != nil {
			return nil, commerceProfileOutput{}, err
		}
		return nil, toCommerceProfileOutput(p), nil
	})
}

type commerceProfileOutput struct {
	DefaultShippingAlias string                    `json:"default_shipping_alias,omitempty"`
	DefaultPaymentAlias  string                    `json:"default_payment_alias,omitempty"`
	Preferences          map[string]map[string]any `json:"preferences"`
}

func toCommerceProfileOutput(p *commerceprofile.CommerceProfile) commerceProfileOutput {
	return commerceProfileOutput{
		DefaultShippingAlias: p.DefaultShippingAlias,
		DefaultPaymentAlias:  p.DefaultPaymentAlias,
		Preferences:          p.Preferences,
	}
}

type updateCommercePreferencesInput struct {
	AgentToken string         `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
	Category   string         `json:"category" jsonschema:"e.g. 'clothing', 'shopping', 'food'"`
	Attributes map[string]any `json:"attributes" jsonschema:"flat key-value attributes to merge into this category, e.g. {\"usual_size\":\"L\"}"`
}
