package solanax402

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/solanatest"
	"github.com/project-algebra/algebra/providers/x402"
)

type rig struct {
	t       *testing.T
	chain   *solanatest.Chain
	rpc     *solana.RPC
	payer   *solana.Keypair
	sponsor *solana.Keypair
	payTo   solana.PublicKey
	mint    solana.PublicKey
	src     solana.PublicKey
	dst     solana.PublicKey
	rail    *Rail
	now     time.Time
}

func newRig(t *testing.T, cluster string) *rig {
	t.Helper()
	genesis, network := solana.MainnetGenesisHash, chain.Solana
	if cluster == "devnet" {
		genesis, network = solana.DevnetGenesisHash, chain.SolanaDevnet
	}
	r := &rig{t: t, chain: solanatest.New(genesis), now: time.Now()}
	r.chain.Clock = func() time.Time { return r.now }
	r.rpc = solana.NewRPC(r.chain.Serve(t), nil)
	r.payer, _ = solana.NewKeypair()
	r.sponsor = r.chain.Sponsor()
	payee, _ := solana.NewKeypair()
	r.payTo = payee.PublicKey()
	mintStr, _ := chain.AssetAddress(network, "USDC")
	r.mint = solana.MustPublicKey(mintStr)
	r.src, _ = solana.AssociatedTokenAddress(r.payer.PublicKey(), r.mint, solana.TokenProgram)
	r.dst, _ = solana.AssociatedTokenAddress(r.payTo, r.mint, solana.TokenProgram)
	r.chain.SetTokenAccount(r.src, 10_000_000)
	r.chain.SetTokenAccount(r.dst, 0)
	rail, err := New(Config{Cluster: cluster, RPC: r.rpc, Signer: r.payer, Now: func() time.Time { return r.now }})
	if err != nil {
		t.Fatal(err)
	}
	r.rail = rail
	return r
}

// challenge is a provider's 402 for this rig, in either protocol version.
func (r *rig) challenge(version int, mutate func(map[string]any)) json.RawMessage {
	net, key := r.rail.Network(), "maxAmountRequired"
	if version == 2 {
		key = "amount"
		net = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"
		if r.rail.Network() == chain.SolanaDevnet {
			net = "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1"
		}
	}
	opt := map[string]any{
		"scheme": "exact", "network": net, key: "5000", "asset": r.mint.String(), "payTo": r.payTo.String(),
		"maxTimeoutSeconds": 60, "extra": map[string]any{"feePayer": r.sponsor.PublicKey().String()},
	}
	if mutate != nil {
		mutate(opt)
	}
	ch := map[string]any{"x402Version": version, "accepts": []any{opt}}
	if version == 2 {
		ch["resource"] = map[string]any{"url": "https://api.example.com/risk", "mimeType": "application/json"}
	}
	b, _ := json.Marshal(ch)
	return b
}

func (r *rig) authorize(version int, hold int64, mutate func(map[string]any)) (*app.PaymentAuthority, error) {
	return r.rail.Authorize(context.Background(), &econ.Reservation{ID: "rsv_1", HoldMinor: hold},
		app.PaymentRequest{Requirements: r.challenge(version, mutate), Resource: "https://api.example.com/risk"})
}

// transactionOf pulls the transaction out of a payment header value.
func transactionOf(t *testing.T, auth *app.PaymentAuthority) (*solana.Transaction, string) {
	t.Helper()
	p, err := x402.DecodePayment(auth.Value)
	if err != nil {
		t.Fatal(err)
	}
	var inner struct {
		Transaction string `json:"transaction"`
	}
	if err := json.Unmarshal(p.Payload, &inner); err != nil {
		t.Fatal(err)
	}
	tx, err := solana.DecodeTransactionBase64(inner.Transaction)
	if err != nil {
		t.Fatal(err)
	}
	return tx, inner.Transaction
}

func (r *rig) evidence(auth *app.PaymentAuthority) econ.Evidence {
	ev := auth.Evidence
	ev.AuthorityIssued, ev.AuthorityIssuedAt = true, &r.now
	return ev
}

func TestAuthorizeBuildsTheTransactionTheSpecDescribes(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			r := newRig(t, "mainnet")
			auth, err := r.authorize(version, 5_000, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantHeader := x402.HeaderPayment
			if version == 2 {
				wantHeader = x402.HeaderPaymentV2
			}
			if auth.Header != wantHeader || auth.AmountMinor != 5_000 {
				t.Errorf("header %q amount %d", auth.Header, auth.AmountMinor)
			}
			tx, _ := transactionOf(t, auth)
			m := tx.Message
			// Sponsor first, then the payer; the sponsor's slot is empty.
			if m.AccountKeys[0] != r.sponsor.PublicKey() || m.AccountKeys[1] != r.payer.PublicKey() || m.NumRequiredSignatures != 2 {
				t.Fatalf("signers: %v", m.AccountKeys[:2])
			}
			if _, signed := tx.SignerSignature(r.sponsor.PublicKey()); signed {
				t.Error("the sponsor's signature is for the sponsor to add")
			}
			if !tx.VerifySignature(r.payer.PublicKey()) {
				t.Error("the payer's signature must verify")
			}
			// [limit, price, TransferChecked, Memo].
			if len(m.Instructions) != 4 {
				t.Fatalf("%d instructions", len(m.Instructions))
			}
			progs := []solana.PublicKey{}
			for _, in := range m.Instructions {
				progs = append(progs, m.ProgramOf(in))
			}
			if progs[0] != solana.ComputeBudgetProgram || progs[1] != solana.ComputeBudgetProgram || progs[2] != solana.TokenProgram || progs[3] != solana.MemoProgram {
				t.Errorf("programs: %v", progs)
			}
			if m.Instructions[0].Data[0] != 2 || m.Instructions[1].Data[0] != 3 {
				t.Errorf("compute budget discriminators: %v %v", m.Instructions[0].Data, m.Instructions[1].Data)
			}
			// The priority fee is a token amount, far under the spec's cap.
			if price := uint64(m.Instructions[1].Data[1]); price != 1 {
				t.Errorf("price = %d micro-lamports", price)
			}
			// TransferChecked: payer's USDC account -> the payee's associated account, exact amount.
			tc := m.Instructions[2]
			accts := m.InstructionAccounts(tc)
			if accts[0] != r.src || accts[1] != r.mint || accts[2] != r.dst || accts[3] != r.payer.PublicKey() {
				t.Errorf("transfer accounts (source, mint, destination, authority): %v", accts)
			}
			if tc.Data[0] != 12 || tc.Data[1] != 0x88 || tc.Data[2] != 0x13 || tc.Data[9] != 6 {
				t.Errorf("transfer data (5000 with 6 decimals): %v", tc.Data)
			}
			// The sponsor is in no instruction, so it can't be drained through this transaction.
			for _, in := range m.Instructions {
				for _, a := range m.InstructionAccounts(in) {
					if a == r.sponsor.PublicKey() {
						t.Error("the sponsor must not appear in any instruction")
					}
				}
			}
			// A random nonce of at least 16 bytes, hex-encoded.
			nonce := string(m.Instructions[3].Data)
			if b, err := hex.DecodeString(nonce); err != nil || len(b) < 16 {
				t.Errorf("memo must be a hex nonce of at least 16 bytes: %q", nonce)
			}
			// The blockhash is a real one from the node, and the compute limit fits the memo.
			if m.RecentBlockhash == [32]byte{} {
				t.Error("blockhash")
			}
			if limit := uint32(m.Instructions[0].Data[1]) | uint32(m.Instructions[0].Data[2])<<8 | uint32(m.Instructions[0].Data[3])<<16; limit < 30_000 || limit > 400_000 {
				t.Errorf("compute limit %d", limit)
			}

			// Payload by version.
			p, _ := x402.DecodePayment(auth.Value)
			if p.Version != version {
				t.Errorf("payload version %d", p.Version)
			}
			if version == 2 {
				if len(p.Accepted) == 0 || !strings.Contains(string(p.Accepted), r.payTo.String()) || !strings.Contains(string(p.Resource), "api.example.com/risk") || p.Scheme != "" {
					t.Errorf("v2 echoes the option it accepted and the resource: %+v", p)
				}
			} else if p.Scheme != "exact" || p.Network != "solana" || len(p.Accepted) != 0 {
				t.Errorf("v1 names scheme and network: %+v", p)
			}

			// Evidence: enough to find and prove this payment later, and nothing secret.
			ev := auth.Evidence
			sig, _ := tx.SignerSignature(r.payer.PublicKey())
			if ev.PaymentID != solana.EncodeBase58(sig[:]) || ev.Rail != RailName || ev.Network != "solana" || ev.Asset != r.mint.String() ||
				ev.Payer != r.payer.PublicKey().String() || ev.PayTo != r.payTo.String() || ev.AmountMinor != 5_000 ||
				ev.ValidUntilHeight != r.chain.Height+150 || ev.Test || ev.Transaction != "" {
				t.Errorf("evidence: %+v", ev)
			}
			if blob, _ := json.Marshal(auth.Evidence); strings.Contains(string(blob), auth.Value) {
				t.Error("the payment value must not be in the evidence")
			}
			// Nothing was submitted: authorization only signs.
			if bal, _ := r.chain.TokenBalance(r.dst); bal != 0 {
				t.Error("authorizing must not move money")
			}
		})
	}
}

func TestAuthorizeUsesAnotherNetworkNameForDevnet(t *testing.T) {
	r := newRig(t, "devnet")
	auth, err := r.authorize(2, 5_000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth.Evidence.Network != "solana-devnet" || !auth.Evidence.Test {
		t.Errorf("a devnet payment is a test payment: %+v", auth.Evidence)
	}
	if auth.Evidence.Asset != "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU" {
		t.Errorf("devnet USDC: %s", auth.Evidence.Asset)
	}
}

func TestAuthorizeHonoursASellersMemo(t *testing.T) {
	r := newRig(t, "mainnet")
	auth, err := r.authorize(2, 5_000, func(o map[string]any) { o["extra"].(map[string]any)["memo"] = "order-42" })
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := transactionOf(t, auth)
	if string(tx.Message.Instructions[3].Data) != "order-42" {
		t.Errorf("the seller's memo goes in verbatim: %q", tx.Message.Instructions[3].Data)
	}
	// A long memo needs a bigger compute budget, and gets one.
	long := strings.Repeat("m", 200)
	auth2, err := r.authorize(2, 5_000, func(o map[string]any) { o["extra"].(map[string]any)["memo"] = long })
	if err != nil {
		t.Fatal(err)
	}
	tx2, _ := transactionOf(t, auth2)
	d := tx2.Message.Instructions[0].Data
	if limit := uint32(d[1]) | uint32(d[2])<<8 | uint32(d[3])<<16; limit < 100_000 {
		t.Errorf("a 200-byte memo needs a larger compute limit, got %d", limit)
	}
}

func TestComputeUnitLimit(t *testing.T) {
	if got := computeUnitLimit(32); got != 32_000 {
		t.Errorf("a 32-byte nonce: %d", got)
	}
	if got := computeUnitLimit(0); got < 30_000 {
		t.Errorf("the floor: %d", got)
	}
	if got := computeUnitLimit(256); got < 100_000 || got > 400_000 {
		t.Errorf("a 256-byte memo: %d", got)
	}
	if got := computeUnitLimit(100_000); got != 400_000 {
		t.Errorf("the ceiling is the spec's 400k: %d", got)
	}
	// The estimate must cover what was measured on devnet: 32 bytes ~13k, 80 bytes ~30k (including ~300 for the budget instructions).
	if computeUnitLimit(80) < 30_072 || computeUnitLimit(32) < 13_075 {
		t.Error("the limit must cover the measured cost")
	}
}

func TestAuthorizeRefusals(t *testing.T) {
	r := newRig(t, "mainnet")
	fakeMint := solana.PublicKey{7, 7, 7}
	cases := map[string]struct {
		hold   int64
		mutate func(map[string]any)
		want   string
	}{
		"look-alike token": {5_000, func(o map[string]any) { o["asset"] = fakeMint.String() }, "not Circle's USDC"},
		"wrong network":    {5_000, func(o map[string]any) { o["network"] = "solana-devnet" }, "x402"},
		"over the hold":    {4_999, nil, "more than this attempt's hold"},
		"over the ceiling": {5_000_000, func(o map[string]any) { o["maxAmountRequired"] = "1000001" }, "hard ceiling"},
		"no fee payer":     {5_000, func(o map[string]any) { delete(o, "extra") }, "fee payer"},
		"bad fee payer":    {5_000, func(o map[string]any) { o["extra"] = map[string]any{"feePayer": "nope"} }, "fee payer"},
		"sponsor is payer": {5_000, func(o map[string]any) { o["extra"] = map[string]any{"feePayer": r.payer.PublicKey().String()} }, "this wallet"},
		"pays itself":      {5_000, func(o map[string]any) { o["payTo"] = r.payer.PublicKey().String() }, "this wallet"},
		"bad payee":        {5_000, func(o map[string]any) { o["payTo"] = "nope" }, "payee"},
		"unknown scheme":   {5_000, func(o map[string]any) { o["scheme"] = "upto" }, "x402"},
		"zero amount":      {5_000, func(o map[string]any) { o["maxAmountRequired"] = "0" }, "amount"},
		"unusable memo":    {5_000, func(o map[string]any) { o["extra"].(map[string]any)["memo"] = strings.Repeat("x", 600) }, "memo"},
	}
	for name, tc := range cases {
		_, err := r.authorize(1, tc.hold, tc.mutate)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want an error containing %q, got %v", name, tc.want, err)
		}
	}
	// Nothing was signed for any of them: no blockhash was even asked for after a refusal that comes first.
	if _, err := r.rail.Authorize(context.Background(), &econ.Reservation{HoldMinor: 5_000}, app.PaymentRequest{Requirements: json.RawMessage(`{"hello":1}`)}); err == nil {
		t.Error("garbage requirements are refused")
	}
}

func TestAuthorizeChecksTheAccountsBeforeSigning(t *testing.T) {
	r := newRig(t, "mainnet")
	// The payee has no USDC account: the sponsor would fail the transfer, so don't sign it.
	empty, _ := solana.NewKeypair()
	_, err := r.authorize(1, 5_000, func(o map[string]any) { o["payTo"] = empty.PublicKey().String() })
	if err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Errorf("a payee without a USDC account: %v", err)
	}
	// The wallet can't cover it.
	r.chain.SetTokenAccount(r.src, 4_999)
	if _, err := r.authorize(1, 5_000, nil); err == nil || !strings.Contains(err.Error(), "insufficient USDC") {
		t.Errorf("an underfunded wallet: %v", err)
	}
	// The wallet has no USDC account at all.
	other := newRig(t, "mainnet")
	other.chain = solanatest.New(solana.MainnetGenesisHash)
	other.rpc = solana.NewRPC(other.chain.Serve(t), nil)
	other.chain.SetTokenAccount(other.dst, 0)
	rail, _ := New(Config{Cluster: "mainnet", RPC: other.rpc, Signer: other.payer})
	other.rail = rail
	if _, err := other.authorize(1, 5_000, nil); err == nil || !strings.Contains(err.Error(), "no USDC account") {
		t.Errorf("a wallet that never held USDC: %v", err)
	}
	// An RPC outage is an error, never a signature.
	r.chain.SetTokenAccount(r.src, 10_000_000)
	r.chain.FailMethod["getLatestBlockhash"] = errors.New("node down")
	if _, err := r.authorize(1, 5_000, nil); err == nil {
		t.Error("no blockhash, no payment")
	}
}

func TestNewRefusesBadConfiguration(t *testing.T) {
	kp, _ := solana.NewKeypair()
	rpc := solana.NewRPC("http://localhost:1", nil)
	for name, cfg := range map[string]Config{
		"no rpc": {Cluster: "mainnet", Signer: kp}, "no signer": {Cluster: "mainnet", RPC: rpc},
		"bad cluster": {Cluster: "testnet", RPC: rpc, Signer: kp}, "no cluster": {RPC: rpc, Signer: kp},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	r, err := New(Config{Cluster: " Mainnet-Beta ", RPC: rpc, Signer: kp})
	if err != nil || r.Network() != "solana" || r.cfg.MaxPaymentMinor != DefaultMaxPaymentMinor || r.Address() != kp.PublicKey() || r.Name() != "x402-solana" {
		t.Errorf("defaults: %+v %v", r, err)
	}
}

func TestStatus(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, "mainnet")
	r.chain.Fund(r.payer.PublicKey(), 3_000_000)
	st, err := r.rail.Status(ctx)
	if err != nil || st.Address != r.payer.PublicKey().String() || st.SOLLamports != 3_000_000 || st.USDCMinor == nil || *st.USDCMinor != 10_000_000 ||
		st.USDCAccount != r.src.String() || st.MaxPaymentMinor != DefaultMaxPaymentMinor {
		t.Errorf("status: %+v %v", st, err)
	}
	// A node on the wrong cluster is refused: never pay "mainnet" through devnet.
	wrong := newRig(t, "devnet")
	rail, _ := New(Config{Cluster: "mainnet", RPC: wrong.rpc, Signer: r.payer})
	if _, err := rail.Status(ctx); !errors.Is(err, ErrWrongCluster) {
		t.Errorf("wrong cluster must be the typed error: %v", err)
	}
	// A wallet with no USDC account reports nil, not an error.
	empty := newRig(t, "mainnet")
	empty.chain = solanatest.New(solana.MainnetGenesisHash)
	rail2, _ := New(Config{Cluster: "mainnet", RPC: solana.NewRPC(empty.chain.Serve(t), nil), Signer: empty.payer})
	if st, err := rail2.Status(ctx); err != nil || st.USDCMinor != nil {
		t.Errorf("no USDC account yet: %+v %v", st, err)
	}
}

// ---- settlement ----

func (r *rig) land(auth *app.PaymentAuthority) string {
	r.t.Helper()
	_, b64 := transactionOf(r.t, auth)
	sig, err := r.chain.Land(b64, r.sponsor)
	if err != nil {
		r.t.Fatal(err)
	}
	return sig
}

func (r *rig) settle(ev econ.Evidence) app.Settlement {
	r.t.Helper()
	st, err := r.rail.Settlement(context.Background(), ev)
	if err != nil {
		r.t.Fatal(err)
	}
	return st
}

func TestSettlementPendingUntilItLandsOrExpires(t *testing.T) {
	r := newRig(t, "mainnet")
	auth, _ := r.authorize(1, 5_000, nil)
	ev := r.evidence(auth)
	if st := r.settle(ev); st.Status != app.SettlementPending || !strings.Contains(st.Detail, "can still land") {
		t.Fatalf("not landed, not expired: %+v", st)
	}
	// Expired at the tip, but not yet at a finalized height: a block it could
	// have landed in may not be final, so still not "no".
	r.chain.Advance(160)
	if st := r.settle(ev); st.Status != app.SettlementPending {
		t.Errorf("expired but not finalized-expired: %+v", st)
	}
}

func TestSettlementFindsThePaymentWithTheProvidersSignature(t *testing.T) {
	r := newRig(t, "mainnet")
	auth, _ := r.authorize(2, 5_000, nil)
	sig := r.land(auth)
	ev := r.evidence(auth)
	ev.Transaction = sig
	st := r.settle(ev)
	if st.Status != app.SettlementSettled || st.AmountMinor != 5_000 || st.Transaction != sig || st.Payer != r.payer.PublicKey().String() || st.PayTo != r.payTo.String() {
		t.Errorf("settled: %+v", st)
	}
	if bal, _ := r.chain.TokenBalance(r.dst); bal != 5_000 {
		t.Errorf("the money moved on chain: %d", bal)
	}
}

// The provider's response, and with it the sponsor's signature, was lost. Our
// own signature is on the transaction, so it is found anyway.
func TestSettlementFindsALostResponsesPaymentFromTheChain(t *testing.T) {
	r := newRig(t, "mainnet")
	auth, _ := r.authorize(1, 5_000, nil)
	sig := r.land(auth)
	ev := r.evidence(auth) // no Transaction: the response never arrived
	st := r.settle(ev)
	if st.Status != app.SettlementSettled || st.Transaction != sig || st.AmountMinor != 5_000 {
		t.Errorf("recovered from the chain: %+v", st)
	}
}

func TestSettlementIgnoresAProvidersFalseClaim(t *testing.T) {
	r := newRig(t, "mainnet")
	// A different payment by the same wallet, really on chain.
	other, _ := r.authorize(1, 5_000, nil)
	otherSig := r.land(other)
	// Ours never lands, but the provider names the other one as ours.
	auth, _ := r.authorize(1, 5_000, nil)
	ev := r.evidence(auth)
	ev.Transaction = otherSig
	if st := r.settle(ev); st.Status != app.SettlementPending {
		t.Fatalf("a real transaction that isn't ours must not settle us: %+v", st)
	}
	// And a signature that doesn't exist at all.
	ev.Transaction = solanatest.RandomSignature()
	if st := r.settle(ev); st.Status != app.SettlementPending {
		t.Errorf("an invented signature: %+v", st)
	}
	// After expiry the false claim still doesn't turn it into a settlement.
	r.chain.Advance(200)
	if st := r.settle(ev); st.Status != app.SettlementNotSettled {
		t.Errorf("proven absent despite the provider's claim: %+v", st)
	}
}

func TestSettlementLandedButFailedMovedNoMoney(t *testing.T) {
	r := newRig(t, "mainnet")
	auth, _ := r.authorize(1, 5_000, nil)
	_, b64 := transactionOf(t, auth)
	sig, err := r.chain.LandFailed(b64, r.sponsor)
	if err != nil {
		t.Fatal(err)
	}
	ev := r.evidence(auth)
	st := r.settle(ev)
	if st.Status != app.SettlementNotSettled || !strings.Contains(st.Detail, "landed and failed") || st.Transaction != sig {
		t.Errorf("a failed transaction is final: %+v", st)
	}
	if bal, _ := r.chain.TokenBalance(r.dst); bal != 0 {
		t.Errorf("no money moved: %d", bal)
	}
}

func TestSettlementProvesAbsenceOnlyAfterFinalizedExpiry(t *testing.T) {
	r := newRig(t, "mainnet")
	auth, _ := r.authorize(1, 5_000, nil)
	ev := r.evidence(auth)
	r.chain.Advance(200) // finalized height is past lastValidBlockHeight
	st := r.settle(ev)
	if st.Status != app.SettlementNotSettled || !strings.Contains(st.Detail, "can never land") {
		t.Fatalf("expired at a finalized height, not in the chain: %+v", st)
	}
	// Without an expiry on record, absence proves nothing.
	ev.ValidUntilHeight = 0
	if st := r.settle(ev); st.Status != app.SettlementUnknown {
		t.Errorf("unknown expiry: %+v", st)
	}
}

// A payment that lands in the last moments before expiry is seen at confirmed
// level; it must be found, not declared absent.
func TestSettlementFindsAPaymentThatLandedJustBeforeExpiry(t *testing.T) {
	r := newRig(t, "mainnet")
	auth, _ := r.authorize(1, 5_000, nil)
	sig := r.land(auth)
	r.chain.ConfirmedOnly(sig) // not finalized yet
	r.chain.Advance(200)
	if st := r.settle(r.evidence(auth)); st.Status != app.SettlementSettled {
		t.Errorf("seen at confirmed: settled, not absent: %+v", st)
	}
	// Under a stricter rail that wants finalized, it isn't settled yet, and
	// crucially isn't declared absent either while the chain catches up.
	strict, _ := New(Config{Cluster: "mainnet", RPC: r.rpc, Signer: r.payer, Commitment: solana.Finalized, Now: func() time.Time { return r.now }})
	st, err := strict.Settlement(context.Background(), r.evidence(auth))
	if err != nil || st.Status == app.SettlementSettled {
		t.Fatalf("a finalized-only rail doesn't count a confirmed-only transaction: %+v %v", st, err)
	}
	r.chain.Finalize(sig)
	if st, _ := strict.Settlement(context.Background(), r.evidence(auth)); st.Status != app.SettlementSettled {
		t.Errorf("once finalized: %+v", st)
	}
}

func TestSettlementScansPastOtherPayments(t *testing.T) {
	r := newRig(t, "mainnet")
	r.chain.SetTokenAccount(r.src, 1_000_000_000)
	mine, _ := r.authorize(1, 5_000, nil)
	mySig := r.land(mine)
	// 120 later payments by the same wallet push ours past the first page of history.
	for i := 0; i < 120; i++ {
		r.now = r.now.Add(time.Second)
		a, err := r.authorize(1, 5_000, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.land(a)
	}
	if st := r.settle(r.evidence(mine)); st.Status != app.SettlementSettled || st.Transaction != mySig {
		t.Errorf("found on the second page of history: %+v", st)
	}
}

// If the wallet's history is too busy to read back to the moment of
// authorization, finding nothing proves nothing.
func TestSettlementDoesNotProveAbsenceFromAnIncompleteScan(t *testing.T) {
	r := newRig(t, "mainnet")
	r.chain.SetTokenAccount(r.src, 1_000_000_000)
	auth, _ := r.authorize(1, 5_000, nil)
	ev := r.evidence(auth)
	// Many payments after ours was authorized, more than a scan will fetch.
	for i := 0; i < maxScanFetch+20; i++ {
		r.now = r.now.Add(time.Millisecond)
		a, err := r.authorize(1, 5_000, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.land(a)
	}
	r.chain.Advance(200)
	st := r.settle(ev)
	if st.Status == app.SettlementNotSettled {
		t.Fatalf("a scan that couldn't reach back must not prove absence: %+v", st)
	}
	if st.Status != app.SettlementPending {
		t.Errorf("want PENDING, got %+v", st)
	}
	// With the busy period older than the authorization it can: the scan
	// reaches back past the moment ours was authorized and finds nothing.
	r2 := newRig(t, "mainnet")
	r2.chain.SetTokenAccount(r2.src, 1_000_000_000)
	for i := 0; i < 30; i++ {
		r2.now = r2.now.Add(-time.Hour) // unrelated payments, long before
		a, _ := r2.authorize(1, 5_000, nil)
		r2.land(a)
	}
	r2.now = time.Now()
	auth2, _ := r2.authorize(1, 5_000, nil)
	ev2 := r2.evidence(auth2)
	r2.chain.Advance(200)
	if st := r2.settle(ev2); st.Status != app.SettlementNotSettled {
		t.Errorf("history reached back past the authorization: %+v", st)
	}
}

func TestSettlementErrorsAreNotAnswers(t *testing.T) {
	r := newRig(t, "mainnet")
	auth, _ := r.authorize(1, 5_000, nil)
	ev := r.evidence(auth)
	for _, method := range []string{"getSignaturesForAddress", "getBlockHeight"} {
		r.chain.FailMethod[method] = errors.New("rate limited")
		if _, err := r.rail.Settlement(context.Background(), ev); err == nil {
			t.Errorf("%s down: an RPC failure must be an error (the coordinator treats it as UNKNOWN), not a verdict", method)
		}
		delete(r.chain.FailMethod, method)
	}
}

func TestSettlementRefusesEvidenceItCannotInterpret(t *testing.T) {
	r := newRig(t, "mainnet")
	auth, _ := r.authorize(1, 5_000, nil)
	ev := r.evidence(auth)
	for name, mutate := range map[string]func(*econ.Evidence){
		"no payment id":  func(e *econ.Evidence) { e.PaymentID = "" },
		"no payer":       func(e *econ.Evidence) { e.Payer = "" },
		"another wallet": func(e *econ.Evidence) { e.Payer = solana.PublicKey{5}.String() },
		"garbage asset":  func(e *econ.Evidence) { e.Asset = "nope" },
		"garbage payee":  func(e *econ.Evidence) { e.PayTo = "nope" },
	} {
		e := ev
		mutate(&e)
		if st := r.settle(e); st.Status != app.SettlementUnknown {
			t.Errorf("%s: %+v", name, st)
		}
	}
}

func TestPaymentIDIsTheSignatureNotSomethingGuessable(t *testing.T) {
	r := newRig(t, "mainnet")
	a, _ := r.authorize(1, 5_000, nil)
	b, _ := r.authorize(1, 5_000, nil)
	if a.Evidence.PaymentID == b.Evidence.PaymentID {
		t.Error("each attempt has its own payment identity")
	}
	raw, err := solana.DecodeBase58(a.Evidence.PaymentID)
	if err != nil || len(raw) != 64 {
		t.Errorf("a payment id is a 64-byte signature: %v %d", err, len(raw))
	}
}

// An operator checking a provider with an unfunded wallet wants to see what
// would be paid and why it would fail, not just a refusal.
func TestDryRunReportsAccountProblemsInsteadOfRefusing(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, "mainnet")
	r.chain.SetTokenAccount(r.src, 100) // underfunded
	empty, _ := solana.NewKeypair()
	req := app.PaymentRequest{Requirements: r.challenge(2, func(o map[string]any) { o["payTo"] = empty.PublicKey().String() }), Resource: "https://api.example.com/risk"}

	auth, problems, err := r.rail.DryRun(ctx, &econ.Reservation{HoldMinor: 5_000}, req)
	if err != nil || auth == nil {
		t.Fatalf("a dry run still builds the transaction: %v", err)
	}
	if len(problems) != 2 || !strings.Contains(problems[0], "insufficient USDC") || !strings.Contains(problems[1], "doesn't exist") {
		t.Errorf("both account problems are reported: %v", problems)
	}
	if tx, _ := transactionOf(t, auth); len(tx.Message.Instructions) != 4 {
		t.Error("the transaction is the real one")
	}
	// The real thing refuses on the same facts.
	if _, err := r.rail.Authorize(ctx, &econ.Reservation{HoldMinor: 5_000}, req); err == nil {
		t.Error("Authorize must refuse what a dry run only reports")
	}
	// A violation of what the rail will pay is an error either way: no transaction is built.
	bad := app.PaymentRequest{Requirements: r.challenge(2, func(o map[string]any) { o["asset"] = solana.PublicKey{7}.String() })}
	if a, _, err := r.rail.DryRun(ctx, &econ.Reservation{HoldMinor: 5_000}, bad); err == nil || a != nil {
		t.Errorf("a look-alike token is never built, even in a dry run: %v", err)
	}
}

// Mainnet and devnet rails can be registered at once, so they must not share
// a name, and a payment's evidence names the rail that made it.
func TestRailsAreNamedByCluster(t *testing.T) {
	main, dev := newRig(t, "mainnet"), newRig(t, "devnet")
	if main.rail.Name() != RailName || dev.rail.Name() != DevnetRailName || RailName == DevnetRailName {
		t.Fatalf("names: %q %q", main.rail.Name(), dev.rail.Name())
	}
	auth, err := dev.authorize(2, 5000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ev := dev.evidence(auth); ev.Rail != DevnetRailName || ev.Network != "solana-devnet" || !ev.Test {
		t.Errorf("devnet evidence: %+v", ev)
	}
}
