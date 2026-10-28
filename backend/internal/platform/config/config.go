// Package config loads Algebra's process configuration from environment
// variables. See .env.example at the repo root for the full list with
// descriptions. Nothing here reads a checked-in secret — local dev uses
// docker-compose defaults, production is expected to inject real values via
// Secret Manager (mandate §37).
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/datadir"
)

type Config struct {
	// Env is APP_ENV: "production" turns on the fail-fast checks in
	// ValidateProduction; anything else is development.
	Env string

	// DatabaseURL is a standard postgres:// connection string.
	DatabaseURL string
	// RedisAddr is host:port for the Redis instance.
	RedisAddr string

	// MasterKeyBase64 is the 32-byte AES-256 data-encryption key for
	// PrivacyResolver, base64-encoded. In production this should be a DEK
	// unwrapped from Cloud KMS at process start, not a static env var — see
	// docs/GCP_DEPLOYMENT.md. Local dev only.
	MasterKeyBase64 string

	// HTTPAddr is the REST API listen address, e.g. ":8080".
	HTTPAddr string

	ApprovalTTL                    time.Duration
	QuoteTTL                       time.Duration
	QuoteAmountToleranceMinorUnits int64

	// MerchantURLAllowlist bounds which domains a merchant-supplied product
	// URL may point at before Algebra passes it on to an agent (mandate
	// §47/§49). Defaults to the merchants this build ships connectors for.
	MerchantURLAllowlist []string

	// CORSAllowedOrigins is the exact-match allow-list for browser-frontend
	// requests (frontend/, the Next.js console) — see internal/api/v1's
	// corsMiddleware. Defaults to the Next.js dev server's own default port.
	CORSAllowedOrigins []string

	Merchants MerchantsConfig

	// GoogleSearchAPIKey/GoogleSearchEngineID configure the optional general
	// web-search fallback (connectors/websearch) used only when no
	// connected merchant can find a product. Both empty (the default) means
	// the capability is off — commerce.web_search returns ErrNotImplemented.
	GoogleSearchAPIKey   string
	GoogleSearchEngineID string

	// GeminiAPIKey enables Gemini-grounded shopping web search
	// (connectors/websearch.Gemini) — preferred over Custom Search when set.
	// GeminiSearchModel defaults to the stable "latest Flash" alias.
	// MCPPublicURL is where this deployment's MCP server is reachable, shown
	// to people connecting an agent with a Spend Pass. The API serves MCP at
	// /mcp itself; empty means PUBLIC_WEB_URL + "/mcp", which the web app
	// proxies to it.
	MCPPublicURL string
	// MCPLegacyTools (MCP_LEGACY_TOOLS=on) also offers the MCP tools of the
	// original shopping product (commerce.*, payments.*, profiles.*,
	// policy.*) and the earlier names algebra.discover_providers and
	// algebra.execution_status. Off by default.
	MCPLegacyTools bool

	GeminiAPIKey      string
	GeminiSearchModel string
	// TavilyAPIKey (optional) makes community deal plugins read through
	// Tavily, which can limit results to the last week; without it they use
	// Gemini's grounded Google search.
	TavilyAPIKey string
	// WebSearchCacheTTL is how long a web-search result is reused (Redis).
	WebSearchCacheTTL time.Duration

	// EconomicSandbox mounts the sandbox payment rail and sandbox x402
	// provider (providers/sandboxpay): simulated money, labelled test
	// everywhere. On by default outside production; in production only
	// with ECONOMIC_SANDBOX=on.
	EconomicSandbox bool
	// MainnetExecution allows real-money payments on Solana mainnet
	// (MAINNET_EXECUTION=on). Off by default: mainnet discovery, quotes and
	// simulations work without it; payments are refused with
	// MAINNET_EXECUTION_DISABLED until an operator turns it on.
	MainnetExecution bool

	// EconomicProviders is a JSON array of x402 providers the operator pins
	// by hand, the "configured" discovery source, e.g.
	//
	//	[{"capability":"solana.token-risk","provider":"acme","endpoint":"https://api.acme.example/risk","method":"POST","network":"solana"}]
	//
	// An agent can ask for them by provider name. They are trusted as native:
	// the operator vouched for them. Empty means none.
	EconomicProviders string

	// Solana is the real payment rail (providers/solanax402): Algebra pays
	// x402 providers in USDC from a wallet it controls. Off unless
	// SOLANA_CLUSTER is set. SolanaRails is every rail to run: that one, and
	// devnet and mainnet configured on their own (SOLANA_DEVNET_*,
	// SOLANA_MAINNET_*), so both clusters can be used at once.
	Solana      SolanaConfig
	SolanaRails []SolanaConfig

	// DevnetFaucet is the in-app devnet faucet: devnet SOL and USDC for a
	// signed-in person's own wallet, from a wallet the operator funds. Off
	// unless a keypair is set.
	DevnetFaucet DevnetFaucetConfig

	// PaySh is the Pay.sh catalog of paid APIs (providers/paysh), which
	// agents can browse and name as providers.
	PaySh PayShConfig
	// Circle is Circle's Agent Marketplace, PayAI PayAI's facilitator bazaar
	// and CDP Coinbase's x402 Bazaar (providers/bazaar).
	Circle CircleConfig
	PayAI  CircleConfig
	CDP    CircleConfig

	// Jupiter configures buying tokens with USDC through Jupiter's Swap API
	// (providers/jupiter); off unless JUPITER_SWAP_ENABLED=on, and it needs the
	// mainnet wallet.
	Jupiter JupiterConfig
	// OnchainPasses configures Spend Passes enforced by Algebra's program on
	// Solana (solana-program/programs/spend-pass). Off unless ONCHAIN_PASSES=on.
	OnchainPasses OnchainPassConfig
	// TestMoney bounds what the shared devnet payer can be made to spend
	// (TEST_MONEY_*), now that a person's payments don't come from a vault.
	TestMoney TestMoneyConfig

	// Results is how long the answer to a paid call is kept, so asking again
	// returns it (RESULT_RETENTION, RESULT_MAX_BYTES; see results.go).
	Results ResultsConfig

	Auth AuthConfig

	Billing BillingConfig
}

// PayShConfig configures reading the Pay.sh catalog.
type PayShConfig struct {
	// Enabled is on unless PAYSH_ENABLED=off. It only reads a public
	// document, when somebody asks, and caches it.
	Enabled bool
	// CatalogURL and DocsURL default to Pay.sh's own (PAYSH_CATALOG_URL,
	// PAYSH_DOCS_URL); they must be https.
	CatalogURL string
	DocsURL    string
}

// CircleConfig configures reading an x402 discovery directory (Circle's
// Agent Marketplace, PayAI's bazaar, Coinbase's CDP bazaar).
type CircleConfig struct {
	// Enabled is on unless CIRCLE_AGENTS_ENABLED=off (PAYAI_ENABLED=off,
	// CDP_BAZAAR_ENABLED=off).
	Enabled bool
	// DiscoveryURL defaults to the directory's own (CIRCLE_DISCOVERY_URL,
	// PAYAI_DISCOVERY_URL, CDP_DISCOVERY_URL); it must be https.
	DiscoveryURL string
}

// SolanaConfig configures the Solana USDC rail.
type SolanaConfig struct {
	// Cluster is "devnet" or "mainnet"; empty turns the rail off.
	Cluster string
	// RPCURL is the node to read from. Empty uses the public endpoint for
	// the cluster, which is rate-limited and fine for trying things out.
	RPCURL string
	// Keypair is the wallet's secret as text (a JSON array or base58), or
	// KeypairFile is a path to the same. Prefer the file: a secret in the
	// environment ends up in process listings and crash reports.
	Keypair     string
	KeypairFile string
	// KMSKeyVersion names one exact Cloud KMS Ed25519 key version
	// (SOLANA_MAINNET_KMS_KEY_VERSION, SOLANA_DEVNET_KMS_KEY_VERSION): the payer
	// key then lives in KMS and never on this server. Required for mainnet in
	// production.
	KMSKeyVersion string
	// Signer is "generated" when Algebra makes the payer key itself and keeps
	// it sealed in the database (SOLANA_DEVNET_SIGNER=generated). Devnet only:
	// a key that moves real money is one the operator controls. Empty means
	// the wallet above.
	Signer string
	// SignerPubkey pins the address the payer key must have
	// (SOLANA_DEVNET_SIGNER_PUBKEY). The server stops, rather than carry on,
	// if the key it would use is any other: every vault made for the pinned
	// key stops working with a different one.
	SignerPubkey string
	// MaxPaymentMinor is the most one payment may be, in micro-USDC: a hard
	// ceiling in the rail itself, whatever any policy says.
	MaxPaymentMinor int64
}

// DevnetFaucetConfig configures the devnet faucet (DEVNET_FAUCET_*).
type DevnetFaucetConfig struct {
	// Keypair is the funded devnet wallet's secret as text (a JSON array or
	// base58), or KeypairFile a path to the same. Use a wallet that holds
	// devnet tokens only, never one that has held real money. Both empty:
	// the faucet is off.
	Keypair     string
	KeypairFile string
	// RPCURL is the devnet node it sends through (DEVNET_FAUCET_RPC_URL);
	// empty uses the devnet rail's, or the public endpoint.
	RPCURL string
	// SOLLamports and USDCMinor are what one drop sends (DEVNET_FAUCET_SOL,
	// DEVNET_FAUCET_USDC): 0.2 SOL and 2 USDC by default.
	SOLLamports uint64
	USDCMinor   uint64
	// PerDay is the most drops in a day, for everyone (DEVNET_FAUCET_PER_DAY).
	PerDay int
}

// TestMoneyConfig configures the test-money guard: on devnet Algebra pays
// real sellers from one wallet of its own, with test money, so what stops one
// stranger from emptying it is a count of payments a day for each account and
// for the whole server. Each payment is also at most SOLANA_MAX_PAYMENT_USDC,
// and the pass's own budget and limits apply as always. On by default.
type TestMoneyConfig struct {
	// Off is TEST_MONEY_GUARD=off; refused in production unless every payment
	// comes from a vault.
	Off bool
	// PerAccountPerDay and PerDay are the most payments a day for one account
	// and for everyone (TEST_MONEY_PER_ACCOUNT_PER_DAY, TEST_MONEY_PER_DAY).
	PerAccountPerDay int
	PerDay           int
	// Testnet credits every account starts with (TEST_CREDITS_USDC, default
	// 10; TEST_CREDITS_SOL, default 5). TEST_CREDITS=off turns them off, and
	// then only the daily counts bound an account.
	CreditsOff         bool
	CreditsUSDCMinor   int64
	CreditsSOLLamports int64
}

// OnchainPassConfig configures on-chain Spend Passes (a vault per pass, kept
// by Algebra's program on Solana). They are off unless ONCHAIN_PASSES=on, and
// then run wherever a Solana rail does, unless SPEND_PASS_PROGRAM_ID is "off".
type OnchainPassConfig struct {
	// ProgramID is the Spend Pass program's address; empty means the
	// address Algebra deployed (spendpass.DefaultProgramID).
	ProgramID string
	// Disabled is true unless ONCHAIN_PASSES=on (or when SPEND_PASS_PROGRAM_ID
	// is "off"); the other settings here are then read but not acted on.
	Disabled bool
	// Required lists clusters ("devnet", "mainnet") where a pass must be on
	// chain to pay at all (SPEND_PASS_REQUIRED=mainnet,devnet), so the payer
	// never spends money that isn't a pass's.
	Required map[string]bool
	// AnyOwner lists clusters where an on-chain pass's owner needn't be a
	// wallet the person signed in with (SPEND_PASS_ANY_OWNER=devnet). Never
	// mainnet: there it must be their own wallet.
	AnyOwner map[string]bool
	// PriorityMicroLamports is the priority fee for pulls and refunds.
	PriorityMicroLamports uint64
}

// BillingConfig configures Razorpay billing for Algebra's own plans. Empty
// keys mean billing is off: everyone stays on the free Developer plan.
type BillingConfig struct {
	RazorpayKeyID         string
	RazorpayKeySecret     string
	RazorpayWebhookSecret string
	// GrowthPlanID uses a plan created in the Razorpay dashboard; otherwise
	// one is created at GrowthPriceMinor on first checkout.
	GrowthPlanID     string
	GrowthPriceMinor int64
}

// AuthConfig configures human sign-in for Algebra's own web app. Every
// OAuth provider is optional: one with no client ID is simply not offered
// on the sign-in page. Email + password always works.
type AuthConfig struct {
	// PublicWebURL is the origin users reach the web app at (and, through
	// its /api/v1 rewrite, this API). OAuth redirect URIs and password-reset
	// links are built from it; https:// also marks session cookies Secure.
	PublicWebURL string

	GoogleClientID     string
	GoogleClientSecret string
	GitHubClientID     string
	GitHubClientSecret string

	// ResendAPIKey/EmailFrom enable real password-reset email. Without
	// them, reset links are written to the API's log (development only).
	ResendAPIKey string
	EmailFrom    string
	// LeadsNotifyEmail (LEADS_NOTIFY_EMAIL) is where each new Book a Demo
	// enquiry is emailed, through the same mailer. Unset: enquiries are only
	// stored, and read with GET /api/v1/leads and the operator token.
	LeadsNotifyEmail string

	SessionTTL time.Duration

	// DevHeaderAuth re-enables the pre-accounts development shortcuts:
	// X-User-ID as a human identity, and unauthenticated POST /users and
	// POST /agents. Off by default; never enable it on a reachable host —
	// it lets any caller act as any user.
	DevHeaderAuth bool

	// DemoAccounts offers "Try the demo" on the sign-in page: a one-click
	// account that shops real listings with a simulated checkout (fake
	// money). On unless DEMO_ACCOUNTS=off.
	DemoAccounts bool
	// PasswordLogin offers email + password sign-in and sign-up. On unless
	// PASSWORD_LOGIN=off — turn it off once Google/GitHub sign-in is the
	// only way into a real account.
	PasswordLogin bool
	// EmailCodeLogin offers sign-in with a six-digit code emailed to the
	// address typed (no password, no wallet; the account is made the first
	// time). On unless EMAIL_CODE_LOGIN=off. Without RESEND_API_KEY the code is
	// written to the server's log instead of sent, which is how it is used in
	// development.
	EmailCodeLogin bool

	// OperatorToken (ALGEBRA_OPERATOR_TOKEN) is the operator's own key, sent
	// as the X-Algebra-Operator-Token header. It is needed to read Book a
	// Demo enquiries (GET /leads), to change a provider's trust (verify,
	// block, unblock) and refresh the registry on demand, and for B2B
	// registration (POST /tenants, /integrators). Unset: those stay open in
	// development and are closed to everyone in production.
	OperatorToken string
	// Production mirrors APP_ENV=production for handlers that behave
	// differently there.
	Production bool

	// Daily caps on what one person can cost: every agent message is an LLM
	// call plus billed web searches, and demo accounts need no signup.
	// 0 turns a cap off. Enforced only when Redis is configured.
	AgentTurnsPerDay        int
	DemoAgentTurnsPerDay    int
	DemoAccountsPerIPPerDay int

	// TrustedProxyHops (TRUSTED_PROXY_HOPS) is how many X-Forwarded-For
	// entries, from the right, our own infrastructure wrote. The web app's
	// rewrite passes the header through unchanged, so count the load
	// balancers in front of it: 1 (default) for one that appends the client
	// address (nginx, AWS ALB, most proxies), 2 for Google Cloud's HTTPS load
	// balancer (it appends the client and its own address). Per-IP limits key
	// on that entry; everything left of it is client-supplied.
	TrustedProxyHops int

	// EdgeSharedSecret (EDGE_SHARED_SECRET) is the key the web app's proxy
	// sends with the visitor's address (X-Algebra-Client-IP). When the web
	// host proxies to this API, every address the API sees is the web host's,
	// so all visitors would share one per-IP budget. With the secret set here
	// and as ALGEBRA_EDGE_SECRET on the web app, the API takes the visitor's
	// address from that header; without it (or without a matching secret on
	// the request) it uses X-Forwarded-For as before. At least 32 characters.
	EdgeSharedSecret string
}

// CookieSecure reports whether session cookies must be Secure.
func (a AuthConfig) CookieSecure() bool {
	return strings.HasPrefix(a.PublicWebURL, "https://")
}

// NoMerchants as ENABLED_MERCHANTS registers no merchant connector: an
// API-only deployment that has no use for the shopping product, and so none
// of its test store either.
const NoMerchants = "none"

// DefaultEnabledMerchants is every connector registered unless
// ENABLED_MERCHANTS says otherwise. Swiggy Instamart is deliberately absent:
// Swiggy reviews production access to its MCP servers, so an operator opts in
// once they have it.
const DefaultEnabledMerchants = "mock,zepto,amazon,flipkart,blinkit,generic-browser"

const (
	defaultConnectorTimeout    = 20 * time.Second
	defaultZeptoMCPEndpoint    = "https://mcp.zepto.co.in/mcp"
	defaultSwiggyIMMCPEndpoint = "https://mcp.swiggy.com/im"
	defaultAmazonMarketplace   = "www.amazon.in"
)

// MerchantsConfig holds per-merchant connector settings. None of these are
// required: a merchant with nothing configured is still listed, with a
// status saying what it needs.
type MerchantsConfig struct {
	// Enabled lists connector names to register.
	Enabled []string
	// SessionDir holds the encrypted linked-account sessions that
	// cmd/merchant-login writes (Zepto, Swiggy Instamart).
	SessionDir string
	// ConnectorTimeout bounds one connector's discovery work. Real merchant
	// APIs need several round trips (search, cart, quote) per intent.
	ConnectorTimeout time.Duration

	ZeptoMCPEndpoint           string
	SwiggyInstamartMCPEndpoint string

	FlipkartAffiliateID    string
	FlipkartAffiliateToken string

	AmazonCredentialID      string
	AmazonCredentialSecret  string
	AmazonCredentialVersion string
	AmazonPartnerTag        string
	AmazonMarketplace       string

	// BankOffersFile is the operator-curated bank/card offers JSON
	// (internal/platform/bankoffers; internal/platform/bankoffers/testdata/bank-offers.example.json). Empty
	// means bank offers are off.
	BankOffersFile string
}

// IsEnabled reports whether the named connector should be registered.
func (m MerchantsConfig) IsEnabled(name string) bool {
	return slices.Contains(m.Enabled, name)
}

// WithDefaults fills in anything left empty, for callers that build a
// Config by hand (tests, tools) rather than through FromEnv.
func (m MerchantsConfig) WithDefaults() MerchantsConfig {
	if len(m.Enabled) == 0 {
		m.Enabled = splitCSV(DefaultEnabledMerchants)
	}
	if m.SessionDir == "" {
		m.SessionDir = datadir.Path("merchant-sessions")
	}
	if m.ConnectorTimeout <= 0 {
		m.ConnectorTimeout = defaultConnectorTimeout
	}
	if m.ZeptoMCPEndpoint == "" {
		m.ZeptoMCPEndpoint = defaultZeptoMCPEndpoint
	}
	if m.SwiggyInstamartMCPEndpoint == "" {
		m.SwiggyInstamartMCPEndpoint = defaultSwiggyIMMCPEndpoint
	}
	if m.AmazonMarketplace == "" {
		m.AmazonMarketplace = defaultAmazonMarketplace
	}
	return m
}

// FromEnv loads configuration from the process environment, applying
// sensible local-development defaults for anything optional. Required
// values that are missing return an error rather than silently defaulting —
// a missing DATABASE_URL should fail fast, not fall back to something that
// looks like it's working.
func FromEnv() (*Config, error) {
	// Load .env into the process environment for local development — see
	// .env.example. godotenv.Load only fills variables not already set, so
	// a real deployment's actual environment (Secret Manager, Cloud Run
	// env vars, ...) always wins over a stray .env file, and a missing
	// .env (every non-local environment) is silently fine: godotenv.Load's
	// error is deliberately ignored, not logged, since "no .env file" is
	// the expected, correct state outside local dev.
	_ = godotenv.Load()

	cfg := &Config{
		Env:             strings.ToLower(getEnv("APP_ENV", "development")),
		DatabaseURL:     getEnv("DATABASE_URL", "postgres://algebra:algebra@localhost:5432/algebra?sslmode=disable"),
		RedisAddr:       getEnv("REDIS_ADDR", "localhost:6379"),
		MasterKeyBase64: os.Getenv("ALGEBRA_MASTER_KEY"),
		HTTPAddr:        getEnv("HTTP_ADDR", ":8080"),
	}

	approvalTTL, err := getDuration("APPROVAL_TTL", 15*time.Minute)
	if err != nil {
		return nil, err
	}
	cfg.ApprovalTTL = approvalTTL

	quoteTTL, err := getDuration("QUOTE_TTL", 5*time.Minute)
	if err != nil {
		return nil, err
	}
	cfg.QuoteTTL = quoteTTL

	tolerance, err := getInt64("QUOTE_AMOUNT_TOLERANCE_MINOR_UNITS", 500) // ₹5 default
	if err != nil {
		return nil, err
	}
	cfg.QuoteAmountToleranceMinorUnits = tolerance

	cfg.MerchantURLAllowlist = splitCSV(getEnv("MERCHANT_URL_ALLOWLIST",
		"zepto.co.in,zeptonow.com,swiggy.com,amazon.in,flipkart.com,blinkit.com"))

	cfg.CORSAllowedOrigins = splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3000"))
	// A browser's Origin never ends in a slash; an address pasted from one often does.
	for i, o := range cfg.CORSAllowedOrigins {
		cfg.CORSAllowedOrigins[i] = strings.TrimRight(o, "/")
	}

	cfg.GoogleSearchAPIKey = os.Getenv("GOOGLE_SEARCH_API_KEY")
	cfg.GoogleSearchEngineID = os.Getenv("GOOGLE_SEARCH_ENGINE_ID")
	cfg.GeminiAPIKey = getEnv("GEMINI_API_KEY", os.Getenv("GOOGLE_GEMINI_API"))
	cfg.MCPPublicURL = strings.TrimRight(os.Getenv("MCP_PUBLIC_URL"), "/")
	cfg.MCPLegacyTools = strings.EqualFold(strings.TrimSpace(os.Getenv("MCP_LEGACY_TOOLS")), "on")
	cfg.GeminiSearchModel = os.Getenv("GEMINI_SEARCH_MODEL")
	cfg.TavilyAPIKey = os.Getenv("TAVILY_API_KEY")
	webSearchTTL, err := getDuration("WEB_SEARCH_CACHE_TTL", 10*time.Minute)
	if err != nil {
		return nil, err
	}
	cfg.WebSearchCacheTTL = webSearchTTL
	cfg.EconomicProviders = strings.TrimSpace(os.Getenv("ECONOMIC_PROVIDERS"))
	if err := loadSolana(&cfg.Solana); err != nil {
		return nil, err
	}
	rails, err := loadSolanaRails(cfg.Solana)
	if err != nil {
		return nil, err
	}
	cfg.SolanaRails = rails
	if err := loadDevnetFaucet(&cfg.DevnetFaucet); err != nil {
		return nil, err
	}
	if err := loadPaySh(&cfg.PaySh); err != nil {
		return nil, err
	}
	if err := loadDirectory(&cfg.Circle, "CIRCLE_AGENTS_ENABLED", "CIRCLE_DISCOVERY_URL"); err != nil {
		return nil, err
	}
	if err := loadDirectory(&cfg.PayAI, "PAYAI_ENABLED", "PAYAI_DISCOVERY_URL"); err != nil {
		return nil, err
	}
	if err := loadDirectory(&cfg.CDP, "CDP_BAZAAR_ENABLED", "CDP_DISCOVERY_URL"); err != nil {
		return nil, err
	}
	if err := loadResults(&cfg.Results); err != nil {
		return nil, err
	}
	if err := loadOnchainPasses(&cfg.OnchainPasses); err != nil {
		return nil, err
	}
	if err := loadTestMoney(&cfg.TestMoney); err != nil {
		return nil, err
	}
	if err := loadJupiter(&cfg.Jupiter); err != nil {
		return nil, err
	}
	cfg.MainnetExecution = strings.EqualFold(strings.TrimSpace(os.Getenv("MAINNET_EXECUTION")), "on")
	// Vaults that are switched off can't make mainnet payments come from a vault.
	mainnetRequired := cfg.OnchainPasses.Required
	if cfg.OnchainPasses.Disabled {
		mainnetRequired = nil
	}
	if err := checkMainnetExecution(cfg.MainnetExecution, mainnetRequired); err != nil {
		return nil, err
	}
	switch strings.ToLower(os.Getenv("ECONOMIC_SANDBOX")) {
	case "on":
		cfg.EconomicSandbox = true
	case "off":
		cfg.EconomicSandbox = false
	default:
		cfg.EconomicSandbox = cfg.Env != "production"
	}

	sessionTTL, err := getDuration("SESSION_TTL", 30*24*time.Hour)
	if err != nil {
		return nil, err
	}
	agentTurns, err := getInt64("AGENT_TURNS_PER_DAY", 200)
	if err != nil {
		return nil, err
	}
	demoAgentTurns, err := getInt64("DEMO_AGENT_TURNS_PER_DAY", 40)
	if err != nil {
		return nil, err
	}
	demoPerIP, err := getInt64("DEMO_ACCOUNTS_PER_IP_PER_DAY", 5)
	if err != nil {
		return nil, err
	}
	proxyHops, err := getInt64("TRUSTED_PROXY_HOPS", 1)
	if err != nil {
		return nil, err
	}
	cfg.Auth = AuthConfig{
		PublicWebURL:       strings.TrimRight(getEnv("PUBLIC_WEB_URL", "http://localhost:3000"), "/"),
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GitHubClientID:     os.Getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
		ResendAPIKey:       os.Getenv("RESEND_API_KEY"),
		EmailFrom:          getEnv("EMAIL_FROM", "Algebra <no-reply@algebra.local>"),
		LeadsNotifyEmail:   strings.TrimSpace(os.Getenv("LEADS_NOTIFY_EMAIL")),
		SessionTTL:         sessionTTL,
		DevHeaderAuth:      os.Getenv("ALGEBRA_DEV_AUTH") == "true",
		DemoAccounts:       !strings.EqualFold(os.Getenv("DEMO_ACCOUNTS"), "off"),
		PasswordLogin:      !strings.EqualFold(os.Getenv("PASSWORD_LOGIN"), "off"),
		EmailCodeLogin:     !strings.EqualFold(os.Getenv("EMAIL_CODE_LOGIN"), "off"),
		OperatorToken:      strings.TrimSpace(os.Getenv("ALGEBRA_OPERATOR_TOKEN")),
		Production:         cfg.Env == "production",

		AgentTurnsPerDay:        int(agentTurns),
		DemoAgentTurnsPerDay:    int(demoAgentTurns),
		DemoAccountsPerIPPerDay: int(demoPerIP),
		TrustedProxyHops:        int(proxyHops),
		EdgeSharedSecret:        strings.TrimSpace(os.Getenv("EDGE_SHARED_SECRET")),
	}

	growthINR, err := getInt64("GROWTH_PRICE_INR", 8499)
	if err != nil {
		return nil, err
	}
	cfg.Billing = BillingConfig{
		RazorpayKeyID:         os.Getenv("RAZORPAY_KEY_ID"),
		RazorpayKeySecret:     os.Getenv("RAZORPAY_KEY_SECRET"),
		RazorpayWebhookSecret: os.Getenv("RAZORPAY_WEBHOOK_SECRET"),
		GrowthPlanID:          os.Getenv("RAZORPAY_PLAN_GROWTH"),
		GrowthPriceMinor:      growthINR * 100,
	}

	connectorTimeout, err := getDuration("CONNECTOR_TIMEOUT", defaultConnectorTimeout)
	if err != nil {
		return nil, err
	}
	cfg.Merchants = MerchantsConfig{
		Enabled:                    splitCSV(getEnv("ENABLED_MERCHANTS", DefaultEnabledMerchants)),
		SessionDir:                 getEnv("MERCHANT_SESSION_DIR", datadir.Path("merchant-sessions")),
		ConnectorTimeout:           connectorTimeout,
		ZeptoMCPEndpoint:           getEnv("ZEPTO_MCP_ENDPOINT", defaultZeptoMCPEndpoint),
		SwiggyInstamartMCPEndpoint: getEnv("SWIGGY_INSTAMART_MCP_ENDPOINT", defaultSwiggyIMMCPEndpoint),
		FlipkartAffiliateID:        os.Getenv("FLIPKART_AFFILIATE_ID"),
		FlipkartAffiliateToken:     os.Getenv("FLIPKART_AFFILIATE_TOKEN"),
		AmazonCredentialID:         os.Getenv("AMAZON_CREATORS_CREDENTIAL_ID"),
		AmazonCredentialSecret:     os.Getenv("AMAZON_CREATORS_CREDENTIAL_SECRET"),
		AmazonCredentialVersion:    os.Getenv("AMAZON_CREATORS_CREDENTIAL_VERSION"),
		AmazonPartnerTag:           os.Getenv("AMAZON_ASSOCIATE_TAG"),
		AmazonMarketplace:          getEnv("AMAZON_MARKETPLACE", defaultAmazonMarketplace),
		BankOffersFile:             getEnv("BANK_OFFERS_FILE", ""),
	}

	if cfg.MasterKeyBase64 == "" {
		return nil, fmt.Errorf("config: ALGEBRA_MASTER_KEY is required (32 random bytes, base64-encoded — see .env.example)")
	}
	if _, err := cfg.MasterKey(); err != nil {
		return nil, err
	}
	if cfg.IsProduction() {
		if err := cfg.ValidateProduction(); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// IsProduction reports APP_ENV=production.
func (c *Config) IsProduction() bool { return c.Env == "production" }

// ValidateProduction refuses to start a production process with a setting
// that is only safe on a developer's machine. Every problem is reported at
// once, so a deploy fails with the full list rather than one at a time.
func (c *Config) ValidateProduction() error {
	var problems []string
	if !strings.HasPrefix(c.Auth.PublicWebURL, "https://") {
		problems = append(problems, "PUBLIC_WEB_URL must be https:// (session cookies are only Secure over https)")
	}
	if c.Auth.DevHeaderAuth {
		problems = append(problems, "ALGEBRA_DEV_AUTH must be off — it lets any caller act as any user")
	}
	if c.Merchants.IsEnabled("mock") && os.Getenv("ALLOW_MOCK_MERCHANT") != "true" {
		problems = append(problems, "ENABLED_MERCHANTS includes the mock test store — remove it (or set ALLOW_MOCK_MERCHANT=true for a staging environment)")
	}
	if c.Auth.ResendAPIKey == "" {
		problems = append(problems, "RESEND_API_KEY is required — without it password-reset links would be written to logs")
	}
	if strings.TrimSpace(os.Getenv("REDIS_ADDR")) == "" {
		problems = append(problems, "REDIS_ADDR is required — rate limiting and execution locks depend on it")
	}
	if strings.Contains(c.DatabaseURL, "sslmode=disable") {
		problems = append(problems, "DATABASE_URL must not use sslmode=disable")
	}
	if c.Billing.RazorpayKeyID != "" {
		if strings.HasPrefix(c.Billing.RazorpayKeyID, "rzp_test_") && os.Getenv("ALLOW_TEST_PAYMENTS") != "true" {
			problems = append(problems, "RAZORPAY_KEY_ID is a test key (rzp_test_) — use live keys, or set ALLOW_TEST_PAYMENTS=true for staging")
		}
		if c.Billing.RazorpayWebhookSecret == "" {
			problems = append(problems, "RAZORPAY_WEBHOOK_SECRET is required with Razorpay — renewals and failed charges arrive by webhook")
		}
	}
	if t := c.Auth.OperatorToken; t != "" && len(t) < 32 {
		problems = append(problems, "ALGEBRA_OPERATOR_TOKEN must be at least 32 characters (openssl rand -hex 32)")
	}
	if s := c.Auth.EdgeSharedSecret; s != "" && len(s) < 32 {
		problems = append(problems, "EDGE_SHARED_SECRET must be at least 32 characters (openssl rand -hex 32)")
	}
	if e := c.Auth.LeadsNotifyEmail; e != "" {
		if a, err := mail.ParseAddress(e); err != nil || a.Address != e {
			problems = append(problems, fmt.Sprintf("LEADS_NOTIFY_EMAIL %q must be a plain email address", e))
		}
	}
	for _, o := range c.CORSAllowedOrigins {
		if !strings.HasPrefix(o, "https://") {
			problems = append(problems, fmt.Sprintf("CORS_ALLOWED_ORIGINS entry %q must be https://", o))
		}
	}
	// Real money is paid from a person's vault: otherwise the payer spends money
	// that is no pass's, with none of the program's limits on it. Test money on
	// devnet may come from the payer's own wallet, but only inside the test-money
	// guard's daily counts. (The simulated sandbox has no wallet.)
	for _, rail := range c.SolanaRails {
		cl := strings.ToLower(rail.Cluster)
		if cl == "mainnet-beta" {
			cl = "mainnet"
		}
		// Real money is signed by a key that never sits on the server.
		if cl == "mainnet" && rail.KMSKeyVersion == "" {
			problems = append(problems, "the Solana mainnet payer must be a Cloud KMS key (SOLANA_MAINNET_KMS_KEY_VERSION): a key in a file or an environment variable on the server is not accepted with real money; see docs/SIGNER.md")
		}
		vaulted := !c.OnchainPasses.Disabled && c.OnchainPasses.Required[cl]
		switch {
		case cl == "mainnet" && !vaulted:
			problems = append(problems, "the Solana mainnet rail needs ONCHAIN_PASSES=on and SPEND_PASS_REQUIRED=mainnet, so that every payment comes from a Spend Pass vault and never from the payer's own funds")
		case cl != "mainnet" && !vaulted && c.TestMoney.Off:
			problems = append(problems, fmt.Sprintf("the Solana %s rail pays from the server's own test wallet, so TEST_MONEY_GUARD can't be off: keep it on, or make every payment come from a vault (ONCHAIN_PASSES=on, SPEND_PASS_REQUIRED=%s)", cl, cl))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("config: refusing to start with APP_ENV=production:\n  - %s", strings.Join(problems, "\n  - "))
}

// MasterKey decodes MasterKeyBase64 into the 32-byte key AESGCMEncryptor
// expects.
func (c *Config) MasterKey() ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(c.MasterKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("config: ALGEBRA_MASTER_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("config: ALGEBRA_MASTER_KEY must decode to 32 bytes, got %d", len(key))
	}
	return key, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s is not a valid duration: %w", key, err)
	}
	return d, nil
}

func getInt64(key string, fallback int64) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s is not a valid integer: %w", key, err)
	}
	return n, nil
}

// splitCSV parses a comma-separated env value, trimming whitespace and
// dropping empties.
func splitCSV(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// loadSolana reads the Solana rail's settings. Mainnet moves real money, so
// it needs its own explicit acknowledgement on top of naming the cluster.
func loadSolana(c *SolanaConfig) error {
	c.Cluster = strings.ToLower(strings.TrimSpace(os.Getenv("SOLANA_CLUSTER")))
	if c.Cluster == "" {
		return nil
	}
	switch c.Cluster {
	case "devnet":
	case "mainnet", "mainnet-beta":
		c.Cluster = "mainnet"
		if strings.ToLower(os.Getenv("SOLANA_ALLOW_MAINNET")) != "yes" {
			return errors.New("config: SOLANA_CLUSTER=mainnet pays with real money; set SOLANA_ALLOW_MAINNET=yes to confirm that is intended")
		}
	default:
		return fmt.Errorf("config: SOLANA_CLUSTER must be devnet or mainnet, not %q", c.Cluster)
	}
	c.RPCURL = strings.TrimSpace(os.Getenv("SOLANA_RPC_URL"))
	c.Keypair = strings.TrimSpace(os.Getenv("SOLANA_KEYPAIR"))
	c.KeypairFile = strings.TrimSpace(os.Getenv("SOLANA_KEYPAIR_FILE"))
	c.SignerPubkey = strings.TrimSpace(os.Getenv("SOLANA_SIGNER_PUBKEY"))
	c.KMSKeyVersion = strings.TrimSpace(os.Getenv("SOLANA_KMS_KEY_VERSION"))
	if c.KMSKeyVersion != "" && (c.Keypair != "" || c.KeypairFile != "") {
		return errors.New("config: choose SOLANA_KMS_KEY_VERSION or a keypair, not both")
	}
	if err := checkKMSKeyVersion("SOLANA_KMS_KEY_VERSION", c.KMSKeyVersion); err != nil {
		return err
	}
	if c.Keypair == "" && c.KeypairFile == "" && c.KMSKeyVersion == "" {
		return errors.New("config: SOLANA_CLUSTER is set but there is no wallet: set SOLANA_KEYPAIR_FILE (preferred), SOLANA_KEYPAIR or SOLANA_KMS_KEY_VERSION")
	}
	c.MaxPaymentMinor = 1_000_000 // 1 USDC
	if v := strings.TrimSpace(os.Getenv("SOLANA_MAX_PAYMENT_USDC")); v != "" {
		n, err := chain.ParseUnits(v, chain.USDCDecimals)
		if err != nil || n <= 0 {
			return fmt.Errorf("config: SOLANA_MAX_PAYMENT_USDC must be a positive amount like 0.50: %v", err)
		}
		c.MaxPaymentMinor = n
	}
	return nil
}

// checkKMSKeyVersion refuses a value that is not one exact CryptoKeyVersion, so
// a typo stops the server at start instead of at the first payment.
func checkKMSKeyVersion(name, v string) error {
	if v == "" {
		return nil
	}
	if !strings.HasPrefix(v, "projects/") || !strings.Contains(v, "/cryptoKeyVersions/") {
		return fmt.Errorf("config: %s must be one exact Cloud KMS key version (projects/…/locations/…/keyRings/…/cryptoKeys/…/cryptoKeyVersions/N)", name)
	}
	return nil
}

// loadDevnetFaucet reads DEVNET_FAUCET_*: the funded devnet wallet, and what
// one drop is. Off (no keypair) is the default.
func loadDevnetFaucet(c *DevnetFaucetConfig) error {
	c.Keypair = strings.TrimSpace(os.Getenv("DEVNET_FAUCET_KEYPAIR"))
	c.KeypairFile = strings.TrimSpace(os.Getenv("DEVNET_FAUCET_KEYPAIR_FILE"))
	c.RPCURL = strings.TrimSpace(os.Getenv("DEVNET_FAUCET_RPC_URL"))
	if c.Keypair != "" && c.KeypairFile != "" {
		return errors.New("config: choose DEVNET_FAUCET_KEYPAIR or DEVNET_FAUCET_KEYPAIR_FILE, not both")
	}
	c.SOLLamports = 200_000_000 // 0.2 SOL
	if v := strings.TrimSpace(os.Getenv("DEVNET_FAUCET_SOL")); v != "" {
		n, err := chain.ParseUnits(v, 9)
		if err != nil || n <= 0 || n > 5_000_000_000 {
			return errors.New("config: DEVNET_FAUCET_SOL must be an amount of SOL above 0 and at most 5, like 0.2")
		}
		c.SOLLamports = uint64(n)
	}
	c.USDCMinor = 2_000_000 // 2 USDC
	if v := strings.TrimSpace(os.Getenv("DEVNET_FAUCET_USDC")); v != "" {
		n, err := chain.ParseUnits(v, chain.USDCDecimals)
		if err != nil || n <= 0 || n > 100_000_000 {
			return errors.New("config: DEVNET_FAUCET_USDC must be an amount of USDC above 0 and at most 100, like 2")
		}
		c.USDCMinor = uint64(n)
	}
	perDay, err := getInt64("DEVNET_FAUCET_PER_DAY", 200)
	if err != nil {
		return err
	}
	if perDay < 1 || perDay > 100_000 {
		return errors.New("config: DEVNET_FAUCET_PER_DAY must be between 1 and 100000")
	}
	c.PerDay = int(perDay)
	return nil
}

// loadSolanaRails gathers every Solana rail to run: the one SOLANA_CLUSTER
// names (if any), plus devnet from SOLANA_DEVNET_KEYPAIR_FILE (or _KEYPAIR)
// and mainnet from SOLANA_MAINNET_KEYPAIR_FILE (or _KEYPAIR), each with its
// own optional _RPC_URL. Mainnet still needs SOLANA_ALLOW_MAINNET=yes, and a
// cluster configured twice is an error rather than a guess.
func loadSolanaRails(legacy SolanaConfig) ([]SolanaConfig, error) {
	var out []SolanaConfig
	if legacy.Cluster != "" {
		out = append(out, legacy)
	}
	for _, cl := range []struct{ cluster, prefix string }{{"devnet", "SOLANA_DEVNET_"}, {"mainnet", "SOLANA_MAINNET_"}} {
		c := SolanaConfig{
			Cluster:     cl.cluster,
			RPCURL:      strings.TrimSpace(os.Getenv(cl.prefix + "RPC_URL")),
			Keypair:     strings.TrimSpace(os.Getenv(cl.prefix + "KEYPAIR")),
			KeypairFile: strings.TrimSpace(os.Getenv(cl.prefix + "KEYPAIR_FILE")),

			SignerPubkey:  strings.TrimSpace(os.Getenv(cl.prefix + "SIGNER_PUBKEY")),
			KMSKeyVersion: strings.TrimSpace(os.Getenv(cl.prefix + "KMS_KEY_VERSION")),
		}
		if err := checkKMSKeyVersion(cl.prefix+"KMS_KEY_VERSION", c.KMSKeyVersion); err != nil {
			return nil, err
		}
		switch v := strings.ToLower(strings.TrimSpace(os.Getenv(cl.prefix + "SIGNER"))); v {
		case "":
		case "generated":
			if cl.cluster != "devnet" {
				return nil, fmt.Errorf("config: %sSIGNER=generated is for devnet only. Real money needs a key held outside this server or one the operator controls", cl.prefix)
			}
			if c.Keypair != "" || c.KeypairFile != "" || c.KMSKeyVersion != "" {
				return nil, fmt.Errorf("config: choose %sSIGNER=generated, a keypair or a Cloud KMS key, not several", cl.prefix)
			}
			c.Signer = v
		default:
			return nil, fmt.Errorf("config: %sSIGNER must be \"generated\" or empty, not %q", cl.prefix, v)
		}
		if c.KMSKeyVersion != "" && (c.Keypair != "" || c.KeypairFile != "") {
			return nil, fmt.Errorf("config: choose %sKMS_KEY_VERSION or a keypair, not both", cl.prefix)
		}
		if c.Keypair == "" && c.KeypairFile == "" && c.Signer == "" && c.KMSKeyVersion == "" {
			continue
		}
		if cl.cluster == "mainnet" && strings.ToLower(os.Getenv("SOLANA_ALLOW_MAINNET")) != "yes" {
			return nil, errors.New("config: SOLANA_MAINNET_KEYPAIR pays with real money; set SOLANA_ALLOW_MAINNET=yes to confirm that is intended")
		}
		for _, have := range out {
			if have.Cluster == cl.cluster {
				return nil, fmt.Errorf("config: the Solana %s rail is configured twice (SOLANA_CLUSTER and %s*); keep one", cl.cluster, cl.prefix)
			}
		}
		c.MaxPaymentMinor = 1_000_000
		if v := strings.TrimSpace(os.Getenv("SOLANA_MAX_PAYMENT_USDC")); v != "" {
			n, err := chain.ParseUnits(v, chain.USDCDecimals)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("config: SOLANA_MAX_PAYMENT_USDC must be a positive amount like 0.50: %v", err)
			}
			c.MaxPaymentMinor = n
		}
		out = append(out, c)
	}
	return out, nil
}

// loadPaySh reads the Pay.sh catalog settings. The URLs, if set, must be
// https with no credentials: they are fetched by the server, so an operator
// typo shouldn't turn into a request to somewhere else.
func loadPaySh(c *PayShConfig) error {
	c.Enabled = !strings.EqualFold(strings.TrimSpace(os.Getenv("PAYSH_ENABLED")), "off")
	for _, v := range []struct {
		name string
		dst  *string
	}{{"PAYSH_CATALOG_URL", &c.CatalogURL}, {"PAYSH_DOCS_URL", &c.DocsURL}} {
		*v.dst = strings.TrimSpace(os.Getenv(v.name))
		if *v.dst == "" {
			continue
		}
		u, err := url.Parse(*v.dst)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return fmt.Errorf("config: %s must be an https URL without credentials", v.name)
		}
	}
	return nil
}

// loadDirectory reads a discovery directory's settings, with the same URL
// rule as Pay.sh's.
func loadDirectory(c *CircleConfig, enabledVar, urlVar string) error {
	c.Enabled = !strings.EqualFold(strings.TrimSpace(os.Getenv(enabledVar)), "off")
	c.DiscoveryURL = strings.TrimSpace(os.Getenv(urlVar))
	if c.DiscoveryURL != "" {
		u, err := url.Parse(c.DiscoveryURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return fmt.Errorf("config: %s must be an https URL without credentials", urlVar)
		}
	}
	return nil
}

// SolanaFromEnv reads only the Solana settings (and a local .env), for
// command-line tools that don't need the rest of the server's configuration.
func SolanaFromEnv() (SolanaConfig, error) {
	_ = godotenv.Load()
	var c SolanaConfig
	err := loadSolana(&c)
	return c, err
}

// checkMainnetExecution refuses real-money payments that aren't bounded on
// chain: with MAINNET_EXECUTION=on every mainnet payment must come from an
// on-chain Spend Pass, so the payer never holds money that is no pass's and
// the program's limits apply to all of it.
func checkMainnetExecution(execution bool, required map[string]bool) error {
	if execution && !required["mainnet"] {
		return errors.New("config: MAINNET_EXECUTION=on pays with real money; set SPEND_PASS_REQUIRED=mainnet as well, so that every mainnet payment comes from an on-chain Spend Pass")
	}
	return nil
}

// loadTestMoney reads TEST_MONEY_*: the test-money guard's daily counts.
func loadTestMoney(c *TestMoneyConfig) error {
	c.Off = strings.EqualFold(strings.TrimSpace(os.Getenv("TEST_MONEY_GUARD")), "off")
	perAccount, err := getInt64("TEST_MONEY_PER_ACCOUNT_PER_DAY", 25)
	if err != nil {
		return err
	}
	if perAccount < 1 || perAccount > 10_000 {
		return errors.New("config: TEST_MONEY_PER_ACCOUNT_PER_DAY must be between 1 and 10000")
	}
	perDay, err := getInt64("TEST_MONEY_PER_DAY", 400)
	if err != nil {
		return err
	}
	if perDay < 1 || perDay > 1_000_000 {
		return errors.New("config: TEST_MONEY_PER_DAY must be between 1 and 1000000")
	}
	c.PerAccountPerDay, c.PerDay = int(perAccount), int(perDay)
	c.CreditsOff = strings.EqualFold(strings.TrimSpace(os.Getenv("TEST_CREDITS")), "off")
	usdc, err := getInt64("TEST_CREDITS_USDC", 10)
	if err != nil {
		return err
	}
	sol, err := getInt64("TEST_CREDITS_SOL", 5)
	if err != nil {
		return err
	}
	if usdc < 0 || usdc > 1_000 || sol < 0 || sol > 1_000 {
		return errors.New("config: TEST_CREDITS_USDC and TEST_CREDITS_SOL must be between 0 and 1000")
	}
	c.CreditsUSDCMinor, c.CreditsSOLLamports = usdc*1_000_000, sol*1_000_000_000
	return nil
}

// loadOnchainPasses reads the on-chain Spend Pass settings. The vault is
// opt-in: without ONCHAIN_PASSES=on the program is never touched and the
// other SPEND_PASS_* settings do nothing.
func loadOnchainPasses(c *OnchainPassConfig) error {
	c.ProgramID = strings.TrimSpace(os.Getenv("SPEND_PASS_PROGRAM_ID"))
	if strings.EqualFold(c.ProgramID, "off") {
		c.ProgramID, c.Disabled = "", true
	}
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("ONCHAIN_PASSES")), "on") {
		c.Disabled = true
	}
	clusters := func(name string) (map[string]bool, error) {
		out := map[string]bool{}
		for _, v := range strings.Split(os.Getenv(name), ",") {
			switch v = strings.ToLower(strings.TrimSpace(v)); v {
			case "":
			case "devnet", "mainnet":
				out[v] = true
			default:
				return nil, fmt.Errorf("config: %s lists clusters (devnet, mainnet), not %q", name, v)
			}
		}
		return out, nil
	}
	var err error
	if c.Required, err = clusters("SPEND_PASS_REQUIRED"); err != nil {
		return err
	}
	if c.AnyOwner, err = clusters("SPEND_PASS_ANY_OWNER"); err != nil {
		return err
	}
	if c.AnyOwner["mainnet"] {
		return errors.New("config: SPEND_PASS_ANY_OWNER can't include mainnet: there a pass's owner must be a wallet the person signed in with")
	}
	c.PriorityMicroLamports = 10_000
	if v := strings.TrimSpace(os.Getenv("SPEND_PASS_PRIORITY_MICROLAMPORTS")); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return fmt.Errorf("config: SPEND_PASS_PRIORITY_MICROLAMPORTS must be a whole number: %v", err)
		}
		c.PriorityMicroLamports = n
	}
	return nil
}
