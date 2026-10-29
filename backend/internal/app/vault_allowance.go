package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/spendpass"
)

// Agents spend from the owner's vault through a capped allowance
// (docs/WALLET.md).
//
// The owner's browser signs an SPL Token ApproveChecked that names Algebra's
// payer as the delegate of the vault's USDC account, for an amount the owner
// chose. The token program refuses any transfer past what is left of that
// amount, so even a compromised Algebra server can take no more. Spend Passes
// and Job Passes are budgets inside it, kept by Algebra's reservations; they
// are not separate funds on chain.
//
// A payment from the vault works as one from an on-chain Spend Pass does: just
// before the signed x402 payment is handed over, the payer pulls exactly its
// amount from the vault into its own USDC account, as the delegate, and the
// payment is released only once that pull is confirmed. Whatever the attempt
// didn't spend goes back to the vault (Reconcile). A refund returns the USDC
// but not the allowance: the token program counts the allowance down on every
// transfer, and only the owner can raise it again.
//
// Algebra pays the network fee of the owner's approve and revoke, so a vault
// that holds only USDC can use them. The payer signs, as fee payer, only a
// transaction it has checked holds nothing but that approve or revoke
// (checkAllowanceTx).

// Allowance states (migrations/0030_vault_allowances.sql).
const (
	AllowanceActive  = "ACTIVE"
	AllowanceRevoked = "REVOKED"
)

// Denial codes for a payment the vault can't fund.
const (
	// DenyVaultAllowanceLow: what is left of the allowance on chain is less
	// than the payment (it was used up, revoked outside Algebra, or given to
	// another delegate).
	DenyVaultAllowanceLow = "VAULT_ALLOWANCE_LOW"
	// DenyVaultUnfunded: the vault holds less USDC than the payment.
	DenyVaultUnfunded = "VAULT_UNFUNDED"
)

// DefaultAllowanceMaxMinor is the largest allowance accepted: a ceiling
// against a typo, not a limit on the owner.
const DefaultAllowanceMaxMinor int64 = 1_000_000_000 // 1,000 USDC

// The compute an approve or revoke may ask for, and the most it may pay per
// unit: together at most 0.00002 SOL of priority fee on Algebra's payer.
const (
	allowanceComputeUnits  = 20_000
	maxAllowanceUnitPrice  = 1_000_000
	allowanceSubmitPerHour = 20
)

// VaultAllowance is the owner's recorded choice on one network.
type VaultAllowance struct {
	UserID        string    `json:"-"`
	Network       string    `json:"network"`
	State         string    `json:"state"`
	ApprovedMinor int64     `json:"approved_minor"`
	Delegate      string    `json:"delegate"`
	TokenAccount  string    `json:"token_account"`
	Signature     string    `json:"signature"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// VaultAllowanceStore keeps one allowance per account and network.
type VaultAllowanceStore interface {
	VaultAllowance(ctx context.Context, userID, network string) (*VaultAllowance, error) // shared.ErrNotFound if none
	SaveVaultAllowance(ctx context.Context, a *VaultAllowance) error
}

// VaultAllowanceView is an allowance as the console shows it: the owner's
// choice, and what the chain says now.
type VaultAllowanceView struct {
	Network string `json:"network"`
	Cluster string `json:"cluster"`
	// Delegate is Algebra's payer: the one key the allowance lets spend.
	Delegate     string `json:"delegate"`
	Mint         string `json:"mint"`
	TokenAccount string `json:"token_account"`
	// Active: payments on this network come from the vault.
	Active        bool  `json:"active"`
	ApprovedMinor int64 `json:"approved_minor"`
	// LeftMinor is what the token program will still let the delegate move,
	// and BalanceMinor what the vault holds; both read from the chain when Read.
	LeftMinor       int64 `json:"left_minor"`
	BalanceMinor    int64 `json:"balance_minor"`
	Read            bool  `json:"read"`
	HasTokenAccount bool  `json:"has_token_account"`
	// OtherDelegate: the vault's USDC account names another delegate, approved
	// outside Algebra. Approving here replaces it.
	OtherDelegate string     `json:"other_delegate,omitempty"`
	Signature     string     `json:"signature,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	MaxMinor      int64      `json:"max_minor"`
}

// AllowanceParams is what the browser needs to build an approve or revoke.
// Nothing in it says how much: the owner decides that, and signs only what
// the browser built.
type AllowanceParams struct {
	Network              string `json:"network"`
	FeePayer             string `json:"fee_payer"`
	Delegate             string `json:"delegate"`
	Mint                 string `json:"mint"`
	Decimals             uint8  `json:"decimals"`
	TokenAccount         string `json:"token_account"`
	Blockhash            string `json:"blockhash"`
	ComputeUnitLimit     uint32 `json:"compute_unit_limit"`
	ComputeUnitPrice     uint64 `json:"compute_unit_price"`
	MaxMinor             int64  `json:"max_minor"`
	LastValidBlockHeight uint64 `json:"last_valid_block_height"`
}

// AllowanceResult is what became of an approve or revoke.
type AllowanceResult struct {
	Signature string `json:"signature"`
	// Pending: sent, but not confirmed in time; the allowance shows it once it lands.
	Pending   bool               `json:"pending"`
	Allowance VaultAllowanceView `json:"allowance"`
}

// VaultAllowanceService runs allowances and funds payments from them.
type VaultAllowanceService struct {
	store  VaultAllowanceStore
	vaults OwnerVaultStore
	passes PassOwners
	// book makes, settles and refunds the pulls, as it does an on-chain
	// pass's, with no program.
	book     *OnchainPassService
	nets     map[string]*OnchainNetwork
	byRail   map[string]*OnchainNetwork
	maxMinor int64
	limiter  RateLimiter
	log      *slog.Logger
	now      func() time.Time
}

// NewVaultAllowanceService runs allowances on the given networks, whose
// payer is the delegate and the fee payer of approves and revokes. Pulls are
// kept in pulls next to on-chain passes' own.
func NewVaultAllowanceService(store VaultAllowanceStore, vaults OwnerVaultStore, passes PassOwners, pulls OnchainPassStore, reservations ReservationReader, nets []*OnchainNetwork, maxMinor int64, limiter RateLimiter) *VaultAllowanceService {
	if maxMinor <= 0 {
		maxMinor = DefaultAllowanceMaxMinor
	}
	if limiter == nil {
		limiter = newWindowCounter(time.Now)
	}
	book := NewOnchainPassService(pulls, nil, nil, reservations, nets)
	book.source = PullSourceVault
	s := &VaultAllowanceService{store: store, vaults: vaults, passes: passes, book: book,
		nets: map[string]*OnchainNetwork{}, byRail: map[string]*OnchainNetwork{},
		maxMinor: maxMinor, limiter: limiter, log: slog.Default(), now: time.Now}
	for _, n := range nets {
		s.nets[n.Network], s.byRail[n.RailName] = n, n
	}
	return s
}

func (s *VaultAllowanceService) network(network string) (*OnchainNetwork, error) {
	if s != nil {
		if n := s.nets[chain.NormalizeNetwork(network)]; n != nil {
			return n, nil
		}
	}
	return nil, fmt.Errorf("%w: agents can't spend from the vault on %q here", ErrInvalidRequest, network)
}

func (s *VaultAllowanceService) owner(ctx context.Context, userID string) (solana.PublicKey, error) {
	v, err := s.vaults.OwnerVault(ctx, userID)
	if errors.Is(err, shared.ErrNotFound) {
		return solana.PublicKey{}, ErrVaultNone
	}
	if err != nil {
		return solana.PublicKey{}, err
	}
	return solana.ParsePublicKey(v.SolanaAddress)
}

func tokenAccountOf(n *OnchainNetwork, owner solana.PublicKey) solana.PublicKey {
	a, _ := solana.AssociatedTokenAddress(owner, n.Mint, solana.TokenProgram)
	return a
}

// read reads the vault's USDC account: nil when it has none yet.
func (s *VaultAllowanceService) read(ctx context.Context, n *OnchainNetwork, owner solana.PublicKey) (*solana.TokenAccount, error) {
	acct, err := n.RPC.GetAccountInfo(ctx, tokenAccountOf(n, owner), solana.Confirmed)
	if err != nil || acct == nil {
		return nil, err
	}
	if acct.Owner != solana.TokenProgram {
		return nil, errors.New("vault allowance: the vault's USDC address isn't a token account")
	}
	return solana.ParseTokenAccount(acct.Data)
}

// Allowances lists the account's allowance on every network that has them.
func (s *VaultAllowanceService) Allowances(ctx context.Context, userID string) ([]VaultAllowanceView, error) {
	owner, err := s.owner(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(s.nets))
	for id := range s.nets {
		ids = append(ids, id)
	}
	slices.Sort(ids) // solana, solana-devnet
	slices.Reverse(ids)
	out := make([]VaultAllowanceView, 0, len(ids))
	for _, id := range ids {
		v, err := s.view(ctx, userID, s.nets[id], owner)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *VaultAllowanceService) view(ctx context.Context, userID string, n *OnchainNetwork, owner solana.PublicKey) (VaultAllowanceView, error) {
	payer := n.Payer.PublicKey()
	v := VaultAllowanceView{Network: n.Network, Cluster: n.Cluster, Delegate: payer.String(), Mint: n.Mint.String(),
		TokenAccount: tokenAccountOf(n, owner).String(), MaxMinor: s.maxMinor}
	rec, err := s.store.VaultAllowance(ctx, userID, n.Network)
	switch {
	case err == nil:
		v.Active, v.ApprovedMinor, v.Signature = rec.State == AllowanceActive, rec.ApprovedMinor, rec.Signature
		at := rec.UpdatedAt
		v.UpdatedAt = &at
	case !errors.Is(err, shared.ErrNotFound):
		return v, err
	}
	c, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ta, err := s.read(c, n, owner)
	if err != nil {
		return v, nil // unread: the console says it couldn't tell
	}
	v.Read = true
	if ta == nil {
		return v, nil
	}
	v.HasTokenAccount, v.BalanceMinor = true, int64(ta.Amount)
	switch {
	case ta.Delegate != nil && *ta.Delegate == payer:
		v.LeftMinor = int64(ta.DelegatedAmount)
	case ta.Delegate != nil:
		v.OtherDelegate = ta.Delegate.String()
	}
	return v, nil
}

// Prepare gives the browser what an approve or revoke on network needs.
func (s *VaultAllowanceService) Prepare(ctx context.Context, userID, network string) (*AllowanceParams, error) {
	n, err := s.network(network)
	if err != nil {
		return nil, err
	}
	owner, err := s.owner(ctx, userID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ta, err := s.read(ctx, n, owner)
	if err != nil {
		return nil, err
	}
	if ta == nil {
		return nil, fmt.Errorf("%w: add USDC to the vault on Solana first: the allowance is set on its USDC account", ErrInvalidRequest)
	}
	bh, err := n.RPC.GetLatestBlockhash(ctx, solana.Confirmed)
	if err != nil {
		return nil, err
	}
	payer := n.Payer.PublicKey().String()
	return &AllowanceParams{Network: n.Network, FeePayer: payer, Delegate: payer, Mint: n.Mint.String(), Decimals: chain.USDCDecimals,
		TokenAccount: tokenAccountOf(n, owner).String(), Blockhash: solana.PublicKey(bh.Blockhash).String(),
		ComputeUnitLimit: allowanceComputeUnits, ComputeUnitPrice: min(n.PriorityMicroLamports, maxAllowanceUnitPrice),
		MaxMinor: s.maxMinor, LastValidBlockHeight: bh.LastValidBlockHeight}, nil
}

// ErrAllowanceTx: the transaction isn't an approve or revoke this service signs.
var ErrAllowanceTx = fmt.Errorf("%w: that transaction isn't an allowance Algebra pays the fee of", ErrInvalidRequest)

// checkAllowanceTx says what a transaction the owner signed would do, and
// refuses it unless it is exactly one approve to payer, or one revoke, on the
// vault's USDC account, with at most a bounded compute budget beside it. The
// payer's signature on it then pays the fee and nothing else: the payer is
// named only as the fee payer and as the delegate, which no instruction
// treats as a signer.
func checkAllowanceTx(tx *solana.Transaction, payer, owner, account, mint solana.PublicKey, maxMinor int64) (revoke bool, amount uint64, err error) {
	m := &tx.Message
	if m.NumRequiredSignatures != 2 || len(m.AccountKeys) < 2 || m.AccountKeys[0] != payer || m.AccountKeys[1] != owner {
		return false, 0, fmt.Errorf("%w: Algebra's payer must pay the fee and the vault must be the only other signer", ErrAllowanceTx)
	}
	var token, limit, price int
	for _, ci := range m.Instructions {
		accts := m.InstructionAccounts(ci)
		switch m.ProgramOf(ci) {
		case solana.ComputeBudgetProgram:
			switch {
			case len(accts) == 0 && len(ci.Data) == 5 && ci.Data[0] == 2 && binary.LittleEndian.Uint32(ci.Data[1:]) <= allowanceComputeUnits:
				limit++
			case len(accts) == 0 && len(ci.Data) == 9 && ci.Data[0] == 3 && binary.LittleEndian.Uint64(ci.Data[1:]) <= maxAllowanceUnitPrice:
				price++
			default:
				return false, 0, fmt.Errorf("%w: its compute budget is out of bounds", ErrAllowanceTx)
			}
		case solana.TokenProgram:
			token++
			switch {
			case len(ci.Data) == 10 && ci.Data[0] == 13 && slices.Equal(accts, []solana.PublicKey{account, mint, payer, owner}):
				if ci.Data[9] != chain.USDCDecimals {
					return false, 0, fmt.Errorf("%w: wrong decimals", ErrAllowanceTx)
				}
				amount = binary.LittleEndian.Uint64(ci.Data[1:9])
				if amount == 0 || amount > uint64(maxMinor) {
					return false, 0, fmt.Errorf("%w: the allowance must be more than zero and at most %s USDC", ErrInvalidRequest, chain.FormatUnits(maxMinor, chain.USDCDecimals))
				}
			case len(ci.Data) == 1 && ci.Data[0] == 5 && slices.Equal(accts, []solana.PublicKey{account, owner}):
				revoke = true
			default:
				return false, 0, fmt.Errorf("%w: only an approve to Algebra's payer, or a revoke, on the vault's USDC account", ErrAllowanceTx)
			}
		default:
			return false, 0, fmt.Errorf("%w: it calls a program other than the token program", ErrAllowanceTx)
		}
	}
	if token != 1 || limit > 1 || price > 1 {
		return false, 0, fmt.Errorf("%w: it must hold exactly one approve or revoke", ErrAllowanceTx)
	}
	return revoke, amount, nil
}

// Submit checks an approve or revoke the owner's browser signed, adds the
// payer's signature as fee payer, sends it and records the owner's choice.
func (s *VaultAllowanceService) Submit(ctx context.Context, userID, network, txBase64 string) (*AllowanceResult, error) {
	n, err := s.network(network)
	if err != nil {
		return nil, err
	}
	owner, err := s.owner(ctx, userID)
	if err != nil {
		return nil, err
	}
	ok, _, err := s.limiter.Allow(ctx, "vault:allowance:"+userID, allowanceSubmitPerHour, time.Hour)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: too many allowance changes this hour; try again later", ErrInvalidRequest)
	}
	raw, err := base64.StdEncoding.DecodeString(txBase64)
	if err != nil {
		return nil, ErrAllowanceTx
	}
	tx, err := solana.DecodeTransaction(raw)
	if err != nil {
		return nil, ErrAllowanceTx
	}
	rt, err := solana.ParseRawTransaction(raw)
	if err != nil {
		return nil, ErrAllowanceTx
	}
	account := tokenAccountOf(n, owner)
	revoke, amount, err := checkAllowanceTx(tx, n.Payer.PublicKey(), owner, account, n.Mint, s.maxMinor)
	if err != nil {
		return nil, err
	}
	// The owner's signature over the exact bytes that will be sent.
	if !ed25519.Verify(owner[:], rt.Message(), tx.Signatures[1][:]) {
		return nil, ErrVaultNotYours
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// The newest last valid height is never earlier than the transaction's
	// own: waiting on it can only take longer to call it expired, never sooner.
	bh, err := n.RPC.GetLatestBlockhash(ctx, solana.Confirmed)
	if err != nil {
		return nil, err
	}
	signedRaw, sig, err := rt.Sign(n.Payer)
	if err != nil {
		return nil, err
	}
	signed := &spendpass.Signed{Base64: base64.StdEncoding.EncodeToString(signedRaw), Signature: solana.EncodeBase58(sig[:]), LastValidBlockHeight: bh.LastValidBlockHeight}
	now := s.now().UTC()
	rec := &VaultAllowance{UserID: userID, Network: n.Network, State: AllowanceActive, ApprovedMinor: int64(amount),
		Delegate: n.Payer.PublicKey().String(), TokenAccount: account.String(), Signature: signed.Signature, CreatedAt: now, UpdatedAt: now}
	if revoke {
		rec.State, rec.ApprovedMinor = AllowanceRevoked, 0
	}
	out := &AllowanceResult{Signature: signed.Signature}
	sendErr := spendpass.Send(ctx, n.RPC, signed, solana.Confirmed)
	switch {
	case sendErr == nil:
	case errors.Is(sendErr, spendpass.ErrNeverLanded), errors.Is(sendErr, solana.ErrTransactionFailed), refusedInPreflight(sendErr):
		return nil, fmt.Errorf("%w: the network didn't take it, so nothing changed: %v", ErrInvalidRequest, sendErr)
	default:
		// Sent and likely to land. The chain is read for what is left, so a
		// recorded choice that never took effect only shows an allowance of zero.
		out.Pending = true
	}
	if prev, err := s.store.VaultAllowance(ctx, userID, n.Network); err == nil {
		rec.CreatedAt = prev.CreatedAt
	}
	if err := s.store.SaveVaultAllowance(ctx, rec); err != nil {
		return nil, err
	}
	s.log.Info("vault allowance: changed", "network", n.Network, "state", rec.State, "approved_minor", rec.ApprovedMinor, "signature", signed.Signature, "pending", out.Pending)
	if out.Allowance, err = s.view(ctx, userID, n, owner); err != nil {
		return nil, err
	}
	return out, nil
}

// active returns the vault's owner key when the pass's account has chosen to
// pay from its vault on n, and false otherwise.
func (s *VaultAllowanceService) active(ctx context.Context, passID string, n *OnchainNetwork) (solana.PublicKey, bool, error) {
	pass, err := s.passes.Get(ctx, passID)
	if err != nil {
		return solana.PublicKey{}, false, fmt.Errorf("vault allowance: reading the pass: %w", err)
	}
	if pass.UserID == "" {
		return solana.PublicKey{}, false, nil
	}
	rec, err := s.store.VaultAllowance(ctx, pass.UserID, n.Network)
	if errors.Is(err, shared.ErrNotFound) {
		return solana.PublicKey{}, false, nil
	}
	if err != nil {
		return solana.PublicKey{}, false, err
	}
	if rec.State != AllowanceActive {
		return solana.PublicKey{}, false, nil
	}
	owner, err := s.owner(ctx, pass.UserID)
	if errors.Is(err, ErrVaultNone) {
		return solana.PublicKey{}, false, nil
	}
	return owner, err == nil, err
}

// Covers says whether payments for passID on network come from the owner's
// vault (so no on-chain Spend Pass is needed for them).
func (s *VaultAllowanceService) Covers(ctx context.Context, passID, network string) bool {
	if s == nil {
		return false
	}
	n := s.nets[chain.NormalizeNetwork(network)]
	if n == nil {
		return false
	}
	_, ok, err := s.active(ctx, passID, n)
	return ok && err == nil
}

// allowanceCode is why the vault's USDC account can't fund amountMinor
// through delegate's allowance, or "".
func allowanceCode(ta *solana.TokenAccount, delegate solana.PublicKey, amountMinor int64) string {
	switch {
	case ta == nil || ta.State != solana.TokenAccountInitialized:
		return DenyVaultUnfunded
	case ta.Delegate == nil || *ta.Delegate != delegate || ta.DelegatedAmount < uint64(amountMinor):
		return DenyVaultAllowanceLow
	case ta.Amount < uint64(amountMinor):
		return DenyVaultUnfunded
	}
	return ""
}

// Check never refuses: revoking the allowance only stops the vault paying.
func (s *VaultAllowanceService) Check(context.Context, string) error { return nil }

// Funding says whether a payment on rail comes from the owner's vault.
func (s *VaultAllowanceService) Funding(ctx context.Context, passID, rail string) (bool, error) {
	n := s.byRail[rail]
	if n == nil {
		return false, nil
	}
	_, ok, err := s.active(ctx, passID, n)
	return ok, err
}

// Fund pulls amountMinor from the owner's vault into the payer, as the
// allowance's delegate, for one attempt, and returns once the pull is
// confirmed. It does nothing where the owner hasn't chosen to pay from the
// vault. A vault that can't fund the payment is an *AuthorityDenied.
func (s *VaultAllowanceService) Fund(ctx context.Context, passID string, rv *econ.Reservation, amountMinor int64) (*OnchainPull, error) {
	n := s.byRail[rv.Rail]
	if n == nil || amountMinor <= 0 {
		return nil, nil
	}
	owner, ok, err := s.active(ctx, passID, n)
	if err != nil || !ok {
		return nil, err
	}
	if prior, err := s.book.store.OnchainPull(ctx, rv.ID); err == nil {
		if prior.State == PullPulled && prior.AmountMinor == amountMinor {
			return prior, nil // already funded for this attempt
		}
		return nil, fmt.Errorf("%w: this attempt already has a pull (%s)", shared.ErrConflict, prior.State)
	} else if !errors.Is(err, shared.ErrNotFound) {
		return nil, err
	}
	ta, err := s.read(ctx, n, owner)
	if err != nil {
		return nil, err
	}
	if code := allowanceCode(ta, n.Payer.PublicKey(), amountMinor); code != "" {
		return nil, &AuthorityDenied{ReasonCodes: []string{code}}
	}
	account := tokenAccountOf(n, owner)
	memo, err := solana.Memo("algebra pull " + rv.ID)
	if err != nil {
		return nil, err
	}
	pull := solana.TransferChecked(solana.TokenProgram, account, n.Mint, s.book.destination(n), n.Payer.PublicKey(), uint64(amountMinor), chain.USDCDecimals)
	p := &OnchainPull{ReservationID: rv.ID, IntentID: rv.IntentID, PassID: passID, Network: n.Network,
		PassAddress: account.String(), OwnerWallet: owner.String(), AmountMinor: amountMinor}
	return s.book.pull(ctx, n, p, pull, memo)
}

// VaultStatus reads the owner's vault for a payment on network, for the dry
// run. Where the owner hasn't chosen to pay from it, it says nothing.
func (s *VaultAllowanceService) VaultStatus(ctx context.Context, passID, network string) VaultState {
	if s == nil {
		return VaultState{}
	}
	n := s.nets[chain.NormalizeNetwork(network)]
	if n == nil {
		return VaultState{}
	}
	owner, ok, err := s.active(ctx, passID, n)
	if err != nil || !ok {
		return VaultState{}
	}
	ta, err := s.read(ctx, n, owner)
	if err != nil {
		return VaultState{} // unknown: Fund reads the chain again
	}
	if code := allowanceCode(ta, n.Payer.PublicKey(), 1); code != "" {
		return VaultState{Code: code}
	}
	out := VaultState{Funded: true, FundedMinor: int64(min(ta.Amount, ta.DelegatedAmount)), shortCode: DenyVaultUnfunded}
	if ta.DelegatedAmount < ta.Amount {
		out.shortCode = DenyVaultAllowanceLow
	}
	return out
}

// PullFor is the pull made for a reservation (FundingReader), from either
// kind of vault.
func (s *VaultAllowanceService) PullFor(ctx context.Context, reservationID string) (*OnchainPull, error) {
	return s.book.PullFor(ctx, reservationID)
}

// Reconcile settles open pulls from vaults and refunds what their attempts
// didn't spend.
func (s *VaultAllowanceService) Reconcile(ctx context.Context) (int, error) {
	return s.book.Reconcile(ctx)
}

var _ PassFunder = (*VaultAllowanceService)(nil)
