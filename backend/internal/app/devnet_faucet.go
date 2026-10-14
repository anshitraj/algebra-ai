package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// The devnet faucet gives a signed-in person a little devnet SOL (fees, rent)
// and devnet USDC (to put in a Spend Pass vault) from a wallet the operator
// funds, so nobody has to copy an address to a public faucet and back. It is
// for devnet only (the wiring refuses any other cluster), sends only to a
// wallet already on the person's account (so it is not a way to send tokens
// to a typed-in address), only tops up what is missing, gives each account one
// airdrop in its lifetime (and each wallet one: the FaucetLedger remembers), and
// is limited per network address and for everyone, per day.

// FaucetRPC is the node the faucet reads and sends through.
type FaucetRPC interface {
	GetBalance(ctx context.Context, pk solana.PublicKey, commitment string) (uint64, error)
	GetTokenAccountBalance(ctx context.Context, pk solana.PublicKey, commitment string) (solana.TokenBalance, error)
	GetLatestBlockhash(ctx context.Context, commitment string) (solana.BlockhashInfo, error)
	SendTransaction(ctx context.Context, txBase64 string, o solana.SendOptions) (string, error)
	ConfirmSignature(ctx context.Context, sig, commitment string, interval time.Duration) (*solana.SignatureStatus, error)
}

// FaucetWallets says which wallets are on an account.
type FaucetWallets interface {
	Wallets(ctx context.Context, userID string) ([]account.Wallet, error)
}

// DevnetFaucetConfig is what one drop is and how often drops are made.
type DevnetFaucetConfig struct {
	// SOLLamports and USDCMinor are what one drop sends; a wallet is topped up
	// when it holds less than half of either.
	SOLLamports uint64
	USDCMinor   uint64
	// PerDay is the most drops in a day, for everyone.
	PerDay int
}

var (
	// ErrFaucetOff: no funded faucet wallet is configured on this server.
	ErrFaucetOff = errors.New("the devnet faucet isn't set up on this server")
	// ErrFaucetEmpty: the faucet wallet can't cover a drop right now.
	ErrFaucetEmpty = errors.New("the devnet faucet is empty right now: the operator needs to refill it")
	// ErrFaucetUnavailable: its limits couldn't be checked, so it sends nothing.
	ErrFaucetUnavailable = errors.New("the devnet faucet can't check its limits right now; try again in a minute")
)

// FaucetReceipt is what an account was given, once it has had its airdrop.
type FaucetReceipt struct {
	Address     string `json:"address"`
	SOLLamports uint64 `json:"sol_lamports"`
	USDCMinor   uint64 `json:"usdc_minor"`
	// Signature is empty while the transfer is being sent.
	Signature string    `json:"signature,omitempty"`
	At        time.Time `json:"at"`
}

// FaucetLedger remembers who has had their one airdrop.
type FaucetLedger interface {
	// Received is what the account was given, or nil when it has had nothing.
	Received(ctx context.Context, userID string) (*FaucetReceipt, error)
	// Claim reserves the account's one airdrop for a wallet, before anything is
	// sent. It answers *FaucetUsedError when the account has had one, or when
	// the wallet has (on any account).
	Claim(ctx context.Context, userID, address string, at time.Time) error
	// Record completes a claim with what was sent.
	Record(ctx context.Context, userID string, d FaucetDrop, at time.Time) error
	// Release gives a claim back, so an airdrop that never arrived doesn't use up the one.
	Release(ctx context.Context, userID string) error
}

// FaucetUsedError: the account has had its one airdrop, or the wallet has had
// its one on another account. Scope is "account" or "wallet"; Received is what
// was given, when it is known.
type FaucetUsedError struct {
	Scope    string
	Received *FaucetReceipt
}

func (e *FaucetUsedError) Error() string {
	if e.Scope == "wallet" {
		return "this wallet already got a devnet airdrop, on another account: it is one per account"
	}
	return "you already got your devnet airdrop: it is one per account"
}

// FaucetNotNeeded: the wallet already holds enough of both.
type FaucetNotNeeded struct{ SOLLamports, USDCMinor uint64 }

func (e *FaucetNotNeeded) Error() string {
	return "your wallet already holds enough devnet SOL and USDC"
}

// FaucetLimitError: a drop is refused until RetryAfter has passed. Scope names
// what was used up: "account", "wallet", "address" or "everyone".
type FaucetLimitError struct {
	Scope      string
	RetryAfter time.Duration
}

func (e *FaucetLimitError) Error() string {
	switch e.Scope {
	case "everyone":
		return "the devnet faucet has given out its share for today; try again tomorrow"
	case "address":
		return "this network address has had its devnet funds for today; try again tomorrow"
	default:
		return "you already got devnet funds today; try again tomorrow"
	}
}

// FaucetDrop is what one drop sent.
type FaucetDrop struct {
	Address     string `json:"address"`
	SOLLamports uint64 `json:"sol_lamports"`
	USDCMinor   uint64 `json:"usdc_minor"`
	Signature   string `json:"signature"`
}

// FaucetStatus is what a person is told about the faucet.
type FaucetStatus struct {
	Enabled     bool   `json:"enabled"`
	Network     string `json:"network"`
	SOLLamports uint64 `json:"sol_lamports,omitempty"`
	USDCMinor   uint64 `json:"usdc_minor,omitempty"`
	// Address is the faucet wallet: public chain data, so the operator can see
	// where to refill it.
	Address string `json:"address,omitempty"`
}

const (
	faucetDay       = 24 * time.Hour
	faucetATARent   = uint64(2_039_280) // a token account, rent exempt
	faucetFee       = uint64(10_000)    // one signature, with room
	faucetKeepLamps = uint64(20_000_000)
	usdcDecimals    = 6
)

// DevnetFaucet makes the drops. Its methods are safe on a nil receiver: a
// server without a faucet answers ErrFaucetOff.
type DevnetFaucet struct {
	key     *solana.Keypair
	mint    solana.PublicKey
	rpc     FaucetRPC
	wallets FaucetWallets
	limiter RateLimiter
	ledger  FaucetLedger
	cfg     DevnetFaucetConfig

	mu sync.Mutex // one drop at a time: they all spend the faucet's own balance
}

// NewDevnetFaucet builds the faucet. limiter may be nil (no Redis: development).
// ledger may be nil too: then an account may have a drop a day instead of one in its lifetime.
func NewDevnetFaucet(key *solana.Keypair, mint solana.PublicKey, rpc FaucetRPC, wallets FaucetWallets, limiter RateLimiter, ledger FaucetLedger, cfg DevnetFaucetConfig) *DevnetFaucet {
	if cfg.PerDay <= 0 {
		cfg.PerDay = 200
	}
	return &DevnetFaucet{key: key, mint: mint, rpc: rpc, wallets: wallets, limiter: limiter, ledger: ledger, cfg: cfg}
}

// Received is what the account has been given, or nil when nothing (or when
// there is no ledger to say).
func (f *DevnetFaucet) Received(ctx context.Context, userID string) (*FaucetReceipt, error) {
	if f == nil || f.ledger == nil {
		return nil, nil
	}
	return f.ledger.Received(ctx, userID)
}

// Status describes the faucet; Enabled is false on a nil receiver.
func (f *DevnetFaucet) Status() FaucetStatus {
	if f == nil {
		return FaucetStatus{Network: "solana-devnet"}
	}
	return FaucetStatus{Enabled: true, Network: "solana-devnet", SOLLamports: f.cfg.SOLLamports, USDCMinor: f.cfg.USDCMinor, Address: f.key.PublicKey().String()}
}

// Drop tops up one of the person's own wallets: SOL if it holds less than half
// a drop's worth, USDC likewise. An account gets one drop in its lifetime, and a
// wallet one. ip is the caller's address, for its limit.
func (f *DevnetFaucet) Drop(ctx context.Context, userID, address, ip string) (*FaucetDrop, error) {
	if f == nil {
		return nil, ErrFaucetOff
	}
	to, err := solana.ParsePublicKey(address)
	if err != nil || to.IsZero() {
		return nil, fmt.Errorf("%w: that isn't a Solana address", ErrInvalidRequest)
	}
	owned, err := f.wallets.Wallets(ctx, userID)
	if err != nil {
		return nil, err
	}
	mine := false
	for _, w := range owned {
		if w.Chain == account.ChainSolana && w.Address == to.String() {
			mine = true
		}
	}
	if !mine || to == f.key.PublicKey() {
		return nil, fmt.Errorf("%w: the faucet only sends to a wallet on your account", ErrInvalidRequest)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// One airdrop per account, ever. Said before anything else, so a person who had theirs
	// is told that, not that their wallet holds enough.
	if f.ledger != nil {
		got, err := f.ledger.Received(ctx, userID)
		if err != nil {
			return nil, ErrFaucetUnavailable
		}
		if got != nil {
			return nil, &FaucetUsedError{Scope: "account", Received: got}
		}
	}

	// What the wallet holds, and so what it lacks.
	sol, err := f.rpc.GetBalance(ctx, to, solana.Confirmed)
	if err != nil {
		return nil, fmt.Errorf("reading your wallet: %w", err)
	}
	dst, err := solana.AssociatedTokenAddress(to, f.mint, solana.TokenProgram)
	if err != nil {
		return nil, err
	}
	var usdc uint64
	missingATA := false
	switch bal, err := f.rpc.GetTokenAccountBalance(ctx, dst, solana.Confirmed); {
	case errors.Is(err, solana.ErrAccountNotFound):
		missingATA = true
	case err != nil:
		return nil, fmt.Errorf("reading your wallet: %w", err)
	default:
		usdc = bal.Amount
	}
	needSOL := f.cfg.SOLLamports > 0 && sol < f.cfg.SOLLamports/2
	needUSDC := f.cfg.USDCMinor > 0 && usdc < f.cfg.USDCMinor/2
	if !needSOL && !needUSDC {
		return nil, &FaucetNotNeeded{SOLLamports: sol, USDCMinor: usdc}
	}

	// What the faucet holds, before anything of the person's limits is used up.
	me := f.key.PublicKey()
	mySOL, err := f.rpc.GetBalance(ctx, me, solana.Confirmed)
	if err != nil {
		return nil, ErrFaucetUnavailable
	}
	src, err := solana.AssociatedTokenAddress(me, f.mint, solana.TokenProgram)
	if err != nil {
		return nil, err
	}
	var myUSDC uint64
	if needUSDC {
		switch bal, err := f.rpc.GetTokenAccountBalance(ctx, src, solana.Confirmed); {
		case errors.Is(err, solana.ErrAccountNotFound):
		case err != nil:
			return nil, ErrFaucetUnavailable
		default:
			myUSDC = bal.Amount
		}
	}
	want := faucetFee + faucetKeepLamps
	if needSOL {
		want += f.cfg.SOLLamports
	}
	if needUSDC && missingATA {
		want += faucetATARent
	}
	if mySOL < want || (needUSDC && myUSDC < f.cfg.USDCMinor) {
		slog.Warn("devnet faucet is empty: fund its address", "faucet", me.String(), "sol_lamports", mySOL, "usdc_minor", myUSDC)
		return nil, ErrFaucetEmpty
	}

	// The one airdrop is claimed before anything is sent, so two clicks, two tabs or two
	// servers can't both send. It is given back if nothing was sent.
	claimed := false
	if f.ledger != nil {
		if err := f.ledger.Claim(ctx, userID, to.String(), time.Now()); err != nil {
			var used *FaucetUsedError
			if errors.As(err, &used) {
				return nil, err
			}
			return nil, ErrFaucetUnavailable
		}
		claimed = true
	}
	release := func() {
		if !claimed {
			return
		}
		// Not the request's own context: a person who closed the tab still gets the claim back.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := f.ledger.Release(rctx, userID); err != nil {
			slog.Error("devnet faucet: couldn't give a claim back", "user", userID, "err", err)
		}
	}

	if err := f.takeQuota(ctx, userID, to.String(), ip); err != nil {
		release()
		return nil, err
	}

	var ixs []solana.Instruction
	drop := &FaucetDrop{Address: to.String()}
	if needSOL {
		ixs = append(ixs, solana.SystemTransfer(me, to, f.cfg.SOLLamports))
		drop.SOLLamports = f.cfg.SOLLamports
	}
	if needUSDC {
		ixs = append(ixs,
			solana.CreateAssociatedTokenAccountIdempotent(me, dst, to, f.mint, solana.TokenProgram),
			solana.TransferChecked(solana.TokenProgram, src, f.mint, dst, me, f.cfg.USDCMinor, usdcDecimals),
		)
		drop.USDCMinor = f.cfg.USDCMinor
	}
	bh, err := f.rpc.GetLatestBlockhash(ctx, solana.Confirmed)
	if err != nil {
		release()
		return nil, fmt.Errorf("the node didn't answer: %w", err)
	}
	msg, err := solana.CompileV0(me, ixs, bh.Blockhash)
	if err != nil {
		release()
		return nil, err
	}
	tx := solana.NewTransaction(msg)
	if err := tx.PartialSign(f.key); err != nil {
		release()
		return nil, err
	}
	sig, err := f.rpc.SendTransaction(ctx, tx.Base64(), solana.SendOptions{})
	if err != nil {
		release()
		return nil, fmt.Errorf("the node refused the transfer: %w", err)
	}
	drop.Signature = sig
	// Sent: the claim now says what went out, whatever the confirmation says next.
	if f.ledger != nil {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if err := f.ledger.Record(rctx, userID, *drop, time.Now()); err != nil {
			slog.Error("devnet faucet: sent a drop but couldn't record it", "user", userID, "signature", sig, "err", err)
		}
		cancel()
	}
	if _, err := f.rpc.ConfirmSignature(ctx, sig, solana.Confirmed, 0); err != nil {
		// A transfer that landed and failed sent nothing: the person keeps their one airdrop.
		// One not seen in time may still land, so that claim stays.
		if errors.Is(err, solana.ErrTransactionFailed) {
			release()
			return nil, fmt.Errorf("the transfer failed on the network: %w", err)
		}
		return nil, fmt.Errorf("the transfer wasn't confirmed (it may still land: check your wallet): %w", err)
	}
	slog.Info("devnet faucet drop", "to", to.String(), "sol_lamports", drop.SOLLamports, "usdc_minor", drop.USDCMinor)
	return drop, nil
}

// takeQuota uses up one drop of the address's and everyone's day (and, with no ledger
// to give each account one airdrop in its lifetime, the account's and the wallet's day).
// It stops at the first that is spent.
func (f *DevnetFaucet) takeQuota(ctx context.Context, userID, address, ip string) error {
	if f.limiter == nil {
		return nil
	}
	type check struct {
		scope, key string
		limit      int
	}
	var checks []check
	if f.ledger == nil {
		checks = append(checks, check{"account", "faucet:user:" + userID, 1}, check{"wallet", "faucet:wallet:" + address, 1})
	}
	if ip != "" {
		checks = append(checks, check{"address", "faucet:ip:" + ip, 5})
	}
	checks = append(checks, check{"everyone", "faucet:day", f.cfg.PerDay})
	for _, c := range checks {
		ok, retry, err := f.limiter.Allow(ctx, c.key, c.limit, faucetDay)
		if err != nil {
			return ErrFaucetUnavailable
		}
		if !ok {
			return &FaucetLimitError{Scope: c.scope, RetryAfter: retry}
		}
	}
	return nil
}
