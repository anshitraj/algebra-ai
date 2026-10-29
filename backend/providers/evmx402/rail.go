// Package evmx402 is the payment rail for x402's "exact" scheme on EVM chains
// where USDC supports EIP-3009 (Base, Arc): it signs USDC payments from a
// wallet Algebra controls and proves, from chain state alone, whether a
// payment settled.
//
// What it signs. For one attempt it signs a transferWithAuthorization: an
// EIP-712 message in which the payer lets anyone move exactly the price to the
// seller, once (a random 32-byte nonce), until a deadline. The payer sends no
// transaction and needs no gas: the seller, or its facilitator, submits the
// authorization and pays the gas. Nobody can change the amount, the payee or
// the token, and a second use of the nonce fails on chain.
//
// What it refuses to sign:
//
//   - anything but USDC at Circle's real address on the configured chain;
//   - more than the attempt's hold, or more than MaxPaymentMinor;
//   - a payee that is the payer;
//   - an authorization valid for longer than ten minutes.
//
// How it proves settlement. USDC records every used authorization
// (authorizationState(payer, nonce)), so the nonce is the payment's identity.
// Used means paid. Unused proves nothing until the deadline has passed at a
// finalized block: only then can the payment never land, and only then is it
// NOT_SETTLED.
package evmx402

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/platform/evm"
	"github.com/project-algebra/algebra/providers/x402"
)

// ErrWrongChain: the RPC node serves a different chain than the rail is for.
var ErrWrongChain = errors.New("evmx402: the RPC node serves a different chain than the rail is configured for")

// DefaultMaxPaymentMinor is one USDC per payment, as on Solana.
const DefaultMaxPaymentMinor = 1_000_000

const (
	// maxValidity bounds how long a signed authorization stays usable, and
	// defaultValidity is used when the seller asks for no particular time.
	maxValidity     = 10 * time.Minute
	minValidity     = 30 * time.Second
	defaultValidity = 5 * time.Minute
	// clockSlack backdates validAfter so a seller whose clock runs behind
	// can still use the authorization at once.
	clockSlack = time.Minute
	// logLookback is how many blocks before the authorization's own block
	// the settlement search starts from, for clocks and reorgs.
	logLookback = 20
)

// network is what the rail knows about each chain it can pay on.
type network struct {
	chainID int64
	caip2   string
	// name and version are USDC's EIP-712 domain on that chain, read from the
	// contract (name(), version()).
	name, version string
}

var networks = map[string]network{
	chain.Base:        {chainID: 8453, caip2: "eip155:8453", name: "USD Coin", version: "2"},
	chain.BaseSepolia: {chainID: 84532, caip2: "eip155:84532", name: "USDC", version: "2"},
	chain.Arc:         {chainID: 5042, caip2: "eip155:5042", name: "USDC", version: "2"},
	chain.ArcTestnet:  {chainID: 5042002, caip2: "eip155:5042002", name: "USDC", version: "2"},
}

// Networks are the chains this rail can be configured for.
func Networks() []string { return []string{chain.BaseSepolia, chain.ArcTestnet, chain.Base, chain.Arc} }

// RailName is how reservations name the rail for a network.
func RailName(network string) string { return "x402-" + chain.NormalizeNetwork(network) }

// Config configures a Rail.
type Config struct {
	// Network is one of Networks().
	Network string
	RPC     *evm.RPC
	Key     *evm.Key
	// MaxPaymentMinor is the most one payment may be, in micro-USDC. Zero
	// means DefaultMaxPaymentMinor.
	MaxPaymentMinor int64
	// Commitment is the block tag a used authorization must be visible at to
	// count as settled. Empty means "latest".
	Commitment string
	Now        func() time.Time
}

// Rail is app.Rail and app.PaymentAuthorizer for USDC over x402 on one EVM chain.
type Rail struct {
	cfg     Config
	network string
	net     network
	usdc    [20]byte
	now     func() time.Time
}

var (
	_ app.Rail              = (*Rail)(nil)
	_ app.PaymentAuthorizer = (*Rail)(nil)
)

// New builds a Rail. It checks configuration only; Status checks the chain.
func New(cfg Config) (*Rail, error) {
	if cfg.RPC == nil || cfg.Key == nil {
		return nil, errors.New("evmx402: an RPC client and a key are required")
	}
	name := chain.NormalizeNetwork(cfg.Network)
	n, ok := networks[name]
	if !ok {
		return nil, fmt.Errorf("evmx402: %q isn't a chain this rail pays on", cfg.Network)
	}
	addr, ok := chain.AssetAddress(name, "USDC")
	if !ok {
		return nil, fmt.Errorf("evmx402: no known USDC on %s", name)
	}
	usdc, err := evm.ParseAddress(strings.ToLower(addr))
	if err != nil {
		return nil, err
	}
	if cfg.MaxPaymentMinor <= 0 {
		cfg.MaxPaymentMinor = DefaultMaxPaymentMinor
	}
	if cfg.Commitment == "" {
		cfg.Commitment = "latest"
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Rail{cfg: cfg, network: name, net: n, usdc: usdc, now: now}, nil
}

func (r *Rail) Name() string { return RailName(r.network) }

// Network is the canonical network this rail pays on.
func (r *Rail) Network() string { return r.network }

// Address is the wallet payments are made from.
func (r *Rail) Address() string { return r.cfg.Key.Hex() }

// Key is the payer's key, for the vault allowance's pulls and refunds.
func (r *Rail) Key() *evm.Key { return r.cfg.Key }

// RPC is the rail's node.
func (r *Rail) RPC() *evm.RPC { return r.cfg.RPC }

// Token is USDC's address on the rail's chain.
func (r *Rail) Token() [20]byte { return r.usdc }

// USDC is the token's address on the rail's chain.
func (r *Rail) USDC() string { return evm.AddressHex(r.usdc) }

// Domain is USDC's EIP-712 domain on the rail's chain.
func (r *Rail) Domain() evm.Domain {
	return evm.Domain{Name: r.net.name, Version: r.net.version, ChainID: big.NewInt(r.net.chainID), VerifyingContract: r.usdc}
}

// WalletStatus is what an operator needs to see before trusting the rail.
type WalletStatus struct {
	Network         string `json:"network"`
	Address         string `json:"address"`
	USDCMinor       int64  `json:"usdc_minor"`
	NativeWei       string `json:"native_wei"`
	MaxPaymentMinor int64  `json:"max_payment_minor"`
}

// Status checks the node serves the right chain and reads the wallet.
func (r *Rail) Status(ctx context.Context) (*WalletStatus, error) {
	id, err := r.cfg.RPC.ChainID(ctx)
	if err != nil {
		return nil, err
	}
	if id.Int64() != r.net.chainID {
		return nil, fmt.Errorf("%w: it serves chain %s, the rail is for %s (%d)", ErrWrongChain, id, r.network, r.net.chainID)
	}
	bal, err := r.cfg.RPC.Uint(ctx, r.USDC(), "balanceOf(address)", r.cfg.Key.Address())
	if err != nil {
		return nil, err
	}
	native, err := r.cfg.RPC.NativeBalance(ctx, r.Address())
	if err != nil {
		return nil, err
	}
	return &WalletStatus{Network: r.network, Address: r.Address(), USDCMinor: bal.Int64(), NativeWei: native.String(), MaxPaymentMinor: r.cfg.MaxPaymentMinor}, nil
}

// networkNames are the names a seller may use for the chain: x402 v1's short
// name and v2's CAIP-2 id.
func (r *Rail) networkNames() []string { return []string{r.network, r.net.caip2} }

// Authorize signs one payment for an executing attempt. It sends nothing: the
// seller submits the authorization when it settles.
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

// DryRun builds a payment as Authorize does but reports, instead of refusing,
// what the wallet can't support, so an operator can see what would be paid.
// The result must be discarded, never sent.
func (r *Rail) DryRun(ctx context.Context, rv *econ.Reservation, req app.PaymentRequest) (*app.PaymentAuthority, []string, error) {
	return r.build(ctx, rv, req)
}

// authorizationPayload is the x402 "exact" EVM payload.
type authorizationPayload struct {
	Signature     string               `json:"signature"`
	Authorization authorizationMessage `json:"authorization"`
}

type authorizationMessage struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"`
}

func (r *Rail) build(ctx context.Context, rv *econ.Reservation, req app.PaymentRequest) (*app.PaymentAuthority, []string, error) {
	sel, err := x402.SelectRaw(req.Requirements, "exact", r.networkNames()...)
	if err != nil {
		return nil, nil, fmt.Errorf("evmx402: %w", err)
	}
	q := sel.Requirements
	amount, err := q.AmountMinor()
	if err != nil {
		return nil, nil, err
	}
	switch {
	case !chain.SameAddress(r.network, q.Asset, r.USDC()):
		return nil, nil, fmt.Errorf("evmx402: the provider asks for asset %q, which is not Circle's USDC on %s", q.Asset, r.network)
	case amount > r.cfg.MaxPaymentMinor:
		return nil, nil, fmt.Errorf("evmx402: %d exceeds this rail's hard ceiling of %d per payment", amount, r.cfg.MaxPaymentMinor)
	case rv != nil && amount > rv.HoldMinor:
		return nil, nil, fmt.Errorf("evmx402: the provider asks for %d, more than this attempt's hold of %d", amount, rv.HoldMinor)
	}
	payTo, err := evm.ParseAddress(strings.ToLower(q.PayTo))
	if err != nil {
		return nil, nil, fmt.Errorf("evmx402: payee: %w", err)
	}
	if payTo == r.cfg.Key.Address() {
		return nil, nil, errors.New("evmx402: the provider's payee is this wallet; refusing")
	}
	if payTo == ([20]byte{}) {
		return nil, nil, errors.New("evmx402: the provider's payee is the zero address; refusing")
	}

	var problems []string
	bal, err := r.cfg.RPC.Uint(ctx, r.USDC(), "balanceOf(address)", r.cfg.Key.Address())
	if err != nil {
		return nil, nil, err
	}
	if bal.Cmp(big.NewInt(amount)) < 0 && !req.FundedByPass {
		problems = append(problems, fmt.Sprintf("evmx402: insufficient USDC: the wallet %s holds %s on %s, the payment is %d", r.Address(), bal, r.network, amount))
	}
	head, err := r.cfg.RPC.BlockByTag(ctx, "latest")
	if err != nil {
		return nil, nil, err
	}

	validity := defaultValidity
	if q.MaxTimeoutSeconds > 0 {
		validity = time.Duration(q.MaxTimeoutSeconds) * time.Second
	}
	validity = min(max(validity, minValidity), maxValidity)
	now := r.now()
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, nil, err
	}
	auth := evm.TransferAuthorization{
		From: r.cfg.Key.Address(), To: payTo, Value: big.NewInt(amount),
		ValidAfter: big.NewInt(now.Add(-clockSlack).Unix()), ValidBefore: big.NewInt(now.Add(validity).Unix()), Nonce: nonce,
	}
	structHash, err := auth.Hash()
	if err != nil {
		return nil, nil, err
	}
	sig, err := r.cfg.Key.Sign(evm.TypedDigest(r.Domain(), structHash))
	if err != nil {
		return nil, nil, err
	}
	nonceHex := "0x" + hex.EncodeToString(nonce[:])
	payload, _ := json.Marshal(authorizationPayload{
		Signature: "0x" + hex.EncodeToString(sig),
		Authorization: authorizationMessage{
			From: r.Address(), To: evm.AddressHex(payTo), Value: strconv.FormatInt(amount, 10),
			ValidAfter: auth.ValidAfter.String(), ValidBefore: auth.ValidBefore.String(), Nonce: nonceHex,
		},
	})
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
			Rail: r.Name(), Protocol: "x402", Scheme: "exact", Network: r.network, Asset: r.USDC(),
			PaymentID: nonceHex, AmountMinor: amount, Payer: r.Address(), PayTo: evm.AddressHex(payTo),
			ValidBefore: auth.ValidBefore.Int64(), FromBlock: head.Number, Test: chain.IsTestNetwork(r.network),
		},
	}, problems, nil
}

// Settlement answers from the chain, never from what a seller or an agent says.
func (r *Rail) Settlement(ctx context.Context, ev econ.Evidence) (app.Settlement, error) {
	base := app.Settlement{Network: ev.Network, Asset: ev.Asset, Payer: ev.Payer, PayTo: ev.PayTo, Test: ev.Test}
	status := func(s app.SettlementStatus, detail string) (app.Settlement, error) {
		base.Status, base.Detail = s, detail
		return base, nil
	}
	nonce, err := evm.Hex32(ev.PaymentID)
	if err != nil || !chain.SameAddress(r.network, ev.Payer, r.Address()) || !chain.SameAddress(r.network, ev.Asset, r.USDC()) || ev.PayTo == "" {
		return status(app.SettlementUnknown, "the evidence is not for a payment made by this wallet")
	}
	used, err := r.cfg.RPC.AuthorizationState(ctx, r.USDC(), r.cfg.Key.Address(), nonce, r.cfg.Commitment)
	if err != nil {
		return base, err
	}
	if used {
		return r.settled(ctx, base, ev, nonce)
	}
	// Unused. That proves nothing until the deadline has passed at a block
	// that can't be undone: block times only go forward, so no later block can
	// include it.
	if ev.ValidBefore == 0 {
		return status(app.SettlementUnknown, "the authorization's deadline isn't recorded, so its absence can't be proven")
	}
	fin, err := r.cfg.RPC.BlockByTag(ctx, "finalized")
	if err != nil {
		return base, err
	}
	if int64(fin.Timestamp) < ev.ValidBefore {
		return status(app.SettlementPending, fmt.Sprintf("not used yet; it can still be used until %s (finalized block time is %s)",
			time.Unix(ev.ValidBefore, 0).UTC().Format(time.RFC3339), time.Unix(int64(fin.Timestamp), 0).UTC().Format(time.RFC3339)))
	}
	usedFinal, err := r.cfg.RPC.AuthorizationState(ctx, r.USDC(), r.cfg.Key.Address(), nonce, "finalized")
	if err != nil {
		return base, err
	}
	if usedFinal {
		return r.settled(ctx, base, ev, nonce)
	}
	return status(app.SettlementNotSettled, "the authorization's deadline passed at a finalized block and it was never used: it can never be")
}

// settled finds the transaction that used the authorization, for the
// receipt. The authorization being used is the proof; the transaction is the
// pointer to it.
func (r *Rail) settled(ctx context.Context, base app.Settlement, ev econ.Evidence, nonce [32]byte) (app.Settlement, error) {
	base.Status, base.AmountMinor, base.Detail = app.SettlementSettled, ev.AmountMinor, "the authorization was used on chain"
	from := "earliest"
	if ev.FromBlock > logLookback {
		from = evm.BlockTag(ev.FromBlock - logLookback)
	}
	logs, err := r.cfg.RPC.Logs(ctx, r.USDC(), []any{evm.AuthorizationUsedTopic, "0x" + hex.EncodeToString(evm.AddressWord(r.cfg.Key.Address())), "0x" + hex.EncodeToString(nonce[:])}, from, "latest")
	if err != nil || len(logs) == 0 {
		// The node limits log ranges, or is behind: the state read stands.
		return base, nil
	}
	base.Transaction = logs[0].TxHash
	return base, nil
}
