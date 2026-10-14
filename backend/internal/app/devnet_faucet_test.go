package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

type fakeFaucetRPC struct {
	mu   sync.Mutex
	sol  map[string]uint64
	usdc map[string]uint64 // by token account; absent = no such account
	sent []string
	err  error
}

func (f *fakeFaucetRPC) GetBalance(_ context.Context, pk solana.PublicKey, _ string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sol[pk.String()], nil
}

func (f *fakeFaucetRPC) GetTokenAccountBalance(_ context.Context, pk solana.PublicKey, _ string) (solana.TokenBalance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.usdc[pk.String()]
	if !ok {
		return solana.TokenBalance{}, solana.ErrAccountNotFound
	}
	return solana.TokenBalance{Amount: v, Decimals: 6}, nil
}

func (f *fakeFaucetRPC) GetLatestBlockhash(context.Context, string) (solana.BlockhashInfo, error) {
	return solana.BlockhashInfo{Blockhash: [32]byte{7}}, nil
}

func (f *fakeFaucetRPC) SendTransaction(_ context.Context, tx string, _ solana.SendOptions) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	f.sent = append(f.sent, tx)
	return "sig" + string(rune('0'+len(f.sent))), nil
}

func (f *fakeFaucetRPC) ConfirmSignature(context.Context, string, string, time.Duration) (*solana.SignatureStatus, error) {
	return &solana.SignatureStatus{}, nil
}

type fakeFaucetWallets map[string][]account.Wallet

func (w fakeFaucetWallets) Wallets(_ context.Context, userID string) ([]account.Wallet, error) {
	return w[userID], nil
}

// counts allows each key limit times, like the real limiter within a window.
type countingLimiter struct {
	mu   sync.Mutex
	n    map[string]int
	fail bool
}

func (l *countingLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (bool, time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail {
		return false, 0, errors.New("redis down")
	}
	if l.n == nil {
		l.n = map[string]int{}
	}
	l.n[key]++
	if l.n[key] > limit {
		return false, time.Hour, nil
	}
	return true, 0, nil
}

type faucetRig struct {
	f      *DevnetFaucet
	rpc    *fakeFaucetRPC
	lim    *countingLimiter
	me     solana.PublicKey
	mint   solana.PublicKey
	person solana.PublicKey
	other  solana.PublicKey
}

func newFaucetRig(t *testing.T) *faucetRig {
	t.Helper()
	key, err := solana.NewKeypair()
	if err != nil {
		t.Fatal(err)
	}
	person, _ := solana.NewKeypair()
	other, _ := solana.NewKeypair()
	mint := solana.MustPublicKey("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU")
	me := key.PublicKey()
	mySrc, _ := solana.AssociatedTokenAddress(me, mint, solana.TokenProgram)
	rpc := &fakeFaucetRPC{
		sol:  map[string]uint64{me.String(): 5_000_000_000},
		usdc: map[string]uint64{mySrc.String(): 100_000_000},
	}
	lim := &countingLimiter{}
	wallets := fakeFaucetWallets{"u1": {{Chain: account.ChainSolana, Address: person.PublicKey().String(), Kind: account.WalletExternal}}}
	f := NewDevnetFaucet(key, mint, rpc, wallets, lim, DevnetFaucetConfig{SOLLamports: 200_000_000, USDCMinor: 2_000_000, PerDay: 3})
	return &faucetRig{f: f, rpc: rpc, lim: lim, me: me, mint: mint, person: person.PublicKey(), other: other.PublicKey()}
}

func TestFaucetSendsSOLAndUSDCToAnEmptyWallet(t *testing.T) {
	r := newFaucetRig(t)
	d, err := r.f.Drop(context.Background(), "u1", r.person.String(), "203.0.113.5")
	if err != nil {
		t.Fatal(err)
	}
	if d.SOLLamports != 200_000_000 || d.USDCMinor != 2_000_000 || d.Signature == "" || d.Address != r.person.String() {
		t.Fatalf("drop %+v", d)
	}
	if len(r.rpc.sent) != 1 {
		t.Fatalf("sent %d transactions, want 1", len(r.rpc.sent))
	}
	// The one transaction is signed by the faucet, which pays its fee.
	raw, err := solana.DecodeTransactionBase64(r.rpc.sent[0])
	if err != nil {
		t.Fatal(err)
	}
	if raw.Message.AccountKeys[0] != r.me {
		t.Fatalf("fee payer %s, want the faucet", raw.Message.AccountKeys[0])
	}
	if len(raw.Message.Instructions) != 3 { // SOL transfer, create account, USDC transfer
		t.Fatalf("%d instructions, want 3", len(raw.Message.Instructions))
	}
}

func TestFaucetOnlyTopsUpWhatIsMissing(t *testing.T) {
	r := newFaucetRig(t)
	r.rpc.sol[r.person.String()] = 3_000_000_000 // plenty of SOL, no USDC account
	d, err := r.f.Drop(context.Background(), "u1", r.person.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	if d.SOLLamports != 0 || d.USDCMinor != 2_000_000 {
		t.Fatalf("drop %+v: only USDC was missing", d)
	}
	raw, _ := solana.DecodeTransactionBase64(r.rpc.sent[0])
	if len(raw.Message.Instructions) != 2 { // create account, USDC transfer
		t.Fatalf("%d instructions, want 2", len(raw.Message.Instructions))
	}
}

func TestFaucetRefusesAWalletThatHasEnough(t *testing.T) {
	r := newFaucetRig(t)
	r.rpc.sol[r.person.String()] = 2_000_000_000
	ata, _ := solana.AssociatedTokenAddress(r.person, r.mint, solana.TokenProgram)
	r.rpc.usdc[ata.String()] = 20_000_000
	_, err := r.f.Drop(context.Background(), "u1", r.person.String(), "")
	var enough *FaucetNotNeeded
	if !errors.As(err, &enough) || enough.SOLLamports != 2_000_000_000 || enough.USDCMinor != 20_000_000 {
		t.Fatalf("err = %v, want FaucetNotNeeded with the balances", err)
	}
	if len(r.rpc.sent) != 0 {
		t.Fatal("nothing should have been sent")
	}
	// A refusal for want of need doesn't use up the person's day.
	if len(r.lim.n) != 0 {
		t.Fatalf("limits used: %v", r.lim.n)
	}
}

func TestFaucetOnlySendsToAWalletOnTheAccount(t *testing.T) {
	r := newFaucetRig(t)
	if _, err := r.f.Drop(context.Background(), "u1", r.other.String(), ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("someone else's address: %v", err)
	}
	if _, err := r.f.Drop(context.Background(), "u2", r.person.String(), ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("another account's wallet: %v", err)
	}
	if _, err := r.f.Drop(context.Background(), "u1", "not an address", ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("garbage: %v", err)
	}
	if len(r.rpc.sent) != 0 {
		t.Fatal("nothing should have been sent")
	}
}

func TestFaucetLimitsAnAccountToOneDropADay(t *testing.T) {
	r := newFaucetRig(t)
	if _, err := r.f.Drop(context.Background(), "u1", r.person.String(), "203.0.113.5"); err != nil {
		t.Fatal(err)
	}
	// Spent it all: the wallet looks empty again to the fake.
	delete(r.rpc.sol, r.person.String())
	_, err := r.f.Drop(context.Background(), "u1", r.person.String(), "203.0.113.5")
	var lim *FaucetLimitError
	if !errors.As(err, &lim) || lim.Scope != "account" || lim.RetryAfter <= 0 {
		t.Fatalf("second drop: %v", err)
	}
	if len(r.rpc.sent) != 1 {
		t.Fatalf("sent %d, want 1", len(r.rpc.sent))
	}
}

func TestFaucetHasADailyShareForEveryone(t *testing.T) {
	r := newFaucetRig(t)
	for i := 0; i < 3; i++ {
		kp, _ := solana.NewKeypair()
		user := "user-" + string(rune('a'+i))
		r.f.wallets = fakeFaucetWallets{user: {{Chain: account.ChainSolana, Address: kp.PublicKey().String()}}}
		if _, err := r.f.Drop(context.Background(), user, kp.PublicKey().String(), ""); err != nil {
			t.Fatalf("drop %d: %v", i, err)
		}
	}
	kp, _ := solana.NewKeypair()
	r.f.wallets = fakeFaucetWallets{"late": {{Chain: account.ChainSolana, Address: kp.PublicKey().String()}}}
	_, err := r.f.Drop(context.Background(), "late", kp.PublicKey().String(), "")
	var lim *FaucetLimitError
	if !errors.As(err, &lim) || lim.Scope != "everyone" {
		t.Fatalf("fourth drop: %v, want the daily share used up", err)
	}
}

func TestFaucetLimitsOneNetworkAddress(t *testing.T) {
	r := newFaucetRig(t)
	r.f.cfg.PerDay = 100
	var last error
	for i := 0; i < 6; i++ {
		kp, _ := solana.NewKeypair()
		user := "user-" + string(rune('a'+i))
		r.f.wallets = fakeFaucetWallets{user: {{Chain: account.ChainSolana, Address: kp.PublicKey().String()}}}
		_, last = r.f.Drop(context.Background(), user, kp.PublicKey().String(), "198.51.100.9")
	}
	var lim *FaucetLimitError
	if !errors.As(last, &lim) || lim.Scope != "address" {
		t.Fatalf("sixth drop from one address: %v", last)
	}
}

func TestFaucetSaysWhenItIsEmptyAndChargesNoLimit(t *testing.T) {
	r := newFaucetRig(t)
	r.rpc.sol[r.me.String()] = 1_000_000 // almost nothing
	_, err := r.f.Drop(context.Background(), "u1", r.person.String(), "")
	if !errors.Is(err, ErrFaucetEmpty) {
		t.Fatalf("err = %v, want ErrFaucetEmpty", err)
	}
	if len(r.lim.n) != 0 {
		t.Fatalf("an empty faucet used up the person's day: %v", r.lim.n)
	}
	// USDC alone short of a drop is empty too.
	r.rpc.sol[r.me.String()] = 5_000_000_000
	src, _ := solana.AssociatedTokenAddress(r.me, r.mint, solana.TokenProgram)
	r.rpc.usdc[src.String()] = 500_000
	if _, err := r.f.Drop(context.Background(), "u1", r.person.String(), ""); !errors.Is(err, ErrFaucetEmpty) {
		t.Fatalf("short of USDC: %v", err)
	}
}

func TestFaucetSendsNothingWhenItsLimitsCannotBeChecked(t *testing.T) {
	r := newFaucetRig(t)
	r.lim.fail = true
	if _, err := r.f.Drop(context.Background(), "u1", r.person.String(), ""); !errors.Is(err, ErrFaucetUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if len(r.rpc.sent) != 0 {
		t.Fatal("sent with no limits")
	}
}

func TestANilFaucetIsOff(t *testing.T) {
	var f *DevnetFaucet
	if st := f.Status(); st.Enabled {
		t.Fatalf("status %+v", st)
	}
	if _, err := f.Drop(context.Background(), "u1", "x", ""); !errors.Is(err, ErrFaucetOff) {
		t.Fatalf("err = %v", err)
	}
}
