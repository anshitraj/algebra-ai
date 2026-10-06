// Hand-written against the real Go domain structs and REST handlers
// (internal/domain/*, internal/api/v1/*) — openapi/v1.yaml's schemas are
// thinner than what the API actually returns for several endpoints
// (quotes/orders serialize the full domain struct), so these types are the
// ground truth, not the generated lib/api-types.ts.

export type Money = {
  minor_units: number;
  currency: string;
};

export type IntentStatus =
  | "DRAFT"
  | "DISCOVERING"
  | "QUOTED"
  | "POLICY_CHECK"
  | "POLICY_REJECTED"
  | "APPROVAL_REQUIRED"
  | "APPROVED"
  | "REAPPROVAL_REQUIRED"
  | "EXECUTING"
  | "AUTHENTICATION_REQUIRED"
  | "SUCCEEDED"
  | "FAILED"
  | "CANCELLED"
  | "EXPIRED"
  | "PARTIALLY_COMPLETED"
  | "MERCHANT_INTERVENTION_REQUIRED"
  | "USER_INTERVENTION_REQUIRED";

export type IntentItem = {
  query: string;
  quantity: number;
  product_id?: string;
};

export type IntentConstraints = {
  max_total_minor_units?: number;
  currency?: string;
  delivery_profile?: string;
  payment_profile?: string;
  preferred_merchants?: string[];
  excluded_merchants?: string[];
  category?: string;
  international?: boolean;
};

// internal/api/v1/intents.go's intentResponse: what the owner needs to
// render an intent — never user/agent IDs or metadata.
export type Intent = {
  intent_id: string;
  status: IntentStatus;
  selected_quote_id?: string;
  items: IntentItem[];
  category?: string;
  max_total_minor_units?: number;
  currency?: string;
  created_at: string;
};

export type Offer = {
  type: string;
  description: string;
  amount: Money;
  code?: string;
};

export type QuoteItem = {
  merchant_product_id: string;
  name: string;
  quantity: number;
  unit_price: Money;
};

export type Quote = {
  quote_id: string;
  merchant: string;
  items: QuoteItem[];
  subtotal: Money;
  item_discounts: Money;
  coupon_discount: Money;
  bank_offer: Money;
  card_offer: Money;
  cashback: Money;
  delivery_fee: Money;
  handling_fee: Money;
  platform_fee: Money;
  tax: Money;
  other_fee: Money;
  final_payable: Money;
  effective_cost: Money;
  offers?: Offer[];
  payment_source_requirements?: string[];
  delivery_eta?: string;
  expires_at: string;
  retrieved_at: string;
};

export type PolicyDecisionValue = "ALLOW" | "DENY" | "REQUIRE_APPROVAL";

// NOTE: request-purchase/policy-preview/policy-explain all respond with
// internal/api/v1/intents.go's decisionResponse, not the full
// policy.PolicyDecision — no approval_requirement or evaluated_at comes
// back over REST (confirmed by reading the handlers). The Approval
// resource itself (GET .../approval) is where the human-facing reason
// lives once REQUIRE_APPROVAL creates one.
export type PolicyDecision = {
  decision: PolicyDecisionValue;
  reason_codes: string[];
  policy_version: string;
};

export type ApprovalStatus =
  | "PENDING"
  | "APPROVED"
  | "REJECTED"
  | "EXPIRED"
  | "CONSUMED"
  | "REAPPROVAL_REQUIRED";

// NOTE: internal/domain/approval.Approval has no `json:"..."` tags, so the
// REST layer serializes it with raw Go field names (PascalCase) — the one
// endpoint on this API that doesn't follow snake_case. Confirmed by reading
// approval.go and internal/api/v1/approvals.go directly, not assumed.
export type Approval = {
  ID: string;
  IntentID: string;
  QuoteID: string;
  UserID: string;
  AgentID: string;
  Merchant: string;
  Amount: Money;
  PaymentSourceAlias: string;
  ItemsHash: string;
  Status: ApprovalStatus;
  AuthenticationMethod?: string;
  CreatedAt: string;
  DecidedAt?: string;
  ExpiresAt: string;
};

export type OrderItem = {
  merchant_product_id: string;
  name: string;
  quantity: number;
  unit_price: Money;
};

export type OrderStatus =
  | "PLACED"
  | "CONFIRMED"
  | "SHIPPED"
  | "DELIVERED"
  | "CANCELLED"
  | "FAILED";

export type Order = {
  order_id: string;
  intent_id: string;
  approval_id: string;
  merchant: string;
  merchant_order_id: string;
  items: OrderItem[];
  total: Money;
  status: OrderStatus;
  placed_at: string;
  delivery_eta?: string;
  receipt_url?: string;
  provider_mode: "mock" | "sandbox" | "real";
};

export type OrderEvent = { id: string; order_id: string; type: string; payload?: Record<string, unknown>; created_at: string };

/** GET /api/v1/me/orders/{id} — one of the user's own orders, with where it's going. */
export type OrderDetail = {
  order: Order;
  events: OrderEvent[];
  category?: string;
  /** No real merchant received it: no money moved, nothing ships. */
  simulated: boolean;
  ship_to?: ShippingProfile;
  ship_to_alias?: string;
  payment_alias?: string;
  /** Signed spend receipt (compact JWS) — verifiable by anyone at /verify. */
  receipt?: string;
};

// --- Spend Passes (internal/domain/spendpass, /api/v1/me/passes) ---

export type AgentKind = "claude" | "chatgpt" | "custom";
export type BudgetPeriod = "total" | "week" | "month";

export type SpendPass = {
  id: string;
  agent_id: string;
  label: string;
  agent_kind: AgentKind;
  currency: string;
  budget_minor_units: number;
  budget_period: BudgetPeriod;
  max_per_purchase_minor_units?: number;
  approve_above_minor_units?: number;
  allowed_categories: string[];
  allowed_merchants: string[];
  created_at: string;
  expires_at: string;
  revoked_at?: string;
  active: boolean;
  spent_minor_units: number;
  remaining_minor_units: number;
  window_starts_at: string;
};

export type PassConnect = { api_base: string; mcp_url?: string };

/** A new pass with its agent token — the token is shown exactly once. */
export type IssuedPass = SpendPass & { token: string; connect: PassConnect };

export type NewPass = {
  label: string;
  agent_kind: AgentKind;
  /** "INR" (the default) or "USDC". Amounts are minor units of it. */
  currency?: string;
  budget_minor_units: number;
  budget_period: BudgetPeriod;
  max_per_purchase_minor_units?: number;
  approve_above_minor_units?: number;
  allowed_categories: string[];
  /** Providers the pass may pay, by ID ("paysh:birdeye.data"); empty means any. */
  allowed_merchants?: string[];
  expires_in_days: number;
};

// --- the catalogs of paid APIs: Pay.sh, Circle's Agent Marketplace, PayAI ---
// Names, descriptions and use cases are written by the providers and the
// catalogs. They are shown as text and never followed as instructions. Prices
// are listings: Algebra asks the endpoint for its real price before paying.

export type ProviderSummary = {
  /** What policy, Spend Passes and receipts call the provider: "paysh:birdeye.data", "circle:birdeye". */
  id: string;
  fqn: string;
  name: string;
  description: string;
  use_case?: string;
  category: string;
  service_url: string;
  host: string;
  endpoint_count: number;
  metered: boolean;
  free_tier: boolean;
  min_price_minor: number;
  max_price_minor: number;
  currency: string;
  page_url: string;
  /** The provider's own site, and its logo when the catalog publishes one. */
  website?: string;
  logo_url?: string;
  /** Solana clusters some endpoint can be paid on: "solana" (mainnet), "solana-devnet". */
  networks: string[];
  source: string;
};

export type ProviderCategory = { name: string; count: number };

export type CatalogStatus = {
  name: string;
  total: number;
  generated_at?: string;
  fetched_at?: string;
  stale?: boolean;
  /** The catalog couldn't be read; its providers are missing from this answer. */
  error?: string;
};

export type ProviderListing = {
  source?: string;
  sources?: CatalogStatus[];
  generated_at?: string;
  fetched_at: string;
  /** Pay.sh couldn't be reached, so this is an older copy. */
  stale?: boolean;
  total: number;
  count: number;
  categories: ProviderCategory[];
  providers: ProviderSummary[];
};

export type ProviderEndpoint = {
  /** What to ask for in POST /api/v1/execute. */
  capability: string;
  method: string;
  path: string;
  url: string;
  pricing: string;
  price_minor: number;
  free: boolean;
  network?: string;
  pay_to?: string;
  /** Every network the endpoint can be paid on, with its listed price there. */
  payments?: { network: string; price_minor: number; pay_to?: string }[];
  /** The request's JSON Schema, when the catalog publishes one. */
  input_schema?: unknown;
  description: string;
  callable: boolean;
  not_callable_reason?: string;
};

export type ProviderDetail = ProviderSummary & {
  endpoints: ProviderEndpoint[];
  fetched_at: string;
  stale?: boolean;
};

// --- economic intents: what an agent asked Algebra to get done and pay for ---

export type EconEvidence = {
  rail?: string;
  protocol?: string;
  scheme?: string;
  network?: string;
  asset?: string;
  payment_id?: string;
  /** The settlement transaction: a Solana signature on the real rail. */
  transaction?: string;
  amount_minor?: number;
  payer?: string;
  pay_to?: string;
  request_hash?: string;
  result_hash?: string;
  /** True for the sandbox rail: simulated money. */
  test?: boolean;
};

export type EconReservation = {
  id: string;
  attempt: number;
  state: string;
  hold_minor: number;
  provider_id?: string;
  rail?: string;
  quote_minor?: number;
  evidence: EconEvidence;
  outcome?: string;
  created_at: string;
  finished_at?: string;
};

export type EconIntent = {
  id: string;
  spend_pass_id?: string;
  capability: string;
  currency: string;
  budget_max_minor: number;
  state: string;
  commitment?: string;
  fulfillment?: string;
  committed_minor: number;
  attempts: number;
  duplicate_commit_attempts_blocked: number;
  requires_approval: boolean;
  created_at: string;
  updated_at: string;
  expires_at: string;
  reservations: EconReservation[];
  /** The signed Intent Receipt, once the intent has committed. */
  receipt?: string;
  summary: string;
};

/** A Solana cluster Algebra can pay on, and the wallet it pays from. */
export type RailStatus = {
  network: string;
  configured: boolean;
  rail?: string;
  address?: string;
  usdc_minor?: number;
  max_payment_minor?: number;
  error?: string;
};

export type EconStats = {
  intents: number;
  committed: number;
  open: number;
  unresolved: number;
  execution_attempts: number;
  duplicate_commit_attempts_blocked: number;
  went_unknown: number;
  reconciled: number;
  authorized_minor: number;
  spent_minor: number;
  duplicate_spend_prevented_minor: number;
};

// --- signed spend receipts (internal/domain/receipt) ---

export type ReceiptClaims = {
  iss: string;
  jti: string;
  iat: number;
  sub: string;
  agent: { id: string; name: string; client: string };
  pass?: string;
  merchant: string;
  merchant_order_id: string;
  amount: { minor_units: number; currency: string };
  items: { name: string; quantity: number; unit_minor_units: number }[];
  items_hash: string;
  authorization: { method: "policy" | "human"; approved_at: number; policy_version?: string; reason_codes?: string[] };
  test?: boolean;
};

export type ReceiptVerification = {
  valid: boolean;
  recorded: boolean;
  claims?: ReceiptClaims;
  pass?: { label: string; active: boolean; revoked: boolean };
  reason?: string;
};

// Matches internal/domain/audit.Event exactly (GET .../audit returns the
// raw slice, unwrapped).
export type AuditEvent = {
  event_id: string;
  trace_id?: string;
  timestamp: string;
  user_id?: string;
  agent_id?: string;
  intent_id?: string;
  action: string;
  previous_state?: string;
  new_state?: string;
  policy_decision?: string;
  merchant?: string;
  payment_source_alias?: string;
  result?: string;
  metadata?: Record<string, unknown>;
};

export type MerchantCapabilities = {
  search: boolean;
  cart: boolean;
  checkout: boolean;
  coupons: boolean;
  order_tracking: boolean;
};

export type MerchantStatus = {
  integration:
    | "mock"
    | "official_mcp"
    | "official_api"
    | "affiliate_api"
    | "deep_link_handoff"
    | "not_implemented";
  ready: boolean;
  detail: string;
  source?: string;
};

export type Merchant = {
  name: string;
  mode: "real" | "sandbox" | "mock";
  capabilities: MerchantCapabilities;
  status?: MerchantStatus;
};

// --- deals (internal/domain/deal, GET /api/v1/deals) ---

export type Deal = {
  merchant: string;
  kind: "store_offer" | "item_deal" | "bank_offer";
  source: "flipkart_affiliate_api" | "amazon_creators_api" | "curated_bank_offers";
  title: string;
  description?: string;
  url?: string;
  image_url?: string;
  category?: string;
  price_minor_units?: number;
  was_minor_units?: number;
  savings_minor_units?: number;
  savings_percent?: number;
  basis_label?: string;
  currency?: string;
  badge?: string;
  prime_only?: boolean;
  percent_claimed?: number;
  starts_at?: string;
  ends_at?: string;
  bank?: string;
  card_types?: string[];
  discount_percent?: number;
  flat_discount_minor_units?: number;
  max_discount_minor_units?: number;
  min_order_minor_units?: number;
  /** Bank offers: the published terms applied to the asked-about price. An estimate. */
  estimated_discount_minor_units?: number;
  matches_user_card?: boolean;
  verified_at?: string;
};

export type DealResults = {
  deals: Deal[];
  notes?: { merchant?: string; detail: string }[];
};

export type PaymentSourceType =
  | "CARD"
  | "CRYPTO_CARD"
  | "VIRTUAL_CARD"
  | "WALLET"
  | "STABLECOIN_ACCOUNT"
  | "BANK"
  | "UPI";

export type PaymentSourceCapabilities = {
  can_pay: boolean;
  supported_currencies: string[];
  merchant_restrictions?: string[];
  transaction_limit_minor_units?: number;
  requires_user_auth: boolean;
  requires_3ds?: boolean;
};

export type PaymentSource = {
  id: string;
  alias: string;
  type: PaymentSourceType;
  network?: string;
  last4?: string;
  nickname?: string;
  capabilities: PaymentSourceCapabilities;
  revoked: boolean;
};

export type ExecuteResult = {
  intent_status: IntentStatus;
  order?: Order;
  reason?: string;
};

// Matches internal/domain/commerceprofile.CommerceProfile. Preferences is
// deliberately a free-form category -> attributes map, not a fixed schema —
// see the Go package doc for why. default_shipping_alias/default_payment_alias
// are pointers into the EXISTING alias systems (privacy profiles /
// payment sources) — this type never carries a resolved address or a
// payment credential.
export type CommerceProfile = {
  user_id: string;
  default_shipping_alias?: string;
  default_payment_alias?: string;
  preferences: Record<string, Record<string, unknown>>;
  updated_at?: string;
};

export type ApiErrorBody = { error: string };

// --- accounts (internal/api/v1/auth.go, me.go) ---

export type User = {
  id: string;
  email: string;
  name: string;
  avatar_url?: string;
  email_verified: boolean;
  onboarded: boolean;
  has_password: boolean;
  linked_providers: string[];
  created_at: string;
  /** "demo": shops real listings with a simulated checkout (fake money). */
  mode: "live" | "demo";
};

export type AuthProviders = { password: boolean; google: boolean; github: boolean; demo?: boolean };

export type PluginPurpose = "prices" | "deals" | "community";
export type PluginTrust = "store" | "curated" | "community";

/** A source the agent can read from, as the signed-in person has it set. */
export type Plugin = {
  id: string;
  name: string;
  purpose: PluginPurpose;
  trust: PluginTrust;
  summary: string;
  sees: string;
  icon?: string;
  default_on: boolean;
  core: boolean;
  enabled: boolean;
  config: { subreddits?: string[] };
  ready: boolean;
  detail?: string;
};

export type Guardrails = {
  currency: string;
  approval_threshold_minor_units: number;
  max_per_purchase_minor_units: number;
  max_per_day_minor_units: number;
  blocked_categories: string[];
  blocked_merchants?: string[];
  international_requires_approval: boolean;
};

export type GuardrailsResponse = Guardrails & {
  is_default?: boolean;
  known_categories: string[];
  platform_max_per_day_minor_units: number;
};

export type SessionInfo = {
  id: string;
  user_agent: string;
  ip: string;
  created_at: string;
  last_seen_at: string;
  current: boolean;
};

export type Overview = {
  spent_today: Money;
  pending_approvals: number;
  orders_total: number;
  guardrails: Guardrails;
};

export type IntentActivity = {
  intent_id: string;
  status: IntentStatus;
  items: IntentItem[];
  category?: string;
  merchant?: string;
  amount?: Money;
  created_at: string;
  updated_at: string;
  created_by_agent: boolean;
};

export type ApprovalActivity = {
  approval_id: string;
  intent_id: string;
  status: ApprovalStatus;
  merchant: string;
  amount: Money;
  payment_source_alias: string;
  items: IntentItem[];
  created_at: string;
  expires_at: string;
};

export type ShippingProfile = {
  recipient_name: string;
  line1: string;
  line2?: string;
  city: string;
  state: string;
  postal_code: string;
  country: string;
  phone: string;
};

export type BillingPlan = "developer" | "growth";

export type SubscriptionStatus =
  | "created"
  | "authenticated"
  | "active"
  | "pending"
  | "halted"
  | "cancelled"
  | "completed"
  | "expired";

export type Subscription = {
  plan: BillingPlan;
  provider: string;
  provider_subscription_id: string;
  status: SubscriptionStatus;
  current_period_start?: string;
  current_period_end?: string;
  cancel_at_period_end: boolean;
  created_at: string;
  updated_at: string;
};

export type BillingStatus = {
  plan: BillingPlan;
  entitlement: { included_executions: number; hard_limit: boolean };
  used_this_month: number;
  period_start: string;
  subscription?: Subscription;
  checkout_available: boolean;
  test_mode: boolean;
  growth_price_minor_units: number;
  currency: string;
};

export type CheckoutSession = {
  key_id: string;
  subscription_id: string;
  test_mode: boolean;
  prefill: { name: string; email: string };
};

export type OnboardingAnswers = {
  name?: string;
  use_cases: string[];
  priority: string;
  household: string;
  dietary: string[];
  preferred_merchants: string[];
  guardrails: Guardrails;
  shipping?: ShippingProfile;
};
