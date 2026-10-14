package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// confirmErr is what confirming the transfer answers (nil: it confirms).
	confirmErr error
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.confirmErr != nil {
		return nil, f.confirmErr
	}
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

// fakeFaucetLedger keeps one airdrop per account and per wallet, like the database does.
type fakeFaucetLedger struct {
	mu     sync.Mutex
	rows   map[string]*FaucetReceipt // by account
	byAddr map[string]string         // wallet -> account
	err    error                     // what every call answers, when set
	claims int
}

func newFakeFaucetLedger() *fakeFaucetLedger {
	return &fakeFaucetLedger{rows: map[string]*FaucetReceipt{}, byAddr: map[string]string{}}
}

func (l *fakeFaucetLedger) Received(_ context.Context, userID string) (*FaucetReceipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	if r := l.rows[userID]; r != nil {
		c := *r
		return &c, nil
	}
	return nil, nil
}

func (l *fakeFaucetLedger) Claim(_ context.Context, userID, address string, at time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	if r := l.rows[userID]; r != nil {
		c := *r
		return &FaucetUsedError{Scope: "account", Received: &c}
	}
	if _, taken := l.byAddr[address]; taken {
		return &FaucetUsedError{Scope: "wallet"}
	}
	l.rows[userID] = &FaucetReceipt{Address: address, At: at}
	l.byAddr[address] = userID
	l.claims++
	return nil
}

func (l *fakeFaucetLedger) Record(_ context.Context, userID string, d FaucetDrop, at time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.rows[userID]
	if r == nil {
		return errors.New("no claim")
	}
	r.Signature, r.SOLLamports, r.USDCMinor, r.At = d.Signature, d.SOLLamports, d.USDCMinor, at
	return nil
}

func (l *fakeFaucetLedger) Release(_ context.Context, userID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r := l.rows[userID]; r != nil {
		delete(l.byAddr, r.Address)
		delete(l.rows, userID)
	}
	return nil
}

func (l *fakeFaucetLedger) held() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.rows)
}

type faucetRig struct {
	f      *DevnetFaucet
	rpc    *fakeFaucetRPC
	lim    *countingLimiter
	ledger *fakeFaucetLedger
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
	ledger := newFakeFaucetLedger()
	f := NewDevnetFaucet(key, mint, rpc, wallets, lim, ledger, DevnetFaucetConfig{SOLLamports: 200_000_000, USDCMinor: 2_000_000, PerDay: 3})
	return &faucetRig{f: f, rpc: rpc, lim: lim, ledger: ledger, me: me, mint: mint, person: person.PublicKey(), other: other.PublicKey()}
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
	// A refusal for want of need doesn't use up the person's one airdrop or their day.
	if len(r.lim.n) != 0 || r.ledger.held() != 0 {
		t.Fatalf("limits used: %v, claims held: %d", r.lim.n, r.ledger.held())
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

func TestFaucetGivesAnAccountOneAirdropEver(t *testing.T) {
	r := newFaucetRig(t)
	first, err := r.f.Drop(context.Background(), "u1", r.person.String(), "203.0.113.5")
	if err != nil {
		t.Fatal(err)
	}
	// Spent it all, a day later: the wallet looks empty again to the fake, and the limiter has forgotten.
	delete(r.rpc.sol, r.person.String())
	r.lim.n = nil
	_, err = r.f.Drop(context.Background(), "u1", r.person.String(), "203.0.113.5")
	var used *FaucetUsedError
	if !errors.As(err, &used) || used.Scope != "account" || used.Received == nil || used.Received.Signature != first.Signature {
		t.Fatalf("second drop: %v, want the account's one airdrop used, with what it got", err)
	}
	if len(r.rpc.sent) != 1 {
		t.Fatalf("sent %d, want 1", len(r.rpc.sent))
	}
	// Refused before any limit of the day was touched.
	if len(r.lim.n) != 0 {
		t.Fatalf("a refused repeat used up limits: %v", r.lim.n)
	}
}

func TestFaucetRecordsWhatItSent(t *testing.T) {
	r := newFaucetRig(t)
	d, err := r.f.Drop(context.Background(), "u1", r.person.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.f.Received(context.Background(), "u1")
	if err != nil || got == nil || got.Address != r.person.String() || got.SOLLamports != d.SOLLamports || got.USDCMinor != d.USDCMinor || got.Signature != d.Signature {
		t.Fatalf("received = %+v (%v), want what the drop sent", got, err)
	}
	if none, err := r.f.Received(context.Background(), "u2"); err != nil || none != nil {
		t.Fatalf("an account that has had nothing: %+v %v", none, err)
	}
}

func TestFaucetGivesAWalletOneAirdropOnAnyAccount(t *testing.T) {
	r := newFaucetRig(t)
	if _, err := r.f.Drop(context.Background(), "u1", r.person.String(), ""); err != nil {
		t.Fatal(err)
	}
	// A second account links the same wallet.
	r.f.wallets = fakeFaucetWallets{"u2": {{Chain: account.ChainSolana, Address: r.person.String()}}}
	delete(r.rpc.sol, r.person.String())
	_, err := r.f.Drop(context.Background(), "u2", r.person.String(), "")
	var used *FaucetUsedError
	if !errors.As(err, &used) || used.Scope != "wallet" {
		t.Fatalf("second account, same wallet: %v", err)
	}
	// What the first account got is not the second's to see.
	if used.Received != nil {
		t.Fatalf("another account's drop was shown: %+v", used.Received)
	}
	if len(r.rpc.sent) != 1 || r.ledger.held() != 1 {
		t.Fatalf("sent %d, claims %d, want 1 and 1", len(r.rpc.sent), r.ledger.held())
	}
}

func TestFaucetGivesTheClaimBackWhenNothingWasSent(t *testing.T) {
	r := newFaucetRig(t)
	r.rpc.err = errors.New("node down")
	if _, err := r.f.Drop(context.Background(), "u1", r.person.String(), ""); err == nil {
		t.Fatal("expected the failed send to be an error")
	}
	if r.ledger.held() != 0 {
		t.Fatal("a drop that sent nothing used up the one airdrop")
	}
	// The same person tries again, and gets it.
	r.rpc.err = nil
	if _, err := r.f.Drop(context.Background(), "u1", r.person.String(), ""); err != nil {
		t.Fatalf("second try: %v", err)
	}
	if r.ledger.held() != 1 {
		t.Fatal("the drop that went through should be on the ledger")
	}
}

func TestFaucetKeepsTheClaimWhenTheTransferMayStillLand(t *testing.T) {
	r := newFaucetRig(t)
	r.rpc.confirmErr = errors.New("solana: not confirmed before giving up: context deadline exceeded")
	_, err := r.f.Drop(context.Background(), "u1", r.person.String(), "")
	if err == nil || !strings.Contains(err.Error(), "may still land") {
		t.Fatalf("err = %v, want the may-still-land answer", err)
	}
	got, _ := r.f.Received(context.Background(), "u1")
	if got == nil || got.Signature == "" {
		t.Fatalf("the sent transfer should be on the ledger with its signature: %+v", got)
	}
	// So a repeat is refused: it may have arrived.
	delete(r.rpc.sol, r.person.String())
	r.rpc.confirmErr = nil
	var used *FaucetUsedError
	if _, err := r.f.Drop(context.Background(), "u1", r.person.String(), ""); !errors.As(err, &used) {
		t.Fatalf("repeat: %v", err)
	}
}

func TestFaucetGivesTheClaimBackWhenTheTransferFailedOnTheNetwork(t *testing.T) {
	r := newFaucetRig(t)
	r.rpc.confirmErr = fmt.Errorf("%w: custom program error", solana.ErrTransactionFailed)
	if _, err := r.f.Drop(context.Background(), "u1", r.person.String(), ""); err == nil {
		t.Fatal("expected an error")
	}
	if r.ledger.held() != 0 {
		t.Fatal("a transfer that failed on the network used up the one airdrop")
	}
	r.rpc.confirmErr = nil
	if _, err := r.f.Drop(context.Background(), "u1", r.person.String(), ""); err != nil {
		t.Fatalf("second try: %v", err)
	}
}

func TestFaucetSendsNothingWhenItsLedgerCannotBeRead(t *testing.T) {
	r := newFaucetRig(t)
	r.ledger.err = errors.New("database down")
	if _, err := r.f.Drop(context.Background(), "u1", r.person.String(), ""); !errors.Is(err, ErrFaucetUnavailable) {
		t.Fatalf("err = %v, want unavailable", err)
	}
	if len(r.rpc.sent) != 0 {
		t.Fatal("sent with no way to remember it")
	}
}

func TestFaucetWithoutALedgerStillLimitsAnAccountToADrop(t *testing.T) {
	r := newFaucetRig(t)
	r.f.ledger = nil // no database to remember it: a drop a day, by the limiter
	if _, err := r.f.Drop(context.Background(), "u1", r.person.String(), "203.0.113.5"); err != nil {
		t.Fatal(err)
	}
	delete(r.rpc.sol, r.person.String())
	_, err := r.f.Drop(context.Background(), "u1", r.person.String(), "203.0.113.5")
	var lim *FaucetLimitError
	if !errors.As(err, &lim) || lim.Scope != "account" || lim.RetryAfter <= 0 {
		t.Fatalf("second drop: %v", err)
	}
}

func TestOneAccountCannotGetTwoAirdropsAtOnce(t *testing.T) {
	r := newFaucetRig(t)
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = r.f.Drop(context.Background(), "u1", r.person.String(), "")
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 || len(r.rpc.sent) != 1 {
		t.Fatalf("%d drops went through and %d transfers were sent, want 1 and 1", wins, len(r.rpc.sent))
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
	// Turned away for the day's share: the account keeps its one airdrop for tomorrow.
	if got, _ := r.f.Received(context.Background(), "late"); got != nil {
		t.Fatalf("a refused account holds a claim: %+v", got)
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
	if r.ledger.held() != 5 {
		t.Fatalf("%d claims held, want 5: the turned-away account keeps its airdrop", r.ledger.held())
	}
}

func TestFaucetSaysWhenItIsEmptyAndChargesNoLimit(t *testing.T) {
	r := newFaucetRig(t)
	r.rpc.sol[r.me.String()] = 1_000_000 // almost nothing
	_, err := r.f.Drop(context.Background(), "u1", r.person.String(), "")
	if !errors.Is(err, ErrFaucetEmpty) {
		t.Fatalf("err = %v, want ErrFaucetEmpty", err)
	}
	if len(r.lim.n) != 0 || r.ledger.held() != 0 {
		t.Fatalf("an empty faucet used up the person's day or airdrop: %v, %d claims", r.lim.n, r.ledger.held())
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
	if r.ledger.held() != 0 {
		t.Fatal("a drop that could not be checked used up the one airdrop")
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
	if got, err := f.Received(context.Background(), "u1"); got != nil || err != nil {
		t.Fatalf("a faucet that is off has given nothing: %+v %v", got, err)
	}
}
