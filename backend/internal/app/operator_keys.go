package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Web-tool keys kept by the operator.
//
// A vendor key in the server's environment (TINY_FISH_API, EXA_API, ...) is the
// operator's own account with that vendor. Every call made with it is billed
// there, so it is never offered to everyone: only to the accounts named in
// ALGEBRA_OPERATOR_EMAILS, and only until the person saves a key of their own.
// Nothing here stores a key; it lives in the process, read from the
// environment at start, and goes only to the vendor's own address (the keyed
// runner checks that, as it does for a saved key).

// OperatorAccounts says which accounts are the operator's own.
type OperatorAccounts interface {
	IsOperator(ctx context.Context, userID string) bool
}

// OperatorUsers finds a first-party account by its address.
type OperatorUsers interface {
	UserByEmail(ctx context.Context, email string) (*account.User, error)
}

// SetOperatorKeys gives the service the vendor keys the operator keeps in the
// environment, by vendor id, for the accounts who names. A key that isn't the
// shape of one, or a vendor Algebra has no tools for, is an error that names
// the vendor and never the key.
func (s *ToolKeys) SetOperatorKeys(keys map[string]string, who OperatorAccounts) error {
	own := make(map[string]string, len(keys))
	for vendor, key := range keys {
		if !s.known(vendor) {
			return fmt.Errorf("app: there are no web tools for %q to keep a key for", vendor)
		}
		if !validToolKey(key) {
			return fmt.Errorf("app: the %s key in the environment doesn't look like an API key (%d to %d printable characters, no spaces)", vendor, minToolKey, maxToolKey)
		}
		own[vendor] = key
	}
	s.operatorKeys, s.operators = own, who
	return nil
}

// isOperator reports whether the operator has keys to give and the account is
// one of the operator's own.
func (s *ToolKeys) isOperator(ctx context.Context, userID string) bool {
	return len(s.operatorKeys) > 0 && s.operators != nil && s.operators.IsOperator(ctx, userID)
}

// operatorKey is the operator's key for a vendor, for an account the operator
// owns.
func (s *ToolKeys) operatorKey(ctx context.Context, userID, vendor string) (string, bool) {
	key := s.operatorKeys[vendor]
	if key == "" || !s.isOperator(ctx, userID) {
		return "", false
	}
	return key, true
}

// OperatorEmails recognises the operator's own accounts by the addresses they
// sign in with. An account counts only when it is a first-party account whose
// person has proved the address (a code, or a verified provider), is not a
// demo, and has no password: a password set on an account before its address
// was verified may be somebody else's, and these accounts spend the operator's
// money. The set is looked up from the addresses at most every half minute, so
// an account the operator signs in with for the first time counts within it.
type OperatorEmails struct {
	emails []string
	users  OperatorUsers
	now    func() time.Time

	mu     sync.Mutex
	ids    map[string]struct{}
	loaded time.Time
}

const operatorAccountsTTL = 30 * time.Second

// NewOperatorEmails builds the check from normalised addresses.
func NewOperatorEmails(emails []string, users OperatorUsers) *OperatorEmails {
	return &OperatorEmails{emails: emails, users: users, now: time.Now}
}

// IsOperator reports whether the account is one of the operator's own. A
// failed lookup says no this time and is tried again on the next call.
func (o *OperatorEmails) IsOperator(ctx context.Context, userID string) bool {
	if o == nil || userID == "" || len(o.emails) == 0 || o.users == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ids == nil || o.now().Sub(o.loaded) >= operatorAccountsTTL {
		ids, err := o.lookup(ctx)
		if err != nil {
			return false
		}
		o.ids, o.loaded = ids, o.now()
	}
	_, yes := o.ids[userID]
	return yes
}

func (o *OperatorEmails) lookup(ctx context.Context) (map[string]struct{}, error) {
	ids := make(map[string]struct{}, len(o.emails))
	for _, email := range o.emails {
		u, err := o.users.UserByEmail(ctx, email)
		if errors.Is(err, shared.ErrNotFound) {
			continue // not signed in yet
		}
		if err != nil {
			return nil, err
		}
		if u.EmailVerifiedAt != nil && !u.IsDemo() && !u.HasPassword() {
			ids[u.ID] = struct{}{}
		}
	}
	return ids, nil
}
