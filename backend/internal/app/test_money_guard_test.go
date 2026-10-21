package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

type testMoneyPasses map[string]*spendpass.Pass

func (p testMoneyPasses) Get(_ context.Context, id string) (*spendpass.Pass, error) {
	if pass, ok := p[id]; ok {
		return pass, nil
	}
	return nil, shared.ErrNotFound
}

func testMoneyOwners() testMoneyPasses {
	return testMoneyPasses{
		"pass_a1": {ID: "pass_a1", UserID: "user_a"},
		"pass_a2": {ID: "pass_a2", UserID: "user_a"},
		"pass_b1": {ID: "pass_b1", UserID: "user_b"},
	}
}

func devnetReservation() *econ.Reservation {
	return &econ.Reservation{ID: "res_1", Rail: "solana-x402-devnet"}
}

func denial(t *testing.T, err error) []string {
	t.Helper()
	var d *AuthorityDenied
	if !errors.As(err, &d) {
		t.Fatalf("want a denial, got %v", err)
	}
	return d.ReasonCodes
}

// One account's allowance is its own: its passes share it, nobody else's.
func TestTestMoneyGuard_CountsEachAccountSeparately(t *testing.T) {
	g := NewTestMoneyGuard([]string{"solana-x402-devnet"}, testMoneyOwners(), nil, 2, 100)
	ctx := context.Background()
	for _, id := range []string{"pass_a1", "pass_a2"} {
		if pull, err := g.Fund(ctx, id, devnetReservation(), 5_000); err != nil || pull != nil {
			t.Fatalf("%s: within the allowance: %v %v", id, pull, err)
		}
	}
	_, err := g.Fund(ctx, "pass_a1", devnetReservation(), 5_000)
	if got := denial(t, err); !slices.Equal(got, []string{DenyTestMoneyAccount}) {
		t.Fatalf("a third payment from the same account: %v", got)
	}
	if _, err := g.Fund(ctx, "pass_b1", devnetReservation(), 5_000); err != nil {
		t.Fatalf("another account is unaffected: %v", err)
	}
}

// The server's allowance is shared by everyone; an account over its own limit
// doesn't use it up.
func TestTestMoneyGuard_TheServerAllowanceIsShared(t *testing.T) {
	g := NewTestMoneyGuard([]string{"solana-x402-devnet"}, testMoneyOwners(), nil, 10, 3)
	ctx := context.Background()
	for _, id := range []string{"pass_a1", "pass_a2", "pass_b1"} {
		if _, err := g.Fund(ctx, id, devnetReservation(), 5_000); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	for _, id := range []string{"pass_a1", "pass_b1"} {
		_, err := g.Fund(ctx, id, devnetReservation(), 5_000)
		if got := denial(t, err); !slices.Equal(got, []string{DenyTestMoneyDay}) {
			t.Fatalf("%s after the day's allowance: %v", id, got)
		}
	}

	g = NewTestMoneyGuard([]string{"solana-x402-devnet"}, testMoneyOwners(), nil, 1, 3)
	if _, err := g.Fund(ctx, "pass_a1", devnetReservation(), 5_000); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := g.Fund(ctx, "pass_a1", devnetReservation(), 5_000); err == nil {
			t.Fatal("over its own limit")
		}
	}
	for _, id := range []string{"pass_b1"} {
		if _, err := g.Fund(ctx, id, devnetReservation(), 5_000); err != nil {
			t.Fatalf("a person hammering their own limit must not use up the server's: %v", err)
		}
	}
}

// The sandbox, mainnet and a free call are not the shared devnet wallet's money.
func TestTestMoneyGuard_OnlyCountsPaymentsFromTheGuardedRails(t *testing.T) {
	g := NewTestMoneyGuard([]string{"solana-x402-devnet"}, testMoneyOwners(), nil, 1, 1)
	ctx := context.Background()
	other := &econ.Reservation{ID: "res_2", Rail: "sandbox"}
	for i := 0; i < 5; i++ {
		if pull, err := g.Fund(ctx, "pass_a1", other, 5_000); err != nil || pull != nil {
			t.Fatalf("sandbox: %v %v", pull, err)
		}
		if pull, err := g.Fund(ctx, "pass_a1", devnetReservation(), 0); err != nil || pull != nil {
			t.Fatalf("a free call: %v %v", pull, err)
		}
	}
	if _, err := g.Fund(ctx, "pass_a1", devnetReservation(), 5_000); err != nil {
		t.Fatalf("the first real payment is still within the allowance: %v", err)
	}
	var nilGuard *TestMoneyGuard
	if pull, err := nilGuard.Fund(ctx, "pass_a1", devnetReservation(), 5_000); err != nil || pull != nil {
		t.Fatalf("no guard, no limit: %v %v", pull, err)
	}
}

type brokenLimiter struct{}

func (brokenLimiter) Allow(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return false, 0, errors.New("redis: connection refused")
}

// A control that can't count must not let the payment through.
func TestTestMoneyGuard_RefusesWhenItCannotCount(t *testing.T) {
	g := NewTestMoneyGuard([]string{"solana-x402-devnet"}, testMoneyOwners(), brokenLimiter{}, 5, 5)
	_, err := g.Fund(context.Background(), "pass_a1", devnetReservation(), 5_000)
	if err == nil {
		t.Fatal("a payment went through with no count")
	}
	var d *AuthorityDenied
	if errors.As(err, &d) {
		t.Fatalf("a failure to count is an error, not a denial the person is told about: %v", err)
	}
	// And a pass it can't find.
	g = NewTestMoneyGuard([]string{"solana-x402-devnet"}, testMoneyOwners(), nil, 5, 5)
	if _, err := g.Fund(context.Background(), "pass_missing", devnetReservation(), 5_000); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("an unknown pass: %v", err)
	}
}

type testClock struct{ at time.Time }

func (c *testClock) now() time.Time { return c.at }

// The allowance comes back a day after the first payment, not before.
func TestTestMoneyGuard_TheAllowanceComesBackAfterADay(t *testing.T) {
	clock := &testClock{at: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)}
	g := NewTestMoneyGuard([]string{"solana-x402-devnet"}, testMoneyOwners(), newWindowCounter(clock.now), 1, 100)
	ctx := context.Background()
	if _, err := g.Fund(ctx, "pass_a1", devnetReservation(), 5_000); err != nil {
		t.Fatal(err)
	}
	clock.at = clock.at.Add(23 * time.Hour)
	if _, err := g.Fund(ctx, "pass_a1", devnetReservation(), 5_000); err == nil {
		t.Fatal("still inside the day")
	}
	clock.at = clock.at.Add(2 * time.Hour)
	if _, err := g.Fund(ctx, "pass_a1", devnetReservation(), 5_000); err != nil {
		t.Fatalf("a day later: %v", err)
	}
}

type chainFake struct {
	fromVault bool
	pull      *OnchainPull
	err       error
	checkErr  error
	funded    int
}

func (c *chainFake) Check(context.Context, string) error { return c.checkErr }
func (c *chainFake) Funding(context.Context, string, string) (bool, error) {
	return c.fromVault, c.err
}
func (c *chainFake) Fund(context.Context, string, *econ.Reservation, int64) (*OnchainPull, error) {
	c.funded++
	return c.pull, c.err
}

// A payment a person's vault paid for isn't counted against the shared wallet.
func TestFunderChain_AVaultPaymentIsNotCounted(t *testing.T) {
	vault := &chainFake{fromVault: true, pull: &OnchainPull{PullSignature: "sig"}}
	guard := NewTestMoneyGuard([]string{"solana-x402-devnet"}, testMoneyOwners(), nil, 1, 1)
	chain := FunderChain{vault, guard}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		pull, err := chain.Fund(ctx, "pass_a1", devnetReservation(), 5_000)
		if err != nil || pull == nil || pull.PullSignature != "sig" {
			t.Fatalf("vault payment %d: %v %v", i, pull, err)
		}
	}
	if from, err := chain.Funding(ctx, "pass_a1", "solana-x402-devnet"); err != nil || !from {
		t.Fatalf("funding: %v %v", from, err)
	}

	// With no vault for the pass, the guard counts: one is allowed, the next is not.
	vault.pull, vault.fromVault = nil, false
	if pull, err := chain.Fund(ctx, "pass_a1", devnetReservation(), 5_000); err != nil || pull != nil {
		t.Fatalf("first payment with no vault: %v %v", pull, err)
	}
	_, err := chain.Fund(ctx, "pass_a1", devnetReservation(), 5_000)
	if got := denial(t, err); !slices.Equal(got, []string{DenyTestMoneyAccount}) {
		t.Fatalf("second payment with no vault: %v", got)
	}
}

func TestFunderChain_AnyRefusalStopsIt(t *testing.T) {
	frozen := errors.New("frozen")
	ctx := context.Background()
	if err := (FunderChain{&chainFake{}, &chainFake{checkErr: frozen}}).Check(ctx, "pass_a1"); !errors.Is(err, frozen) {
		t.Fatalf("check: %v", err)
	}
	if _, err := (FunderChain{&chainFake{}, &chainFake{err: frozen}}).Funding(ctx, "pass_a1", "r"); !errors.Is(err, frozen) {
		t.Fatalf("funding: %v", err)
	}
	second := &chainFake{}
	if _, err := (FunderChain{&chainFake{err: frozen}, second}).Fund(ctx, "pass_a1", devnetReservation(), 5_000); !errors.Is(err, frozen) || second.funded != 0 {
		t.Fatalf("fund: %v (second funder ran %d times)", err, second.funded)
	}
}
