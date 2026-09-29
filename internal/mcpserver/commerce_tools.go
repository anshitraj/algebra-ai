package mcpserver

import (
	"context"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/websearch"
	"github.com/project-algebra/algebra/policy"
)

// --- commerce.search_products / commerce.compare_products ---

type searchProductsInput struct {
	AgentToken string `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
	Query      string `json:"query" jsonschema:"free-text product search query"`
	Limit      int    `json:"limit,omitempty" jsonschema:"max results per merchant, default 5"`
	// MaxPriceMinorUnits is used by commerce.web_search only: listings priced
	// above it are dropped (paise for INR; 0 = no ceiling).
	MaxPriceMinorUnits int64 `json:"max_price_minor_units,omitempty" jsonschema:"web_search only: drop listings priced above this, in minor units (paise)"`
}

type searchProductsOutput struct {
	Results []app.MerchantSearchResult `json:"results"`
}

// --- commerce.find_deals ---

type findDealsInput struct {
	AgentToken      string   `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
	Query           string   `json:"query" jsonschema:"what the user is shopping for, e.g. 'wireless mouse'; empty returns headline offers"`
	Merchants       []string `json:"merchants,omitempty" jsonschema:"connector names to check, e.g. ['amazon','flipkart']; default every merchant that publishes deals"`
	PriceMinorUnits int64    `json:"price_minor_units,omitempty" jsonschema:"price of the item being considered, in minor units (paise): drops bank offers with a higher minimum order and estimates the rest"`
	Banks           []string `json:"banks,omitempty" jsonschema:"banks the user holds cards from, e.g. ['HDFC','SBI']: matching bank offers are flagged and listed first"`
	Limit           int      `json:"limit,omitempty" jsonschema:"max deals per merchant, default 5, max 10"`
}

func (srv *Server) registerCommerceTools(s *gomcp.Server) {
	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "commerce.search_products",
		Description: "Search for products by free-text query across every connected merchant that supports search. Merchants Algebra cannot search (e.g. Blinkit) appear with a handoff_url for the user to open instead of products.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in searchProductsInput) (*gomcp.CallToolResult, searchProductsOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, searchProductsOutput{}, err
		}
		results, err := srv.Discovery.SearchProducts(ctx, ag.ID, in.Query, in.Limit)
		if err != nil {
			return nil, searchProductsOutput{}, err
		}
		return nil, searchProductsOutput{Results: results}, nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name: "commerce.web_search",
		Description: "Last-resort fallback when commerce.search_products finds nothing on any connected merchant: general web search (title/snippet/link only, backed by Google Custom Search). " +
			"Never a priced product, never a cart, never an order — just links for the user to open. Returns not-implemented if no web-search fallback is configured.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in searchProductsInput) (*gomcp.CallToolResult, webSearchOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, webSearchOutput{}, err
		}
		results, err := srv.Discovery.SearchWebWithin(ctx, ag.ID, in.Query, in.Limit, in.MaxPriceMinorUnits)
		if err != nil {
			return nil, webSearchOutput{}, err
		}
		return nil, webSearchOutput{Results: results}, nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "commerce.compare_products",
		Description: "Like search_products, but merges results from every merchant and sorts by price ascending for easy comparison.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in searchProductsInput) (*gomcp.CallToolResult, searchProductsOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, searchProductsOutput{}, err
		}
		results, err := srv.Discovery.SearchProducts(ctx, ag.ID, in.Query, in.Limit)
		if err != nil {
			return nil, searchProductsOutput{}, err
		}
		merged := []app.MerchantSearchResult{{Merchant: "all", Products: flattenAndSortByPrice(results)}}
		for _, r := range results {
			if r.HandoffURL != "" {
				merged = append(merged, r) // handoff-only merchants have no price to sort by
			}
		}
		return nil, searchProductsOutput{Results: merged}, nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name: "commerce.find_deals",
		Description: "Find what's discounted for a product: each merchant's own published deals (Flipkart offers and Deals of the Day, Amazon price drops and time-boxed deals, via their official APIs) " +
			"plus curated bank/card offers with their terms, minimum order and end date. Informational only — nothing is applied to a cart, and no coupon code is ever returned because neither merchant's API publishes codes. " +
			"notes explain any merchant that returned nothing (e.g. not configured).",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in findDealsInput) (*gomcp.CallToolResult, app.DealResults, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, app.DealResults{}, err
		}
		res, err := srv.Discovery.FindDeals(ctx, ag.ID, app.DealQuery{
			Query: in.Query, Merchants: in.Merchants, PriceMinorUnits: in.PriceMinorUnits, Banks: in.Banks, Limit: in.Limit,
		})
		if err != nil {
			return nil, app.DealResults{}, err
		}
		return nil, *res, nil
	})

	// --- commerce.create_purchase_intent / get_purchase_intent ---

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "commerce.create_purchase_intent",
		Description: "Create a structured PurchaseIntent from item queries and spending constraints. Does not start discovery.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in createIntentInput) (*gomcp.CallToolResult, intentOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, intentOutput{}, err
		}
		items := make([]intent.Item, len(in.Items))
		for i, it := range in.Items {
			items[i] = intent.Item{Query: it.Query, Quantity: it.Quantity}
		}
		pi, err := srv.Intents.CreateIntent(ctx, srv.Idempotency, in.IdempotencyKey, app.CreateIntentInput{
			UserID:  ag.UserID,
			AgentID: ag.ID,
			Items:   items,
			Constraints: intent.Constraints{
				MaxTotalMinorUnits: in.Constraints.MaxTotalMinorUnits,
				Currency:           in.Constraints.Currency,
				DeliveryProfile:    in.Constraints.DeliveryProfile,
				PaymentProfile:     in.Constraints.PaymentProfile,
				PreferredMerchants: in.Constraints.PreferredMerchants,
				ExcludedMerchants:  in.Constraints.ExcludedMerchants,
				Category:           in.Constraints.Category,
			},
		})
		if err != nil {
			return nil, intentOutput{}, err
		}
		return nil, toIntentOutput(pi), nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "commerce.get_purchase_intent",
		Description: "Fetch the current state of a PurchaseIntent by ID.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in intentIDInput) (*gomcp.CallToolResult, intentOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, intentOutput{}, err
		}
		pi, err := srv.Intents.GetIntent(ctx, ag.ID, in.IntentID)
		if err != nil {
			return nil, intentOutput{}, err
		}
		return nil, toIntentOutput(pi), nil
	})

	// --- commerce.get_quotes / select_quote ---

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "commerce.get_quotes",
		Description: "Run merchant discovery (if not already done) and return normalized checkout quotes for a PurchaseIntent.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in intentIDInput) (*gomcp.CallToolResult, quotesOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, quotesOutput{}, err
		}
		pi, err := srv.Intents.GetIntent(ctx, ag.ID, in.IntentID)
		if err != nil {
			return nil, quotesOutput{}, err
		}
		var quotes []*quote.CheckoutQuote
		if pi.Status == intent.StateDraft {
			quotes, err = srv.Discovery.Discover(ctx, ag.ID, in.IntentID)
		} else {
			quotes, err = srv.Quotes.GetQuotes(ctx, ag.ID, in.IntentID)
		}
		if err != nil {
			return nil, quotesOutput{}, err
		}
		out := make([]quote.CheckoutQuote, len(quotes))
		for i, q := range quotes {
			out[i] = stripInternal(q)
		}
		return nil, quotesOutput{Quotes: out}, nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "commerce.select_quote",
		Description: "Record which quote the agent wants to proceed with, ahead of requesting purchase.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in selectQuoteInput) (*gomcp.CallToolResult, okOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, okOutput{}, err
		}
		if err := srv.Quotes.SelectQuote(ctx, ag.ID, in.IntentID, in.QuoteID); err != nil {
			return nil, okOutput{}, err
		}
		return nil, okOutput{OK: true}, nil
	})

	// --- commerce.request_purchase / approve_purchase / cancel_purchase ---

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "commerce.request_purchase",
		Description: "Evaluate the selected quote against policy. Moves the intent to POLICY_REJECTED (terminal), APPROVAL_REQUIRED, or APPROVED.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in intentIDInput) (*gomcp.CallToolResult, decisionOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, decisionOutput{}, err
		}
		dec, err := srv.Policy.EvaluateAndTransition(ctx, ag.ID, in.IntentID)
		if err != nil {
			return nil, decisionOutput{}, err
		}
		return nil, toDecisionOutput(dec), nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name: "commerce.approve_purchase",
		Description: "Proceed to execution for an intent that is already APPROVED (either auto-approved by policy, or approved by the " +
			"user through the approval UI/REST endpoint — this tool cannot itself grant an approval a human hasn't given).",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in executeInput) (*gomcp.CallToolResult, executeOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, executeOutput{}, err
		}
		outcome, err := srv.Orders.Execute(ctx, srv.Idempotency, in.IdempotencyKey, ag.ID, in.IntentID)
		if err != nil {
			return nil, executeOutput{}, err
		}
		return nil, toExecuteOutput(outcome), nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "commerce.cancel_purchase",
		Description: "Cancel a PurchaseIntent.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in intentIDInput) (*gomcp.CallToolResult, intentOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, intentOutput{}, err
		}
		pi, err := srv.Intents.CancelIntent(ctx, ag.ID, in.IntentID)
		if err != nil {
			return nil, intentOutput{}, err
		}
		return nil, toIntentOutput(pi), nil
	})

	// --- commerce.get_order_status / get_receipt / list_merchants ---

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "commerce.get_order_status",
		Description: "Get the order placed (if any) for a PurchaseIntent.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in intentIDInput) (*gomcp.CallToolResult, orderOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, orderOutput{}, err
		}
		ord, err := srv.Orders.GetOrderStatus(ctx, ag.ID, in.IntentID)
		if err != nil {
			return nil, orderOutput{}, err
		}
		return nil, orderOutput{Order: ord}, nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "commerce.get_receipt",
		Description: "Get the receipt for a completed PurchaseIntent.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in intentIDInput) (*gomcp.CallToolResult, orderOutput, error) {
		ag, err := srv.resolveAgent(ctx, in.AgentToken)
		if err != nil {
			return nil, orderOutput{}, err
		}
		ord, err := srv.Orders.GetReceipt(ctx, ag.ID, in.IntentID)
		if err != nil {
			return nil, orderOutput{}, err
		}
		return nil, orderOutput{Order: ord}, nil
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "commerce.list_merchants",
		Description: "List every merchant option with its honest capability matrix and readiness status: how it is integrated, whether it is usable now, and what setup it still needs.",
	}, func(ctx context.Context, _ *gomcp.CallToolRequest, in agentTokenOnlyInput) (*gomcp.CallToolResult, merchantsOutput, error) {
		if _, err := srv.resolveAgent(ctx, in.AgentToken); err != nil {
			return nil, merchantsOutput{}, err
		}
		return nil, merchantsOutput{Merchants: app.DescribeMerchants(srv.Connectors)}, nil
	})
}

func flattenAndSortByPrice(results []app.MerchantSearchResult) []merchant.Product {
	var all []merchant.Product
	for _, r := range results {
		all = append(all, r.Products...)
	}
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].PriceMinorUnits < all[j-1].PriceMinorUnits; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	return all
}

// --- shared input/output DTOs ---

type agentTokenOnlyInput struct {
	AgentToken string `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
}

type intentIDInput struct {
	AgentToken string `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
	IntentID   string `json:"intent_id"`
}

type executeInput struct {
	AgentToken     string `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
	IntentID       string `json:"intent_id"`
	IdempotencyKey string `json:"idempotency_key,omitempty" jsonschema:"client-supplied key so a retried call cannot double-execute"`
}

type selectQuoteInput struct {
	AgentToken string `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
	IntentID   string `json:"intent_id"`
	QuoteID    string `json:"quote_id"`
}

type itemInput struct {
	Query    string `json:"query"`
	Quantity int    `json:"quantity"`
}

type constraintsInput struct {
	MaxTotalMinorUnits int64    `json:"max_total_minor_units"`
	Currency           string   `json:"currency"`
	DeliveryProfile    string   `json:"delivery_profile,omitempty" jsonschema:"privacy alias, e.g. shipping:home"`
	PaymentProfile     string   `json:"payment_profile,omitempty" jsonschema:"privacy alias, e.g. payment:personal"`
	PreferredMerchants []string `json:"preferred_merchants,omitempty"`
	ExcludedMerchants  []string `json:"excluded_merchants,omitempty"`
	Category           string   `json:"category,omitempty"`
}

type createIntentInput struct {
	AgentToken     string           `json:"agent_token" jsonschema:"bearer token identifying the calling agent"`
	Items          []itemInput      `json:"items"`
	Constraints    constraintsInput `json:"constraints"`
	IdempotencyKey string           `json:"idempotency_key,omitempty"`
}

type intentOutput struct {
	IntentID        string `json:"intent_id"`
	Status          string `json:"status"`
	SelectedQuoteID string `json:"selected_quote_id,omitempty"`
}

func toIntentOutput(pi *intent.PurchaseIntent) intentOutput {
	return intentOutput{IntentID: pi.ID, Status: string(pi.Status), SelectedQuoteID: pi.SelectedQuoteID}
}

type quotesOutput struct {
	Quotes []quote.CheckoutQuote `json:"quotes"`
}

type okOutput struct {
	OK bool `json:"ok"`
}

type decisionOutput struct {
	Decision      string   `json:"decision"`
	ReasonCodes   []string `json:"reason_codes"`
	PolicyVersion string   `json:"policy_version"`
}

type orderOutput struct {
	Order *order.Order `json:"order,omitempty"`
}

type merchantsOutput struct {
	Merchants []app.MerchantInfo `json:"merchants"`
}

type webSearchOutput struct {
	Results []websearch.Result `json:"results"`
}

type executeOutput struct {
	IntentStatus string       `json:"intent_status"`
	Order        *order.Order `json:"order,omitempty"`
	Reason       string       `json:"reason,omitempty"`
	// Receipt is the signed spend receipt for a placed order — proof, for
	// the store or anyone, that the person authorized this purchase.
	Receipt string `json:"receipt,omitempty"`
}

func toDecisionOutput(dec *policy.PolicyDecision) decisionOutput {
	return decisionOutput{Decision: string(dec.Decision), ReasonCodes: dec.ReasonCodes, PolicyVersion: dec.PolicyVersion}
}

func toExecuteOutput(o *app.ExecuteOutcome) executeOutput {
	return executeOutput{IntentStatus: string(o.IntentStatus), Order: o.Order, Reason: o.Reason, Receipt: o.Receipt}
}
