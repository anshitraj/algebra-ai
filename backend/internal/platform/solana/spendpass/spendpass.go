// Package spendpass speaks to Algebra's Spend Pass program on Solana
// (solana-program/programs/spend-pass): the instructions, the pass account's
// layout, its addresses, and its errors. It builds instructions and reads
// accounts; it never holds a key that isn't handed to it.
//
// A pass is a vault of an owner's USDC that one agent key can pull from only
// to one destination, within a per-pull cap, a total budget, an optional
// per-window cap and an expiry, all checked by the program. For Algebra the
// agent key is the rail's payer and the destination is the payer's USDC
// account, so a pull funds exactly one x402 payment just before it is made.
package spendpass

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

// The program exists twice, with the same instructions, accounts, layout,
// errors and events: written on Anchor (solana-program/programs/spend-pass)
// and rewritten on Pinocchio (solana-program/pinocchio), which is a fifth of
// the size and so a fifth of the rent. New passes are made on the Pinocchio
// one; passes made on either keep working.
const (
	PinocchioProgramID = "F1Uu8ynQUviLZHUgejHDMkKmFAJJ7ZKDwU3Svc7Xpj21"
	AnchorProgramID    = "46gQDUuJCt6VdDMaguGPoD8SKEtFqvpG7ECtpC2F9tdS"
	// DefaultProgramID is where new passes are made.
	DefaultProgramID = PinocchioProgramID
)

// KnownProgramIDs are every deployment of this program whose passes Algebra
// honours.
var KnownProgramIDs = []string{PinocchioProgramID, AnchorProgramID}

// The program's one-byte discriminators (lib.rs, state.rs).
const (
	accountDiscriminator = 1

	ixCreatePass = 0
	ixDeposit    = 1
	ixPull       = 2
	ixSetFrozen  = 3
	ixSetAgent   = 4
	ixWithdraw   = 5
	ixRevoke     = 6
	ixClosePass  = 7
	ixRefund     = 8
)

// AccountSize is the pass account's size: a one-byte discriminator and the
// fields of state::Pass in order.
const AccountSize = 1 + 4*32 + 11*8 + 32 + 3

var seed = []byte("pass")

// Terms are what an owner fixes when making a pass. Amounts are in the
// mint's smallest unit (micro-USDC); times are Unix seconds.
type Terms struct {
	PerCallCap  uint64 `json:"per_call_cap"`
	TotalBudget uint64 `json:"total_budget"`
	// WindowSecs 0 means no window cap (WindowCap must then be 0).
	WindowSecs int64  `json:"window_secs"`
	WindowCap  uint64 `json:"window_cap"`
	ExpiresAt  int64  `json:"expires_at"`
}

// Validate mirrors PassTerms::validate, so a bad pass is refused before a
// wallet is asked to sign it.
func (t Terms) Validate(now int64) error {
	switch {
	case t.PerCallCap == 0:
		return errors.New("the per-call cap must be more than zero")
	case t.TotalBudget < t.PerCallCap:
		return errors.New("the budget can't be less than the per-call cap")
	case t.ExpiresAt <= now:
		return errors.New("the pass must expire in the future")
	case t.WindowSecs == 0 && t.WindowCap != 0:
		return errors.New("a window cap needs a window length")
	case t.WindowSecs < 0:
		return errors.New("the window length can't be negative")
	case t.WindowSecs > 0 && t.WindowCap < t.PerCallCap:
		return errors.New("the window cap can't be less than the per-call cap")
	}
	return nil
}

// State is a pass account as the program stores it.
type State struct {
	Owner       solana.PublicKey `json:"owner"`
	Agent       solana.PublicKey `json:"agent"`
	Mint        solana.PublicKey `json:"mint"`
	Destination solana.PublicKey `json:"destination"`
	ID          uint64           `json:"id"`
	PerCallCap  uint64           `json:"per_call_cap"`
	TotalBudget uint64           `json:"total_budget"`
	Spent       uint64           `json:"spent"`
	WindowSecs  int64            `json:"window_secs"`
	WindowCap   uint64           `json:"window_cap"`
	WindowStart int64            `json:"window_start"`
	WindowSpent uint64           `json:"window_spent"`
	ExpiresAt   int64            `json:"expires_at"`
	CreatedAt   int64            `json:"created_at"`
	Pulls       uint64           `json:"pulls"`
	LastIntent  [32]byte         `json:"-"`
	Frozen      bool             `json:"frozen"`
	Revoked     bool             `json:"revoked"`
	Bump        uint8            `json:"-"`
}

// Remaining is what the budget still allows.
func (s *State) Remaining() uint64 {
	if s.Spent >= s.TotalBudget {
		return 0
	}
	return s.TotalBudget - s.Spent
}

// CanPull reports, as the program would, whether a pull of amount at time
// now would pass every limit but the vault's balance.
func (s *State) CanPull(now int64, amount uint64) error {
	switch {
	case s.Revoked:
		return ErrRevoked
	case s.Frozen:
		return ErrFrozen
	case now >= s.ExpiresAt:
		return ErrExpired
	case amount == 0:
		return errors.New("spendpass: nothing to pull")
	case amount > s.PerCallCap:
		return fmt.Errorf("%w: %d is more than the pass's %d per call", ErrOverLimit, amount, s.PerCallCap)
	case amount > s.Remaining():
		return fmt.Errorf("%w: %d is more than the %d left in the pass's budget", ErrOverLimit, amount, s.Remaining())
	}
	if s.WindowSecs > 0 {
		start := s.CreatedAt + (max(now-s.CreatedAt, 0)/s.WindowSecs)*s.WindowSecs
		spent := uint64(0)
		if start == s.WindowStart {
			spent = s.WindowSpent
		}
		if spent+amount > s.WindowCap {
			return fmt.Errorf("%w: %d more would pass the window cap of %d", ErrOverLimit, amount, s.WindowCap)
		}
	}
	return nil
}

// Errors a caller can act on.
var (
	ErrNotAPass  = errors.New("spendpass: not a Spend Pass account")
	ErrRevoked   = errors.New("spendpass: the pass is revoked")
	ErrFrozen    = errors.New("spendpass: the pass is frozen by its owner")
	ErrExpired   = errors.New("spendpass: the pass has expired")
	ErrOverLimit = errors.New("spendpass: over the pass's on-chain limit")
)

// Decode reads a pass account's data.
func Decode(data []byte) (*State, error) {
	if len(data) < AccountSize || data[0] != accountDiscriminator {
		return nil, ErrNotAPass
	}
	r := data[1:]
	key := func() (k solana.PublicKey) { copy(k[:], r[:32]); r = r[32:]; return }
	u64 := func() uint64 { v := binary.LittleEndian.Uint64(r); r = r[8:]; return v }
	i64 := func() int64 { return int64(u64()) }
	s := &State{}
	s.Owner, s.Agent, s.Mint, s.Destination = key(), key(), key(), key()
	s.ID, s.PerCallCap, s.TotalBudget, s.Spent = u64(), u64(), u64(), u64()
	s.WindowSecs, s.WindowCap, s.WindowStart, s.WindowSpent = i64(), u64(), i64(), u64()
	s.ExpiresAt, s.CreatedAt, s.Pulls = i64(), i64(), u64()
	copy(s.LastIntent[:], r[:32])
	r = r[32:]
	if r[0] > 1 || r[1] > 1 {
		return nil, ErrNotAPass
	}
	s.Frozen, s.Revoked, s.Bump = r[0] == 1, r[1] == 1, r[2]
	return s, nil
}

// Program is the deployed program, by address.
type Program struct{ ID solana.PublicKey }

// New returns the program at id, or the default address when id is empty.
func New(id string) (Program, error) {
	if id == "" {
		id = DefaultProgramID
	}
	pk, err := solana.ParsePublicKey(id)
	if err != nil {
		return Program{}, fmt.Errorf("spendpass: program id: %w", err)
	}
	return Program{ID: pk}, nil
}

// PassAddress is the pass an owner made with their own number id.
func (p Program) PassAddress(owner solana.PublicKey, id uint64) (solana.PublicKey, error) {
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], id)
	pk, _, err := solana.FindProgramAddress([][]byte{seed, owner[:], n[:]}, p.ID)
	return pk, err
}

// Vault is the token account that holds a pass's tokens.
func Vault(pass, mint solana.PublicKey) (solana.PublicKey, error) {
	return solana.AssociatedTokenAddress(pass, mint, solana.TokenProgram)
}

func data(disc byte, n int) []byte {
	b := make([]byte, 1, 1+n)
	b[0] = disc
	return b
}

func le64(b []byte, v uint64) []byte { return binary.LittleEndian.AppendUint64(b, v) }

func ro(k solana.PublicKey) solana.AccountMeta { return solana.AccountMeta{Pubkey: k} }
func rw(k solana.PublicKey) solana.AccountMeta {
	return solana.AccountMeta{Pubkey: k, IsWritable: true}
}
func signer(k solana.PublicKey, writable bool) solana.AccountMeta {
	return solana.AccountMeta{Pubkey: k, IsSigner: true, IsWritable: writable}
}

// CreatePass makes pass number id for owner. It returns the pass's address.
func (p Program) CreatePass(owner, agent, mint, destination solana.PublicKey, id uint64, t Terms) (solana.Instruction, solana.PublicKey, error) {
	pass, err := p.PassAddress(owner, id)
	if err != nil {
		return solana.Instruction{}, solana.PublicKey{}, err
	}
	vault, err := Vault(pass, mint)
	if err != nil {
		return solana.Instruction{}, solana.PublicKey{}, err
	}
	d := data(ixCreatePass, 48)
	d = le64(d, id)
	d = le64(d, t.PerCallCap)
	d = le64(d, t.TotalBudget)
	d = le64(d, uint64(t.WindowSecs))
	d = le64(d, t.WindowCap)
	d = le64(d, uint64(t.ExpiresAt))
	return solana.Instruction{ProgramID: p.ID, Data: d, Accounts: []solana.AccountMeta{
		signer(owner, true), ro(agent), ro(mint), ro(destination), rw(pass), rw(vault),
		ro(solana.AssociatedTokenProgram), ro(solana.TokenProgram), ro(solana.SystemProgram),
	}}, pass, nil
}

// Deposit moves amount from the owner's token account into the vault.
func (p Program) Deposit(owner, pass, mint, ownerToken solana.PublicKey, amount uint64) (solana.Instruction, error) {
	vault, err := Vault(pass, mint)
	if err != nil {
		return solana.Instruction{}, err
	}
	return solana.Instruction{ProgramID: p.ID, Data: le64(data(ixDeposit, 8), amount), Accounts: []solana.AccountMeta{
		signer(owner, false), ro(pass), ro(mint), rw(ownerToken), rw(vault), ro(solana.TokenProgram),
	}}, nil
}

// Pull moves amount from the vault to the pass's destination, signed by the
// agent. intent ties it to the request it pays for.
func (p Program) Pull(agent, pass, mint, destination solana.PublicKey, amount uint64, intent [32]byte) (solana.Instruction, error) {
	vault, err := Vault(pass, mint)
	if err != nil {
		return solana.Instruction{}, err
	}
	d := le64(data(ixPull, 40), amount)
	d = append(d, intent[:]...)
	return solana.Instruction{ProgramID: p.ID, Data: d, Accounts: []solana.AccountMeta{
		signer(agent, false), rw(pass), ro(mint), rw(vault), rw(destination), ro(solana.TokenProgram),
	}}, nil
}

// Refund puts amount back from the destination into the vault and gives it
// back to the budget, signed by the destination's authority.
func (p Program) Refund(authority, pass, mint, destination solana.PublicKey, amount uint64, intent [32]byte) (solana.Instruction, error) {
	vault, err := Vault(pass, mint)
	if err != nil {
		return solana.Instruction{}, err
	}
	d := le64(data(ixRefund, 40), amount)
	d = append(d, intent[:]...)
	return solana.Instruction{ProgramID: p.ID, Data: d, Accounts: []solana.AccountMeta{
		signer(authority, false), rw(pass), ro(mint), rw(vault), rw(destination), ro(solana.TokenProgram),
	}}, nil
}

// SetFrozen is the owner's kill switch for one pass.
func (p Program) SetFrozen(owner, pass solana.PublicKey, frozen bool) solana.Instruction {
	d := data(ixSetFrozen, 1)
	if frozen {
		d = append(d, 1)
	} else {
		d = append(d, 0)
	}
	return solana.Instruction{ProgramID: p.ID, Data: d, Accounts: []solana.AccountMeta{signer(owner, false), rw(pass)}}
}

// SetAgent replaces the key that may pull.
func (p Program) SetAgent(owner, pass, agent solana.PublicKey) solana.Instruction {
	d := append(data(ixSetAgent, 32), agent[:]...)
	return solana.Instruction{ProgramID: p.ID, Data: d, Accounts: []solana.AccountMeta{signer(owner, false), rw(pass)}}
}

// Withdraw takes amount back out of the vault to the owner's token account.
func (p Program) Withdraw(owner, pass, mint, ownerToken solana.PublicKey, amount uint64) (solana.Instruction, error) {
	vault, err := Vault(pass, mint)
	if err != nil {
		return solana.Instruction{}, err
	}
	return solana.Instruction{ProgramID: p.ID, Data: le64(data(ixWithdraw, 8), amount), Accounts: []solana.AccountMeta{
		signer(owner, false), ro(pass), ro(mint), rw(ownerToken), rw(vault), ro(solana.TokenProgram),
	}}, nil
}

// Revoke ends the pass for good.
func (p Program) Revoke(owner, pass solana.PublicKey) solana.Instruction {
	return solana.Instruction{ProgramID: p.ID, Data: data(ixRevoke, 0), Accounts: []solana.AccountMeta{signer(owner, false), rw(pass)}}
}

// ClosePass returns the vault's tokens to the owner and closes the vault and
// the pass.
func (p Program) ClosePass(owner, pass, mint, ownerToken solana.PublicKey) (solana.Instruction, error) {
	vault, err := Vault(pass, mint)
	if err != nil {
		return solana.Instruction{}, err
	}
	return solana.Instruction{ProgramID: p.ID, Data: data(ixClosePass, 0), Accounts: []solana.AccountMeta{
		signer(owner, true), rw(pass), ro(mint), rw(ownerToken), rw(vault), ro(solana.TokenProgram),
	}}, nil
}

// CreateATAIdempotent makes owner's associated token account for mint if it
// doesn't exist, paid by payer.
func CreateATAIdempotent(payer, owner, mint solana.PublicKey) (solana.Instruction, error) {
	ata, err := solana.AssociatedTokenAddress(owner, mint, solana.TokenProgram)
	if err != nil {
		return solana.Instruction{}, err
	}
	return solana.Instruction{ProgramID: solana.AssociatedTokenProgram, Data: []byte{1}, Accounts: []solana.AccountMeta{
		signer(payer, true), rw(ata), ro(owner), ro(mint), ro(solana.SystemProgram), ro(solana.TokenProgram),
	}}, nil
}

// errorNames are the program's own errors (error.rs), from 6000.
var errorNames = []string{
	"PassRevoked", "PassFrozen", "PassExpired", "ZeroAmount", "OverPerCallCap", "OverBudget",
	"OverWindowCap", "InvalidTerms", "InvalidAgent", "InvalidDestination", "AlreadyRevoked",
	"Overflow", "RefundOverSpent",
}

// ErrorName names a custom error code the program returned, or "" when it
// isn't one of the program's.
func ErrorName(code uint32) string {
	if code >= 6000 && int(code-6000) < len(errorNames) {
		return errorNames[code-6000]
	}
	return ""
}
