package app

import (
	"context"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
)

// UserWalletStore keeps the wallets recorded for accounts, as public
// addresses only. Accounts no longer get a wallet at sign-in (sign-in is a code
// emailed to the person), so what is here is what earlier sign-ins left.
type UserWalletStore interface {
	// SaveUserWallets records wallets as the user's. A wallet already
	// recorded for another user is left with them.
	SaveUserWallets(ctx context.Context, userID string, wallets []account.Wallet, at time.Time) error
	ListUserWallets(ctx context.Context, userID string) ([]account.Wallet, error)
}

// SetWalletStore turns on reading the wallets recorded for accounts.
func (s *AccountService) SetWalletStore(w UserWalletStore) { s.wallets = w }

// Wallets lists the user's recorded wallets (none when no store is set).
func (s *AccountService) Wallets(ctx context.Context, userID string) ([]account.Wallet, error) {
	if s.wallets == nil {
		return nil, nil
	}
	return s.wallets.ListUserWallets(ctx, userID)
}

// IsWalletOnlyEmail reports whether an account's email is the placeholder
// given to an account that was made with a wallet and no email.
func IsWalletOnlyEmail(email string) bool {
	return strings.HasSuffix(email, "@"+account.WalletEmailDomain)
}
