package jupiter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// Rail signs a swap from a wallet Algebra controls and proves, from chain state
// alone, whether it settled.
//
// What makes it safe to sign a transaction somebody else built. Jupiter's
// transaction is an opaque list of instructions across many programs, and a
// hostile or compromised API could return one that drains the wallet. So the
// rail does not read the instructions; it judges the effect. Before signing it
// simulates the transaction against the node's current state and compares the
// wallet's accounts before and after:
//
//   - the SOL balance may fall by at most MaxSolSpendLamports (fees, and rent for
//     a new token account);
//   - USDC may fall by at most the amount authorized, and the bought token must
//     rise by at least the quote less the slippage;
//   - every other token account the wallet has must hold at least what it held;
//   - no token account of the wallet may change owner, gain a delegate or a
//     close authority, be frozen or be closed: an approval gives someone the
//     balance later, which no balance check would see.
//
// Anything else is refused and nothing is signed. The wallet should be one made
// for this: it is judged on the accounts it has, so what it holds is what is
// protected.
//
// How it proves settlement. The signature is the transaction's identity and
// ours alone (the wallet is the fee payer), so the transaction is found by it,
// whatever Jupiter says. It is NOT_SETTLED only when it landed and failed, or
// its blockhash expired at a finalized height and it is nowhere on chain.
// Anything short of that is PENDING or UNKNOWN, never "no".
type Rail struct {
	cfg    RailConfig
	wallet solana.PublicKey
	usdc   solana.PublicKey
	now    func() time.Time
}

var (
	_ app.Rail              = (*Rail)(nil)
	_ app.PaymentAuthorizer = (*Rail)(nil)
)

// RailConfig configures a Rail.
type RailConfig struct {
	RPC    *solana.RPC
	Signer *solana.Keypair
	// MaxSwapMinor is the most one swap may spend, in micro-USDC, whatever the
	// policy above says. Zero means DefaultMaxSwapMinor.
	MaxSwapMinor int64
	// MaxSlippageBps is the most slippage the rail will sign for. Zero means 300.
	MaxSlippageBps int
	// MaxSolSpendLamports is the most SOL a swap may cost the wallet: fees and
	// rent for a new token account. Zero means 5,000,000 (0.005 SOL).
	MaxSolSpendLamports uint64
	// MaxTokenAccounts is the most token accounts the wallet may have for the
	// rail to check them all. Zero means 40.
	MaxTokenAccounts int
	// Commitment is how final a swap must be to count as settled. Empty means
	// confirmed.
	Commitment string
	// Now overrides the clock (tests).
	Now func() time.Time
}

const (
	defaultRailMaxSlippageBps = 300
	defaultMaxSolSpend        = 5_000_000
	defaultMaxTokenAccounts   = 40
)

// NewRail builds a Rail for Solana mainnet, the only cluster Jupiter serves. It
// checks configuration only.
func NewRail(cfg RailConfig) (*Rail, error) {
	if cfg.RPC == nil || cfg.Signer == nil {
		return nil, errors.New("jupiter: an RPC client and a signer are required")
	}
	if cfg.MaxSwapMinor <= 0 {
		cfg.MaxSwapMinor = DefaultMaxSwapMinor
	}
	if cfg.MaxSlippageBps <= 0 {
		cfg.MaxSlippageBps = defaultRailMaxSlippageBps
	}
	if cfg.MaxSolSpendLamports == 0 {
		cfg.MaxSolSpendLamports = defaultMaxSolSpend
	}
	if cfg.MaxTokenAccounts <= 0 {
		cfg.MaxTokenAccounts = defaultMaxTokenAccounts
	}
	if cfg.Commitment == "" {
		cfg.Commitment = solana.Confirmed
	}
	mint, err := solana.ParsePublicKey(usdcMint())
	if err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Rail{cfg: cfg, wallet: cfg.Signer.PublicKey(), usdc: mint, now: now}, nil
}

// Name is how reservations name the rail.
func (r *Rail) Name() string { return RailName }

// Address is the wallet the rail signs from.
func (r *Rail) Address() solana.PublicKey { return r.wallet }

// refuse is a refusal to sign: nothing was signed and nothing can move.
func refuse(format string, args ...any) error {
	return fmt.Errorf("the swap rail refused to sign: "+format, args...)
}

// Authorize signs the swap if, and only if, simulating it shows the wallet's
// accounts changing as the swap says and nothing else.
func (r *Rail) Authorize(ctx context.Context, rv *econ.Reservation, req app.PaymentRequest) (*app.PaymentAuthority, error) {
	var t Terms
	if err := json.Unmarshal(req.Requirements, &t); err != nil {
		return nil, refuse("the terms aren't readable")
	}
	out, err := r.checkTerms(rv, t)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(t.Transaction)
	if err != nil {
		return nil, refuse("the transaction isn't base64")
	}
	tx, err := solana.ParseRawTransaction(raw)
	if err != nil {
		return nil, refuse("%v", err)
	}
	if tx.FeePayer() != r.wallet {
		return nil, refuse("the transaction's fee is paid by another account")
	}
	if _, ok := tx.SignerIndex(r.wallet); !ok {
		return nil, refuse("the wallet is not a signer of the transaction")
	}

	keys, pre, err := r.snapshot(ctx, out)
	if err != nil {
		return nil, err
	}
	sim, err := r.cfg.RPC.SimulateWithAccounts(ctx, t.Transaction, keys, solana.Processed)
	if err != nil {
		return nil, fmt.Errorf("simulating the swap: %w", err)
	}
	if sim.Err != nil {
		e, _ := json.Marshal(sim.Err)
		return nil, refuse("the swap would fail on chain (%s)", e)
	}
	outAtoms, _ := strconv.ParseUint(t.OutAmount, 10, 64)
	v, err := judge(judgement{
		wallet: r.wallet, usdc: r.usdc, out: out, amount: uint64(t.AmountMinor), minOut: MinOut(outAtoms, t.SlippageBps),
		maxSol: r.cfg.MaxSolSpendLamports, keys: keys, pre: pre, post: sim.Accounts,
	})
	if err != nil {
		return nil, err
	}

	signed, sig, err := tx.Sign(r.cfg.Signer)
	if err != nil {
		return nil, refuse("%v", err)
	}
	ev := econ.Evidence{
		Rail: RailName, Protocol: "jupiter", Scheme: "exact-in", Network: chain.Solana, Asset: r.usdc.String(),
		PaymentID: t.RequestID, Transaction: solana.EncodeBase58(sig[:]), AmountMinor: t.AmountMinor, Payer: r.wallet.String(),
		ValidUntilHeight: t.LastValidBlockHeight,
	}
	_ = v // what the simulation promised is judged again from the chain at settlement
	return &app.PaymentAuthority{Value: base64.StdEncoding.EncodeToString(signed), Evidence: ev, AmountMinor: t.AmountMinor}, nil
}

// checkTerms holds the swap to the rail's own ceilings, whatever asked for it.
func (r *Rail) checkTerms(rv *econ.Reservation, t Terms) (solana.PublicKey, error) {
	var zero solana.PublicKey
	out, err := solana.ParsePublicKey(t.OutputMint)
	switch {
	case err != nil || out.IsZero():
		return zero, refuse("the output mint isn't a mint address")
	case t.InputMint != r.usdc.String():
		return zero, refuse("only USDC is spent")
	case t.OutputMint == r.usdc.String() || t.OutputMint == wrappedSOL:
		return zero, refuse("the output must be a token other than USDC and SOL")
	case t.AmountMinor <= 0:
		return zero, refuse("the amount must be positive")
	case t.AmountMinor > r.cfg.MaxSwapMinor:
		return zero, refuse("%s USDC is more than the most one swap may spend, %s", chain.FormatUnits(t.AmountMinor, chain.USDCDecimals), chain.FormatUnits(r.cfg.MaxSwapMinor, chain.USDCDecimals))
	case rv != nil && t.AmountMinor > rv.HoldMinor:
		return zero, refuse("the swap spends more than this attempt's hold")
	case t.SlippageBps < 1 || t.SlippageBps > r.cfg.MaxSlippageBps:
		return zero, refuse("slippage must be between 1 and %d basis points", r.cfg.MaxSlippageBps)
	case t.LastValidBlockHeight == 0:
		return zero, refuse("the transaction has no expiry, so its absence could never be proven")
	case t.Transaction == "":
		return zero, refuse("there is no transaction")
	}
	if n, err := strconv.ParseUint(t.OutAmount, 10, 64); err != nil || MinOut(n, t.SlippageBps) == 0 {
		return zero, refuse("the quoted output isn't a positive amount")
	}
	return out, nil
}

// snapshot is every account the swap could touch that the wallet owns, read
// now: the wallet itself, its USDC and bought-token accounts, and every other
// token account it has, since any of them could be the target of a transaction
// that isn't what it says.
func (r *Rail) snapshot(ctx context.Context, out solana.PublicKey) ([]solana.PublicKey, []*solana.AccountInfo, error) {
	mint, err := r.cfg.RPC.GetAccountInfo(ctx, out, solana.Confirmed)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the output mint: %w", err)
	}
	if mint == nil || (mint.Owner != solana.TokenProgram && mint.Owner != solana.Token2022Program) {
		return nil, nil, refuse("the output isn't a token mint")
	}
	usdcATA, err := solana.AssociatedTokenAddress(r.wallet, r.usdc, solana.TokenProgram)
	if err != nil {
		return nil, nil, err
	}
	outATA, err := solana.AssociatedTokenAddress(r.wallet, out, mint.Owner)
	if err != nil {
		return nil, nil, err
	}
	keys := []solana.PublicKey{r.wallet, usdcATA, outATA}
	seen := map[solana.PublicKey]bool{r.wallet: true, usdcATA: true, outATA: true}
	held := 0
	for _, program := range []solana.PublicKey{solana.TokenProgram, solana.Token2022Program} {
		accounts, err := r.cfg.RPC.GetTokenAccountsByOwner(ctx, r.wallet, program, solana.Confirmed)
		if err != nil {
			return nil, nil, fmt.Errorf("listing the wallet's token accounts: %w", err)
		}
		held += len(accounts)
		for _, a := range accounts {
			if !seen[a.Address] {
				seen[a.Address] = true
				keys = append(keys, a.Address)
			}
		}
	}
	if held > r.cfg.MaxTokenAccounts {
		return nil, nil, refuse("the wallet has %d token accounts, more than the %d the rail can check; use a wallet made for this", held, r.cfg.MaxTokenAccounts)
	}
	pre, err := r.cfg.RPC.GetMultipleAccounts(ctx, keys, solana.Processed)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the wallet's accounts: %w", err)
	}
	if pre[1] == nil {
		return nil, nil, refuse("the wallet has no USDC account")
	}
	return keys, pre, nil
}

// judgement is what judge needs: the accounts before and after.
type judgement struct {
	wallet, usdc, out solana.PublicKey
	amount, minOut    uint64
	maxSol            uint64
	// keys[0] is the wallet itself; pre and post line up with keys.
	keys      []solana.PublicKey
	pre, post []*solana.AccountInfo
}

// verdict is what the simulated swap would do to the wallet.
type verdict struct {
	Debit, Credit, SolSpent uint64
}

func isTokenProgram(owner solana.PublicKey) bool {
	return owner == solana.TokenProgram || owner == solana.Token2022Program
}

// judge decides whether a simulated swap is the swap that was asked for and
// nothing else, by what it does to the wallet's accounts.
func judge(j judgement) (verdict, error) {
	var v verdict
	if len(j.pre) != len(j.keys) || len(j.post) != len(j.keys) || len(j.keys) < 3 {
		return v, refuse("the simulation didn't cover the wallet's accounts")
	}
	w0, w1 := j.pre[0], j.post[0]
	switch {
	case w0 == nil:
		return v, refuse("the wallet doesn't exist on chain")
	case w1 == nil:
		return v, refuse("the swap would close the wallet's account")
	case w1.Owner != w0.Owner || len(w1.Data) != len(w0.Data) || w1.Executable != w0.Executable:
		return v, refuse("the swap would change the wallet's own account")
	}
	if w1.Lamports < w0.Lamports {
		v.SolSpent = w0.Lamports - w1.Lamports
		if v.SolSpent > j.maxSol {
			return v, refuse("the swap would cost %d lamports of SOL, more than the %d allowed", v.SolSpent, j.maxSol)
		}
	}

	for i := 1; i < len(j.keys); i++ {
		a, b := j.pre[i], j.post[i]
		switch {
		case a == nil && b == nil:
		case a == nil:
			// Created by the swap: the bought token's account, and only that.
			if !isTokenProgram(b.Owner) {
				return v, refuse("the swap would create an account that isn't a token account")
			}
			tb, err := solana.ParseTokenAccount(b.Data)
			if err != nil {
				return v, refuse("the swap would create an unreadable token account")
			}
			if tb.Owner != j.wallet || tb.Mint != j.out || tb.State != solana.TokenAccountInitialized || tb.Delegate != nil || tb.CloseAuthority != nil || tb.IsNative != nil {
				return v, refuse("the swap would create a token account that isn't a plain one for the bought token owned by the wallet")
			}
			v.Credit += tb.Amount
		case b == nil:
			return v, refuse("the swap would close one of the wallet's token accounts")
		default:
			if a.Owner != b.Owner || !isTokenProgram(a.Owner) {
				return v, refuse("the swap would reassign one of the wallet's accounts")
			}
			ta, err := solana.ParseTokenAccount(a.Data)
			if err != nil {
				return v, refuse("one of the wallet's accounts isn't a token account")
			}
			tb, err := solana.ParseTokenAccount(b.Data)
			if err != nil {
				return v, refuse("the swap would corrupt one of the wallet's token accounts")
			}
			if !ta.SameExceptAmount(tb) {
				return v, refuse("the swap would change more than a balance of %s: its owner, delegate, state or close authority", ta.Mint)
			}
			switch ta.Mint {
			case j.usdc:
				if tb.Amount > ta.Amount {
					return v, refuse("the swap would add USDC to the wallet instead of spending it")
				}
				v.Debit += ta.Amount - tb.Amount
			case j.out:
				if tb.Amount < ta.Amount {
					return v, refuse("the swap would take from the token being bought")
				}
				v.Credit += tb.Amount - ta.Amount
			default:
				if tb.Amount < ta.Amount {
					return v, refuse("the swap would take %d of %s, which it has no business with", ta.Amount-tb.Amount, ta.Mint)
				}
			}
		}
	}
	switch {
	case v.Debit == 0:
		return v, refuse("the swap would spend no USDC")
	case v.Debit > j.amount:
		return v, refuse("the swap would spend %d micro-USDC, more than the %d authorized", v.Debit, j.amount)
	case v.Credit < j.minOut:
		return v, refuse("the swap would deliver %d, less than the least acceptable %d", v.Credit, j.minOut)
	}
	return v, nil
}

// Settlement reads what the chain says about a swap this rail signed.
func (r *Rail) Settlement(ctx context.Context, ev econ.Evidence) (app.Settlement, error) {
	base := app.Settlement{Network: ev.Network, Asset: ev.Asset, Payer: ev.Payer, Transaction: ev.Transaction, Test: ev.Test}
	status := func(s app.SettlementStatus, detail string) (app.Settlement, error) {
		base.Status, base.Detail = s, detail
		return base, nil
	}
	if ev.Transaction == "" || ev.Payer != r.wallet.String() {
		return status(app.SettlementUnknown, "the evidence is not for a swap made by this wallet")
	}
	sts, err := r.cfg.RPC.GetSignatureStatuses(ctx, []string{ev.Transaction}, true)
	if err != nil {
		return base, err
	}
	if len(sts) == 1 && sts[0] != nil {
		st := sts[0]
		if st.Failed() {
			return status(app.SettlementNotSettled, "the swap landed and failed on chain ("+string(st.Err)+"): no USDC moved")
		}
		if !st.Reached(r.cfg.Commitment) {
			return status(app.SettlementPending, "the swap is on chain but not yet "+r.cfg.Commitment)
		}
		return r.verdict(ctx, base, ev.Transaction)
	}
	// Not seen. That proves nothing until the swap can no longer land.
	if ev.ValidUntilHeight == 0 {
		return status(app.SettlementUnknown, "the swap's expiry isn't recorded, so its absence can't be proven")
	}
	final, err := r.cfg.RPC.GetBlockHeight(ctx, solana.Finalized)
	if err != nil {
		return base, err
	}
	if final <= ev.ValidUntilHeight {
		return status(app.SettlementPending, fmt.Sprintf("not on chain yet; it can still land until block height %d (finalized height is %d)", ev.ValidUntilHeight, final))
	}
	return status(app.SettlementNotSettled, "the swap's blockhash expired at a finalized height and it is not in the chain: it can never land")
}

// verdict reads a landed swap's token balances: what left the wallet is what
// settled, and what arrived is recorded with it.
func (r *Rail) verdict(ctx context.Context, base app.Settlement, sig string) (app.Settlement, error) {
	tb, err := r.cfg.RPC.GetTransactionBalances(ctx, sig, r.cfg.Commitment)
	if err != nil {
		return base, err
	}
	if tb == nil {
		base.Status, base.Detail = app.SettlementPending, "the swap is confirmed but its balances aren't readable yet"
		return base, nil
	}
	if tb.Failed() {
		base.Status, base.Detail = app.SettlementNotSettled, "the swap landed and failed on chain: no USDC moved"
		return base, nil
	}
	var spent int64
	bought := map[string]int64{}
	for _, d := range tb.Deltas {
		if d.Owner != r.wallet.String() {
			continue
		}
		if d.Mint == r.usdc.String() {
			spent += int64(d.Pre) - int64(d.Post)
		} else if c := d.Change(); c > 0 {
			bought[d.Mint] += c
		}
	}
	if spent <= 0 {
		base.Status, base.Detail = app.SettlementUnknown, "a transaction carries our signature but no USDC left the wallet"
		return base, nil
	}
	base.Status, base.AmountMinor = app.SettlementSettled, spent
	base.Detail = "the swap is on chain"
	for mint, n := range bought {
		base.Detail += fmt.Sprintf("; the wallet received %d of %s", n, mint)
	}
	return base, nil
}
