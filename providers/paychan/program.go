// Package paychan is a client for the Solana Foundation's payment-channels
// program (github.com/solana-foundation/payment-channels): the on-chain escrow
// behind x402's "upto" scheme and MPP sessions.
//
// A channel escrows a ceiling once; the provider later settles the actual
// amount from an Ed25519-signed cumulative voucher, and the payer gets the rest
// back in the same final instruction. For an agent that pays per call, that
// turns "authorize exactly X before you know the cost" into "authorize at most
// X, pay what it really cost": the shape metered APIs (LLM tokens, bytes,
// compute) need, with the refund enforced by the program rather than promised
// by the provider.
//
// This package builds the program's instructions and reads its accounts. It
// signs nothing and sends nothing by itself.
package paychan

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// ProgramID is the canonical deployment, the same address on mainnet and
// devnet. The x402 upto spec forbids trusting a program id a server sends.
var ProgramID = solana.MustPublicKey("CHNLxYvVA28MJP9PrFuDXccuoGXAx7jBacfLEkahyGsX")

// Treasury owners receive rounding dust at the final distribute; the program
// validates the treasury token account against a per-cluster constant. The
// devnet owner was read from devnet distribute transactions (the program's
// source leaves it a placeholder).
var treasuryOwners = map[string]solana.PublicKey{
	chain.Solana:       solana.MustPublicKey("Cs2zdfUNonRdRGsiZUQQLdTxzxVvJZmgiX2mpLYKuEqP"),
	chain.SolanaDevnet: solana.MustPublicKey("4zTeC5mVqWLruDexgU2mV66p9t5vCA9JyiZqdGDUspap"),
}

// TreasuryOwner is the cluster's treasury owner.
func TreasuryOwner(network string) (solana.PublicKey, bool) {
	pk, ok := treasuryOwners[chain.NormalizeNetwork(network)]
	return pk, ok
}

// Instruction discriminators.
const (
	ixOpen          = 1
	ixSettle        = 2
	ixTopUp         = 3
	ixSettleAndSeal = 4
	ixRequestClose  = 5
	ixSeal          = 6
	ixDistribute    = 7
	ixWithdrawPayer = 8
	ixReclaim       = 9
)

// MaxRecipients bounds a distribution.
const MaxRecipients = 32

// OpenSlotWindow is how many slots after its open_slot an open may land.
const OpenSlotWindow = 1_500

// EventAuthority is the program's event-authority PDA.
func EventAuthority() solana.PublicKey {
	pk, _, err := solana.FindProgramAddress([][]byte{[]byte("event_authority")}, ProgramID)
	if err != nil {
		panic(err) // a fixed seed always has a program address
	}
	return pk
}

// Entry is one share of the settled amount: bps of it to recipient. The
// payee keeps 10000 minus the sum.
type Entry struct {
	Recipient solana.PublicKey
	Bps       uint16
}

// preimage is the distribution's wire form: count u32 LE, then 34-byte
// entries. The program stores its SHA-256 at open and checks it at distribute.
func preimage(es []Entry) ([]byte, error) {
	if len(es) > MaxRecipients {
		return nil, fmt.Errorf("paychan: at most %d recipients", MaxRecipients)
	}
	var sum uint32
	seen := map[solana.PublicKey]bool{}
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(es)))
	for _, e := range es {
		if e.Bps == 0 {
			return nil, errors.New("paychan: a recipient's share must be more than zero")
		}
		if seen[e.Recipient] {
			return nil, errors.New("paychan: a recipient appears twice")
		}
		seen[e.Recipient] = true
		sum += uint32(e.Bps)
		b = append(b, e.Recipient[:]...)
		b = binary.LittleEndian.AppendUint16(b, e.Bps)
	}
	if sum > 10_000 {
		return nil, errors.New("paychan: shares add up to more than 100%")
	}
	return b, nil
}

// ChannelAddress derives a channel's PDA.
func ChannelAddress(payer, payee, mint, authorizedSigner solana.PublicKey, salt, openSlot uint64) (solana.PublicKey, error) {
	pk, _, err := solana.FindProgramAddress([][]byte{
		[]byte("channel"), payer[:], payee[:], mint[:], authorizedSigner[:],
		binary.LittleEndian.AppendUint64(nil, salt), binary.LittleEndian.AppendUint64(nil, openSlot),
	}, ProgramID)
	return pk, err
}

// OpenParams describe a channel to open.
type OpenParams struct {
	Payer, RentPayer, Payee, Mint, AuthorizedSigner solana.PublicKey
	// TokenProgram is the mint's token program; zero means SPL Token.
	TokenProgram solana.PublicKey
	Salt         uint64
	Deposit      uint64
	GracePeriod  uint32
	OpenSlot     uint64
	Recipients   []Entry
}

// Open builds the open instruction and returns the channel it creates.
func Open(p OpenParams) (solana.Instruction, solana.PublicKey, error) {
	if p.Deposit == 0 || p.GracePeriod == 0 {
		return solana.Instruction{}, solana.PublicKey{}, errors.New("paychan: a channel needs a deposit and a grace period")
	}
	tp := p.TokenProgram
	if tp.IsZero() {
		tp = solana.TokenProgram
	}
	ch, err := ChannelAddress(p.Payer, p.Payee, p.Mint, p.AuthorizedSigner, p.Salt, p.OpenSlot)
	if err != nil {
		return solana.Instruction{}, solana.PublicKey{}, err
	}
	payerATA, err := solana.AssociatedTokenAddress(p.Payer, p.Mint, tp)
	if err != nil {
		return solana.Instruction{}, solana.PublicKey{}, err
	}
	escrow, err := solana.AssociatedTokenAddress(ch, p.Mint, tp)
	if err != nil {
		return solana.Instruction{}, solana.PublicKey{}, err
	}
	pre, err := preimage(p.Recipients)
	if err != nil {
		return solana.Instruction{}, solana.PublicKey{}, err
	}
	data := []byte{ixOpen}
	data = binary.LittleEndian.AppendUint64(data, p.Salt)
	data = binary.LittleEndian.AppendUint64(data, p.Deposit)
	data = binary.LittleEndian.AppendUint32(data, p.GracePeriod)
	data = binary.LittleEndian.AppendUint64(data, p.OpenSlot)
	data = append(data, pre...)
	return solana.Instruction{ProgramID: ProgramID, Data: data, Accounts: []solana.AccountMeta{
		{Pubkey: p.Payer, IsSigner: true, IsWritable: true},
		{Pubkey: p.RentPayer, IsSigner: true, IsWritable: true},
		{Pubkey: p.Payee},
		{Pubkey: p.Mint},
		{Pubkey: p.AuthorizedSigner},
		{Pubkey: ch, IsWritable: true},
		{Pubkey: payerATA, IsWritable: true},
		{Pubkey: escrow, IsWritable: true},
		{Pubkey: tp},
		{Pubkey: solana.SystemProgram},
		{Pubkey: solana.RentSysvar},
		{Pubkey: solana.AssociatedTokenProgram},
		{Pubkey: EventAuthority()},
		{Pubkey: ProgramID},
	}}, ch, nil
}

// SettleAndSeal is the payee's cooperative close. With a voucher, the
// transaction must carry the voucher's Ed25519 instruction immediately before
// this one (see VoucherInstructions).
func SettleAndSeal(payee, channel solana.PublicKey, hasVoucher bool) solana.Instruction {
	v := byte(0)
	if hasVoucher {
		v = 1
	}
	return solana.Instruction{ProgramID: ProgramID, Data: []byte{ixSettleAndSeal, v}, Accounts: []solana.AccountMeta{
		{Pubkey: payee, IsSigner: true},
		{Pubkey: channel, IsWritable: true},
		{Pubkey: solana.InstructionsSysvar},
	}}
}

// Settle advances the settled watermark from a voucher (permissionless; the
// Ed25519 instruction must precede it).
func Settle(channel solana.PublicKey) solana.Instruction {
	return solana.Instruction{ProgramID: ProgramID, Data: []byte{ixSettle}, Accounts: []solana.AccountMeta{
		{Pubkey: channel, IsWritable: true},
		{Pubkey: solana.InstructionsSysvar},
	}}
}

// DistributeParams describe a payout.
type DistributeParams struct {
	Channel, Payer, RentPayer, Payee, Mint solana.PublicKey
	TokenProgram                           solana.PublicKey
	Network                                string
	Recipients                             []Entry
}

// Distribute pays the settled amount out by the committed shares and, once
// sealed, refunds the payer and closes the escrow.
func Distribute(p DistributeParams) (solana.Instruction, error) {
	tp := p.TokenProgram
	if tp.IsZero() {
		tp = solana.TokenProgram
	}
	treasury, ok := TreasuryOwner(p.Network)
	if !ok {
		return solana.Instruction{}, fmt.Errorf("paychan: no treasury owner known for %q", p.Network)
	}
	ata := func(owner solana.PublicKey) (solana.PublicKey, error) {
		return solana.AssociatedTokenAddress(owner, p.Mint, tp)
	}
	escrow, err := ata(p.Channel)
	if err != nil {
		return solana.Instruction{}, err
	}
	payerATA, err := ata(p.Payer)
	if err != nil {
		return solana.Instruction{}, err
	}
	payeeATA, err := ata(p.Payee)
	if err != nil {
		return solana.Instruction{}, err
	}
	treasuryATA, err := ata(treasury)
	if err != nil {
		return solana.Instruction{}, err
	}
	pre, err := preimage(p.Recipients)
	if err != nil {
		return solana.Instruction{}, err
	}
	accs := []solana.AccountMeta{
		{Pubkey: p.Channel, IsWritable: true},
		{Pubkey: p.Payer, IsWritable: true},
		{Pubkey: p.RentPayer, IsWritable: true},
		{Pubkey: escrow, IsWritable: true},
		{Pubkey: payerATA, IsWritable: true},
		{Pubkey: payeeATA, IsWritable: true},
		{Pubkey: treasuryATA, IsWritable: true},
		{Pubkey: p.Mint},
		{Pubkey: tp},
		{Pubkey: EventAuthority()},
		{Pubkey: ProgramID},
	}
	for _, e := range p.Recipients {
		r, err := ata(e.Recipient)
		if err != nil {
			return solana.Instruction{}, err
		}
		accs = append(accs, solana.AccountMeta{Pubkey: r, IsWritable: true})
	}
	return solana.Instruction{ProgramID: ProgramID, Data: append([]byte{ixDistribute}, pre...), Accounts: accs}, nil
}

// RequestClose is the payer's escape hatch: it starts the grace period after
// which anyone may seal the channel and the payer may withdraw the rest.
func RequestClose(payer, channel solana.PublicKey) solana.Instruction {
	return solana.Instruction{ProgramID: ProgramID, Data: []byte{ixRequestClose}, Accounts: []solana.AccountMeta{
		{Pubkey: payer, IsSigner: true},
		{Pubkey: channel, IsWritable: true},
	}}
}

// Seal locks a closing channel's watermark once its grace period is over
// (permissionless).
func Seal(channel solana.PublicKey) solana.Instruction {
	return solana.Instruction{ProgramID: ProgramID, Data: []byte{ixSeal}, Accounts: []solana.AccountMeta{{Pubkey: channel, IsWritable: true}}}
}

// WithdrawPayer returns a sealed channel's unsettled remainder to the payer.
func WithdrawPayer(payer, channel, mint, tokenProgram solana.PublicKey) (solana.Instruction, error) {
	if tokenProgram.IsZero() {
		tokenProgram = solana.TokenProgram
	}
	escrow, err := solana.AssociatedTokenAddress(channel, mint, tokenProgram)
	if err != nil {
		return solana.Instruction{}, err
	}
	payerATA, err := solana.AssociatedTokenAddress(payer, mint, tokenProgram)
	if err != nil {
		return solana.Instruction{}, err
	}
	return solana.Instruction{ProgramID: ProgramID, Data: []byte{ixWithdrawPayer}, Accounts: []solana.AccountMeta{
		{Pubkey: payer, IsSigner: true},
		{Pubkey: channel, IsWritable: true},
		{Pubkey: escrow, IsWritable: true},
		{Pubkey: payerATA, IsWritable: true},
		{Pubkey: mint},
		{Pubkey: tokenProgram},
	}}, nil
}

// Reclaim returns a distributed channel's rent to its rent payer
// (permissionless, once the open-slot window has passed).
func Reclaim(channel, rentPayer solana.PublicKey) solana.Instruction {
	return solana.Instruction{ProgramID: ProgramID, Data: []byte{ixReclaim}, Accounts: []solana.AccountMeta{
		{Pubkey: channel, IsWritable: true},
		{Pubkey: rentPayer, IsWritable: true},
	}}
}
