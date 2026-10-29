package app

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/evm"
)

// Agents spend from the owner's vault on Base and Arc through a capped USDC
// allowance, the EVM counterpart of vault_allowance.go (docs/WALLET.md).
//
// The owner's browser signs an EIP-2612 permit that lets Algebra's EVM payer
// spend up to an amount of the vault's USDC. Signing costs the owner nothing;
// the payer submits the permit and pays its gas. USDC refuses any transferFrom
// past what is left of the allowance, so even a compromised Algebra server can
// take no more. A permit for 0 stops it.
//
// A payment from the vault works as on Solana: just before the signed x402
// payment is handed over, the payer pulls exactly its amount from the vault
// with transferFrom, and the payment is released only once that transaction
// is mined and succeeded. Whatever the attempt didn't spend is sent back to
// the vault (Reconcile). A refund returns the USDC but not the allowance.

// PullSourceVaultEVM: a pull from an owner's vault on an EVM chain.
const PullSourceVaultEVM = "owner_vault_evm"

const (
	// permitMaxLifetime bounds a permit's deadline, and permitMinLifetime is
	// how long it must still be usable when it is submitted.
	permitMaxLifetime = 400 * 24 * time.Hour
	permitMinLifetime = 10 * time.Minute
	// evmReceiptWait is how long a pull or permit is waited for before the
	// call returns; the reconciler takes over after that.
	evmReceiptWait = 25 * time.Second
	// evmPullAbandon: a pull transaction the chain never mined in this long is
	// treated as one that never took effect, after its nonce has been used.
	evmPullAbandon = 15 * time.Minute
)

// EVMChain is what the allowance needs from an EVM node.
type EVMChain interface {
	Uint(ctx context.Context, token, signature string, args ...[20]byte) (*big.Int, error)
	Params(ctx context.Context, from, to, data string) (*evm.TxParams, error)
	SendRaw(ctx context.Context, raw string) (string, error)
	TransactionReceipt(ctx context.Context, hash string) (*evm.Receipt, error)
}

// EVMNetwork is one EVM chain where the owner's vault can fund payments.
type EVMNetwork struct {
	Network  string
	RailName string
	RPC      EVMChain
	Payer    *evm.Key
	Token    [20]byte
	Domain   evm.Domain
	// Rail proves what a payment spent, for refunds.
	Rail Rail
}

func (n *EVMNetwork) token() string { return evm.AddressHex(n.Token) }

// EVMAllowanceService runs vault allowances on EVM chains.
type EVMAllowanceService struct {
	store        VaultAllowanceStore
	vaults       OwnerVaultStore
	passes       PassOwners
	pulls        OnchainPassStore
	reservations ReservationReader
	nets         map[string]*EVMNetwork
	byRail       map[string]*EVMNetwork
	maxMinor     int64
	limiter      RateLimiter
	log          *slog.Logger
	now          func() time.Time
	wait         time.Duration
	poll         time.Duration
}

func NewEVMAllowanceService(store VaultAllowanceStore, vaults OwnerVaultStore, passes PassOwners, pulls OnchainPassStore, reservations ReservationReader, nets []*EVMNetwork, maxMinor int64, limiter RateLimiter) *EVMAllowanceService {
	s := &EVMAllowanceService{store: store, vaults: vaults, passes: passes, pulls: pulls, reservations: reservations,
		nets: map[string]*EVMNetwork{}, byRail: map[string]*EVMNetwork{}, maxMinor: maxMinor, limiter: limiter,
		log: slog.Default(), now: time.Now, wait: evmReceiptWait, poll: 2 * time.Second}
	if s.maxMinor <= 0 {
		s.maxMinor = DefaultAllowanceMaxMinor
	}
	for _, n := range nets {
		s.nets[n.Network] = n
		s.byRail[n.RailName] = n
	}
	return s
}

// SetLogger sets where allowance events are logged.
func (s *EVMAllowanceService) SetLogger(l *slog.Logger) { s.log = l }

// Has reports whether network is one of this service's.
func (s *EVMAllowanceService) Has(network string) bool {
	return s != nil && s.nets[chain.NormalizeNetwork(network)] != nil
}

func (s *EVMAllowanceService) owner(ctx context.Context, userID string) ([20]byte, error) {
	v, err := s.vaults.OwnerVault(ctx, userID)
	if errors.Is(err, shared.ErrNotFound) {
		return [20]byte{}, ErrVaultNone
	}
	if err != nil {
		return [20]byte{}, err
	}
	return evm.ParseAddress(strings.ToLower(v.EVMAddress))
}

// Allowances is the account's allowance on each EVM chain, as recorded and as
// the chain says now.
func (s *EVMAllowanceService) Allowances(ctx context.Context, userID string) ([]VaultAllowanceView, error) {
	out := []VaultAllowanceView{}
	owner, err := s.owner(ctx, userID)
	if err != nil {
		return out, err
	}
	for _, network := range []string{chain.BaseSepolia, chain.ArcTestnet, chain.Base, chain.Arc} {
		n := s.nets[network]
		if n == nil {
			continue
		}
		out = append(out, s.view(ctx, userID, n, owner))
	}
	return out, nil
}

func (s *EVMAllowanceService) view(ctx context.Context, userID string, n *EVMNetwork, owner [20]byte) VaultAllowanceView {
	v := VaultAllowanceView{Network: n.Network, Cluster: n.Network, Delegate: n.Payer.Hex(), Mint: n.token(), TokenAccount: evm.AddressHex(owner), MaxMinor: s.maxMinor, HasTokenAccount: true}
	if rec, err := s.store.VaultAllowance(ctx, userID, n.Network); err == nil {
		v.Active, v.ApprovedMinor, v.Signature = rec.State == AllowanceActive, rec.ApprovedMinor, rec.Signature
		t := rec.UpdatedAt
		v.UpdatedAt = &t
	}
	left, err1 := n.RPC.Uint(ctx, n.token(), "allowance(address,address)", owner, n.Payer.Address())
	bal, err2 := n.RPC.Uint(ctx, n.token(), "balanceOf(address)", owner)
	if err1 == nil && err2 == nil {
		v.Read, v.LeftMinor, v.BalanceMinor = true, clampInt64(left), clampInt64(bal)
	}
	return v
}

func clampInt64(v *big.Int) int64 {
	if !v.IsInt64() {
		return 1<<63 - 1
	}
	return v.Int64()
}

// PermitParams is what the browser needs to sign a permit. The amount is the
// owner's to choose; the browser signs only what it built.
type PermitParams struct {
	Network string `json:"network"`
	ChainID int64  `json:"chain_id"`
	Token   string `json:"token"`
	// Name and Version are USDC's EIP-712 domain on the chain.
	Name     string `json:"name"`
	Version  string `json:"version"`
	Owner    string `json:"owner"`
	Spender  string `json:"spender"`
	Nonce    string `json:"nonce"`
	Deadline int64  `json:"deadline"`
	MaxMinor int64  `json:"max_minor"`
}

// Prepare reads the permit's nonce and names the spender.
func (s *EVMAllowanceService) Prepare(ctx context.Context, userID, network string) (*PermitParams, error) {
	n := s.nets[chain.NormalizeNetwork(network)]
	if n == nil {
		return nil, fmt.Errorf("%w: agents can't spend from a vault on %s here", shared.ErrNotFound, network)
	}
	owner, err := s.owner(ctx, userID)
	if err != nil {
		return nil, err
	}
	nonce, err := n.RPC.Uint(ctx, n.token(), "nonces(address)", owner)
	if err != nil {
		return nil, err
	}
	return &PermitParams{
		Network: n.Network, ChainID: n.Domain.ChainID.Int64(), Token: n.token(), Name: n.Domain.Name, Version: n.Domain.Version,
		Owner: evm.AddressHex(owner), Spender: n.Payer.Hex(), Nonce: nonce.String(), Deadline: s.now().Add(365 * 24 * time.Hour).Unix(), MaxMinor: s.maxMinor,
	}, nil
}

// PermitSubmit is the owner's signed permit.
type PermitSubmit struct {
	Network    string `json:"network"`
	ValueMinor int64  `json:"value_minor"`
	Deadline   int64  `json:"deadline"`
	Signature  string `json:"signature"`
}

// ErrPermit: a permit Algebra won't submit.
var ErrPermit = fmt.Errorf("%w: permit", ErrInvalidRequest)

// Submit checks the owner's permit and has the payer submit it, paying the
// gas. A value of 0 stops agents spending from the vault on that chain.
func (s *EVMAllowanceService) Submit(ctx context.Context, userID string, in PermitSubmit) (*AllowanceResult, error) {
	if s.limiter != nil {
		if ok, retry, err := s.limiter.Allow(ctx, "vault:allowance:"+userID, allowanceSubmitPerHour, time.Hour); err == nil && !ok {
			return nil, &RateLimited{Retry: retry}
		}
	}
	n := s.nets[chain.NormalizeNetwork(in.Network)]
	if n == nil {
		return nil, fmt.Errorf("%w: agents can't spend from a vault on %s here", shared.ErrNotFound, in.Network)
	}
	owner, err := s.owner(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	switch {
	case in.ValueMinor < 0 || in.ValueMinor > s.maxMinor:
		return nil, fmt.Errorf("%w: the limit is between 0 and %d", ErrPermit, s.maxMinor)
	case in.Deadline < now.Add(permitMinLifetime).Unix() || in.Deadline > now.Add(permitMaxLifetime).Unix():
		return nil, fmt.Errorf("%w: the permit's deadline is out of range", ErrPermit)
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(in.Signature), "0x"))
	if err != nil || len(sig) != 65 {
		return nil, fmt.Errorf("%w: the signature is 65 bytes of hex", ErrPermit)
	}
	nonce, err := n.RPC.Uint(ctx, n.token(), "nonces(address)", owner)
	if err != nil {
		return nil, err
	}
	p := evm.Permit{Owner: owner, Spender: n.Payer.Address(), Value: big.NewInt(in.ValueMinor), Nonce: nonce, Deadline: big.NewInt(in.Deadline)}
	h, err := p.Hash()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrPermit, err.Error())
	}
	// The permit must be the vault owner's, for this payer, this value and the
	// current nonce: anything else recovers to someone else and is refused.
	if who, err := evm.Recover(evm.TypedDigest(n.Domain, h), sig); err != nil || who != owner {
		return nil, fmt.Errorf("%w: the permit isn't signed by this vault for Algebra's payer", ErrPermit)
	}
	data, err := evm.PermitCalldata(p, sig)
	if err != nil {
		return nil, err
	}
	hash, mined, err := s.send(ctx, n, data)
	if err != nil {
		return nil, err
	}
	state := AllowanceActive
	if in.ValueMinor == 0 {
		state = AllowanceRevoked
	}
	rec := &VaultAllowance{UserID: userID, Network: n.Network, State: state, ApprovedMinor: in.ValueMinor, Delegate: n.Payer.Hex(),
		TokenAccount: evm.AddressHex(owner), Signature: hash, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if prev, err := s.store.VaultAllowance(ctx, userID, n.Network); err == nil {
		rec.CreatedAt = prev.CreatedAt
	}
	if err := s.store.SaveVaultAllowance(ctx, rec); err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "vault allowance: changed", "network", n.Network, "state", state, "approved_minor", in.ValueMinor, "tx", hash, "pending", !mined)
	return &AllowanceResult{Signature: hash, Pending: !mined, Allowance: s.view(ctx, userID, n, owner)}, nil
}

// send has the payer send a transaction to USDC and waits a while for it.
// mined is false when it hadn't been mined by then; a transaction that was
// mined and failed is an error.
func (s *EVMAllowanceService) send(ctx context.Context, n *EVMNetwork, data []byte) (hash string, mined bool, err error) {
	params, err := n.RPC.Params(ctx, n.Payer.Hex(), n.token(), "0x"+hex.EncodeToString(data))
	if err != nil {
		return "", false, fmt.Errorf("evm allowance: preparing the transaction (the payer may need gas on %s): %w", n.Network, err)
	}
	raw, hash, err := n.Payer.SignTx(evm.Tx1559{ChainID: params.ChainID, Nonce: params.Nonce, Tip: params.MaxPriorityFeePerGas, MaxFee: params.MaxFeePerGas, Gas: params.GasLimit, To: n.Token, Data: data})
	if err != nil {
		return "", false, err
	}
	if _, err := n.RPC.SendRaw(ctx, raw); err != nil {
		return "", false, noGas(n, err)
	}
	ok, done, err := s.await(ctx, n, hash)
	if err != nil {
		return hash, false, err
	}
	if done && !ok {
		return hash, true, fmt.Errorf("evm allowance: transaction %s was mined and failed", hash)
	}
	return hash, done, nil
}

// noGas words a refused send: the payer without gas is the usual reason on a
// new chain, and says what to do; anything else keeps the node's words.
func noGas(n *EVMNetwork, err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "insufficient funds") {
		return fmt.Errorf("%w: Algebra's payer %s has no gas on %s yet, so nothing was sent and nothing changed", shared.ErrConflict, n.Payer.Hex(), n.Network)
	}
	return fmt.Errorf("evm allowance: sending: %w", err)
}

// await polls for a receipt until s.wait has passed.
func (s *EVMAllowanceService) await(ctx context.Context, n *EVMNetwork, hash string) (ok, done bool, err error) {
	deadline := s.now().Add(s.wait)
	for {
		rc, err := n.RPC.TransactionReceipt(ctx, hash)
		if err == nil && rc != nil {
			return rc.Status == "0x1", true, nil
		}
		if !s.now().Before(deadline) {
			return false, false, nil
		}
		select {
		case <-ctx.Done():
			return false, false, ctx.Err()
		case <-time.After(s.poll):
		}
	}
}

// --- Paying from the vault ---

func (s *EVMAllowanceService) active(ctx context.Context, passID string, n *EVMNetwork) ([20]byte, bool, error) {
	pass, err := s.passes.Get(ctx, passID)
	if err != nil {
		return [20]byte{}, false, fmt.Errorf("evm allowance: reading the pass: %w", err)
	}
	rec, err := s.store.VaultAllowance(ctx, pass.UserID, n.Network)
	if errors.Is(err, shared.ErrNotFound) {
		return [20]byte{}, false, nil
	}
	if err != nil {
		return [20]byte{}, false, err
	}
	if rec.State != AllowanceActive {
		return [20]byte{}, false, nil
	}
	owner, err := s.owner(ctx, pass.UserID)
	if errors.Is(err, ErrVaultNone) {
		return [20]byte{}, false, nil
	}
	return owner, err == nil, err
}

// Check refuses nothing: the allowance is checked when a payment is funded.
func (s *EVMAllowanceService) Check(context.Context, string) error { return nil }

// Funding says whether a payment on rail comes from the owner's vault.
func (s *EVMAllowanceService) Funding(ctx context.Context, passID, rail string) (bool, error) {
	n := s.byRail[rail]
	if n == nil {
		return false, nil
	}
	_, ok, err := s.active(ctx, passID, n)
	return ok, err
}

// Fund pulls amountMinor from the owner's vault into the payer for one
// attempt and returns once the pull is mined. A vault that can't fund the
// payment is an *AuthorityDenied; a pull not mined in time is an error, and
// the payment isn't handed over.
func (s *EVMAllowanceService) Fund(ctx context.Context, passID string, rv *econ.Reservation, amountMinor int64) (*OnchainPull, error) {
	n := s.byRail[rv.Rail]
	if n == nil || amountMinor <= 0 {
		return nil, nil
	}
	owner, ok, err := s.active(ctx, passID, n)
	if err != nil || !ok {
		return nil, err
	}
	if prior, err := s.pulls.OnchainPull(ctx, rv.ID); err == nil {
		if prior.State == PullPulled && prior.AmountMinor == amountMinor {
			return prior, nil // already funded for this attempt
		}
		return nil, fmt.Errorf("%w: this attempt already has a pull (%s)", shared.ErrConflict, prior.State)
	}
	left, err := n.RPC.Uint(ctx, n.token(), "allowance(address,address)", owner, n.Payer.Address())
	if err != nil {
		return nil, err
	}
	bal, err := n.RPC.Uint(ctx, n.token(), "balanceOf(address)", owner)
	if err != nil {
		return nil, err
	}
	amount := big.NewInt(amountMinor)
	switch {
	case left.Cmp(amount) < 0:
		return nil, &AuthorityDenied{ReasonCodes: []string{DenyVaultAllowanceLow}}
	case bal.Cmp(amount) < 0:
		return nil, &AuthorityDenied{ReasonCodes: []string{DenyVaultUnfunded}}
	}
	data := evm.TransferFromCalldata(owner, n.Payer.Address(), amount)
	params, err := n.RPC.Params(ctx, n.Payer.Hex(), n.token(), "0x"+hex.EncodeToString(data))
	if err != nil {
		return nil, fmt.Errorf("evm allowance: preparing the pull (the payer may need gas on %s): %w", n.Network, err)
	}
	raw, hash, err := n.Payer.SignTx(evm.Tx1559{ChainID: params.ChainID, Nonce: params.Nonce, Tip: params.MaxPriorityFeePerGas, MaxFee: params.MaxFeePerGas, Gas: params.GasLimit, To: n.Token, Data: data})
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	p := &OnchainPull{ReservationID: rv.ID, Source: PullSourceVaultEVM, IntentID: rv.IntentID, PassID: passID, Network: n.Network,
		PassAddress: evm.AddressHex(owner), OwnerWallet: evm.AddressHex(owner), AmountMinor: amountMinor, PullSignature: hash,
		State: PullSending, CreatedAt: now, UpdatedAt: now}
	// Recorded before it is sent, so a pull that lands while this call fails is
	// still found and refunded.
	if err := s.pulls.InsertOnchainPull(ctx, p); err != nil {
		return nil, err
	}
	if _, err := n.RPC.SendRaw(ctx, raw); err != nil {
		s.log.WarnContext(ctx, "evm allowance: sending the pull failed; the reconciler will settle it", "tx", hash, "error", err)
		return nil, noGas(n, err)
	}
	ok, done, err := s.await(ctx, n, hash)
	if err != nil || !done {
		return nil, fmt.Errorf("evm allowance: the pull %s isn't confirmed yet; the payment wasn't sent", hash)
	}
	if !ok {
		p.State, p.Detail, p.UpdatedAt = PullVoid, "the pull was mined and failed", s.now().UTC()
		_ = s.pulls.UpdateOnchainPull(ctx, p, PullSending)
		return nil, &AuthorityDenied{ReasonCodes: []string{DenyVaultAllowanceLow}}
	}
	p.State, p.UpdatedAt = PullPulled, s.now().UTC()
	if err := s.pulls.UpdateOnchainPull(ctx, p, PullSending); err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "evm allowance: pulled", "network", n.Network, "from", p.OwnerWallet, "amount_minor", amountMinor, "tx", hash, "reservation_id", rv.ID)
	return p, nil
}

// Reconcile settles open pulls and refunds what their attempts didn't spend.
func (s *EVMAllowanceService) Reconcile(ctx context.Context) (int, error) {
	open, err := s.pulls.OpenOnchainPulls(ctx, PullSourceVaultEVM, 50)
	if err != nil {
		return 0, err
	}
	changed := 0
	var errs []error
	for i := range open {
		p := &open[i]
		n := s.nets[p.Network]
		if n == nil {
			continue
		}
		before := p.State
		if err := s.step(ctx, n, p); err != nil {
			errs = append(errs, fmt.Errorf("pull %s: %w", p.PullSignature, err))
		}
		if p.State != before {
			changed++
		}
	}
	return changed, errors.Join(errs...)
}

func (s *EVMAllowanceService) step(ctx context.Context, n *EVMNetwork, p *OnchainPull) error {
	from := p.State
	save := func() error {
		p.UpdatedAt = s.now().UTC()
		err := s.pulls.UpdateOnchainPull(ctx, p, from)
		if err == nil {
			from = p.State
		}
		return err
	}
	switch p.State {
	case PullSending:
		rc, err := n.RPC.TransactionReceipt(ctx, p.PullSignature)
		if err != nil {
			return err
		}
		switch {
		case rc != nil && rc.Status == "0x1":
			p.State = PullPulled
		case rc != nil:
			p.State, p.Detail = PullVoid, "the pull was mined and failed"
		case s.now().Sub(p.CreatedAt) > evmPullAbandon:
			// Unmined for this long: the payer's nonce has moved on (every later
			// payer transaction would have replaced it), so it never will be.
			p.State, p.Detail = PullVoid, "the pull was never mined"
		default:
			return nil
		}
		return save()
	case PullPulled:
		used, final, err := s.used(ctx, n, p)
		if err != nil || !final {
			return err
		}
		p.UsedMinor = &used
		refund := p.AmountMinor - used
		p.RefundMinor = &refund
		if refund <= 0 {
			zero := int64(0)
			p.RefundMinor, p.State = &zero, PullSettled
			return save()
		}
		owner, err := evm.ParseAddress(p.OwnerWallet)
		if err != nil {
			return err
		}
		data := transferCalldata(owner, big.NewInt(refund))
		params, err := n.RPC.Params(ctx, n.Payer.Hex(), n.token(), "0x"+hex.EncodeToString(data))
		if err != nil {
			return err
		}
		raw, hash, err := n.Payer.SignTx(evm.Tx1559{ChainID: params.ChainID, Nonce: params.Nonce, Tip: params.MaxPriorityFeePerGas, MaxFee: params.MaxFeePerGas, Gas: params.GasLimit, To: n.Token, Data: data})
		if err != nil {
			return err
		}
		p.State, p.RefundSignature = PullRefunding, hash
		if err := save(); err != nil {
			return err
		}
		_, err = n.RPC.SendRaw(ctx, raw)
		return err
	case PullRefunding:
		rc, err := n.RPC.TransactionReceipt(ctx, p.RefundSignature)
		if err != nil {
			return err
		}
		switch {
		case rc != nil && rc.Status == "0x1":
			p.State = PullRefunded
		case rc != nil || s.now().Sub(p.UpdatedAt) > evmPullAbandon:
			p.State, p.RefundSignature, p.Detail = PullPulled, "", "refund attempt failed; retrying"
		default:
			return nil
		}
		return save()
	}
	return nil
}

// used is what the attempt behind a pull spent, once that is final.
func (s *EVMAllowanceService) used(ctx context.Context, n *EVMNetwork, p *OnchainPull) (int64, bool, error) {
	rs, err := s.reservations.Reservations(ctx, p.IntentID)
	if err != nil {
		return 0, false, err
	}
	var r *econ.Reservation
	for i := range rs {
		if rs[i].ID == p.ReservationID {
			r = &rs[i]
		}
	}
	if r == nil {
		return 0, false, fmt.Errorf("reservation %s not found", p.ReservationID)
	}
	if !r.Evidence.AuthorityIssued {
		if r.State.Live() {
			return 0, false, nil
		}
		return 0, true, nil // the payment was never handed over
	}
	if r.State.Live() && r.State != econ.ReservationUnknown && r.State != econ.ReservationReconciling {
		return 0, false, nil
	}
	if n.Rail == nil {
		return 0, false, nil
	}
	st, err := n.Rail.Settlement(ctx, r.Evidence)
	if err != nil {
		return 0, false, err
	}
	switch st.Status {
	case SettlementSettled:
		return min(st.AmountMinor, p.AmountMinor), true, nil
	case SettlementNotSettled:
		return 0, true, nil
	}
	return 0, false, nil
}

// transferCalldata is transfer(to, value).
func transferCalldata(to [20]byte, value *big.Int) []byte {
	out := evm.Keccak256([]byte("transfer(address,uint256)"))[:4]
	out = append(out, evm.AddressWord(to)...)
	w, _ := evm.Word(value)
	return append(out, w...)
}

// PullFor is the pull that funded an attempt, for its receipt.
func (s *EVMAllowanceService) PullFor(ctx context.Context, reservationID string) (*OnchainPull, error) {
	p, err := s.pulls.OnchainPull(ctx, reservationID)
	if err != nil || p.Source != PullSourceVaultEVM {
		return nil, shared.ErrNotFound
	}
	return p, nil
}

var _ PassFunder = (*EVMAllowanceService)(nil)
