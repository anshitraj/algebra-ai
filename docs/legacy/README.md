# Legacy documents

Algebra began as a grocery shopping agent for India (Swiggy Instamart, Zepto, Amazon, Flipkart, Blinkit), and then as agentic-payments infrastructure for card apps and wallets: tenants, policy sets, `AgenticPaymentIntent`s and a payment-provider interface. That product still builds, its tests pass and its code is kept (`connectors/`, `internal/domain/intent`, `internal/domain/merchant`, `internal/domain/paymentintent`, and the `commerce.*` and `payments.*` MCP tools), because web search and the merchant connectors are useful if people actually search for things.

It is not what Algebra is now. Algebra is the router and spend firewall for AI agents that pay for APIs on Solana: start at the [README](../../README.md).

The documents in this folder describe the original product and are kept as they were, with their links repaired:

| | |
|---|---|
| [MERCHANT_CONNECTORS.md](MERCHANT_CONNECTORS.md) | What each merchant connector can really do, and what it needs. |
| [ONDC_CONNECTOR.md](ONDC_CONNECTOR.md) | A scope for an ONDC connector that was never built. |
| [B2B_INTEGRATION.md](B2B_INTEGRATION.md), [AGENTIC_PAYMENT_INTENT.md](AGENTIC_PAYMENT_INTENT.md), [INTEGRATING.md](INTEGRATING.md) | Tenants, policy sets and `AgenticPaymentIntent` for card apps and wallets; the policy engine used standalone. |
| [PAYMENT_PROVIDER_INTERFACE.md](PAYMENT_PROVIDER_INTERFACE.md), [PROVIDER_STATUS.md](PROVIDER_STATUS.md), [PAYMENT_SECURITY.md](PAYMENT_SECURITY.md) | The payment-provider interface, which integrations were real, sandbox or stubs, and the card-data rules. |
| [PRIVACY.md](PRIVACY.md) | The alias boundary for addresses and payment details in the shopping flow. |
| [BUILD_PLAN.md](BUILD_PLAN.md) | The original repository audit and build plan, a point-in-time snapshot. |

The REST spec for that surface is [openapi/v1.yaml](../../openapi/v1.yaml). The execution API of the current product is [openapi/execution.yaml](../../openapi/execution.yaml).
