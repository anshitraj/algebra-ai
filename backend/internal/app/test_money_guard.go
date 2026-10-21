package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// Denial codes for a payment the test-money guard refuses.
const (
	// DenyTestMoneyAccount: the account has made today's allowance of payments
	// from Algebra's own test wallet.
	DenyTestMoneyAccount = "TEST_MONEY_ACCOUNT_LIMIT"
	// DenyTestMoneyDay: everyone together have made today's allowance.
	DenyTestMoneyDay = "TEST_MONEY_DAY_LIMIT"
)

// testMoneyWindow is how long a count lasts: a fixed window from the first
// payment, so "a day" is a day after it, not midnight.
const testMoneyWindow = 24 * time.Hour

// PassOwners says whose a pass is (the pass store).
type PassOwners interface {
	Get(ctx context.Context, id string) (*spendpass.Pass, error)
}

// TestMoneyGuard keeps the shared devnet payer from being drained. On devnet
// Algebra pays real sellers from one wallet of its own, with test money, so
// there is no vault behind a person's payment; what bounds one stranger is a
// count of payments a day for the account and for the whole server. A payment
// is also at most the rail's own cap, and the pass's budget and limits apply
// first, as always.
//
// It is a PassFunder that moves no money: Fund only counts, and refuses
// once a count is used up. It touches only the rails it was made for (devnet),
// never the sandbox and never mainnet, whose payments come from a vault.
//
// The counts are in Redis when there is one, so every instance of the server
// shares them, and in memory otherwise. A count that can't be read refuses the
// payment: a safety control that fails open is no control.
type TestMoneyGuard struct {
	rails      map[string]bool
	passes     PassOwners
	limiter    RateLimiter
	perAccount int
	perDay     int
}

// NewTestMoneyGuard guards the named rails: at most perAccount payments a
// day for one account and perDay for everyone. A nil limiter counts in memory.
func NewTestMoneyGuard(rails []string, passes PassOwners, limiter RateLimiter, perAccount, perDay int) *TestMoneyGuard {
	if limiter == nil {
		limiter = newWindowCounter(time.Now)
	}
	g := &TestMoneyGuard{rails: map[string]bool{}, passes: passes, limiter: limiter, perAccount: perAccount, perDay: perDay}
	for _, r := range rails {
		g.rails[r] = true
	}
	return g
}

// Check never refuses: freezing and revoking a pass are on the pass itself.
func (g *TestMoneyGuard) Check(context.Context, string) error { return nil }

// Funding says the payment is not from a vault: the payer pays from its own wallet.
func (g *TestMoneyGuard) Funding(context.Context, string, string) (bool, error) {
	return false, nil
}

// Fund counts one payment against the account and against the server. It
// returns no pull: the money is the payer's own.
func (g *TestMoneyGuard) Fund(ctx context.Context, passID string, rv *econ.Reservation, amountMinor int64) (*OnchainPull, error) {
	if g == nil || !g.rails[rv.Rail] || amountMinor <= 0 {
		return nil, nil
	}
	pass, err := g.passes.Get(ctx, passID)
	if err != nil {
		return nil, fmt.Errorf("test money: reading the pass: %w", err)
	}
	account := pass.UserID
	if account == "" {
		account = pass.ID
	}
	// The account first, so a person at their own limit doesn't also use up
	// the server's.
	ok, _, err := g.limiter.Allow(ctx, "testmoney:acct:"+account, g.perAccount, testMoneyWindow)
	if err != nil {
		return nil, fmt.Errorf("test money: counting: %w", err)
	}
	if !ok {
		return nil, &AuthorityDenied{ReasonCodes: []string{DenyTestMoneyAccount}}
	}
	ok, _, err = g.limiter.Allow(ctx, "testmoney:all", g.perDay, testMoneyWindow)
	if err != nil {
		return nil, fmt.Errorf("test money: counting: %w", err)
	}
	if !ok {
		return nil, &AuthorityDenied{ReasonCodes: []string{DenyTestMoneyDay}}
	}
	return nil, nil
}

// FunderChain asks several funders in turn. Fund stops at the first that
// pulls money from a vault, so a payment a person's own vault paid for is not
// counted against the shared wallet's allowance.
type FunderChain []PassFunder

// Check refuses if any funder does.
func (c FunderChain) Check(ctx context.Context, passID string) error {
	for _, f := range c {
		if err := f.Check(ctx, passID); err != nil {
			return err
		}
	}
	return nil
}

// Funding is true if any funder says the payment comes from a vault, and an
// error if any refuses.
func (c FunderChain) Funding(ctx context.Context, passID, rail string) (bool, error) {
	fromVault := false
	for _, f := range c {
		v, err := f.Funding(ctx, passID, rail)
		if err != nil {
			return false, err
		}
		fromVault = fromVault || v
	}
	return fromVault, nil
}

// Fund runs the funders in order until one returns a pull.
func (c FunderChain) Fund(ctx context.Context, passID string, rv *econ.Reservation, amountMinor int64) (*OnchainPull, error) {
	for _, f := range c {
		pull, err := f.Fund(ctx, passID, rv, amountMinor)
		if err != nil {
			return nil, err
		}
		if pull != nil {
			return pull, nil
		}
	}
	return nil, nil
}

// windowCounter is the guard's counter when there is no Redis: a fixed window
// from the first hit on a key, like the Redis limiter's.
type windowCounter struct {
	mu   sync.Mutex
	now  func() time.Time
	hits map[string]windowHits
}

type windowHits struct {
	start time.Time
	n     int
}

func newWindowCounter(now func() time.Time) *windowCounter {
	return &windowCounter{now: now, hits: map[string]windowHits{}}
}

// Allow counts one hit and says whether it is within limit for the window.
func (w *windowCounter) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	if len(w.hits) > 10_000 {
		for k, h := range w.hits {
			if now.Sub(h.start) >= window {
				delete(w.hits, k)
			}
		}
	}
	h, ok := w.hits[key]
	if !ok || now.Sub(h.start) >= window {
		h = windowHits{start: now}
	}
	h.n++
	w.hits[key] = h
	if h.n > limit {
		return false, window - now.Sub(h.start), nil
	}
	return true, 0, nil
}
