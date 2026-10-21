package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Sign in with an emailed code: someone types their address, Algebra emails a
// six-digit code, and the code signs them in (making the account the first
// time). No password and no wallet. See account.LoginCode* for the limits.

// ErrLoginCodesOff is returned when this server has no emailed-code sign-in.
var ErrLoginCodesOff = errors.New("sign-in with an emailed code is turned off on this server")

// LoginCodeOutcome is what checking a code found.
type LoginCodeOutcome int

const (
	// LoginCodeAccepted: the right code, live. The check used it up.
	LoginCodeAccepted LoginCodeOutcome = iota
	// LoginCodeWrong: a live code, but not this one. A try was counted.
	LoginCodeWrong
	// LoginCodeGone: no live code: never sent, expired, used, or tried too often.
	LoginCodeGone
)

// LoginCodeStore keeps each address's one live login code.
type LoginCodeStore interface {
	// LoginCodeSentAt is when the address's live code was made, or the zero time when it has none.
	LoginCodeSentAt(ctx context.Context, addressKey string) (time.Time, error)
	// PutLoginCode makes codeHash the address's one live code. It replaces any earlier
	// one, and the count of wrong tries starts again from zero.
	PutLoginCode(ctx context.Context, addressKey, codeHash string, at, expires time.Time) error
	// TryLoginCode checks a code in one step. The right one is used up; a wrong one is
	// counted, and a code that has been wrong maxTries times is gone.
	TryLoginCode(ctx context.Context, addressKey, codeHash string, now time.Time, maxTries int) (LoginCodeOutcome, error)
}

// SetLoginCodes turns on sign-in with an emailed code. key is a secret of the
// server's own: stored codes are keyed hashes under it.
func (s *AccountService) SetLoginCodes(store LoginCodeStore, key []byte) {
	s.codes, s.codeKey = store, key
}

// LoginCodesOn reports whether sign-in with an emailed code is available.
func (s *AccountService) LoginCodesOn() bool { return s.codes != nil && len(s.codeKey) > 0 }

// RequestLoginCode emails a six-digit code to an address. It answers the same
// whether or not the address has an account, because signing in with a code
// makes one. A second request inside account.LoginCodeCooldown is refused with
// *account.LoginCodeTooSoon.
func (s *AccountService) RequestLoginCode(ctx context.Context, email, linkBase string) error {
	if !s.LoginCodesOn() {
		return ErrLoginCodesOff
	}
	address, err := account.NormalizeEmail(email)
	if err != nil {
		return err
	}
	key := account.LoginCodeAddressKey(s.codeKey, address)
	now := s.now()
	if sent, err := s.codes.LoginCodeSentAt(ctx, key); err != nil {
		return err
	} else if !sent.IsZero() {
		if wait := account.LoginCodeCooldown - now.Sub(sent); wait > 0 {
			return &account.LoginCodeTooSoon{RetryAfter: wait}
		}
	}
	code, err := account.NewLoginCode()
	if err != nil {
		return err
	}
	if err := s.codes.PutLoginCode(ctx, key, account.LoginCodeHash(s.codeKey, address, code), now, now.Add(account.LoginCodeTTL)); err != nil {
		return err
	}
	doc := EmailDoc{
		Preheader: code + " is your Algebra sign-in code. It works once, for 10 minutes.",
		Heading:   "Your sign-in code",
		Lead:      []string{"Enter this code on the Algebra sign-in page:"},
		Hero:      &EmailHero{Value: code, Caption: "works once, for 10 minutes"},
		Notes:     []string{"If you didn't ask to sign in, ignore this email: nobody can get in without the code."},
	}
	return SendEmail(ctx, s.mailer, BrandFor(linkBase), address, "Your Algebra sign-in code: "+code, doc, "", nil)
}

// SignInWithLoginCode signs in with the code emailed to an address, making the
// account when the address has none. Having the code proves the address, so
// the address counts as verified.
func (s *AccountService) SignInWithLoginCode(ctx context.Context, email, code string, meta account.ClientMeta) (*SignInResult, error) {
	if !s.LoginCodesOn() {
		return nil, ErrLoginCodesOff
	}
	address, err := account.NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	code = strings.TrimSpace(code)
	if !account.ValidLoginCodeShape(code) {
		return nil, account.ErrLoginCodeWrong
	}
	now := s.now()
	switch outcome, err := s.codes.TryLoginCode(ctx, account.LoginCodeAddressKey(s.codeKey, address), account.LoginCodeHash(s.codeKey, address, code), now, account.LoginCodeAttempts); {
	case err != nil:
		return nil, err
	case outcome == LoginCodeWrong:
		return nil, account.ErrLoginCodeWrong
	case outcome != LoginCodeAccepted:
		return nil, account.ErrLoginCodeGone
	}

	created := false
	u, err := s.store.GetUserByEmail(ctx, address)
	switch {
	case err == nil:
		if u.EmailVerifiedAt == nil {
			_ = s.store.MarkEmailVerified(ctx, u.ID, now)
			u.EmailVerifiedAt = &now
		}
	case errors.Is(err, shared.ErrNotFound):
		u = &account.User{ID: newID("user"), Email: address, EmailVerifiedAt: &now, CreatedAt: now}
		if err := s.store.CreateUser(ctx, u); err != nil {
			if !errors.Is(err, shared.ErrConflict) {
				return nil, err
			}
			// Two first sign-ins at once: both are the account that won.
			if u, err = s.store.GetUserByEmail(ctx, address); err != nil {
				return nil, err
			}
		} else {
			created = true
		}
	default:
		return nil, err
	}
	res, err := s.startSession(ctx, u, meta)
	if err != nil {
		return nil, err
	}
	res.Created = created
	return res, nil
}
