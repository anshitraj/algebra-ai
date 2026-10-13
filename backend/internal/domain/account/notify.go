package account

import (
	"strings"
	"time"
)

// NotifySettings is where a person's email notifications go and which kinds
// they get. The zero value of Email means "the address they sign in with",
// when they have one.
type NotifySettings struct {
	UserID string
	// Email is an address the person added and confirmed: notifications go
	// here instead of the account's own.
	Email           string
	EmailVerifiedAt *time.Time
	// PendingEmail was asked for and waits for its confirmation link. Only
	// that link is ever sent to it.
	PendingEmail     string
	PendingTokenHash string
	PendingExpiresAt *time.Time
	PendingSentAt    *time.Time
	// SignIns: an email after each sign-in. Receipts: an email with the
	// receipt of each payment that lands on a chain.
	SignIns  bool
	Receipts bool
}

// DefaultNotifySettings is what an account has before it saves any: both
// kinds on, no added address.
func DefaultNotifySettings(userID string) *NotifySettings {
	return &NotifySettings{UserID: userID, SignIns: true, Receipts: true}
}

// ConfirmedEmail is the added address, once its link was opened.
func (s *NotifySettings) ConfirmedEmail() string {
	if s == nil || s.EmailVerifiedAt == nil {
		return ""
	}
	return s.Email
}

// Deliverable reports whether mail can reach an address. The placeholders
// Algebra gives accounts without an email (a wallet sign-in, a demo, an
// erased account) are all under the reserved .invalid top-level domain.
func Deliverable(email string) bool {
	e := strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(e, "@")
	if at <= 0 || at == len(e)-1 {
		return false
	}
	domain := e[at+1:]
	return domain != "invalid" && !strings.HasSuffix(domain, ".invalid")
}

const emailTokenPrefix = "alg_mail_"

// NewEmailToken is the token in the link that confirms an added address:
// the raw value goes in the email, its hash in the database.
func NewEmailToken() (raw, hash string, err error) { return newToken(emailTokenPrefix) }
