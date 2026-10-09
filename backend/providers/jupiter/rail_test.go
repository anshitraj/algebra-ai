package jupiter

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

const (
	spend  = 2_500_000 // micro-USDC
	quoted = "1000000" // atoms of the bought token
)

type railRig struct {
	t      *testing.T
	wallet *solana.Keypair
	w      *world
	rail   *Rail
}

func newRig(t *testing.T, tune ...func(*RailConfig)) *railRig {
	t.Helper()
	wallet, _ := solana.NewKeypair()
	w := newWorld(wallet.PublicKey())
	cfg := RailConfig{RPC: w.serve(t), Signer: wallet}
	for _, f := range tune {
		f(&cfg)
	}
	rail, err := NewRail(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &railRig{t: t, wallet: wallet, w: w, rail: rail}
}

// swapped scripts the simulation of a good swap: spends exactly `spend`,
// delivers the quote, costs the wallet a little SOL and a new token account.
func (g *railRig) swapped() {
	pk := g.wallet.PublicKey()
	g.w.after[g.w.usdcATA()] = tokenAcct(usdcPK, pk, 10_000_000-spend)
	g.w.after[g.w.boughtATA()] = tokenAcct(boughtMint, pk, 1_000_000)
	g.w.after[pk] = acct(50_000_000-2_100_000, solana.SystemProgram, nil)
}

func (g *railRig) terms() Terms {
	return Terms{
		InputMint: usdcMint(), OutputMint: boughtMint.String(), AmountMinor: spend, SlippageBps: 50, OutAmount: quoted,
		RequestID: "req-1", LastValidBlockHeight: 1_150, Transaction: swapTx(g.t, g.wallet.PublicKey()),
	}
}

func (g *railRig) authorize(t Terms) (*app.PaymentAuthority, error) {
	raw, _ := json.Marshal(t)
	return g.rail.Authorize(context.Background(), &econ.Reservation{HoldMinor: spend}, app.PaymentRequest{Requirements: raw})
}

func TestRailSignsAGoodSwapAndTheSignatureIsTheTransactionsIdentity(t *testing.T) {
	g := newRig(t)
	g.swapped()
	tm := g.terms()
	auth, err := g.authorize(tm)
	if err != nil {
		t.Fatal(err)
	}
	if auth.AmountMinor != spend || auth.Evidence.Rail != RailName || auth.Evidence.Network != "solana" || auth.Evidence.Asset != usdcMint() ||
		auth.Evidence.PaymentID != "req-1" || auth.Evidence.ValidUntilHeight != 1_150 || auth.Evidence.Payer != g.wallet.PublicKey().String() || auth.Evidence.AmountMinor != spend {
		t.Errorf("authority: %+v", auth)
	}
	raw, err := base64.StdEncoding.DecodeString(auth.Value)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := solana.ParseRawTransaction(raw)
	if err != nil {
		t.Fatal(err)
	}
	slot := raw[1:65]
	pk := g.wallet.PublicKey()
	if !ed25519.Verify(pk[:], signed.Message(), slot) || auth.Evidence.Transaction != solana.EncodeBase58(slot) {
		t.Error("the wallet's signature is on the message, and its base58 is the transaction id recorded as evidence")
	}
	if g.w.simulated != 1 {
		t.Errorf("simulated once before signing: %d", g.w.simulated)
	}
}

func TestRailRefusesToSignWhatTheSimulationShows(t *testing.T) {
	other, _ := solana.AssociatedTokenAddress(solana.PublicKey{}, otherMint, solana.TokenProgram) // replaced per rig below
	_ = other
	for name, tc := range map[string]struct {
		mutate func(g *railRig)
		want   string
	}{
		"takes from another token account of the wallet": {func(g *railRig) {
			a, _ := solana.AssociatedTokenAddress(g.wallet.PublicKey(), otherMint, solana.TokenProgram)
			g.w.accounts[a] = tokenAcct(otherMint, g.wallet.PublicKey(), 5_000)
			g.w.after[a] = tokenAcct(otherMint, g.wallet.PublicKey(), 0)
		}, "which it has no business with"},
		"spends more USDC than authorized": {func(g *railRig) {
			g.w.after[g.w.usdcATA()] = tokenAcct(usdcPK, g.wallet.PublicKey(), 7_000_000)
		}, "more than the 2500000 authorized"},
		"adds USDC instead of spending it": {func(g *railRig) {
			g.w.after[g.w.usdcATA()] = tokenAcct(usdcPK, g.wallet.PublicKey(), 20_000_000)
		}, "add USDC"},
		"spends no USDC": {func(g *railRig) {
			g.w.after[g.w.usdcATA()] = tokenAcct(usdcPK, g.wallet.PublicKey(), 10_000_000)
		}, "spend no USDC"},
		"delivers less than the quote less the slippage": {func(g *railRig) {
			g.w.after[g.w.boughtATA()] = tokenAcct(boughtMint, g.wallet.PublicKey(), 994_999)
		}, "less than the least acceptable 995000"},
		"delivers nothing": {func(g *railRig) { g.w.after[g.w.boughtATA()] = nil }, "less than the least acceptable"},
		"sets a delegate on the USDC account": {func(g *railRig) {
			d := stranger
			data := (&solana.TokenAccount{Mint: usdcPK, Owner: g.wallet.PublicKey(), Amount: 10_000_000 - spend, Delegate: &d, DelegatedAmount: 1 << 40, State: 1}).Encode()
			g.w.after[g.w.usdcATA()] = acct(2_039_280, solana.TokenProgram, data)
		}, "its owner, delegate, state or close authority"},
		"hands the USDC account to someone else": {func(g *railRig) {
			data := (&solana.TokenAccount{Mint: usdcPK, Owner: stranger, Amount: 10_000_000 - spend, State: 1}).Encode()
			g.w.after[g.w.usdcATA()] = acct(2_039_280, solana.TokenProgram, data)
		}, "its owner, delegate, state or close authority"},
		"freezes the USDC account": {func(g *railRig) {
			data := (&solana.TokenAccount{Mint: usdcPK, Owner: g.wallet.PublicKey(), Amount: 10_000_000 - spend, State: solana.TokenAccountFrozen}).Encode()
			g.w.after[g.w.usdcATA()] = acct(2_039_280, solana.TokenProgram, data)
		}, "its owner, delegate, state or close authority"},
		"closes the USDC account": {func(g *railRig) { g.w.after[g.w.usdcATA()] = nil }, "close one of the wallet's token accounts"},
		"reassigns a token account to another program": {func(g *railRig) {
			g.w.after[g.w.usdcATA()] = acct(2_039_280, solana.SystemProgram, tokenAcct(usdcPK, g.wallet.PublicKey(), 7_500_000).Data)
		}, "reassign one of the wallet's accounts"},
		"spends too much SOL": {func(g *railRig) {
			g.w.after[g.wallet.PublicKey()] = acct(50_000_000-6_000_000, solana.SystemProgram, nil)
		}, "more than the 5000000 allowed"},
		"reassigns the wallet's own account": {func(g *railRig) {
			g.w.after[g.wallet.PublicKey()] = acct(50_000_000, solana.TokenProgram, nil)
		}, "change the wallet's own account"},
		"closes the wallet's account": {func(g *railRig) { g.w.after[g.wallet.PublicKey()] = nil }, "close the wallet's account"},
		"creates the bought token's account for someone else": {func(g *railRig) {
			g.w.after[g.w.boughtATA()] = tokenAcct(boughtMint, stranger, 1_000_000)
		}, "isn't a plain one for the bought token owned by the wallet"},
		"creates the bought token's account with a close authority": {func(g *railRig) {
			c := stranger
			data := (&solana.TokenAccount{Mint: boughtMint, Owner: g.wallet.PublicKey(), Amount: 1_000_000, State: 1, CloseAuthority: &c}).Encode()
			g.w.after[g.w.boughtATA()] = acct(2_039_280, solana.TokenProgram, data)
		}, "isn't a plain one"},
		"creates something that isn't a token account": {func(g *railRig) {
			g.w.after[g.w.boughtATA()] = acct(2_039_280, solana.SystemProgram, make([]byte, 165))
		}, "isn't a token account"},
		"takes from the token being bought": {func(g *railRig) {
			g.w.accounts[g.w.boughtATA()] = tokenAcct(boughtMint, g.wallet.PublicKey(), 5_000_000)
			g.w.after[g.w.boughtATA()] = tokenAcct(boughtMint, g.wallet.PublicKey(), 4_000_000)
		}, "take from the token being bought"},
		"would fail on chain": {func(g *railRig) { g.w.simErr = map[string]any{"InstructionError": []any{2, "Custom"}} }, "would fail on chain"},
	} {
		g := newRig(t)
		g.swapped()
		tc.mutate(g)
		auth, err := g.authorize(g.terms())
		if err == nil || auth != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want a refusal containing %q, got %v (authority %v)", name, tc.want, err, auth)
		}
	}
}

func TestRailCreditsAnExistingBoughtTokenAccountToo(t *testing.T) {
	g := newRig(t)
	g.swapped()
	g.w.accounts[g.w.boughtATA()] = tokenAcct(boughtMint, g.wallet.PublicKey(), 5_000_000)
	g.w.after[g.w.boughtATA()] = tokenAcct(boughtMint, g.wallet.PublicKey(), 6_000_000)
	if _, err := g.authorize(g.terms()); err != nil {
		t.Errorf("a swap into an account the wallet already had: %v", err)
	}
	// What it held already doesn't count towards what it must deliver.
	g.w.after[g.w.boughtATA()] = tokenAcct(boughtMint, g.wallet.PublicKey(), 5_500_000)
	if _, err := g.authorize(g.terms()); err == nil || !strings.Contains(err.Error(), "least acceptable") {
		t.Errorf("credit is the increase, not the balance: %v", err)
	}
}

func TestRailHoldsTheSwapToItsOwnCeilingsWhateverAskedForIt(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(t *Terms)
		want   string
	}{
		"more than one swap may spend":    {func(t *Terms) { t.AmountMinor = 6_000_000 }, "more than the most one swap may spend"},
		"more than the hold":              {func(t *Terms) { t.AmountMinor = spend + 1 }, "more than this attempt's hold"},
		"no amount":                       {func(t *Terms) { t.AmountMinor = 0 }, "amount must be positive"},
		"no slippage":                     {func(t *Terms) { t.SlippageBps = 0 }, "slippage must be between 1 and 300"},
		"too much slippage":               {func(t *Terms) { t.SlippageBps = 301 }, "slippage must be between 1 and 300"},
		"spending something else":         {func(t *Terms) { t.InputMint = otherMint.String() }, "only USDC is spent"},
		"buying USDC":                     {func(t *Terms) { t.OutputMint = usdcMint() }, "must be a token other than USDC and SOL"},
		"buying SOL":                      {func(t *Terms) { t.OutputMint = wrappedSOL }, "must be a token other than USDC and SOL"},
		"an output that isn't a mint":     {func(t *Terms) { t.OutputMint = "nope" }, "isn't a mint address"},
		"no expiry":                       {func(t *Terms) { t.LastValidBlockHeight = 0 }, "no expiry"},
		"no transaction":                  {func(t *Terms) { t.Transaction = "" }, "no transaction"},
		"a quote of nothing":              {func(t *Terms) { t.OutAmount = "0" }, "isn't a positive amount"},
		"a quote that isn't a number":     {func(t *Terms) { t.OutAmount = "lots" }, "isn't a positive amount"},
		"a transaction that isn't base64": {func(t *Terms) { t.Transaction = "!!!" }, "isn't base64"},
	} {
		g := newRig(t)
		g.swapped()
		tm := g.terms()
		tc.mutate(&tm)
		if auth, err := g.authorize(tm); err == nil || auth != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want a refusal containing %q, got %v", name, tc.want, err)
		}
		if g.w.simulated != 0 {
			t.Errorf("%s: refused before the node was asked anything", name)
		}
	}
}

func TestRailRefusesTransactionsThatArentTheWalletsToPay(t *testing.T) {
	g := newRig(t)
	g.swapped()
	tm := g.terms()
	tm.Transaction = swapTx(t, stranger, g.wallet.PublicKey()) // somebody else pays the fee; the wallet signs too
	if _, err := g.authorize(tm); err == nil || !strings.Contains(err.Error(), "fee is paid by another account") {
		t.Errorf("another fee payer: %v", err)
	}
	tm.Transaction = swapTx(t, stranger) // the wallet is not a signer at all
	if _, err := g.authorize(tm); err == nil || !strings.Contains(err.Error(), "fee is paid by another account") {
		t.Errorf("not a signer: %v", err)
	}
	// The wallet pays but a second signer is needed (a market maker's): its slot is left alone.
	tm.Transaction = swapTx(t, g.wallet.PublicKey(), stranger)
	auth, err := g.authorize(tm)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(auth.Value)
	for _, b := range raw[65:129] {
		if b != 0 {
			t.Fatal("the other signer's slot is left for it to fill")
		}
	}
}

func TestRailChecksTheWalletsWholeEstateBeforeSigning(t *testing.T) {
	t.Run("an output that isn't a token mint", func(t *testing.T) {
		g := newRig(t)
		g.swapped()
		g.w.accounts[boughtMint] = acct(1, solana.SystemProgram, nil)
		if _, err := g.authorize(g.terms()); err == nil || !strings.Contains(err.Error(), "isn't a token mint") {
			t.Errorf("%v", err)
		}
		delete(g.w.accounts, boughtMint)
		if _, err := g.authorize(g.terms()); err == nil || !strings.Contains(err.Error(), "isn't a token mint") {
			t.Errorf("a mint that doesn't exist: %v", err)
		}
	})
	t.Run("a wallet with no USDC account", func(t *testing.T) {
		g := newRig(t)
		g.swapped()
		delete(g.w.accounts, g.w.usdcATA())
		if _, err := g.authorize(g.terms()); err == nil || !strings.Contains(err.Error(), "no USDC account") {
			t.Errorf("%v", err)
		}
	})
	t.Run("too many token accounts to check them all", func(t *testing.T) {
		g := newRig(t, func(c *RailConfig) { c.MaxTokenAccounts = 1 })
		g.swapped()
		a, _ := solana.AssociatedTokenAddress(g.wallet.PublicKey(), otherMint, solana.TokenProgram)
		g.w.accounts[a] = tokenAcct(otherMint, g.wallet.PublicKey(), 1)
		if _, err := g.authorize(g.terms()); err == nil || !strings.Contains(err.Error(), "more than the 1 the rail can check") {
			t.Errorf("%v", err)
		}
	})
	t.Run("another wallets accounts are none of its business", func(t *testing.T) {
		g := newRig(t)
		g.swapped()
		theirs, _ := solana.AssociatedTokenAddress(stranger, otherMint, solana.TokenProgram)
		g.w.accounts[theirs] = tokenAcct(otherMint, stranger, 99)
		g.w.after[theirs] = tokenAcct(otherMint, stranger, 0)
		if _, err := g.authorize(g.terms()); err != nil {
			t.Errorf("the swap isn't judged by what happens to accounts that aren't the wallet's: %v", err)
		}
	})
}

func TestRailsCeilingsAreItsOwn(t *testing.T) {
	g := newRig(t, func(c *RailConfig) { c.MaxSwapMinor = 1_000_000 })
	g.swapped()
	if _, err := g.authorize(g.terms()); err == nil || !strings.Contains(err.Error(), "more than the most one swap may spend, 1") {
		t.Errorf("%v", err)
	}
	if _, err := NewRail(RailConfig{}); err == nil {
		t.Error("it needs an RPC client and a signer")
	}
}

// --- settlement ---

func (g *railRig) evidence(sig string) econ.Evidence {
	return econ.Evidence{Transaction: sig, Payer: g.wallet.PublicKey().String(), Network: "solana", Asset: usdcMint(), ValidUntilHeight: 2_000}
}

func confirmed() *solana.SignatureStatus {
	return &solana.SignatureStatus{ConfirmationStatus: "confirmed"}
}

func TestSettlementReadsTheChainBySignature(t *testing.T) {
	ctx := context.Background()
	pk := ""
	g := newRig(t)
	pk = g.wallet.PublicKey().String()
	usdcATA, boughtATA := g.w.usdcATA().String(), g.w.boughtATA().String()

	// Landed: what left the wallet is what settled.
	g.w.statuses["sig-ok"] = confirmed()
	g.w.balances["sig-ok"] = []balance{
		{usdcATA, usdcMint(), pk, 10_000_000, 7_500_000},
		{boughtATA, boughtMint.String(), pk, 0, 1_003_000},
		{"someone-elses", usdcMint(), stranger.String(), 100, 2_600_000}, // the pool's side: not the wallet's
	}
	s, err := g.rail.Settlement(ctx, g.evidence("sig-ok"))
	if err != nil || s.Status != app.SettlementSettled || s.AmountMinor != 2_500_000 || s.Transaction != "sig-ok" || !strings.Contains(s.Detail, "1003000") {
		t.Errorf("settled: %+v %v", s, err)
	}

	// Landed and failed on chain: nothing moved.
	g.w.statuses["sig-failed"] = &solana.SignatureStatus{ConfirmationStatus: "confirmed", Err: json.RawMessage(`{"InstructionError":[1,"Custom"]}`)}
	if s, _ := g.rail.Settlement(ctx, g.evidence("sig-failed")); s.Status != app.SettlementNotSettled || !strings.Contains(s.Detail, "failed") {
		t.Errorf("failed: %+v", s)
	}

	// Seen but not yet confirmed.
	g.w.statuses["sig-processed"] = &solana.SignatureStatus{ConfirmationStatus: "processed"}
	if s, _ := g.rail.Settlement(ctx, g.evidence("sig-processed")); s.Status != app.SettlementPending {
		t.Errorf("processed: %+v", s)
	}

	// Confirmed but a signature that carries no USDC leaving: claim nothing.
	g.w.statuses["sig-odd"] = confirmed()
	g.w.balances["sig-odd"] = []balance{{boughtATA, boughtMint.String(), pk, 0, 5}}
	if s, _ := g.rail.Settlement(ctx, g.evidence("sig-odd")); s.Status != app.SettlementUnknown {
		t.Errorf("no USDC left: %+v", s)
	}

	// Confirmed but the balances aren't readable yet.
	g.w.statuses["sig-early"] = confirmed()
	if s, _ := g.rail.Settlement(ctx, g.evidence("sig-early")); s.Status != app.SettlementPending {
		t.Errorf("balances not there yet: %+v", s)
	}
}

func TestSettlementOnlyCallsItNotSettledWhenItCanNeverLand(t *testing.T) {
	ctx := context.Background()
	g := newRig(t)

	// Not on chain, and it can still land.
	g.w.height = 1_500
	if s, _ := g.rail.Settlement(ctx, g.evidence("sig-missing")); s.Status != app.SettlementPending || !strings.Contains(s.Detail, "can still land until block height 2000") {
		t.Errorf("before expiry: %+v", s)
	}
	// Expired at a finalized height and nowhere on chain: it can never land.
	g.w.height = 2_001
	if s, _ := g.rail.Settlement(ctx, g.evidence("sig-missing")); s.Status != app.SettlementNotSettled || !strings.Contains(s.Detail, "can never land") {
		t.Errorf("after expiry: %+v", s)
	}
	// With no expiry recorded, its absence proves nothing.
	ev := g.evidence("sig-missing")
	ev.ValidUntilHeight = 0
	if s, _ := g.rail.Settlement(ctx, ev); s.Status != app.SettlementUnknown {
		t.Errorf("no expiry: %+v", s)
	}
	// Evidence that isn't ours is not judged.
	other := g.evidence("sig-x")
	other.Payer = stranger.String()
	if s, _ := g.rail.Settlement(ctx, other); s.Status != app.SettlementUnknown {
		t.Errorf("another wallet's: %+v", s)
	}
	if s, _ := g.rail.Settlement(ctx, econ.Evidence{Payer: g.wallet.PublicKey().String()}); s.Status != app.SettlementUnknown {
		t.Errorf("no transaction: %+v", s)
	}
}
