// Package wiring constructs every application service exactly once from
// configuration, and is called by BOTH cmd/api and cmd/mcp. This is what
// makes "REST and MCP share the same domain logic" (mandate §53) a
// structural fact rather than a convention two entrypoints could drift
// apart on — there is only one place a service gets `new`'d.
package wiring

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/project-algebra/algebra/connectors/democheckout"
	"github.com/project-algebra/algebra/connectors/websearch"
	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/billing"
	"github.com/project-algebra/algebra/internal/domain/confidential"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/paymentprovider"
	"github.com/project-algebra/algebra/internal/domain/plugin"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/platform/bankoffers"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/identity"
	"github.com/project-algebra/algebra/internal/platform/postgres"
	redisplatform "github.com/project-algebra/algebra/internal/platform/redis"
	"github.com/project-algebra/algebra/internal/platform/resilience"
	"github.com/project-algebra/algebra/policy"
	"github.com/project-algebra/algebra/providers/arcium"
	"github.com/project-algebra/algebra/providers/paymentdemo"
	"github.com/project-algebra/algebra/providers/razorpay"
	"github.com/project-algebra/algebra/providers/vault"
)

// Bundle is every long-lived, shared dependency a transport (REST or MCP)
// needs. Transports hold a *Bundle and never construct a service
// themselves.
type Bundle struct {
	DB *postgres.DB

	Agents            *postgres.AgentRepo
	AgentSvc          *app.AgentService
	Integrators       *postgres.IntegratorRepo
	IntegratorSvc     *app.IntegratorService
	TransactionPolicy *app.TransactionPolicyService
	Users             *app.UserService
	Intents           *app.IntentService
	Discovery         *app.DiscoveryService
	Quotes            *app.QuoteService
	Policy            *app.PolicyService
	Approvals         *app.ApprovalService
	Orders            *app.OrderService
	Payments          *app.PaymentService
	Privacy           *privacy.Resolver
	Connectors        *app.ConnectorRegistry
	Idempotency       app.IdempotencyStore
	Audit             *postgres.AuditRepo

	CommerceProfiles   *postgres.CommerceProfileRepo
	CommerceProfileSvc *app.CommerceProfileService

	// --- Human accounts for the first-party web app (internal/domain/account) ---
	Billing    *app.BillingService
	Plugins    *app.PluginService
	Accounts   *app.AccountService
	Activity   *app.ActivityService
	Onboarding *app.OnboardingService
	Demo       *app.DemoService

	// --- Spend Passes and signed receipts (internal/domain/spendpass, receipt) ---
	SpendPasses *app.SpendPassService
	Receipts    *app.ReceiptService
	// MCPPublicURL: see config.Config.MCPPublicURL.
	MCPPublicURL string
	// OAuthProviders holds only the providers with credentials configured,
	// keyed by name ("google", "github").
	OAuthProviders map[string]identity.Provider
	AuthConfig     config.AuthConfig
	// OAuthStateKey signs the short-lived OAuth state/PKCE cookie. Derived
	// from the master key, never used for anything else.
	OAuthStateKey []byte

	// --- B2B agentic-payments infrastructure (see internal/domain/tenant) ---
	Tenants          *postgres.TenantRepo
	TenantSvc        *app.TenantService
	PolicySets       *postgres.PolicySetRepo
	PolicySetSvc     *app.PolicySetService
	PaymentIntents   *postgres.PaymentIntentRepo
	PaymentIntentSvc *app.PaymentIntentService
	WebhookEndpoints *postgres.WebhookEndpointRepo
	WebhookDispatch  *app.WebhookDispatchService
	// PaymentProvider is the only payment rail actually wired live in this
	// build — providers/paymentdemo.Provider, deterministic and in-memory.
	// Real rails (Visa Intelligent Commerce, Mastercard Agent Pay, ...) are
	// a registration here once real credentials exist, not a rewrite.
	PaymentProvider    paymentprovider.Provider
	CapabilityResolver *app.PaymentCapabilityResolver

	// Redis is nil if REDIS_ADDR was unset or unreachable at startup —
	// every consumer (RateLimiter, Locker below) degrades gracefully when
	// nil rather than failing, per mandate §35's "Postgres remains
	// authoritative" principle applied to Redis too.
	Redis   *redisplatform.Client
	Limiter app.RateLimiter

	Webhooks *app.WebhookService
	AuditSvc *app.AuditService

	// Confidential is the optional confidential-compute provider (mandate
	// §26). It is always constructed with the LOCAL implementation (real
	// AES-256-GCM, no external dependency) so the abstraction is live
	// rather than dead code; swapping in providers/arcium.ArciumProvider
	// requires a real Arcium program/cluster, which no environment here
	// has.
	Confidential confidential.Provider
}

// Build connects to Postgres, runs migrations, registers every merchant
// connector this build ships, and constructs every application service.
// migrationsDir should point at the repo's migrations/ folder.
func Build(ctx context.Context, cfg *config.Config, migrationsDir string) (*Bundle, error) {
	db, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("wiring: connecting to postgres: %w", err)
	}
	if err := db.Migrate(ctx, migrationsDir); err != nil {
		db.Close()
		return nil, fmt.Errorf("wiring: running migrations: %w", err)
	}

	masterKey, err := cfg.MasterKey()
	if err != nil {
		db.Close()
		return nil, err
	}
	encryptor, err := privacy.NewAESGCMEncryptor(masterKey)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("wiring: building encryptor: %w", err)
	}

	agents := postgres.NewAgentRepo(db)
	intents := postgres.NewIntentRepo(db)
	quotes := postgres.NewQuoteRepo(db)
	decisions := postgres.NewPolicyDecisionRepo(db)
	approvals := postgres.NewApprovalRepo(db)
	paymentSources := postgres.NewPaymentSourceRepo(db)
	orders := postgres.NewOrderRepo(db)
	auditRepo := postgres.NewAuditRepo(db)
	ledger := postgres.NewSpendLedgerRepo(db)
	idempotency := postgres.NewIdempotencyRepo(db)
	privacyRepo := postgres.NewPrivacyProfileRepo(db)
	users := postgres.NewUserRepo(db)
	tenants := postgres.NewTenantRepo(db)
	policySets := postgres.NewPolicySetRepo(db)
	paymentIntents := postgres.NewPaymentIntentRepo(db)
	webhookEndpoints := postgres.NewWebhookEndpointRepo(db)
	commerceProfiles := postgres.NewCommerceProfileRepo(db)

	connectors, err := buildConnectors(cfg.Merchants, encryptor)
	if err != nil {
		db.Close()
		return nil, err
	}
	warmConnectors(connectors)

	// LocalProvider is the sole policy authority — merchant/category/INR and
	// crypto-rail (recipient/USDC) purchases alike — deterministic, no
	// external service to reach. See policy.Rules and policy.Provider's doc
	// comments.
	var policyProvider policy.Provider = policy.NewLocalProvider(policy.DefaultRules())

	cardVault := vault.NewSandboxProvider()

	privacyResolver := privacy.NewResolver(privacyRepo, encryptor, &postgres.ResolutionAuditSink{Logger: auditRepo, Now: time.Now})

	intentSvc := app.NewIntentService(intents, agents, auditRepo)
	discoverySvc := app.NewDiscoveryService(intents, agents, quotes, connectors, auditRepo, cfg.QuoteTTL)
	discoverySvc.SetResilience(resilience.NewRegistry(5, 30*time.Second), cfg.Merchants.WithDefaults().ConnectorTimeout)
	urlAllowlist := merchant.NewAllowedDomains(cfg.MerchantURLAllowlist...)
	discoverySvc.SetURLAllowlist(urlAllowlist)
	// Bank/card offers are curated by the operator (neither Amazon nor
	// Flipkart publishes them through an API); off unless BANK_OFFERS_FILE
	// is set, and FindDeals says so.
	if path := cfg.Merchants.BankOffersFile; path != "" {
		discoverySvc.SetBankOffers(bankoffers.NewFile(path, urlAllowlist))
	}
	// General web-search fallback is optional: off (commerce.web_search
	// returns ErrNotImplemented) unless both env vars are set — SetWebSearcher
	// is simply never called otherwise, and DiscoveryService.SearchWeb's nil
	// check is what reports that honestly.
	// Gemini with Google Search grounding is preferred: live results with
	// prices, on the same key the agent already uses. Custom Search remains
	// for deployments that have it (Google closed it to new customers).
	var gemini *websearch.Gemini
	switch {
	case cfg.GeminiAPIKey != "":
		gemini = websearch.NewGemini(websearch.GeminiConfig{
			APIKey: cfg.GeminiAPIKey,
			Model:  cfg.GeminiSearchModel,
		})
		discoverySvc.SetWebSearcher(gemini)
	case cfg.GoogleSearchAPIKey != "" && cfg.GoogleSearchEngineID != "":
		discoverySvc.SetWebSearcher(websearch.New(websearch.Config{
			APIKey:         cfg.GoogleSearchAPIKey,
			SearchEngineID: cfg.GoogleSearchEngineID,
		}))
	}
	// Plugins: each person switches the agent's deal and community sources on
	// or off (internal/domain/plugin). A plugin this server can't run shows
	// as needing setup instead of silently returning nothing.
	unready := map[string]string{}
	if gemini == nil && (cfg.GoogleSearchAPIKey == "" || cfg.GoogleSearchEngineID == "") {
		unready[plugin.WebPrices] = "Needs GEMINI_API_KEY on the server."
	}
	if cfg.Merchants.BankOffersFile == "" {
		unready[plugin.BankOffers] = "Needs a curated bank offers file (BANK_OFFERS_FILE) on the server."
	}
	if cfg.Merchants.AmazonCredentialID == "" || cfg.Merchants.AmazonCredentialSecret == "" {
		unready[plugin.AmazonDeals] = "Needs Amazon Creators API keys on the server."
	}
	if cfg.Merchants.FlipkartAffiliateID == "" || cfg.Merchants.FlipkartAffiliateToken == "" {
		unready[plugin.FlipkartOffers] = "Needs Flipkart affiliate keys on the server."
	}
	if gemini == nil {
		unready[plugin.RedditDeals] = "Needs GEMINI_API_KEY on the server."
		unready[plugin.DesiDimeDeals] = "Needs GEMINI_API_KEY on the server."
	}
	pluginSvc := app.NewPluginService(postgres.NewPluginRepo(db), agents, unready)
	discoverySvc.SetPlugins(pluginSvc)
	if gemini != nil {
		if cfg.TavilyAPIKey != "" {
			discoverySvc.SetCommunitySearcher(websearch.NewTavily(websearch.TavilyConfig{APIKey: cfg.TavilyAPIKey}, gemini))
		} else {
			discoverySvc.SetCommunitySearcher(gemini)
		}
	}

	// Demo accounts check out through the demo store: real listings from the
	// same cached web search the agent uses, simulated checkout, fake money.
	// It's registered only when demo accounts are on; live accounts are never
	// routed to it (SetAccountModes below).
	if cfg.Auth.DemoAccounts {
		var search democheckout.SearchFunc
		if cfg.GeminiAPIKey != "" || (cfg.GoogleSearchAPIKey != "" && cfg.GoogleSearchEngineID != "") {
			search = discoverySvc.WebListings
		}
		demo := democheckout.New(search)
		connectors.Register(demo)
		discoverySvc.SetListingObserver(demo.Remember)
	}
	quoteSvc := app.NewQuoteService(intents, agents, quotes, connectors)
	policySvc := app.NewPolicyService(intents, agents, quotes, decisions, approvals, ledger, policyProvider, auditRepo, cfg.ApprovalTTL)
	approvalSvc := app.NewApprovalService(intents, paymentIntents, approvals, quoteSvc, auditRepo)
	orderSvc := app.NewOrderService(intents, agents, approvals, orders, quoteSvc, connectors, policyProvider, ledger, auditRepo, cfg.QuoteAmountToleranceMinorUnits)
	// The privacy resolver is what turns "shipping:home" into a real
	// address, once, inside a checkout call — without this line the whole
	// alias mechanism would be decorative (mandate §24).
	orderSvc.SetPrivacyResolver(privacyResolver)
	paymentSvc := app.NewPaymentService(paymentSources, agents, cardVault)
	agentSvc := app.NewAgentService(agents)
	integrators := postgres.NewIntegratorRepo(db)
	integratorSvc := app.NewIntegratorService(integrators)
	transactionPolicySvc := app.NewTransactionPolicyService(integrators, auditRepo)
	userSvc := app.NewUserService(users)
	commerceProfileSvc := app.NewCommerceProfileService(commerceProfiles, agents)

	var mailer app.Mailer = identity.LogMailer{Logger: slog.Default()}
	if cfg.Auth.ResendAPIKey != "" {
		mailer = identity.NewResendMailer(cfg.Auth.ResendAPIKey, cfg.Auth.EmailFrom)
	}
	accountSvc := app.NewAccountService(postgres.NewAccountRepo(db), agentSvc, agents, encryptor, mailer, postgres.NewGuardrailRepo(db), cfg.Auth.SessionTTL)
	discoverySvc.SetAccountModes(accountSvc)
	// Every policy evaluation — at request-purchase and again at payment
	// time — uses the user's own guardrails when they've set any.
	policySvc.SetUserRules(accountSvc)
	orderSvc.SetUserRules(accountSvc)
	activitySvc := app.NewActivityService(postgres.NewActivityRepo(db), ledger)

	// Billing for Algebra's own plans. No keys → no gateway: everyone stays
	// on the free plan and checkout reports "not configured".
	var gateway billing.Gateway
	if cfg.Billing.RazorpayKeyID != "" && cfg.Billing.RazorpayKeySecret != "" {
		gateway = razorpay.Gateway{Client: razorpay.New(razorpay.Config{
			KeyID: cfg.Billing.RazorpayKeyID, KeySecret: cfg.Billing.RazorpayKeySecret,
			WebhookSecret: cfg.Billing.RazorpayWebhookSecret,
		})}
	}
	billingSvc := app.NewBillingService(postgres.NewBillingRepo(db), gateway, app.BillingConfig{
		GrowthPlanID: cfg.Billing.GrowthPlanID, GrowthAmountMinor: cfg.Billing.GrowthPriceMinor, Currency: "INR",
	})
	orderSvc.SetExecutionGate(billingSvc)

	// Spend Passes: every purchase by an agent holding one clears the pass
	// as well as the person's guardrails, and every placed order gets a
	// receipt signed with a key derived from the master key.
	passRepo := postgres.NewSpendPassRepo(db)
	spendPassSvc := app.NewSpendPassService(passRepo, agentSvc, passRepo)
	policySvc.SetSpendPasses(spendPassSvc)
	orderSvc.SetSpendPasses(spendPassSvc)
	signer, err := receipt.NewSigner(masterKey)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("wiring: receipt signer: %w", err)
	}
	receiptSvc := app.NewReceiptService(signer, cfg.Auth.PublicWebURL, postgres.NewReceiptRepo(db), agents, decisions, passRepo)
	orderSvc.SetReceipts(receiptSvc)
	onboardingSvc := app.NewOnboardingService(accountSvc, commerceProfileSvc, privacyResolver)
	demoSvc := app.NewDemoService(accountSvc, onboardingSvc)
	oauthProviders := map[string]identity.Provider{}
	if cfg.Auth.GoogleClientID != "" && cfg.Auth.GoogleClientSecret != "" {
		oauthProviders["google"] = identity.NewGoogle(cfg.Auth.GoogleClientID, cfg.Auth.GoogleClientSecret)
	}
	if cfg.Auth.GitHubClientID != "" && cfg.Auth.GitHubClientSecret != "" {
		oauthProviders["github"] = identity.NewGitHub(cfg.Auth.GitHubClientID, cfg.Auth.GitHubClientSecret)
	}
	stateMAC := hmac.New(sha256.New, masterKey)
	stateMAC.Write([]byte("algebra:oauth-state:v1"))
	oauthStateKey := stateMAC.Sum(nil)
	webhookSvc := app.NewWebhookService(postgres.NewWebhookRepo(db), auditRepo, envWebhookSecret)
	auditSvc := app.NewAuditService(auditRepo, agents)
	confidentialProvider := arcium.NewLocalEncryptedProvider(encryptor)

	// B2B agentic-payments infrastructure. paymentdemo.Provider is the only
	// payment rail actually wired live in this build — see PaymentProvider's
	// doc comment on Bundle.
	tenantSvc := app.NewTenantService(tenants)
	policySetSvc := app.NewPolicySetService(policySets)
	webhookDispatchSvc := app.NewWebhookDispatchService(webhookEndpoints)
	var demoProvider paymentprovider.Provider = paymentdemo.New()
	paymentIntentSvc := app.NewPaymentIntentService(paymentIntents, agents, users, approvals, policySetSvc, demoProvider, auditRepo, cfg.ApprovalTTL)
	paymentIntentSvc.SetWebhookDispatcher(webhookDispatchSvc)
	capabilityResolver := app.NewPaymentCapabilityResolver(demoProvider)

	// Redis is optional (mandate §35) — a missing or unreachable REDIS_ADDR
	// degrades to "no rate limiting, no fast-fail lock" rather than
	// preventing startup. Postgres already provides the real correctness
	// guarantees (approvals.MarkConsumed) that don't depend on this.
	var redisClient *redisplatform.Client
	var limiter app.RateLimiter
	if cfg.RedisAddr != "" {
		rc, err := redisplatform.Connect(ctx, cfg.RedisAddr)
		if err != nil && cfg.IsProduction() {
			db.Close()
			return nil, fmt.Errorf("wiring: Redis unavailable at %q (required in production): %w", cfg.RedisAddr, err)
		}
		if err != nil {
			log.Printf("wiring: Redis unavailable at %q, continuing without rate limiting/locks: %v", cfg.RedisAddr, err)
		} else {
			redisClient = rc
			limiter = rc
			orderSvc.SetLocker(rc)
			// Web search costs money per query — don't pay twice for the
			// same product within a few minutes.
			discoverySvc.SetSearchCache(rc, cfg.WebSearchCacheTTL)
		}
	}

	return &Bundle{
		DB: db, Agents: agents, AgentSvc: agentSvc, Integrators: integrators, IntegratorSvc: integratorSvc, TransactionPolicy: transactionPolicySvc,
		Users: userSvc, Intents: intentSvc, Discovery: discoverySvc, Quotes: quoteSvc,
		Policy: policySvc, Approvals: approvalSvc, Orders: orderSvc, Payments: paymentSvc,
		Privacy: privacyResolver, Connectors: connectors, Idempotency: idempotency, Audit: auditRepo,
		CommerceProfiles: commerceProfiles, CommerceProfileSvc: commerceProfileSvc,
		Billing: billingSvc, Plugins: pluginSvc, Accounts: accountSvc, Activity: activitySvc, Onboarding: onboardingSvc, Demo: demoSvc, SpendPasses: spendPassSvc, Receipts: receiptSvc, MCPPublicURL: cfg.MCPPublicURL, OAuthProviders: oauthProviders,
		AuthConfig: cfg.Auth, OAuthStateKey: oauthStateKey,
		Redis: redisClient, Limiter: limiter,
		Webhooks: webhookSvc, AuditSvc: auditSvc, Confidential: confidentialProvider,

		Tenants: tenants, TenantSvc: tenantSvc,
		PolicySets: policySets, PolicySetSvc: policySetSvc,
		PaymentIntents: paymentIntents, PaymentIntentSvc: paymentIntentSvc,
		WebhookEndpoints: webhookEndpoints, WebhookDispatch: webhookDispatchSvc,
		PaymentProvider: demoProvider, CapabilityResolver: capabilityResolver,
	}, nil
}

// envWebhookSecret looks up WEBHOOK_SECRET_<PROVIDER> (uppercased). No real
// webhook-sending provider is configured in this environment, so every
// lookup returns "" today — see app.WebhookService.Receive, which rejects
// any provider with no configured secret rather than accepting it
// unverified.
func envWebhookSecret(provider string) string {
	return os.Getenv("WEBHOOK_SECRET_" + strings.ToUpper(provider))
}
