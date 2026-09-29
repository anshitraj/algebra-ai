# Spend Passes and signed receipts

Algebra lets any AI agent spend a person's money **without holding it**: the
person gives the agent a *Spend Pass* instead of a card, and every purchase
the agent makes comes with a *signed receipt* that anyone can verify.

- **People** give Claude, ChatGPT or any bot a pass ("groceries, ₹2,000 a
  week, ask me above ₹500, 30 days") and revoke it with one click.
- **Businesses running agents** issue a pass per agent, per team or per
  customer: budgets, categories and approvals come built in, plus a receipt
  trail for audit.
- **Stores and payment companies** verify a receipt to know that the person
  really authorized that exact purchase, which settles "my AI bought this"
  disputes.

## How a pass decides

A pass never loosens anything. Every purchase must clear **both** the
person's own guardrails and the pass, and the stricter answer wins
(`spendpass.Combine`: DENY > REQUIRE_APPROVAL > ALLOW, with every reason
kept):

| Pass rule | Result when broken | Reason code |
|---|---|---|
| Revoked / expired | Deny | `PASS_REVOKED` / `PASS_EXPIRED` |
| Category outside `allowed_categories` (empty = any) | Deny | `PASS_CATEGORY_NOT_ALLOWED` |
| Store outside `allowed_merchants` (empty = any) | Deny | `PASS_MERCHANT_NOT_ALLOWED` |
| Over `max_per_purchase` | Deny | `PASS_PER_PURCHASE_LIMIT` |
| Spent + this > budget for the window (`total`, rolling `week` or `month`) | Deny | `PASS_BUDGET_EXCEEDED` |
| At or above `approve_above` (0 = always) | Ask the person | `PASS_APPROVAL_REQUIRED` |

It's checked when the agent requests the purchase, and **again right before
paying**, so a pass revoked after approval, or two purchases approved side
by side, can't spend past it. Spend comes from real orders
(`PassSpendLedger`), never from anything the agent reports. A pass's agent
holds only shopping permissions: approving, guardrails, addresses and
payment methods stay with the person.

## Connecting an agent

Create a pass in the console (**Spend passes**) or with
`POST /api/v1/me/passes`. The response carries the agent's token, **shown
once**. The agent then authenticates with `Authorization: Bearer <token>`:

- **REST:** everything under `/api/v1` an agent can call. `GET /api/v1/pass`
  returns the pass's limits and remaining budget. The execute response
  (`POST /api/v1/intents/{id}/execute`) includes `receipt`.
- **MCP** (streamable HTTP, `cmd/mcp -http`, published at `MCP_PUBLIC_URL`):
  send the token as an HTTP header; tool calls may leave `agent_token` empty,
  so the token never passes through the model. `algebra.spend_pass` reads the
  pass; `commerce.approve_purchase` returns the receipt.
  - Claude Code: `claude mcp add --transport http algebra $MCP_PUBLIC_URL --header "Authorization: Bearer $TOKEN"`
  - Claude Desktop / Cursor: `npx -y mcp-remote $MCP_PUBLIC_URL --header "Authorization: Bearer $TOKEN"`
  - OpenAI Agents SDK: `MCPServerStreamableHttp(params={"url": ..., "headers": {"Authorization": "Bearer ..."}})`
  - One-click connectors inside the ChatGPT and claude.ai apps need OAuth
    sign-in (not built yet).

## Receipts

Every placed order gets a receipt: a compact JWS, `alg: EdDSA` (Ed25519),
`typ: algebra-spend-receipt+jwt`, signed with a key derived from the
deployment's master key (HKDF, so there's nothing new to store, and old
receipts keep verifying). Claims:

```json
{
  "iss": "https://app.example", "jti": "rcpt_…", "iat": 1790647501,
  "sub": "person_c75e20522639a917f8f77db4",
  "agent": {"id": "agent_…", "name": "Claude — electronics", "client": "spend-pass:claude"},
  "pass": "pass_…",
  "merchant": "demo_checkout", "merchant_order_id": "DEMO-S8VPP8BN",
  "amount": {"minor_units": 45900, "currency": "INR"},
  "items": [{"name": "HP M120 Wireless Mouse — JioMart", "quantity": 1, "unit_minor_units": 41900}],
  "items_hash": "16a1a517…",
  "authorization": {"method": "policy", "approved_at": 1790647492, "policy_version": "user-guardrails-v1+pass:pass_…", "reason_codes": ["…", "PASS_OK"]},
  "test": true
}
```

- `sub` is a stable pseudonym (HMAC of the user ID). It links one person's
  receipts without saying who they are, and no personal data is included.
- `authorization.method` is `human` when the person approved this purchase
  themselves, and `policy` when it fell within their own limits.
- `test` marks simulated orders (demo checkout). The receipt itself is
  genuine.

**Verify** with the public key set at `/.well-known/jwks.json` (standard JWS
libraries handle Ed25519), with `POST /api/v1/receipts/verify
{"receipt": "…"}` (which also says whether Algebra has it on record and
whether the pass has since been revoked), or by pasting it at `/verify`. No
account is needed.
