// Package payment defines PaymentSource and the CardVaultProvider
// abstraction. Algebra is non-custodial by design: it never stores PAN,
// CVV, wallet private keys, or seed phrases. PaymentSource structurally
// cannot carry any of those — there is no field for them anywhere in this
// package.
package payment

import "time"

// SourceType is the kind of rail a PaymentSource executes over.
type SourceType string

const (
	SourceCard              SourceType = "CARD"
	SourceCryptoCard        SourceType = "CRYPTO_CARD"
	SourceVirtualCard       SourceType = "VIRTUAL_CARD"
	SourceWallet            SourceType = "WALLET"
	SourceStablecoinAccount SourceType = "STABLECOIN_ACCOUNT"
	SourceBank              SourceType = "BANK"
	SourceUPI               SourceType = "UPI"
)

// Capabilities describes what a PaymentSource can do, so callers (policy,
// the approval UI, the agent-facing payments.get_spending_capability tool)
// can reason about it without ever touching the underlying credential.
type Capabilities struct {
	CanPay                     bool     `json:"can_pay"`
	SupportedCurrencies        []string `json:"supported_currencies"`
	MerchantRestrictions       []string `json:"merchant_restrictions,omitempty"`
	TransactionLimitMinorUnits int64    `json:"transaction_limit_minor_units,omitempty"`
	RequiresUserAuth           bool     `json:"requires_user_auth"`
	Requires3DS                bool     `json:"requires_3ds"`
}

// PaymentSource is what an agent operates on: an alias-scoped reference
// (e.g. "payment:personal") to money, never the money-moving credential
// itself. ProviderTokenRef is an opaque reference into a CardVaultProvider
// (or a wallet's non-custodial signing flow) — never a PAN, never
// decryptable by Algebra into one.
type PaymentSource struct {
	ID     string     `json:"id"`
	UserID string     `json:"user_id"`
	Alias  string     `json:"alias"`
	Type   SourceType `json:"type"`

	// ProviderMode is sandbox|real, set by whichever CardVaultProvider
	// tokenized this source (ProviderSandbox.Tokenize sets it from its own
	// Mode()) — surfaced so a sandbox token can never be mistaken for a
	// real one downstream.
	ProviderMode ProviderMode `json:"provider_mode"`

	// ProviderTokenRef is a vault-issued token reference. It is treated as a
	// secret even though it isn't a PAN: never logged, never returned
	// through MCP, never shown to an agent.
	ProviderTokenRef string `json:"-"`

	Network          string            `json:"network,omitempty"` // e.g. "visa", "mastercard"
	Last4            string            `json:"last4,omitempty"`
	IssuerMeta       map[string]string `json:"issuer_meta,omitempty"`
	ExpiryMeta       string            `json:"expiry_meta,omitempty"` // e.g. "12/2027", never CVV
	Nickname         string            `json:"nickname,omitempty"`
	BillingProfileID string            `json:"-"` // resolved only by PrivacyResolver at execution time

	Capabilities Capabilities `json:"capabilities"`

	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Safe returns a copy with ProviderTokenRef and BillingProfileID cleared —
// the shape that is safe to serialize into an MCP tool result or REST
// response. Callers must use this (not the raw struct) on any path that
// might reach an agent.
func (p PaymentSource) Safe() PaymentSource {
	p.ProviderTokenRef = ""
	p.BillingProfileID = ""
	return p
}

func (p *PaymentSource) IsRevoked() bool { return p.RevokedAt != nil }

// ChallengeMechanism is the kind of strong-customer-authentication step a
// payment execution may require. Per mandate §23, authentication is not a
// failure — it's a state (AUTHENTICATION_REQUIRED) with an explicit next
// step, and the agent never receives the OTP/challenge secret itself.
type ChallengeMechanism string

const (
	ChallengeThreeDS         ChallengeMechanism = "3DS"
	ChallengeOTP             ChallengeMechanism = "OTP"
	ChallengeUPIApproval     ChallengeMechanism = "UPI_APPROVAL"
	ChallengePasskey         ChallengeMechanism = "PASSKEY"
	ChallengeWalletSignature ChallengeMechanism = "WALLET_SIGNATURE"
	ChallengeMerchantAuth    ChallengeMechanism = "MERCHANT_AUTH"
)

// AuthorizationChallenge is returned when execution needs the user to
// complete strong customer authentication. Algebra relays the challenge
// (e.g. a redirect URL) to the user's own device/session — it never
// attempts to intercept, store, or forward the OTP/credential itself.
type AuthorizationChallenge struct {
	ChallengeID string             `json:"challenge_id"`
	IntentID    string             `json:"intent_id"`
	Mechanism   ChallengeMechanism `json:"mechanism"`
	RedirectURL string             `json:"redirect_url,omitempty"`
	ExpiresAt   time.Time          `json:"expires_at"`
	Status      string             `json:"status"`
}
