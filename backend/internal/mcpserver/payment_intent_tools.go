package mcpserver

import (
	"context"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/paymentintent"
)

// registerPaymentIntentTools wires the B2B agentic-payments surface for a
// tenant's agent: create+evaluate, read, and execute an AgenticPaymentIntent
// (internal/domain/paymentintent). There is deliberately no approve/reject
// tool here — approval is a human action taken in the tenant's own console
// via REST (POST /api/v1/payment-intents/{id}/approve), never something an
// agent can grant itself through its own tool-calling loop, the same
// safety boundary commerce.approve_purchase already carries for the
// commerce flow.
func (srv *Server) registerPaymentIntentTools(s *gomcp.Server) {
	gomcp.AddTool(s, &gomcp.Tool{
		Name: "payments.create_intent",
		Description: "Create and evaluate an agentic payment intent — a tenant's agent requesting to spend a bounded " +
			"amount at a specific merchant using an existing payment source alias. Evaluates against the tenant's " +
			"persisted policy synchronously. On status APPROVAL_REQUIRED, stop and tell the user a human must approve " +
			"it in the tenant's own console; never assume approval and never call payments.execute next. On DENIED, " +
			"explain the reason and stop.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in createPaymentIntentMCPInput) (*gomcp.CallToolResult, createPaymentIntentOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, createPaymentIntentOutput{}, err
		}
		result, err := srv.PaymentIntents.Create(ctx, app.CreatePaymentIntentInput{
			AgentID: ag.ID, Purpose: in.Purpose, Merchant: in.Merchant, MerchantDomain: in.MerchantDomain,
			Category: in.Category, International: in.International, AmountMinorUnits: in.AmountMinorUnits,
			Currency: in.Currency, ToleranceMinorUnits: in.ToleranceMinorUnits, ProductRef: in.ProductRef,
			PaymentSourceAlias: in.PaymentSourceAlias, RequestedCapability: in.RequestedCapability,
		})
		if err != nil {
			return nil, createPaymentIntentOutput{}, err
		}
		return nil, createPaymentIntentOutput{
			paymentIntentOutput: toPaymentIntentOutput(result.PaymentIntent),
			Decision:            toDecisionOutput(result.Decision),
		}, nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "payments.get_intent",
		Description: "Get the current status of an agentic payment intent.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in paymentIntentIDInput) (*gomcp.CallToolResult, paymentIntentOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, paymentIntentOutput{}, err
		}
		p, err := srv.PaymentIntents.Get(ctx, ag.ID, in.PaymentIntentID)
		if err != nil {
			return nil, paymentIntentOutput{}, err
		}
		return nil, toPaymentIntentOutput(p), nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name: "payments.execute",
		Description: "Execute an AUTHORIZED agentic payment intent through the payment provider. Only call this " +
			"after the intent's status is AUTHORIZED (policy allowed it, or a human approved it and told you to " +
			"continue) — never on APPROVAL_REQUIRED.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in executePaymentIntentInput) (*gomcp.CallToolResult, executePaymentIntentOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, executePaymentIntentOutput{}, err
		}
		outcome, err := srv.PaymentIntents.Execute(ctx, srv.Idempotency, in.IdempotencyKey, ag.ID, in.PaymentIntentID)
		if err != nil {
			return nil, executePaymentIntentOutput{}, err
		}
		out := executePaymentIntentOutput{Status: string(outcome.Status), Reason: outcome.Reason}
		if outcome.Result != nil {
			out.ProviderTransactionID = outcome.Result.ProviderTransactionID
		}
		return nil, out, nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "payments.get_status",
		Description: "Alias for payments.get_intent — get the current status of an agentic payment intent.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in paymentIntentIDInput) (*gomcp.CallToolResult, paymentIntentOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, paymentIntentOutput{}, err
		}
		p, err := srv.PaymentIntents.Get(ctx, ag.ID, in.PaymentIntentID)
		if err != nil {
			return nil, paymentIntentOutput{}, err
		}
		return nil, toPaymentIntentOutput(p), nil
	})
}

type createPaymentIntentMCPInput struct {
	AgentToken          string `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
	Purpose             string `json:"purpose,omitempty"`
	Merchant            string `json:"merchant"`
	MerchantDomain      string `json:"merchant_domain,omitempty"`
	Category            string `json:"category,omitempty"`
	International       bool   `json:"international,omitempty"`
	AmountMinorUnits    int64  `json:"amount_minor_units"`
	Currency            string `json:"currency"`
	ToleranceMinorUnits int64  `json:"tolerance_minor_units,omitempty"`
	ProductRef          string `json:"product_ref,omitempty"`
	PaymentSourceAlias  string `json:"payment_source_alias" jsonschema:"privacy alias, e.g. payment:personal"`
	RequestedCapability string `json:"requested_capability,omitempty"`
}

type paymentIntentIDInput struct {
	AgentToken      string `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
	PaymentIntentID string `json:"payment_intent_id"`
}

type executePaymentIntentInput struct {
	AgentToken      string `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
	PaymentIntentID string `json:"payment_intent_id"`
	IdempotencyKey  string `json:"idempotency_key,omitempty" jsonschema:"client-supplied key so a retried call cannot double-execute"`
}

type paymentIntentOutput struct {
	PaymentIntentID       string `json:"payment_intent_id"`
	Status                string `json:"status"`
	Merchant              string `json:"merchant"`
	AmountMinorUnits      int64  `json:"amount_minor_units"`
	Currency              string `json:"currency"`
	PolicyVersion         string `json:"policy_version,omitempty"`
	ProviderTransactionID string `json:"provider_transaction_id,omitempty"`
	ProviderStatus        string `json:"provider_status,omitempty"`
}

func toPaymentIntentOutput(p *paymentintent.AgenticPaymentIntent) paymentIntentOutput {
	return paymentIntentOutput{
		PaymentIntentID: p.ID, Status: string(p.Status), Merchant: p.Merchant,
		AmountMinorUnits: p.AmountMinorUnits, Currency: p.Currency,
		PolicyVersion: p.PolicyVersion, ProviderTransactionID: p.ProviderTransactionID, ProviderStatus: p.ProviderStatus,
	}
}

type createPaymentIntentOutput struct {
	paymentIntentOutput
	Decision decisionOutput `json:"decision"`
}

type executePaymentIntentOutput struct {
	Status                string `json:"status"`
	ProviderTransactionID string `json:"provider_transaction_id,omitempty"`
	Reason                string `json:"reason,omitempty"`
}
