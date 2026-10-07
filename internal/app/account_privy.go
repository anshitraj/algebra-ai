package app

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// UserWalletStore keeps the wallets people signed in with or were given at
// sign-in. Public addresses only.
type UserWalletStore interface {
	// SaveUserWallets records wallets as the user's. A wallet already
	// recorded for another user is left with them.
	SaveUserWallets(ctx context.Context, userID string, wallets []account.Wallet, at time.Time) error
	ListUserWallets(ctx context.Context, userID string) ([]account.Wallet, error)
}

// SetWalletStore turns on keeping sign-in wallets.
func (s *AccountService) SetWalletStore(w UserWalletStore) { s.wallets = w }

// Wallets lists the user's sign-in wallets (none when no store is set).
func (s *AccountService) Wallets(ctx context.Context, userID string) ([]account.Wallet, error) {
	if s.wallets == nil {
		return nil, nil
	}
	return s.wallets.ListUserWallets(ctx, userID)
}

// IsWalletOnlyEmail reports whether an account's email is the placeholder
// given to someone who signed in with a wallet and no email.
func IsWalletOnlyEmail(email string) bool {
	return strings.HasSuffix(email, "@"+account.WalletEmailDomain)
}

// SignInWithPrivy signs in (or up) with a verified Privy identity: p names
// the Privy user (provider "privy", their DID), with an email when Privy
// verified one, and wallets are their Solana wallets.
//
// With a verified email it is an OAuth sign-in like Google's, linking to an
// existing account with that email. With a wallet and no email the account
// is found by the Privy user alone, never by email, and a new one gets an
// undeliverable placeholder address (account.WalletEmailDomain).
func (s *AccountService) SignInWithPrivy(ctx context.Context, p account.OAuthProfile, wallets []account.Wallet, meta account.ClientMeta) (*SignInResult, error) {
	if p.Provider == "" || p.ProviderUserID == "" {
		return nil, errors.New("privy identity is missing the user")
	}
	var (
		res *SignInResult
		err error
	)
	if p.Email != "" && p.EmailVerified {
		res, err = s.SignInWithOAuth(ctx, p, meta)
	} else {
		if len(wallets) == 0 {
			return nil, errors.New("sign in with an email or a Solana wallet")
		}
		res, err = s.signInWithWallet(ctx, p, meta)
	}
	if err != nil {
		return nil, err
	}
	if s.wallets != nil && len(wallets) > 0 {
		// The person is signed in either way; a wallet that can't be recorded
		// only goes missing from their settings.
		if err := s.wallets.SaveUserWallets(ctx, res.User.ID, wallets, s.now()); err != nil {
			log.Printf("accounts: recording %d sign-in wallet(s) for %s: %v", len(wallets), res.User.ID, err)
		}
	}
	return res, nil
}

func (s *AccountService) signInWithWallet(ctx context.Context, p account.OAuthProfile, meta account.ClientMeta) (*SignInResult, error) {
	if userID, err := s.store.GetOAuthIdentity(ctx, p.Provider, p.ProviderUserID); err == nil {
		u, err := s.store.GetUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		s.fillProfile(ctx, u, p)
		return s.startSession(ctx, u, meta)
	} else if !errors.Is(err, shared.ErrNotFound) {
		return nil, err
	}

	now := s.now()
	id := newID("user")
	// No name unless the person gives one: the console shows the wallet.
	u := &account.User{
		ID: id, Email: "wallet-" + strings.TrimPrefix(id, "user_") + "@" + account.WalletEmailDomain,
		Name: strings.TrimSpace(p.Name), AvatarURL: p.AvatarURL, CreatedAt: now,
	}
	if err := s.store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	if err := s.store.LinkOAuthIdentity(ctx, p.Provider, p.ProviderUserID, u.ID, "", now); err != nil {
		return nil, err
	}
	// Two first sign-ins at once (a double click) both create an account;
	// only one is linked, and both sessions go to that one.
	if linked, err := s.store.GetOAuthIdentity(ctx, p.Provider, p.ProviderUserID); err == nil && linked != u.ID {
		if u, err = s.store.GetUser(ctx, linked); err != nil {
			return nil, err
		}
		return s.startSession(ctx, u, meta)
	}
	res, err := s.startSession(ctx, u, meta)
	if err != nil {
		return nil, err
	}
	res.Created = true
	return res, nil
}
