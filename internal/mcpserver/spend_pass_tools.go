package mcpserver

import (
	"context"
	"errors"
	"fmt"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type spendPassInput struct {
	AgentToken string `json:"agent_token,omitempty" jsonschema:"bearer token identifying the calling agent; omit when the connection sends Authorization: Bearer"`
}

func (srv *Server) registerSpendPassTools(s *gomcp.Server) {
	gomcp.AddTool(s, &gomcp.Tool{
		Name: "algebra.spend_pass",
		Description: "Read the Spend Pass you're spending under: the budget and what's left of it this period, the most one purchase may cost, " +
			"what you may buy (categories) and where (stores), the amount above which the person must approve, and when the pass expires. " +
			"Check it before shopping so you only propose purchases the person has allowed. Purchases outside it are refused by Algebra, whatever you do.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in spendPassInput) (*gomcp.CallToolResult, app.PassView, error) {
		if srv.SpendPasses == nil {
			return nil, app.PassView{}, fmt.Errorf("spend passes are not enabled on this server")
		}
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, app.PassView{}, err
		}
		p, err := srv.SpendPasses.ForAgent(ctx, ag.ID)
		if errors.Is(err, shared.ErrNotFound) {
			return nil, app.PassView{}, fmt.Errorf("this agent has no Spend Pass — it spends under the person's own guardrails only")
		}
		if err != nil {
			return nil, app.PassView{}, err
		}
		return nil, *p, nil
	})
}
