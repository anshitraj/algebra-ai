package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/spendpass"
)

// On-chain Spend Passes.
//
// A Spend Pass can be backed by a pass on Solana (solana-program/programs/
// spend-pass): a vault of the owner's USDC that Algebra's payer can pull from
// only into its own USDC account, only within a per-call cap, a total budget,
// an optional window cap and an expiry, and never while the owner has frozen
// or revoked it. The program checks every one of those on every pull, so even
// a compromised Algebra server can't take more than the owner allowed.
//
// How a payment uses it: when Algebra releases payment authority for an
// attempt (EconomicService.AuthorizePayment), it first pulls exactly the
// payment's amount from the pass into the payer, and only once that pull is
// confirmed does it hand the signed x402 payment over. The x402 payment itself
// stays a plain transfer, as facilitators require. Whatever the attempt
// didn't spend (a payment that never landed, an escrow that settled for less)
// is refunded to the vault by Reconcile, which proves what was spent from the
// chain through the rail, never from anyone's word.
//
// The owner's wallet signs everything else. Algebra builds those
// transactions unsigned (OwnerTransaction) and never sees the owner's key.

// ErrInvalidRequest: what was asked for can't be done as asked (HTTP 400).
var ErrInvalidRequest = errors.New("algebra: invalid request")

// OnchainBinding ties a Spend Pass to its on-chain pass.
type OnchainBinding struct {
	PassID      string    `json:"pass_id"`
	UserID      string    `json:"-"`
	Network     string    `json:"network"`
	ProgramID   string    `json:"program_id"`
	Address     string    `json:"address"`
	OwnerWallet string    `json:"owner_wallet"`
	PassNumber  uint64    `json:"pass_number"`
	LinkedAt    time.Time `json:"linked_at"`
}

// Pull states (migrations/0020_onchain_passes.sql).
const (
	PullSending   = "SENDING"
	PullPulled    = "PULLED"
	PullVoid      = "VOID"
	PullSettled   = "SETTLED"
	PullRefunding = "REFUNDING"
	PullRefunded  = "REFUNDED"
)

// OnchainPull is one pull for one paid attempt, and what became of it.
type OnchainPull struct {
	ReservationID    string    `json:"reservation_id"`
	IntentID         string    `json:"intent_id"`
	PassID           string    `json:"pass_id"`
	Network          string    `json:"network"`
	PassAddress      string    `json:"pass_address"`
	OwnerWallet      string    `json:"owner_wallet"`
	AmountMinor      int64     `json:"amount_minor"`
	PullSignature    string    `json:"pull_signature"`
	PullValidUntil   uint64    `json:"-"`
	State            string    `json:"state"`
	UsedMinor        *int64    `json:"used_minor,omitempty"`
	RefundMinor      *int64    `json:"refund_minor,omitempty"`
	RefundSignature  string    `json:"refund_signature,omitempty"`
	RefundValidUntil uint64    `json:"-"`
	Detail           string    `json:"detail,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// OnchainPassStore keeps bindings and pulls.
type OnchainPassStore interface {
	OnchainBinding(ctx context.Context, passID string) (*OnchainBinding, error) // shared.ErrNotFound if none
	SaveOnchainBinding(ctx context.Context, b *OnchainBinding) error
	DeleteOnchainBinding(ctx context.Context, passID string) error
	OnchainPull(ctx context.Context, reservationID string) (*OnchainPull, error) // shared.ErrNotFound if none
	InsertOnchainPull(ctx context.Context, p *OnchainPull) error
	UpdateOnchainPull(ctx context.Context, p *OnchainPull) error
	OpenOnchainPulls(ctx context.Context, limit int) ([]OnchainPull, error)
	OnchainPullsForPass(ctx context.Context, passID string, limit int) ([]OnchainPull, error)
}

// ReservationReader reads an intent's attempts.
type ReservationReader interface {
	Reservations(ctx context.Context, intentID string) ([]econ.Reservation, error)
}

// OnchainNetwork is one cluster where passes live, and the rail that pays
// there. Payer is the rail's own key: every pass names it as the agent, and
// its USDC account as the destination.
type OnchainNetwork struct {
	Network  string // chain.Solana or chain.SolanaDevnet
	Cluster  string // "mainnet" or "devnet", for explorer links
	RailName string
	Program  spendpass.Program
	RPC      *solana.RPC
	Payer    *solana.Keypair
	Mint     solana.PublicKey
	// Rail proves what a payment spent, for refunds.
	Rail Rail
	// PriorityMicroLamports is the priority fee per compute unit for pulls,
	// refunds and the owner's transactions.
	PriorityMicroLamports uint64
	// Required: on this network, a pass must be on chain to pay at all, so
	// the payer never spends money that isn't a pass's.
	Required bool
}

// OnchainNetworkInfo is what a wallet needs to make a pass Algebra accepts.
type OnchainNetworkInfo struct {
	Network     string `json:"network"`
	Cluster     string `json:"cluster"`
	ProgramID   string `json:"program_id"`
	Agent       string `json:"agent"`
	Destination string `json:"destination"`
	Mint        string `json:"mint"`
	Required    bool   `json:"required"`
}

// OnchainPassService links Spend Passes to on-chain passes, funds payments
// from them and refunds what payments didn't spend.
type OnchainPassService struct {
	store        OnchainPassStore
	passes       *SpendPassService
	wallets      UserWalletStore
	reservations ReservationReader
	nets         map[string]*OnchainNetwork
	byRail       map[string]*OnchainNetwork
	// AnyOwnerOn lists networks where an owner wallet needn't be one the
	// person signed in with (devnet demos with CLI-made wallets). The pass
	// number still binds the on-chain pass to this Spend Pass.
	AnyOwnerOn map[string]bool
	log        *slog.Logger
	now        func() time.Time
}

// NewOnchainPassService builds the service for the given networks.
func NewOnchainPassService(store OnchainPassStore, passes *SpendPassService, wallets UserWalletStore, reservations ReservationReader, nets []*OnchainNetwork) *OnchainPassService {
	s := &OnchainPassService{store: store, passes: passes, wallets: wallets, reservations: reservations,
		nets: map[string]*OnchainNetwork{}, byRail: map[string]*OnchainNetwork{}, AnyOwnerOn: map[string]bool{},
		log: slog.Default(), now: time.Now}
	for _, n := range nets {
		s.nets[n.Network] = n
		s.byRail[n.RailName] = n
	}
	return s
}

// PassNumber is the number an on-chain pass for passID must be made with.
// It ties the on-chain pass to this Spend Pass alone: another person's
// on-chain pass, made for their Spend Pass, can't be linked to yours.
func PassNumber(passID string) uint64 {
	h := sha256.Sum256([]byte("algebra:spend-pass:" + passID))
	return binary.LittleEndian.Uint64(h[:8])
}

func intentHash(reservationID string) [32]byte {
	return sha256.Sum256([]byte("algebra:reservation:" + reservationID))
}

// Networks lists where passes can be made.
func (s *OnchainPassService) Networks() []OnchainNetworkInfo {
	out := []OnchainNetworkInfo{}
	for _, network := range []string{chain.Solana, chain.SolanaDevnet} {
		n := s.nets[network]
		if n == nil {
			continue
		}
		dest, _ := solana.AssociatedTokenAddress(n.Payer.PublicKey(), n.Mint, solana.TokenProgram)
		out = append(out, OnchainNetworkInfo{Network: n.Network, Cluster: n.Cluster, ProgramID: n.Program.ID.String(),
			Agent: n.Payer.PublicKey().String(), Destination: dest.String(), Mint: n.Mint.String(), Required: n.Required})
	}
	return out
}

func (s *OnchainPassService) network(network string) (*OnchainNetwork, error) {
	n := s.nets[chain.NormalizeNetwork(network)]
	if n == nil {
		return nil, fmt.Errorf("%w: on-chain passes aren't available on %q here", ErrInvalidRequest, network)
	}
	return n, nil
}

func (s *OnchainPassService) destination(n *OnchainNetwork) solana.PublicKey {
	d, _ := solana.AssociatedTokenAddress(n.Payer.PublicKey(), n.Mint, solana.TokenProgram)
	return d
}

// ownerAllowed: the wallet must be one the person signed in with, unless the
// network allows any owner.
func (s *OnchainPassService) ownerAllowed(ctx context.Context, userID string, n *OnchainNetwork, owner solana.PublicKey) error {
	if owner == n.Payer.PublicKey() {
		return fmt.Errorf("%w: the owner can't be Algebra's own payer", ErrInvalidRequest)
	}
	if s.AnyOwnerOn[n.Network] {
		return nil
	}
	if s.wallets != nil {
		ws, err := s.wallets.ListUserWallets(ctx, userID)
		if err != nil {
			return err
		}
		for _, w := range ws {
			if strings.EqualFold(w.Chain, "solana") && w.Address == owner.String() {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %s isn't a wallet you signed in with; sign in with that wallet first", ErrInvalidRequest, owner)
}

// PreparedPass is an unsigned transaction that makes (and optionally funds)
// the on-chain pass for a Spend Pass, for the owner's wallet to sign and send.
type PreparedPass struct {
	Network              string          `json:"network"`
	PassAddress          string          `json:"pass_address"`
	PassNumber           uint64          `json:"pass_number"`
	Owner                string          `json:"owner"`
	Terms                spendpass.Terms `json:"terms"`
	DepositMinor         int64           `json:"deposit_minor"`
	Transaction          string          `json:"transaction"`
	LastValidBlockHeight uint64          `json:"last_valid_block_height"`
}

// PrepareOptions are the parts of the on-chain terms the Spend Pass doesn't
// already fix.
type PrepareOptions struct {
	Network      string `json:"network"`
	Owner        string `json:"owner"`
	DepositMinor int64  `json:"deposit_minor"`
	WindowSecs   int64  `json:"window_secs"`
	WindowCap    int64  `json:"window_cap_minor"`
}

// Prepare builds the transaction that puts a Spend Pass on chain, with the
// pass's own limits: its budget is the total budget, its per-call limit the
// per-call cap, its expiry the expiry.
func (s *OnchainPassService) Prepare(ctx context.Context, userID, passID string, o PrepareOptions) (*PreparedPass, error) {
	p, err := s.passes.OwnedActive(ctx, userID, passID)
	if err != nil {
		return nil, err
	}
	if p.Currency != "USDC" {
		return nil, fmt.Errorf("%w: only a USDC pass can live on Solana", ErrInvalidRequest)
	}
	if _, err := s.store.OnchainBinding(ctx, passID); err == nil {
		return nil, fmt.Errorf("%w: this pass is already on chain", shared.ErrConflict)
	} else if !errors.Is(err, shared.ErrNotFound) {
		return nil, err
	}
	n, err := s.network(o.Network)
	if err != nil {
		return nil, err
	}
	owner, err := solana.ParsePublicKey(o.Owner)
	if err != nil {
		return nil, fmt.Errorf("%w: owner: %v", ErrInvalidRequest, err)
	}
	if err := s.ownerAllowed(ctx, userID, n, owner); err != nil {
		return nil, err
	}
	if o.DepositMinor < 0 || o.WindowSecs < 0 || o.WindowCap < 0 {
		return nil, fmt.Errorf("%w: amounts can't be negative", ErrInvalidRequest)
	}
	perCall := p.BudgetMinorUnits
	if p.MaxPerPurchaseMinorUnits != nil {
		perCall = *p.MaxPerPurchaseMinorUnits
	}
	terms := spendpass.Terms{PerCallCap: uint64(perCall), TotalBudget: uint64(p.BudgetMinorUnits), WindowSecs: o.WindowSecs,
		WindowCap: uint64(o.WindowCap), ExpiresAt: p.ExpiresAt.Unix()}
	if err := terms.Validate(s.now().Unix()); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	number := PassNumber(passID)
	ata, err := spendpass.CreateATAIdempotent(owner, n.Payer.PublicKey(), n.Mint)
	if err != nil {
		return nil, err
	}
	create, addr, err := n.Program.CreatePass(owner, n.Payer.PublicKey(), n.Mint, s.destination(n), number, terms)
	if err != nil {
		return nil, err
	}
	ixs := []solana.Instruction{ata, create}
	if o.DepositMinor > 0 {
		ownerToken, err := solana.AssociatedTokenAddress(owner, n.Mint, solana.TokenProgram)
		if err != nil {
			return nil, err
		}
		dep, err := n.Program.Deposit(owner, addr, n.Mint, ownerToken, uint64(o.DepositMinor))
		if err != nil {
			return nil, err
		}
		ixs = append(ixs, dep)
	}
	tx, lvbh, err := spendpass.Unsigned(ctx, n.RPC, owner, n.PriorityMicroLamports, ixs...)
	if err != nil {
		return nil, err
	}
	return &PreparedPass{Network: n.Network, PassAddress: addr.String(), PassNumber: number, Owner: owner.String(), Terms: terms,
		DepositMinor: o.DepositMinor, Transaction: tx, LastValidBlockHeight: lvbh}, nil
}

// readPass reads and decodes a pass account, refusing anything the program
// doesn't own.
func (s *OnchainPassService) readPass(ctx context.Context, n *OnchainNetwork, addr solana.PublicKey) (*spendpass.State, error) {
	acct, err := n.RPC.GetAccountInfo(ctx, addr, solana.Confirmed)
	if err != nil {
		return nil, err
	}
	if acct == nil {
		return nil, fmt.Errorf("%w: no pass at %s on %s (not made yet, or closed)", shared.ErrNotFound, addr, n.Network)
	}
	if acct.Owner != n.Program.ID {
		return nil, fmt.Errorf("%w: %s doesn't belong to the Spend Pass program", ErrInvalidRequest, addr)
	}
	return spendpass.Decode(acct.Data)
}

// Link records the on-chain pass at address as passID's, after checking on
// chain that it is the pass Prepare described: made for this Spend Pass's
// number, by a wallet of the person's, naming Algebra's payer as the agent
// and its USDC account as the destination.
func (s *OnchainPassService) Link(ctx context.Context, userID, passID, network, address string) (*OnchainBinding, error) {
	if _, err := s.passes.OwnedActive(ctx, userID, passID); err != nil {
		return nil, err
	}
	n, err := s.network(network)
	if err != nil {
		return nil, err
	}
	addr, err := solana.ParsePublicKey(address)
	if err != nil {
		return nil, fmt.Errorf("%w: address: %v", ErrInvalidRequest, err)
	}
	if b, err := s.store.OnchainBinding(ctx, passID); err == nil {
		if b.Address == addr.String() && b.Network == n.Network {
			return b, nil
		}
		return nil, fmt.Errorf("%w: this pass is already linked to %s", shared.ErrConflict, b.Address)
	} else if !errors.Is(err, shared.ErrNotFound) {
		return nil, err
	}
	st, err := s.readPass(ctx, n, addr)
	if err != nil {
		return nil, err
	}
	number := PassNumber(passID)
	want, err := n.Program.PassAddress(st.Owner, number)
	if err != nil {
		return nil, err
	}
	switch {
	case want != addr:
		return nil, fmt.Errorf("%w: that on-chain pass wasn't made for this Spend Pass", ErrInvalidRequest)
	case st.Agent != n.Payer.PublicKey():
		return nil, fmt.Errorf("%w: that pass names %s as its agent, not Algebra's payer %s", ErrInvalidRequest, st.Agent, n.Payer.PublicKey())
	case st.Destination != s.destination(n):
		return nil, fmt.Errorf("%w: that pass pays into %s, not Algebra's payer account", ErrInvalidRequest, st.Destination)
	case st.Mint != n.Mint:
		return nil, fmt.Errorf("%w: that pass holds another token, not USDC", ErrInvalidRequest)
	case st.Revoked:
		return nil, fmt.Errorf("%w: that pass is revoked", ErrInvalidRequest)
	}
	if err := s.ownerAllowed(ctx, userID, n, st.Owner); err != nil {
		return nil, err
	}
	b := &OnchainBinding{PassID: passID, UserID: userID, Network: n.Network, ProgramID: n.Program.ID.String(), Address: addr.String(),
		OwnerWallet: st.Owner.String(), PassNumber: number, LinkedAt: s.now().UTC()}
	if err := s.store.SaveOnchainBinding(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

// OnchainPassView is a linked pass as the chain has it now.
type OnchainPassView struct {
	Binding    OnchainBinding   `json:"binding"`
	Exists     bool             `json:"exists"`
	State      *spendpass.State `json:"state,omitempty"`
	Vault      string           `json:"vault"`
	VaultMinor *uint64          `json:"vault_minor,omitempty"`
	Remaining  uint64           `json:"remaining_minor"`
	Explorer   string           `json:"explorer"`
	Pulls      []OnchainPull    `json:"pulls"`
}

// State reads a linked pass from the chain.
func (s *OnchainPassService) State(ctx context.Context, userID, passID string) (*OnchainPassView, error) {
	b, n, err := s.owned(ctx, userID, passID)
	if err != nil {
		return nil, err
	}
	addr := solana.MustPublicKey(b.Address)
	vault, _ := spendpass.Vault(addr, n.Mint)
	v := &OnchainPassView{Binding: *b, Vault: vault.String(), Explorer: Explorer(n.Cluster, "address/"+b.Address)}
	st, err := s.readPass(ctx, n, addr)
	switch {
	case errors.Is(err, shared.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		v.Exists, v.State, v.Remaining = true, st, st.Remaining()
		if bal, err := n.RPC.GetTokenAccountBalance(ctx, vault, solana.Confirmed); err == nil {
			v.VaultMinor = &bal.Amount
		}
	}
	if v.Pulls, err = s.store.OnchainPullsForPass(ctx, passID, 20); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *OnchainPassService) owned(ctx context.Context, userID, passID string) (*OnchainBinding, *OnchainNetwork, error) {
	b, err := s.store.OnchainBinding(ctx, passID)
	if err != nil {
		return nil, nil, err
	}
	if b.UserID != userID {
		return nil, nil, fmt.Errorf("%w: pass %s", shared.ErrNotFound, passID)
	}
	n, err := s.network(b.Network)
	if err != nil {
		return nil, nil, err
	}
	return b, n, nil
}

// Owner actions a wallet signs.
const (
	ActionFreeze   = "freeze"
	ActionUnfreeze = "unfreeze"
	ActionDeposit  = "deposit"
	ActionWithdraw = "withdraw"
	ActionRevoke   = "revoke"
	ActionClose    = "close"
)

// OwnerTx is an unsigned transaction for the owner's wallet.
type OwnerTx struct {
	Action               string `json:"action"`
	Transaction          string `json:"transaction"`
	LastValidBlockHeight uint64 `json:"last_valid_block_height"`
}

// OwnerTransaction builds one owner action on a linked pass, unsigned. The
// pass needn't be active in Algebra: an owner can always freeze, withdraw or
// close.
func (s *OnchainPassService) OwnerTransaction(ctx context.Context, userID, passID, action string, amountMinor int64) (*OwnerTx, error) {
	b, n, err := s.owned(ctx, userID, passID)
	if err != nil {
		return nil, err
	}
	owner, pass := solana.MustPublicKey(b.OwnerWallet), solana.MustPublicKey(b.Address)
	ownerToken, err := solana.AssociatedTokenAddress(owner, n.Mint, solana.TokenProgram)
	if err != nil {
		return nil, err
	}
	needAmount := func() error {
		if amountMinor <= 0 {
			return fmt.Errorf("%w: give an amount more than zero", ErrInvalidRequest)
		}
		return nil
	}
	var ixs []solana.Instruction
	switch action {
	case ActionFreeze, ActionUnfreeze:
		ixs = append(ixs, n.Program.SetFrozen(owner, pass, action == ActionFreeze))
	case ActionRevoke:
		ixs = append(ixs, n.Program.Revoke(owner, pass))
	case ActionDeposit, ActionWithdraw:
		if err := needAmount(); err != nil {
			return nil, err
		}
		var ix solana.Instruction
		if action == ActionDeposit {
			ix, err = n.Program.Deposit(owner, pass, n.Mint, ownerToken, uint64(amountMinor))
		} else {
			ata, aerr := spendpass.CreateATAIdempotent(owner, owner, n.Mint)
			if aerr != nil {
				return nil, aerr
			}
			ixs = append(ixs, ata)
			ix, err = n.Program.Withdraw(owner, pass, n.Mint, ownerToken, uint64(amountMinor))
		}
		if err != nil {
			return nil, err
		}
		ixs = append(ixs, ix)
	case ActionClose:
		ata, err := spendpass.CreateATAIdempotent(owner, owner, n.Mint)
		if err != nil {
			return nil, err
		}
		ix, err := n.Program.ClosePass(owner, pass, n.Mint, ownerToken)
		if err != nil {
			return nil, err
		}
		ixs = append(ixs, ata, ix)
	default:
		return nil, fmt.Errorf("%w: action must be freeze, unfreeze, deposit, withdraw, revoke or close", ErrInvalidRequest)
	}
	tx, lvbh, err := spendpass.Unsigned(ctx, n.RPC, owner, n.PriorityMicroLamports, ixs...)
	if err != nil {
		return nil, err
	}
	return &OwnerTx{Action: action, Transaction: tx, LastValidBlockHeight: lvbh}, nil
}

// Unlink forgets the on-chain pass. Pulls still being settled keep their own
// records and are refunded all the same.
func (s *OnchainPassService) Unlink(ctx context.Context, userID, passID string) error {
	if _, _, err := s.owned(ctx, userID, passID); err != nil {
		return err
	}
	return s.store.DeleteOnchainBinding(ctx, passID)
}

// Check is the on-chain kill switch, read before an intent is made: a pass
// its owner froze, revoked or closed on chain, or that has expired there, is
// refused at once (HTTP 403), as Algebra's own kill switch is. A node that
// can't be reached refuses nothing here; Fund reads the chain again before
// any money moves.
func (s *OnchainPassService) Check(ctx context.Context, passID string) error {
	b, err := s.store.OnchainBinding(ctx, passID)
	if errors.Is(err, shared.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	n := s.nets[b.Network]
	if n == nil {
		return nil
	}
	st, err := s.readPass(ctx, n, solana.MustPublicKey(b.Address))
	switch {
	case errors.Is(err, shared.ErrNotFound):
		return fmt.Errorf("%w: the agent's Spend Pass was closed on Solana by its owner", shared.ErrUnauthorized)
	case err != nil:
		s.log.Warn("onchain pass: couldn't read the pass before an intent", "pass", b.Address, "err", err)
		return nil
	case st.Revoked:
		return fmt.Errorf("%w: the agent's Spend Pass was revoked on Solana by its owner", shared.ErrUnauthorized)
	case st.Frozen:
		return fmt.Errorf("%w: the agent's Spend Pass is frozen on Solana by its owner's kill switch", shared.ErrUnauthorized)
	case s.now().Unix() >= st.ExpiresAt:
		return fmt.Errorf("%w: the agent's Spend Pass has expired on Solana", shared.ErrUnauthorized)
	}
	return nil
}

// Denial codes for a pull the pass doesn't allow.
const (
	DenyOnchainRequired     = "ONCHAIN_PASS_REQUIRED"
	DenyOnchainOtherNetwork = "ONCHAIN_PASS_OTHER_NETWORK"
	DenyOnchainMissing      = "ONCHAIN_PASS_MISSING"
	DenyOnchainFrozen       = "ONCHAIN_PASS_FROZEN"
	DenyOnchainRevoked      = "ONCHAIN_PASS_REVOKED"
	DenyOnchainExpired      = "ONCHAIN_PASS_EXPIRED"
	DenyOnchainLimit        = "ONCHAIN_PASS_LIMIT"
	DenyOnchainUnfunded     = "ONCHAIN_PASS_UNFUNDED"
)

func denyFor(err error) error {
	code := ""
	var pe *spendpass.ProgramError
	switch {
	case errors.Is(err, spendpass.ErrFrozen):
		code = DenyOnchainFrozen
	case errors.Is(err, spendpass.ErrRevoked):
		code = DenyOnchainRevoked
	case errors.Is(err, spendpass.ErrExpired):
		code = DenyOnchainExpired
	case errors.Is(err, spendpass.ErrOverLimit):
		code = DenyOnchainLimit
	case errors.As(err, &pe):
		switch pe.Name {
		case "PassFrozen":
			code = DenyOnchainFrozen
		case "PassRevoked":
			code = DenyOnchainRevoked
		case "PassExpired":
			code = DenyOnchainExpired
		case "OverPerCallCap", "OverBudget", "OverWindowCap":
			code = DenyOnchainLimit
		}
	}
	if code == "" {
		return nil
	}
	return &AuthorityDenied{ReasonCodes: []string{code}}
}

// Fund pulls amountMinor from the pass's on-chain vault into the payer for
// one attempt, and returns once the pull is confirmed. It does nothing for a
// rail it doesn't manage, or for a pass that isn't on chain where that is
// allowed. A pull the pass's own limits refuse is an *AuthorityDenied.
func (s *OnchainPassService) Fund(ctx context.Context, passID string, rv *econ.Reservation, amountMinor int64) (*OnchainPull, error) {
	n := s.byRail[rv.Rail]
	if n == nil || amountMinor <= 0 {
		return nil, nil
	}
	b, err := s.store.OnchainBinding(ctx, passID)
	switch {
	case errors.Is(err, shared.ErrNotFound):
		if n.Required {
			return nil, &AuthorityDenied{ReasonCodes: []string{DenyOnchainRequired}}
		}
		return nil, nil
	case err != nil:
		return nil, err
	}
	if b.Network != n.Network {
		return nil, &AuthorityDenied{ReasonCodes: []string{DenyOnchainOtherNetwork}}
	}
	if prior, err := s.store.OnchainPull(ctx, rv.ID); err == nil {
		if prior.State == PullPulled && prior.AmountMinor == amountMinor {
			return prior, nil // already funded for this attempt
		}
		return nil, fmt.Errorf("%w: this attempt already has a pull (%s)", shared.ErrConflict, prior.State)
	} else if !errors.Is(err, shared.ErrNotFound) {
		return nil, err
	}
	pass := solana.MustPublicKey(b.Address)
	st, err := s.readPass(ctx, n, pass)
	if errors.Is(err, shared.ErrNotFound) {
		return nil, &AuthorityDenied{ReasonCodes: []string{DenyOnchainMissing}}
	}
	if err != nil {
		return nil, err
	}
	if err := st.CanPull(s.now().Unix(), uint64(amountMinor)); err != nil {
		if d := denyFor(err); d != nil {
			return nil, d
		}
		return nil, err
	}
	vault, _ := spendpass.Vault(pass, n.Mint)
	if bal, err := n.RPC.GetTokenAccountBalance(ctx, vault, solana.Confirmed); err != nil || bal.Amount < uint64(amountMinor) {
		return nil, &AuthorityDenied{ReasonCodes: []string{DenyOnchainUnfunded}}
	}
	ix, err := n.Program.Pull(n.Payer.PublicKey(), pass, n.Mint, s.destination(n), uint64(amountMinor), intentHash(rv.ID))
	if err != nil {
		return nil, err
	}
	signed, err := spendpass.Sign(ctx, n.RPC, n.Payer, nil, n.PriorityMicroLamports, ix)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	p := &OnchainPull{ReservationID: rv.ID, IntentID: rv.IntentID, PassID: passID, Network: n.Network, PassAddress: b.Address,
		OwnerWallet: b.OwnerWallet, AmountMinor: amountMinor, PullSignature: signed.Signature, PullValidUntil: signed.LastValidBlockHeight,
		State: PullSending, CreatedAt: now, UpdatedAt: now}
	// Recorded before it is sent: whatever happens next, the pull is known.
	if err := s.store.InsertOnchainPull(ctx, p); err != nil {
		return nil, err
	}
	sendErr := spendpass.Send(ctx, n.RPC, signed, solana.Confirmed)
	switch {
	case sendErr == nil:
		p.State = PullPulled
	case denyFor(sendErr) != nil:
		p.State, p.Detail = PullVoid, trunc(sendErr.Error(), 300)
	case errors.Is(sendErr, spendpass.ErrNeverLanded), errors.Is(sendErr, solana.ErrTransactionFailed), refusedInPreflight(sendErr):
		p.State, p.Detail = PullVoid, trunc(sendErr.Error(), 300)
	default:
		// Unknown: it may still land. Reconcile settles it either way; no
		// payment is released on a pull that isn't confirmed.
		p.Detail = trunc(sendErr.Error(), 300)
		p.UpdatedAt = s.now().UTC()
		_ = s.store.UpdateOnchainPull(ctx, p)
		return nil, fmt.Errorf("funding the payment from the on-chain pass didn't confirm; nothing was paid and the pull will be reconciled: %w", sendErr)
	}
	p.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateOnchainPull(ctx, p); err != nil {
		s.log.Error("onchain pass: recording a pull failed", "reservation_id", rv.ID, "signature", p.PullSignature, "state", p.State, "err", err)
	}
	if p.State == PullVoid {
		if d := denyFor(sendErr); d != nil {
			return nil, d
		}
		return nil, fmt.Errorf("the on-chain pull didn't land; nothing was paid: %w", sendErr)
	}
	s.log.Info("onchain pass: pulled", "pass", b.Address, "amount_minor", amountMinor, "signature", p.PullSignature, "reservation_id", rv.ID)
	return p, nil
}

// refusedInPreflight: the node simulated the transaction, it failed, and so
// the node never forwarded it.
func refusedInPreflight(err error) bool {
	var re *solana.RPCError
	return errors.As(err, &re) && re.Code == -32002
}

// Reconcile moves every open pull forward: a pull whose fate wasn't known
// is settled from the chain, and once an attempt has ended, whatever it
// didn't spend goes back to the pass. It returns how many pulls changed.
func (s *OnchainPassService) Reconcile(ctx context.Context) (int, error) {
	open, err := s.store.OpenOnchainPulls(ctx, 50)
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

func (s *OnchainPassService) step(ctx context.Context, n *OnchainNetwork, p *OnchainPull) error {
	save := func() error { p.UpdatedAt = s.now().UTC(); return s.store.UpdateOnchainPull(ctx, p) }
	switch p.State {
	case PullSending:
		fate, err := spendpass.StatusOf(ctx, n.RPC, p.PullSignature, p.PullValidUntil)
		if err != nil {
			return err
		}
		switch fate {
		case spendpass.Landed:
			p.State = PullPulled
		case spendpass.Failed, spendpass.Expired:
			p.State, p.Detail = PullVoid, "the pull never took effect ("+string(fate)+")"
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
		return s.refund(ctx, n, p, save)
	case PullRefunding:
		fate, err := spendpass.StatusOf(ctx, n.RPC, p.RefundSignature, p.RefundValidUntil)
		if err != nil {
			return err
		}
		switch fate {
		case spendpass.Landed:
			p.State = PullRefunded
		case spendpass.Failed, spendpass.Expired:
			// Try again with a new transaction.
			p.State, p.RefundSignature, p.Detail = PullPulled, "", "refund attempt "+string(fate)+"; retrying"
		default:
			return nil
		}
		return save()
	}
	return nil
}

// used is what the attempt behind a pull spent, once that is final.
func (s *OnchainPassService) used(ctx context.Context, n *OnchainNetwork, p *OnchainPull) (int64, bool, error) {
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

// refund returns what an attempt didn't spend: into the vault while the pass
// exists, or straight to the owner once it has been closed.
func (s *OnchainPassService) refund(ctx context.Context, n *OnchainNetwork, p *OnchainPull, save func() error) error {
	pass := solana.MustPublicKey(p.PassAddress)
	dest := s.destination(n)
	var ixs []solana.Instruction
	acct, err := n.RPC.GetAccountInfo(ctx, pass, solana.Confirmed)
	if err != nil {
		return err
	}
	if acct != nil && acct.Owner == n.Program.ID {
		st, err := spendpass.Decode(acct.Data)
		if err != nil {
			return err
		}
		if uint64(*p.RefundMinor) > st.Spent {
			// The pass's spent count can't go below zero; give back what it allows.
			if st.Spent == 0 {
				acct = nil
			} else {
				part := int64(st.Spent)
				p.RefundMinor = &part
			}
		}
	}
	if acct != nil && acct.Owner == n.Program.ID {
		ix, err := n.Program.Refund(n.Payer.PublicKey(), pass, n.Mint, dest, uint64(*p.RefundMinor), intentHash(p.ReservationID))
		if err != nil {
			return err
		}
		ixs = append(ixs, ix)
	} else {
		owner := solana.MustPublicKey(p.OwnerWallet)
		ata, err := spendpass.CreateATAIdempotent(n.Payer.PublicKey(), owner, n.Mint)
		if err != nil {
			return err
		}
		ownerToken, _ := solana.AssociatedTokenAddress(owner, n.Mint, solana.TokenProgram)
		ixs = append(ixs, ata, solana.TransferChecked(solana.TokenProgram, dest, n.Mint, ownerToken, n.Payer.PublicKey(), uint64(*p.RefundMinor), chain.USDCDecimals))
	}
	signed, err := spendpass.Sign(ctx, n.RPC, n.Payer, nil, n.PriorityMicroLamports, ixs...)
	if err != nil {
		return err
	}
	p.State, p.RefundSignature, p.RefundValidUntil = PullRefunding, signed.Signature, signed.LastValidBlockHeight
	if err := save(); err != nil {
		return err // not sent: nothing to lose
	}
	if err := spendpass.Send(ctx, n.RPC, signed, solana.Confirmed); err != nil {
		s.log.Warn("onchain pass: refund not confirmed yet", "signature", signed.Signature, "err", err)
		return nil // REFUNDING: the next pass settles it from the chain
	}
	p.State, p.Detail = PullRefunded, ""
	s.log.Info("onchain pass: refunded", "pass", p.PassAddress, "amount_minor", *p.RefundMinor, "signature", signed.Signature)
	return save()
}

// Explorer is a Solana Explorer link on a cluster.
func Explorer(cluster, path string) string {
	u := "https://explorer.solana.com/" + path
	if cluster == "devnet" {
		u += "?cluster=devnet"
	}
	return u
}
