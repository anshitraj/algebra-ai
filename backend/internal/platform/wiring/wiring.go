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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/connectors/democheckout"
	"github.com/project-algebra/algebra/connectors/websearch"
	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/billing"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/confidential"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/paymentprovider"
	"github.com/project-algebra/algebra/internal/domain/plugin"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/bankoffers"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/identity"
	"github.com/project-algebra/algebra/internal/platform/postgres"
	redisplatform "github.com/project-algebra/algebra/internal/platform/redis"
	"github.com/project-algebra/algebra/internal/platform/resilience"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/spendpass"
	"github.com/project-algebra/algebra/policy"
	"github.com/project-algebra/algebra/providers/arcium"
	"github.com/project-algebra/algebra/providers/bazaar"
	"github.com/project-algebra/algebra/providers/catalog"
	"github.com/project-algebra/algebra/providers/jupiter"
	"github.com/project-algebra/algebra/providers/monid"
	"github.com/project-algebra/algebra/providers/paymentdemo"
	"github.com/project-algebra/algebra/providers/paysh"
	"github.com/project-algebra/algebra/providers/razorpay"
	"github.com/project-algebra/algebra/providers/sandboxpay"
	"github.com/project-algebra/algebra/providers/solanax402"
	"github.com/project-algebra/algebra/providers/vault"
	"github.com/project-algebra/algebra/providers/webdiscovery"
	"github.com/project-algebra/algebra/providers/x402client"
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

	// --- Economic coordination (internal/domain/econ) ---
	Economic       *app.EconomicService
	IntentReceipts *app.IntentReceiptService
	// SandboxProvider is the SANDBOX x402 provider, nil unless
	// ECONOMIC_SANDBOX is on (see config.Config.EconomicSandbox).
	SandboxProvider *sandboxpay.Provider
	// SandboxPersonas are the sandbox providers of token.price (alpha, beta,
	// flaky, greedy, trap), nil unless ECONOMIC_SANDBOX is on.
	SandboxPersonas *sandboxpay.PersonaServer
	// Execution runs routed work: it drives the coordinator, makes the paid
	// x402 call and judges the result (internal/domain/routing).
	Execution *app.ExecutionService
	// Candidates turns what an agent asks for into candidates: the providers
	// the operator pinned (ECONOMIC_PROVIDERS) and, in sandbox mode, the
	// simulated one, plus the Pay.sh catalog and the endpoints an agent
	// supplies itself.
	Candidates app.CandidateResolver
	// Classes groups the catalogs' endpoints by the work they do, for routing
	// across providers and for the console's comparison. Nil when the
	// catalogs are off.
	Classes *catalog.ClassIndex
	// Health probes providers without paying, for the router and console.
	Health *app.HealthService
	// SolanaRails are the Solana payment rails running (mainnet, devnet or
	// both), for the console's wallet status.
	SolanaRails []*solanax402.Rail
	// OnchainPasses links Spend Passes to Algebra's program on Solana and
	// funds payments from them; nil when no Solana rail runs or it is off.
	OnchainPasses *app.OnchainPassService
	// Directory is the catalogs of paid APIs agents can browse and name
	// (Pay.sh, Circle's Agent Marketplace, PayAI, Coinbase's Bazaar), nil
	// when all are off.
	Directory *catalog.Multi

	// MCPPublicURL: see config.Config.MCPPublicURL.
	MCPPublicURL string
	// OAuthProviders holds only the providers with credentials configured,
	// keyed by name ("google", "github").
	OAuthProviders map[string]identity.Provider
	// Privy verifies Privy sign-ins; nil unless PRIVY_APP_ID is set.
	Privy      *identity.Privy
	AuthConfig config.AuthConfig
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
	accountRepo := postgres.NewAccountRepo(db)
	accountSvc := app.NewAccountService(accountRepo, agentSvc, agents, encryptor, mailer, postgres.NewGuardrailRepo(db), cfg.Auth.SessionTTL)
	accountSvc.SetWalletStore(accountRepo)
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

	// Economic coordination: executors act under their own Spend Pass; the
	// database guarantees one live attempt and one commitment per intent.
	econRepo := postgres.NewEconRepo(db)
	spendPassSvc.CountEconomicSpend(econRepo)
	econSvc := app.NewEconomicService(econRepo, agents, passRepo, passRepo)
	intentReceiptSvc := app.NewIntentReceiptService(signer, cfg.Auth.PublicWebURL, agents, econRepo)
	econSvc.SetReceipts(intentReceiptSvc)
	var sandboxProvider *sandboxpay.Provider
	var sandboxPersonas *sandboxpay.PersonaServer
	if cfg.EconomicSandbox {
		rail := sandboxpay.NewRail(time.Minute)
		econSvc.RegisterRail(rail)
		sandboxProvider = sandboxpay.NewProvider(rail, cfg.Auth.PublicWebURL+"/api/v1/sandbox/x402/token-risk", 0)
		econSvc.RegisterRecovery(sandboxpay.ProviderID, sandboxProvider)
		// Sandbox providers of token.price that misbehave like real ones
		// (slow, down, overcharging, trap-priced): the router and its guards,
		// shown with no chain and no money.
		sandboxPersonas = sandboxpay.NewPersonaServer(rail, cfg.Auth.PublicWebURL+"/api/v1/sandbox/x402/prices", nil)
		for _, p := range sandboxpay.DefaultPersonas {
			econSvc.RegisterRecovery(p.ID(), sandboxPersonas.Recovery(p.Name))
		}
	}
	execSvc, execProviders, solanaRails, err := buildExecution(ctx, cfg, econSvc, postgres.NewExecutionRepo(db))
	if err == nil && sandboxPersonas != nil {
		if port := listenPort(cfg.HTTPAddr); port > 0 {
			var cands []routing.Candidate
			if cands, err = sandboxPersonas.Candidates("http://127.0.0.1:" + strconv.Itoa(port) + "/api/v1/sandbox/x402/prices"); err == nil {
				for _, c := range cands {
					execProviders[c.Provider] = append(execProviders[c.Provider], c)
				}
			}
		}
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("wiring: %w", err)
	}
	// The answers to paid calls are kept for a limited time, sealed under a key
	// derived from the master key and used for nothing else, so that asking again
	// returns the answer instead of "already committed" (RESULT_RETENTION).
	if cfg.Results.Retention > 0 {
		resultMAC := hmac.New(sha256.New, masterKey)
		resultMAC.Write([]byte("algebra:result-store:v1"))
		resultEnc, err := privacy.NewAESGCMEncryptor(resultMAC.Sum(nil))
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("wiring: result sealing: %w", err)
		}
		execSvc.SetResults(app.NewResultVault(postgres.NewResultRepo(db), resultEnc, cfg.Results.Retention, cfg.Results.MaxBytes))
	}
	directory := buildDirectory(cfg)
	candidates := app.CandidateResolver{Configured: execProviders}
	var classIndex *catalog.ClassIndex
	if directory != nil {
		candidates.Catalog = catalogSource{directory}
		classIndex = catalog.NewClassIndex(directory)
		candidates.Classes = classIndex
		// Build the index now, in the background, so the first agent to ask
		// for a class doesn't wait for every catalog to be read.
		go func() {
			wctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			if _, _, err := classIndex.Summaries(wctx); err != nil {
				log.Printf("wiring: building the class index failed (it is retried on first use): %v", err)
			}
		}()
	}
	// The free health probe: unpaid 402 requests, never a payment. Its
	// findings keep dead and overcharging endpoints out of the router.
	health := app.NewHealthService(execSvc, postgres.NewHealthRepo(db), candidates)
	if os.Getenv("ALGEBRA_HEALTH_PROBES") == "off" {
		health.Interval = 0
	}
	execSvc.SetHealth(health)
	onboardingSvc := app.NewOnboardingService(accountSvc, commerceProfileSvc, privacyResolver)
	demoSvc := app.NewDemoService(accountSvc, onboardingSvc)
	oauthProviders := map[string]identity.Provider{}
	if cfg.Auth.GoogleClientID != "" && cfg.Auth.GoogleClientSecret != "" {
		oauthProviders["google"] = identity.NewGoogle(cfg.Auth.GoogleClientID, cfg.Auth.GoogleClientSecret)
	}
	if cfg.Auth.GitHubClientID != "" && cfg.Auth.GitHubClientSecret != "" {
		oauthProviders["github"] = identity.NewGitHub(cfg.Auth.GitHubClientID, cfg.Auth.GitHubClientSecret)
	}
	var privy *identity.Privy
	if cfg.Auth.PrivyAppID != "" {
		if privy, err = identity.NewPrivy(cfg.Auth.PrivyAppID, cfg.Auth.PrivyVerificationKey); err != nil {
			db.Close()
			return nil, fmt.Errorf("wiring: %w", err)
		}
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

	// With a Gemini key, agents can search the open web for endpoints no catalog
	// lists (algebra.discover_web); the limiter, when there is one, holds each
	// agent to a few searches an hour.
	if gemini != nil {
		execSvc.SetWebFinder(webdiscovery.New(gemini), limiter)
	}

	onchainPasses, err := buildOnchainPasses(cfg, solanaRails, db, spendPassSvc, accountRepo, econRepo)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("wiring: %w", err)
	}
	if onchainPasses != nil {
		econSvc.SetFunder(onchainPasses)
	}

	return &Bundle{
		DB: db, Agents: agents, AgentSvc: agentSvc, Integrators: integrators, IntegratorSvc: integratorSvc, TransactionPolicy: transactionPolicySvc,
		Users: userSvc, Intents: intentSvc, Discovery: discoverySvc, Quotes: quoteSvc,
		Policy: policySvc, Approvals: approvalSvc, Orders: orderSvc, Payments: paymentSvc,
		Privacy: privacyResolver, Connectors: connectors, Idempotency: idempotency, Audit: auditRepo,
		CommerceProfiles: commerceProfiles, CommerceProfileSvc: commerceProfileSvc,
		Billing: billingSvc, Plugins: pluginSvc, Accounts: accountSvc, Activity: activitySvc, Onboarding: onboardingSvc, Demo: demoSvc, SpendPasses: spendPassSvc, Receipts: receiptSvc,
		Economic: econSvc, IntentReceipts: intentReceiptSvc, SandboxProvider: sandboxProvider, SandboxPersonas: sandboxPersonas, Execution: execSvc, Candidates: candidates, Classes: classIndex, Health: health, Directory: directory, SolanaRails: solanaRails, OnchainPasses: onchainPasses, MCPPublicURL: cfg.MCPPublicURL, OAuthProviders: oauthProviders, Privy: privy,
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

// buildExecution wires the executor: the x402 runner over an HTTP client that
// can only reach public addresses (plus the sandbox provider's own port in
// sandbox mode), the capability catalog, and the providers an agent can name.
func buildExecution(ctx context.Context, cfg *config.Config, econSvc *app.EconomicService, store app.ExecutionStore) (*app.ExecutionService, map[string][]routing.Candidate, []*solanax402.Rail, error) {
	var solanaRails []*solanax402.Rail
	caps := app.DefaultCapabilities()
	if cfg.Jupiter.Enabled {
		caps = append(caps, jupiter.CapabilityInfo())
	}
	catalog, err := app.NewStaticCatalog(caps...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("capability catalog: %w", err)
	}
	providers, err := ParseConfiguredProviders(cfg.EconomicProviders)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ECONOMIC_PROVIDERS: %w", err)
	}
	httpOpts := safehttp.Options{}
	// A provider the operator configured on this machine (the devnet demo
	// providers, say) may be reached on its own port and no other.
	httpOpts.LoopbackPorts = configuredLoopbackPorts(providers)
	networks := map[string]string{}
	if cfg.EconomicSandbox {
		networks[chain.Sandbox] = "sandbox"
		// The sandbox provider is served by this API, on this machine: allow
		// loopback on its port and no other.
		if port := listenPort(cfg.HTTPAddr); port > 0 {
			httpOpts.LoopbackPorts = append(httpOpts.LoopbackPorts, port)
			c, err := routing.Candidate{
				Capability: "solana.token-risk", Provider: sandboxpay.ProviderID, Name: "Sandbox token risk (simulated)",
				ExecutionType: routing.ExecX402, Method: "POST", Network: chain.Sandbox, Sources: []routing.DiscoverySource{routing.SourceConfigured},
				Endpoint: "http://127.0.0.1:" + strconv.Itoa(port) + "/api/v1/sandbox/x402/token-risk",
			}.Normalize()
			if err != nil {
				return nil, nil, nil, fmt.Errorf("sandbox candidate: %w", err)
			}
			providers[c.Provider] = append(providers[c.Provider], c)
		}
	}
	for _, sc := range cfg.SolanaRails {
		rail, err := buildSolanaRail(ctx, sc)
		if err != nil {
			return nil, nil, nil, err
		}
		if rail != nil {
			econSvc.RegisterRail(rail)
			networks[rail.Network()] = rail.Name()
			solanaRails = append(solanaRails, rail)
		}
	}
	exec := app.NewExecutionService(econSvc, store)
	exec.SetCapabilities(catalog)
	exec.RegisterRunner(x402client.New(x402client.Config{
		HTTP: safehttp.New(httpOpts), Networks: networks, ReuseQuoteFor: 15 * time.Second,
	}))
	if cfg.Jupiter.Enabled {
		if err := wireJupiter(cfg, solanaRails, econSvc, exec, providers); err != nil {
			return nil, nil, nil, err
		}
	}
	return exec, providers, solanaRails, nil
}

// wireJupiter turns on buying tokens with USDC through Jupiter. It spends from
// the wallet of the verified Solana mainnet payment rail, so it is off, and
// says so, when there isn't one: Jupiter has no devnet, and a swap is never
// attempted on a cluster that couldn't be verified.
func wireJupiter(cfg *config.Config, rails []*solanax402.Rail, econSvc *app.EconomicService, exec *app.ExecutionService, providers map[string][]routing.Candidate) error {
	var mainnet *config.SolanaConfig
	for i := range cfg.SolanaRails {
		if c := strings.ToLower(cfg.SolanaRails[i].Cluster); c == "mainnet" || c == "mainnet-beta" {
			mainnet = &cfg.SolanaRails[i]
		}
	}
	verified := false
	for _, r := range rails {
		verified = verified || r.Network() == chain.Solana
	}
	if mainnet == nil || !verified {
		log.Printf("wiring: JUPITER_SWAP_ENABLED is on but there is no verified Solana mainnet wallet (SOLANA_MAINNET_KEYPAIR_FILE with SOLANA_ALLOW_MAINNET=yes): swaps are OFF")
		return nil
	}
	kp, rpcURL, err := loadSolanaWallet(*mainnet)
	if err != nil {
		return err
	}
	rail, err := jupiter.NewRail(jupiter.RailConfig{RPC: solana.NewRPC(rpcURL, nil), Signer: kp, MaxSwapMinor: cfg.Jupiter.MaxSwapMinor})
	if err != nil {
		return err
	}
	econSvc.RegisterRail(rail)
	exec.RegisterRunner(jupiter.NewRunner(jupiter.RunnerConfig{
		Client: jupiter.NewClient(cfg.Jupiter.BaseURL, cfg.Jupiter.APIKey, safehttp.New(safehttp.Options{Timeout: 30 * time.Second})),
		Wallet: kp.PublicKey(), MaxSwapMinor: cfg.Jupiter.MaxSwapMinor, MaxSlippageBps: cfg.Jupiter.MaxSlippageBps,
	}))
	c, err := jupiter.Candidate()
	if err != nil {
		return err
	}
	providers[c.Provider] = append(providers[c.Provider], c)
	most := cfg.Jupiter.MaxSwapMinor
	if most <= 0 {
		most = jupiter.DefaultMaxSwapMinor
	}
	log.Printf("wiring: Jupiter swaps enabled: wallet %s, at most %s USDC per swap", kp.PublicKey(), chain.FormatUnits(most, chain.USDCDecimals))
	return nil
}

// buildDirectory builds the catalogs agents can browse and name: Pay.sh,
// Circle's Agent Marketplace, PayAI's bazaar and Coinbase's CDP bazaar, each on
// unless turned off, or nil when all are.
// Nothing is fetched until somebody asks. The HTTP client reaches public
// addresses only, never follows a redirect, and refuses an oversized body.
func buildDirectory(cfg *config.Config) *catalog.Multi {
	var sources []catalog.Source
	if cfg.PaySh.Enabled {
		sources = append(sources, paysh.New(paysh.Config{
			CatalogURL: cfg.PaySh.CatalogURL, DocsURL: cfg.PaySh.DocsURL,
			HTTP: safehttp.New(safehttp.Options{Timeout: 15 * time.Second, MaxBody: 2 << 20}),
		}))
	}
	for _, d := range []struct {
		on      bool
		profile bazaar.Profile
	}{{cfg.Circle.Enabled, bazaar.Circle(cfg.Circle.DiscoveryURL)}, {cfg.PayAI.Enabled, bazaar.PayAI(cfg.PayAI.DiscoveryURL)}, {cfg.CDP.Enabled, bazaar.CDP(cfg.CDP.DiscoveryURL)}} {
		if d.on {
			sources = append(sources, bazaar.New(bazaar.Config{
				Profile: d.profile,
				HTTP:    safehttp.New(safehttp.Options{Timeout: 30 * time.Second, MaxBody: 16 << 20}),
			}))
		}
	}
	// Monid's tools (monid.ai), listed from its open-source connector repo for
	// discovery and comparison. They bill a prepaid Monid balance, not x402,
	// so they are never routed to. MONID_CATALOG=off leaves them out.
	if !strings.EqualFold(os.Getenv("MONID_CATALOG"), "off") {
		sources = append(sources, monid.New())
	}
	if len(sources) == 0 {
		return nil
	}
	return catalog.NewMulti(sources...)
}

// catalogSource adapts the catalogs to app.CatalogSource: a capability no
// catalog knows is "nothing found", not an error.
type catalogSource struct{ *catalog.Multi }

func (s catalogSource) ForCapability(ctx context.Context, capability string) ([]routing.Candidate, error) {
	cands, err := s.Multi.ForCapability(ctx, capability)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil, nil
	}
	return cands, err
}

// defaultSolanaRPC are the public endpoints, rate-limited and fine for trying
// things out; anything that matters should name its own node.
var defaultSolanaRPC = map[string]string{
	"devnet":  "https://api.devnet.solana.com",
	"mainnet": "https://api.mainnet-beta.solana.com",
}

// buildOnchainPasses turns on on-chain Spend Passes for every Solana rail that
// runs: each rail's own payer is the agent of every pass on its cluster, so a
// pull lands where the rail pays from.
func buildOnchainPasses(cfg *config.Config, rails []*solanax402.Rail, db *postgres.DB, passes *app.SpendPassService, wallets app.UserWalletStore, reservations app.ReservationReader) (*app.OnchainPassService, error) {
	if cfg.OnchainPasses.Disabled || len(rails) == 0 {
		return nil, nil
	}
	program, err := spendpass.New(cfg.OnchainPasses.ProgramID)
	if err != nil {
		return nil, err
	}
	// Passes made on any deployment of the program keep working: the same
	// program on Anchor and on Pinocchio, with the same bytes and rules.
	accepted := []spendpass.Program{program}
	for _, id := range spendpass.KnownProgramIDs {
		if p, err := spendpass.New(id); err == nil && p.ID != program.ID {
			accepted = append(accepted, p)
		}
	}
	var nets []*app.OnchainNetwork
	anyOwner := map[string]bool{}
	for _, rail := range rails {
		cluster := "mainnet"
		if rail.Network() == chain.SolanaDevnet {
			cluster = "devnet"
		}
		var sc *config.SolanaConfig
		for i := range cfg.SolanaRails {
			c := strings.ToLower(cfg.SolanaRails[i].Cluster)
			if c == cluster || (cluster == "mainnet" && c == "mainnet-beta") {
				sc = &cfg.SolanaRails[i]
			}
		}
		if sc == nil {
			continue
		}
		kp, url, err := loadSolanaWallet(*sc)
		if err != nil {
			return nil, err
		}
		if kp.PublicKey() != rail.Address() {
			return nil, fmt.Errorf("on-chain passes: the %s wallet doesn't match its rail", cluster)
		}
		rpc := solana.NewRPC(url, nil)
		// Only where the program really is: a pass made against an address
		// with no program behind it would just fail in the person's wallet.
		checkCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		acct, err := rpc.GetAccountInfo(checkCtx, program.ID, solana.Confirmed)
		cancel()
		if err != nil || acct == nil || !acct.Executable {
			log.Printf("wiring: on-chain Spend Passes OFF on %s: program %s isn't deployed there (or the node couldn't say: %v)", cluster, program.ID, err)
			if cfg.OnchainPasses.Required[cluster] {
				return nil, fmt.Errorf("on-chain passes are required on %s, but program %s isn't deployed there", cluster, program.ID)
			}
			continue
		}
		mintStr, _ := chain.AssetAddress(rail.Network(), "USDC")
		nets = append(nets, &app.OnchainNetwork{
			Network: rail.Network(), Cluster: cluster, RailName: rail.Name(), Program: program, Accepted: accepted,
			RPC: rpc, Payer: kp, Mint: solana.MustPublicKey(mintStr), Rail: rail,
			PriorityMicroLamports: cfg.OnchainPasses.PriorityMicroLamports, Required: cfg.OnchainPasses.Required[cluster],
		})
		if cfg.OnchainPasses.AnyOwner[cluster] {
			anyOwner[rail.Network()] = true
		}
		log.Printf("wiring: on-chain Spend Passes on %s: program %s, agent %s, required=%v", cluster, program.ID, kp.PublicKey(), cfg.OnchainPasses.Required[cluster])
	}
	if len(nets) == 0 {
		return nil, nil
	}
	svc := app.NewOnchainPassService(postgres.NewOnchainPassRepo(db), passes, wallets, reservations, nets)
	svc.AnyOwnerOn = anyOwner
	return svc, nil
}

// loadSolanaWallet reads a cluster's wallet and the node to use for it. The
// error never echoes the key.
func loadSolanaWallet(sc config.SolanaConfig) (*solana.Keypair, string, error) {
	text := sc.Keypair
	if sc.KeypairFile != "" {
		b, err := os.ReadFile(sc.KeypairFile)
		if err != nil {
			return nil, "", fmt.Errorf("SOLANA_KEYPAIR_FILE: %w", err)
		}
		text = string(b)
	}
	kp, err := solana.ParseKeypair(text)
	if err != nil {
		return nil, "", fmt.Errorf("the Solana wallet couldn't be loaded: %w", err)
	}
	url := sc.RPCURL
	if url == "" {
		url = defaultSolanaRPC[sc.Cluster]
	}
	return kp, url, nil
}

// buildSolanaRail builds the Solana USDC rail and checks it against the
// cluster. A mistake that could send money to the wrong place (a wallet that
// won't load, a node on another cluster) stops startup. A node that is merely
// unreachable does not: the rail is left out and said so, so the API still
// serves and no payment is made on a cluster that couldn't be verified.
func buildSolanaRail(ctx context.Context, sc config.SolanaConfig) (*solanax402.Rail, error) {
	kp, url, err := loadSolanaWallet(sc)
	if err != nil {
		return nil, err
	}
	rail, err := solanax402.New(solanax402.Config{
		Cluster: sc.Cluster, RPC: solana.NewRPC(url, nil), Signer: kp, MaxPaymentMinor: sc.MaxPaymentMinor,
	})
	if err != nil {
		return nil, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	st, err := rail.Status(checkCtx)
	switch {
	case errors.Is(err, solanax402.ErrWrongCluster):
		return nil, err
	case err != nil:
		log.Printf("wiring: SOLANA RAIL DISABLED: couldn't verify the %s cluster at %s: %v. No Solana payment will be made until this is fixed.", sc.Cluster, url, err)
		return nil, nil
	}
	usdc := "no USDC account yet"
	if st.USDCMinor != nil {
		usdc = chain.FormatUnits(int64(*st.USDCMinor), chain.USDCDecimals) + " USDC"
	}
	log.Printf("wiring: Solana %s rail enabled: wallet %s holds %s; at most %s USDC per payment", sc.Cluster, st.Address, usdc,
		chain.FormatUnits(st.MaxPaymentMinor, chain.USDCDecimals))
	return rail, nil
}

// ParseConfiguredProviders reads the ECONOMIC_PROVIDERS JSON array into
// candidates from the "configured" source: providers the operator pinned, and
// so trusted as native. A mistake here fails startup rather than silently
// dropping a provider.
func ParseConfiguredProviders(raw string) (map[string][]routing.Candidate, error) {
	out := map[string][]routing.Candidate{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	var entries []struct {
		Capability string `json:"capability"`
		Provider   string `json:"provider"`
		Name       string `json:"name"`
		Endpoint   string `json:"endpoint"`
		Method     string `json:"method"`
		Network    string `json:"network"`
		// PriceMinor is the provider's listed price in micro-USDC, which the
		// router holds its live 402 to.
		PriceMinor int64 `json:"price_minor"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("not a JSON array of providers: %w", err)
	}
	seen := map[string]bool{}
	for i, e := range entries {
		c, err := routing.Candidate{
			Capability: e.Capability, Provider: e.Provider, Name: e.Name, ExecutionType: routing.ExecX402,
			Endpoint: e.Endpoint, Method: e.Method, Network: e.Network, Sources: []routing.DiscoverySource{routing.SourceConfigured},
			PriceMinor: e.PriceMinor, Asset: assetIfPriced(e.PriceMinor),
		}.Normalize()
		if err != nil {
			return nil, fmt.Errorf("entry %d (%s): %w", i+1, e.Provider, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("entry %d (%s) repeats an earlier one", i+1, e.Provider)
		}
		seen[c.ID] = true
		out[c.Provider] = append(out[c.Provider], c)
	}
	return out, nil
}

// listenPort is the port of a listen address like ":8080" or "0.0.0.0:8080";
// zero when there isn't one.
func listenPort(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return 0
	}
	return n
}

// assetIfPriced is USDC for a listed price, nothing otherwise.
func assetIfPriced(minor int64) string {
	if minor > 0 {
		return "USDC"
	}
	return ""
}

// configuredLoopbackPorts are the ports of operator-configured providers on
// this machine. Only the operator can configure a provider, so this opens no
// door an agent controls.
func configuredLoopbackPorts(providers map[string][]routing.Candidate) []int {
	var ports []int
	for _, cs := range providers {
		for _, c := range cs {
			u, err := url.Parse(c.Endpoint)
			if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
				continue
			}
			if p, err := strconv.Atoi(u.Port()); err == nil && p > 0 && !slices.Contains(ports, p) {
				ports = append(ports, p)
			}
		}
	}
	return ports
}
