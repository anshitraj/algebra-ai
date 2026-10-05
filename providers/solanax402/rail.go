// Package solanax402 is the real payment rail for x402's "exact" scheme on
// Solana: it signs USDC payments from a wallet Algebra controls and proves,
// from chain state alone, whether a payment settled.
//
// What it signs. For one attempt it builds the transaction the x402 spec
// describes: a v0 transaction whose fee payer is the provider's sponsor (so
// the payer needs no SOL), carrying [SetComputeUnitLimit, SetComputeUnitPrice,
// TransferChecked, Memo]. Only the payer signs; the sponsor adds its signature
// and submits. The sponsor can neither alter the transfer (the payer's
// signature covers it) nor be drained by it (it appears in no instruction).
//
// What it refuses to sign:
//
//   - anything but USDC at Circle's real mint on the configured cluster;
//   - more than the attempt's hold, or more than MaxPaymentMinor, a hard
//     ceiling that holds even if every policy above it is wrong;
//   - a payment to a token account that doesn't exist, or from one that can't
//     cover it;
//   - a sponsor that is the payer.
//
// How it proves settlement. The payer's signature on the transaction is the
// payment's identity and is unforgeable by anyone else, so the transaction can
// be found on chain even when the provider's response (and with it the
// sponsor's transaction signature) was lost. A payment is NOT_SETTLED only when
// its blockhash has expired at a finalized height and a finalized scan finds
// nothing, or the transaction landed and failed. Anything short of that is
// PENDING or UNKNOWN, never "no".
package solanax402

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/providers/x402"
)

// ErrWrongCluster: the RPC node serves a different cluster than the rail was
// configured for. It is a configuration mistake, not an outage, and must never
// be retried or worked around.
var ErrWrongCluster = errors.New("solanax402: the RPC node serves a different cluster than the rail is configured for")

// RailName is how reservations name this rail.
const RailName = "x402-solana"

// DefaultMaxPaymentMinor is one USDC: a deliberately small ceiling for a rail
// that is new. Raise it on purpose.
const DefaultMaxPaymentMinor = 1_000_000

// Scan bounds. A history scan fetches one transaction per entry, so it is
// bounded; hitting a bound means "can't prove", never "didn't happen".
const (
	pageSize     = 100
	maxScanPages = 3
	maxScanFetch = 150
	// scanSlack widens the time window for clock differences and slow blocks.
	scanSlack = 3 * time.Minute
)

// Config configures a Rail.
type Config struct {
	// Cluster is "mainnet" or "devnet".
	Cluster string
	RPC     *solana.RPC
	Signer  *solana.Keypair
	// MaxPaymentMinor is the most one payment may be, in micro-USDC, whatever
	// the policy above says. Zero means DefaultMaxPaymentMinor.
	MaxPaymentMinor int64
	// ComputePriceMicroLamports is the priority fee per compute unit. The
	// sponsor pays fees, so this is kept at a token amount. Zero means 1.
	ComputePriceMicroLamports uint64
	// Commitment is how final a payment must be to count as settled. Empty
	// means confirmed.
	Commitment string
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Rail is app.Rail and app.PaymentAuthorizer for Solana USDC over x402.
type Rail struct {
	cfg     Config
	network string
	mint    solana.PublicKey
	payer   solana.PublicKey
	now     func() time.Time
}

var (
	_ app.Rail              = (*Rail)(nil)
	_ app.PaymentAuthorizer = (*Rail)(nil)
)

// New builds a Rail. It checks configuration only; Status checks the cluster.
func New(cfg Config) (*Rail, error) {
	if cfg.RPC == nil || cfg.Signer == nil {
		return nil, errors.New("solanax402: an RPC client and a signer are required")
	}
	var network string
	switch strings.ToLower(strings.TrimSpace(cfg.Cluster)) {
	case "mainnet", "mainnet-beta":
		network = chain.Solana
	case "devnet":
		network = chain.SolanaDevnet
	default:
		return nil, fmt.Errorf("solanax402: cluster must be mainnet or devnet, not %q", cfg.Cluster)
	}
	mintStr, ok := chain.AssetAddress(network, "USDC")
	if !ok {
		return nil, fmt.Errorf("solanax402: no known USDC on %s", network)
	}
	mint, err := solana.ParsePublicKey(mintStr)
	if err != nil {
		return nil, err
	}
	if cfg.MaxPaymentMinor <= 0 {
		cfg.MaxPaymentMinor = DefaultMaxPaymentMinor
	}
	if cfg.ComputePriceMicroLamports == 0 {
		cfg.ComputePriceMicroLamports = 1
	}
	if cfg.Commitment == "" {
		cfg.Commitment = solana.Confirmed
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Rail{cfg: cfg, network: network, mint: mint, payer: cfg.Signer.PublicKey(), now: now}, nil
}

func (r *Rail) Name() string { return RailName }

// Network is the canonical network this rail pays on.
func (r *Rail) Network() string { return r.network }

// Address is the wallet payments are made from.
func (r *Rail) Address() solana.PublicKey { return r.payer }

// WalletStatus is what an operator needs to see before trusting the rail.
type WalletStatus struct {
	Network string `json:"network"`
	Address string `json:"address"`
	// SOLLamports is for information: the sponsor pays fees, so none is needed.
	SOLLamports uint64 `json:"sol_lamports"`
	USDCAccount string `json:"usdc_account"`
	// USDCMinor is nil when the wallet has no USDC account yet.
	USDCMinor       *uint64 `json:"usdc_minor"`
	MaxPaymentMinor int64   `json:"max_payment_minor"`
}

// Status checks that the RPC node serves the cluster the rail was configured
// for, and reports the wallet. A node on the wrong cluster is refused: paying
// "mainnet" through a devnet node, or the reverse, must never be possible.
func (r *Rail) Status(ctx context.Context) (*WalletStatus, error) {
	genesis, err := r.cfg.RPC.GetGenesisHash(ctx)
	if err != nil {
		return nil, err
	}
	want := solana.MainnetGenesisHash
	if r.network == chain.SolanaDevnet {
		want = solana.DevnetGenesisHash
	}
	if genesis != want {
		return nil, fmt.Errorf("%w: it serves %s, the rail is for %s", ErrWrongCluster, genesis, r.network)
	}
	ata, err := solana.AssociatedTokenAddress(r.payer, r.mint, solana.TokenProgram)
	if err != nil {
		return nil, err
	}
	st := &WalletStatus{Network: r.network, Address: r.payer.String(), USDCAccount: ata.String(), MaxPaymentMinor: r.cfg.MaxPaymentMinor}
	if st.SOLLamports, err = r.cfg.RPC.GetBalance(ctx, r.payer, solana.Confirmed); err != nil {
		return nil, err
	}
	switch bal, err := r.cfg.RPC.GetTokenAccountBalance(ctx, ata, solana.Confirmed); {
	case errors.Is(err, solana.ErrAccountNotFound):
	case err != nil:
		return nil, err
	default:
		st.USDCMinor = &bal.Amount
	}
	return st, nil
}

// networkNames are the names a provider may use for this rail's network: the
// short name (x402 v1) and the CAIP-2 id (v2).
func (r *Rail) networkNames() []string {
	if r.network == chain.SolanaDevnet {
		return []string{chain.SolanaDevnet, "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1"}
	}
	return []string{chain.Solana, "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"}
}

// memoUnits estimates the compute a memo of n bytes costs: measured on devnet
// at about 13k units for 32 bytes and 30k for 80, growing with length.
func memoUnits(n int) int {
	u := 12_800
	if n > 32 {
		u += 360 * (n - 32)
	}
	return u
}

// computeUnitLimit sizes the compute budget to the transaction with a margin,
// since a limit that is too low fails the payment and one that is far too high
// only costs the sponsor nothing at this price.
func computeUnitLimit(memoLen int) uint32 {
	units := (300 + 8_000 + memoUnits(memoLen)) * 3 / 2
	units = (units + 999) / 1000 * 1000
	return uint32(min(max(units, 30_000), 400_000))
}

// Authorize signs one payment for an executing attempt. It never submits
// anything: the returned value is a single-use payment header that the
// provider's sponsor submits when it settles.
func (r *Rail) Authorize(ctx context.Context, rv *econ.Reservation, req app.PaymentRequest) (*app.PaymentAuthority, error) {
	auth, problems, err := r.build(ctx, rv, req)
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		return nil, errors.New(problems[0])
	}
	return auth, nil
}

// DryRun builds a payment exactly as Authorize does but, instead of refusing
// when the wallet or the payee's account can't support it, reports why and
// still returns the transaction, so an operator can see what would be paid
// and have a node simulate it with an unfunded wallet. A policy refusal (a
// look-alike token, a payment over the ceiling) is still an error. The result
// must be discarded, never sent.
func (r *Rail) DryRun(ctx context.Context, rv *econ.Reservation, req app.PaymentRequest) (*app.PaymentAuthority, []string, error) {
	return r.build(ctx, rv, req)
}

// build parses and validates a payment request and builds the signed
// transaction. Violations of what the rail will pay are errors; facts about the
// accounts, which the chain can change, come back as problems.
func (r *Rail) build(ctx context.Context, rv *econ.Reservation, req app.PaymentRequest) (*app.PaymentAuthority, []string, error) {
	sel, err := x402.SelectRaw(req.Requirements, "exact", r.networkNames()...)
	if err != nil {
		return nil, nil, fmt.Errorf("solanax402: %w", err)
	}
	q := sel.Requirements
	amount, err := q.AmountMinor()
	if err != nil {
		return nil, nil, err
	}
	switch {
	case !chain.SameAddress(r.network, q.Asset, r.mint.String()):
		return nil, nil, fmt.Errorf("solanax402: the provider asks for asset %q, which is not Circle's USDC on %s", q.Asset, r.network)
	case amount > r.cfg.MaxPaymentMinor:
		return nil, nil, fmt.Errorf("solanax402: %d exceeds this rail's hard ceiling of %d per payment", amount, r.cfg.MaxPaymentMinor)
	case rv != nil && amount > rv.HoldMinor:
		return nil, nil, fmt.Errorf("solanax402: the provider asks for %d, more than this attempt's hold of %d", amount, rv.HoldMinor)
	}
	payTo, err := solana.ParsePublicKey(q.PayTo)
	if err != nil {
		return nil, nil, fmt.Errorf("solanax402: payee: %w", err)
	}
	feePayer, err := solana.ParsePublicKey(q.ExtraField("feePayer"))
	if err != nil {
		return nil, nil, errors.New("solanax402: the requirements name no fee payer (extra.feePayer), so the provider has no sponsor to submit this")
	}
	switch {
	case feePayer == r.payer:
		return nil, nil, errors.New("solanax402: the provider's fee payer is this wallet; refusing")
	case payTo == r.payer:
		return nil, nil, errors.New("solanax402: the provider's payee is this wallet; refusing")
	}

	src, err := solana.AssociatedTokenAddress(r.payer, r.mint, solana.TokenProgram)
	if err != nil {
		return nil, nil, err
	}
	dst, err := solana.AssociatedTokenAddress(payTo, r.mint, solana.TokenProgram)
	if err != nil {
		return nil, nil, err
	}
	// Read-only checks, so a payment that cannot work is refused with a
	// reason instead of failing on chain.
	var problems []string
	switch bal, err := r.cfg.RPC.GetTokenAccountBalance(ctx, src, solana.Confirmed); {
	case errors.Is(err, solana.ErrAccountNotFound):
		problems = append(problems, fmt.Sprintf("solanax402: this wallet (%s) has no USDC account on %s", r.payer, r.network))
	case err != nil:
		return nil, nil, err
	case bal.Amount < uint64(amount):
		problems = append(problems, fmt.Sprintf("solanax402: insufficient USDC: the wallet holds %d, the payment is %d", bal.Amount, amount))
	}
	switch info, err := r.cfg.RPC.GetAccountInfo(ctx, dst, solana.Confirmed); {
	case err != nil:
		return nil, nil, err
	case info == nil || info.Owner != solana.TokenProgram:
		problems = append(problems, fmt.Sprintf("solanax402: the provider's USDC account %s doesn't exist on %s, so it can't be paid", dst, r.network))
	}

	bh, err := r.cfg.RPC.GetLatestBlockhash(ctx, solana.Finalized)
	if err != nil {
		return nil, nil, err
	}
	memoText := q.ExtraField("memo")
	if memoText == "" {
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return nil, nil, err
		}
		memoText = hex.EncodeToString(nonce)
	}
	memo, err := solana.Memo(memoText)
	if err != nil {
		return nil, nil, fmt.Errorf("solanax402: the provider's memo is unusable: %w", err)
	}
	msg, err := solana.CompileV0(feePayer, []solana.Instruction{
		solana.SetComputeUnitLimit(computeUnitLimit(len(memoText))),
		solana.SetComputeUnitPrice(r.cfg.ComputePriceMicroLamports),
		solana.TransferChecked(solana.TokenProgram, src, r.mint, dst, r.payer, uint64(amount), chain.USDCDecimals),
		memo,
	}, bh.Blockhash)
	if err != nil {
		return nil, nil, err
	}
	tx := solana.NewTransaction(msg)
	if err := tx.PartialSign(r.cfg.Signer); err != nil {
		return nil, nil, err
	}
	sig, _ := tx.SignerSignature(r.payer)
	paymentID := solana.EncodeBase58(sig[:])

	payload, _ := json.Marshal(map[string]string{"transaction": tx.Base64()})
	pp := x402.PaymentPayload{Version: sel.Version, Payload: payload}
	header := x402.HeaderPayment
	if sel.Version >= 2 {
		header = x402.HeaderPaymentV2
		pp.Accepted = sel.Raw
		pp.Resource = sel.Resource
		if len(pp.Resource) == 0 && req.Resource != "" {
			pp.Resource, _ = json.Marshal(map[string]string{"url": req.Resource})
		}
	} else {
		pp.Scheme, pp.Network = "exact", q.Network
	}
	value, err := pp.Encode()
	if err != nil {
		return nil, nil, err
	}
	return &app.PaymentAuthority{
		Header: header, Value: value, AmountMinor: amount,
		Evidence: econ.Evidence{
			Rail: RailName, Protocol: "x402", Scheme: "exact", Network: r.network, Asset: r.mint.String(),
			PaymentID: paymentID, AmountMinor: amount, Payer: r.payer.String(), PayTo: payTo.String(),
			ValidUntilHeight: bh.LastValidBlockHeight, Test: chain.IsTestNetwork(r.network),
		},
	}, problems, nil
}

// Settlement answers from the chain, never from what a provider or an agent
// says.
func (r *Rail) Settlement(ctx context.Context, ev econ.Evidence) (app.Settlement, error) {
	base := app.Settlement{Network: ev.Network, Asset: ev.Asset, Payer: ev.Payer, PayTo: ev.PayTo, Test: ev.Test}
	status := func(s app.SettlementStatus, detail string) (app.Settlement, error) {
		base.Status, base.Detail = s, detail
		return base, nil
	}
	if ev.PaymentID == "" || ev.Payer == "" || ev.PayTo == "" {
		return status(app.SettlementUnknown, "the evidence doesn't identify a payment")
	}
	payer, err1 := solana.ParsePublicKey(ev.Payer)
	payTo, err2 := solana.ParsePublicKey(ev.PayTo)
	mint, err3 := solana.ParsePublicKey(ev.Asset)
	if err1 != nil || err2 != nil || err3 != nil || payer != r.payer {
		return status(app.SettlementUnknown, "the evidence is not for a payment made by this wallet")
	}
	src, err := solana.AssociatedTokenAddress(payer, mint, solana.TokenProgram)
	if err != nil {
		return base, err
	}
	dst, err := solana.AssociatedTokenAddress(payTo, mint, solana.TokenProgram)
	if err != nil {
		return base, err
	}
	m := match{ev: ev, src: src.String(), dst: dst.String(), mint: mint.String(), payer: payer.String()}

	// 1. The transaction the provider reported, if it is ours. A provider that
	// names a transaction that isn't ours is ignored, not believed.
	if ev.Transaction != "" {
		tx, err := r.cfg.RPC.GetTransaction(ctx, ev.Transaction, r.cfg.Commitment)
		if err != nil {
			return base, err
		}
		if tx != nil && slices.Contains(tx.Signatures, ev.PaymentID) {
			return m.verdict(base, tx), nil
		}
	}
	// 2. The payer's token account history: our signature is on the
	// transaction wherever it landed, whatever the provider says.
	tx, _, err := r.scan(ctx, src, m, r.cfg.Commitment)
	if err != nil {
		return base, err
	}
	if tx != nil {
		return m.verdict(base, tx), nil
	}
	// 3. Not found. That proves nothing until the payment can no longer land.
	if ev.ValidUntilHeight == 0 {
		return status(app.SettlementUnknown, "the payment's expiry isn't recorded, so its absence can't be proven")
	}
	final, err := r.cfg.RPC.GetBlockHeight(ctx, solana.Finalized)
	if err != nil {
		return base, err
	}
	if final <= ev.ValidUntilHeight {
		return status(app.SettlementPending, fmt.Sprintf("not on chain yet; it can still land until block height %d (finalized height is %d)", ev.ValidUntilHeight, final))
	}
	// Expired at a finalized height: every block it could have landed in is
	// final. Look once more at finalized commitment.
	tx, complete, err := r.scan(ctx, src, m, solana.Finalized)
	if err != nil {
		return base, err
	}
	if tx != nil {
		return m.verdict(base, tx), nil
	}
	if !complete {
		return status(app.SettlementPending, "the payment's blockhash expired, but the wallet's history is too long to scan back far enough to prove it never landed")
	}
	return status(app.SettlementNotSettled, "the payment's blockhash expired at a finalized height and it is not in the chain: it can never land")
}

// match carries what identifies our payment on chain.
type match struct {
	ev                    econ.Evidence
	src, dst, mint, payer string
}

// verdict reads a transaction that carries our signature.
func (m match) verdict(base app.Settlement, tx *solana.TxInfo) app.Settlement {
	base.Transaction = tx.Signatures[0]
	if tx.Err != nil {
		e, _ := json.Marshal(tx.Err)
		base.Status, base.Detail = app.SettlementNotSettled, "the transaction landed and failed on chain ("+string(e)+"): no money moved"
		return base
	}
	for _, t := range tx.Transfers() {
		if t.Source == m.src && t.Destination == m.dst && t.Mint == m.mint && t.Authority == m.payer && int64(t.Amount) == m.ev.AmountMinor {
			base.Status, base.AmountMinor = app.SettlementSettled, int64(t.Amount)
			base.Detail = "the transfer is on chain"
			return base
		}
	}
	// Our signature on a transaction whose transfer isn't the one we signed
	// can't happen; if it does, claim nothing.
	base.Status, base.Detail = app.SettlementUnknown, "a transaction carries our signature but not the transfer we signed"
	return base
}

// scan looks through the payer's token account history, newest first, for the
// transaction that carries our signature. complete is true when it reached
// back past the moment the payment was authorized, so not finding it means
// something; false when a bound stopped it first, so it doesn't.
func (r *Rail) scan(ctx context.Context, src solana.PublicKey, m match, commitment string) (found *solana.TxInfo, complete bool, err error) {
	var cutoff int64
	if m.ev.AuthorityIssuedAt != nil {
		cutoff = m.ev.AuthorityIssuedAt.Add(-scanSlack).Unix()
	}
	before, fetched := "", 0
	for page := 0; page < maxScanPages; page++ {
		sigs, err := r.cfg.RPC.GetSignaturesForAddress(ctx, src, pageSize, before, commitment)
		if err != nil {
			return nil, false, err
		}
		for _, s := range sigs {
			if cutoff > 0 && s.BlockTime != nil && *s.BlockTime < cutoff {
				return nil, true, nil
			}
			if fetched >= maxScanFetch {
				return nil, false, nil
			}
			fetched++
			tx, err := r.cfg.RPC.GetTransaction(ctx, s.Signature, commitment)
			if err != nil {
				return nil, false, err
			}
			if tx != nil && slices.Contains(tx.Signatures, m.ev.PaymentID) {
				return tx, true, nil
			}
		}
		if len(sigs) < pageSize {
			return nil, true, nil // the whole history was read
		}
		before = sigs[len(sigs)-1].Signature
	}
	return nil, false, nil
}
