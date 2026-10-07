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
		Description: "Read the Spend Pass you are spending under: the budget in USDC and what is left of it this period, the most one call may cost, " +
			"the amount above which the person must approve, which providers it allows, its controls (calls per minute, what to do about a provider it has never paid, " +
			"whether it is frozen) and when it expires. Check it before asking for something expensive: Algebra refuses anything outside it, whatever you do. " +
			"Amounts are in micro-USDC (1000000 is one USDC).",
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
