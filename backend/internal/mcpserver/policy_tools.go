package mcpserver

import (
	"context"
	"fmt"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/policy"
)

// registerPolicyTools wires policy.evaluate_intent (a read-only preview —
// see PolicyService.PreviewDecision) and policy.explain_decision (surfaces
// the actually-recorded decision for an intent, not a generated
// explanation of it).
func (srv *Server) registerPolicyTools(s *gomcp.Server) {
	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "policy.evaluate_intent",
		Description: "Preview whether the intent's selected quote would currently be ALLOWed, DENYed, or REQUIRE_APPROVAL, without changing any state.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in intentIDInput) (*gomcp.CallToolResult, decisionOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, decisionOutput{}, err
		}
		dec, err := srv.Policy.PreviewDecision(ctx, ag.ID, in.IntentID)
		if err != nil {
			return nil, decisionOutput{}, err
		}
		return nil, toDecisionOutput(dec), nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "policy.explain_decision",
		Description: "Return the most recently recorded policy decision and reason codes for an intent.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in intentIDInput) (*gomcp.CallToolResult, decisionOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, decisionOutput{}, err
		}
		dec, err := srv.Policy.ExplainDecision(ctx, ag.ID, in.IntentID)
		if err != nil {
			return nil, decisionOutput{}, err
		}
		return nil, toDecisionOutput(dec), nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name: "policy.evaluate_transaction",
		Description: "Standalone policy decision for a third-party integrator's own transaction " +
			"(no Algebra purchase intent involved) — e.g. a wallet app asking whether a card charge " +
			"to a store should go through. Requires an integrator_token, not an agent_token: an " +
			"integrator has no Algebra shopping permissions, it can only ask for a decision.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in evaluateTransactionMCPInput) (*gomcp.CallToolResult, decisionOutput, error) {
		if in.IntegratorToken == "" {
			return nil, decisionOutput{}, fmt.Errorf("integrator_token is required")
		}
		if in.Conditions == nil {
			return nil, decisionOutput{}, fmt.Errorf(`missing "conditions" — Algebra does not assume a default budget policy for a third-party integrator; send at least an empty {} to explicitly allow everything`)
		}
		integ, err := srv.resolveIntegrator(ctx, in.IntegratorToken)
		if err != nil {
			return nil, decisionOutput{}, err
		}
		dec, err := srv.TransactionPolicy.EvaluateTransaction(ctx, integ.ID, app.EvaluateTransactionInput{
			UserRef:              in.UserRef,
			Category:             in.Category,
			Merchant:             in.Merchant,
			International:        in.International,
			AmountMinorUnits:     in.AmountMinorUnits,
			Currency:             in.Currency,
			SpendTodayMinorUnits: in.SpendTodayMinorUnits,
			PaymentRef:           in.PaymentRef,
			Rules:                in.Conditions.toRules(),
		})
		if err != nil {
			return nil, decisionOutput{}, err
		}
		return nil, toDecisionOutput(dec), nil
	})
}

type evaluateTransactionMCPInput struct {
	IntegratorToken      string                         `json:"integrator_token" jsonschema:"bearer token identifying the calling integrator (not an agent_token)"`
	UserRef              string                         `json:"user_ref" jsonschema:"the integrator's own opaque identifier for its user — Algebra never resolves or stores it"`
	Category             string                         `json:"category,omitempty"`
	Merchant             string                         `json:"merchant"`
	International        bool                           `json:"international,omitempty"`
	AmountMinorUnits     int64                          `json:"amount_minor_units"`
	Currency             string                         `json:"currency"`
	SpendTodayMinorUnits int64                          `json:"spend_today_minor_units,omitempty" jsonschema:"caller-supplied — Algebra keeps no ledger for transactions it never processes"`
	PaymentRef           string                         `json:"payment_ref,omitempty" jsonschema:"the integrator's own opaque payment-method reference"`
	Conditions           *evaluateTransactionConditions `json:"conditions" jsonschema:"the integrator's own budget/category/merchant policy — required, pass {} to explicitly allow everything"`
}

// evaluateTransactionConditions is policy.Rules's shape without the
// crypto-rail fields (no counterpart in this card/wallet-shaped tool) —
// mirrors internal/api/v1's conditionsPayload; kept as its own type rather
// than shared across the package boundary, same as decisionOutput already
// duplicates decisionResponse rather than importing internal/api/v1.
type evaluateTransactionConditions struct {
	MaxPerTransactionMinorUnits   int64    `json:"max_per_transaction_minor_units,omitempty"`
	MaxPerDayMinorUnits           int64    `json:"max_per_day_minor_units,omitempty"`
	ApprovalThresholdMinorUnits   int64    `json:"approval_threshold_minor_units,omitempty"`
	AllowedMerchants              []string `json:"allowed_merchants,omitempty"`
	BlockedMerchants              []string `json:"blocked_merchants,omitempty"`
	BlockedCategories             []string `json:"blocked_categories,omitempty"`
	AllowedPaymentProfiles        []string `json:"allowed_payment_profiles,omitempty"`
	InternationalRequiresApproval bool     `json:"international_requires_approval,omitempty"`
}

func (c evaluateTransactionConditions) toRules() policy.Rules {
	return policy.Rules{
		MaxPerTransactionMinorUnits:   c.MaxPerTransactionMinorUnits,
		MaxPerDayMinorUnits:           c.MaxPerDayMinorUnits,
		ApprovalThresholdMinorUnits:   c.ApprovalThresholdMinorUnits,
		AllowedMerchants:              c.AllowedMerchants,
		BlockedMerchants:              c.BlockedMerchants,
		BlockedCategories:             c.BlockedCategories,
		AllowedPaymentProfiles:        c.AllowedPaymentProfiles,
		InternationalRequiresApproval: c.InternationalRequiresApproval,
	}
}
