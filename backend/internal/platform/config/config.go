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
	// MaxPaymentMinor is the most one payment may be, in micro-USDC: a hard
	// ceiling in the rail itself, whatever any policy says.
	MaxPaymentMinor int64
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

	// PrivyAppID turns on sign-in with Privy (email, Google or a Solana
	// wallet; anyone without a wallet is given an embedded Solana one).
	// PrivyVerificationKey is the app's PEM verification key from the
	// Privy dashboard, optional: without it the key is fetched from
	// Privy's JWKS for the app.
	PrivyAppID           string
	PrivyVerificationKey string

	// ResendAPIKey/EmailFrom enable real password-reset email. Without
	// them, reset links are written to the API's log (development only).
	ResendAPIKey string
	EmailFrom    string

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

	// OperatorToken (ALGEBRA_OPERATOR_TOKEN) gates B2B registration —
	// POST /tenants and POST /integrators — and lets the operator revoke any
	// tenant or integrator. Sent as the X-Algebra-Operator-Token header.
	// Unset: registration stays open in development and is closed in
	// production.
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

	cfg.GoogleSearchAPIKey = os.Getenv("GOOGLE_SEARCH_API_KEY")
	cfg.GoogleSearchEngineID = os.Getenv("GOOGLE_SEARCH_ENGINE_ID")
	cfg.GeminiAPIKey = getEnv("GEMINI_API_KEY", os.Getenv("GOOGLE_GEMINI_API"))
	cfg.MCPPublicURL = strings.TrimRight(os.Getenv("MCP_PUBLIC_URL"), "/")
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
	if err := loadJupiter(&cfg.Jupiter); err != nil {
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
		PublicWebURL:         strings.TrimRight(getEnv("PUBLIC_WEB_URL", "http://localhost:3000"), "/"),
		GoogleClientID:       os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:   os.Getenv("GOOGLE_CLIENT_SECRET"),
		GitHubClientID:       os.Getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret:   os.Getenv("GITHUB_CLIENT_SECRET"),
		PrivyAppID:           strings.TrimSpace(os.Getenv("PRIVY_APP_ID")),
		PrivyVerificationKey: os.Getenv("PRIVY_VERIFICATION_KEY"),
		ResendAPIKey:         os.Getenv("RESEND_API_KEY"),
		EmailFrom:            getEnv("EMAIL_FROM", "Algebra <no-reply@algebra.local>"),
		SessionTTL:           sessionTTL,
		DevHeaderAuth:        os.Getenv("ALGEBRA_DEV_AUTH") == "true",
		DemoAccounts:         !strings.EqualFold(os.Getenv("DEMO_ACCOUNTS"), "off"),
		PasswordLogin:        !strings.EqualFold(os.Getenv("PASSWORD_LOGIN"), "off"),
		OperatorToken:        strings.TrimSpace(os.Getenv("ALGEBRA_OPERATOR_TOKEN")),
		Production:           cfg.Env == "production",

		AgentTurnsPerDay:        int(agentTurns),
		DemoAgentTurnsPerDay:    int(demoAgentTurns),
		DemoAccountsPerIPPerDay: int(demoPerIP),
		TrustedProxyHops:        int(proxyHops),
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
	for _, o := range c.CORSAllowedOrigins {
		if !strings.HasPrefix(o, "https://") {
			problems = append(problems, fmt.Sprintf("CORS_ALLOWED_ORIGINS entry %q must be https://", o))
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
	if c.Keypair == "" && c.KeypairFile == "" {
		return errors.New("config: SOLANA_CLUSTER is set but there is no wallet: set SOLANA_KEYPAIR_FILE (preferred) or SOLANA_KEYPAIR")
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
		}
		if c.Keypair == "" && c.KeypairFile == "" {
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
