package paychan

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

// Status is a channel's state in the program's state machine.
type Status uint8

const (
	StatusOpen        Status = 0
	StatusSealed      Status = 1
	StatusClosing     Status = 2
	StatusDistributed Status = 3
)

func (s Status) String() string {
	switch s {
	case StatusOpen:
		return "open"
	case StatusSealed:
		return "sealed"
	case StatusClosing:
		return "closing"
	case StatusDistributed:
		return "distributed"
	}
	return fmt.Sprintf("status(%d)", uint8(s))
}

// ChannelSize is the channel account's fixed size.
const ChannelSize = 256

// Channel is a decoded channel account. Its fields, not the escrow's token
// balance, are what the program treats as authoritative.
type Channel struct {
	Version          uint8
	Status           Status
	Salt             uint64
	Deposit          uint64
	Settled          uint64
	PayoutWatermark  uint64
	ClosureStartedAt int64
	PayerWithdrawnAt int64
	GracePeriod      uint32
	DistributionHash [32]byte
	Payer            solana.PublicKey
	Payee            solana.PublicKey
	AuthorizedSigner solana.PublicKey
	Mint             solana.PublicKey
	RentPayer        solana.PublicKey
	OpenSlot         uint64
}

// DecodeChannel reads a channel account's data.
func DecodeChannel(b []byte) (*Channel, error) {
	if len(b) != ChannelSize {
		return nil, fmt.Errorf("paychan: a channel account is %d bytes, not %d", ChannelSize, len(b))
	}
	if b[0] != 1 {
		return nil, errors.New("paychan: not a live channel account")
	}
	le := binary.LittleEndian
	c := &Channel{
		Version: b[1], Status: Status(b[3]),
		Salt: le.Uint64(b[4:]), Deposit: le.Uint64(b[12:]), Settled: le.Uint64(b[20:]), PayoutWatermark: le.Uint64(b[28:]),
		ClosureStartedAt: int64(le.Uint64(b[36:])), PayerWithdrawnAt: int64(le.Uint64(b[44:])), GracePeriod: le.Uint32(b[52:]),
		OpenSlot: le.Uint64(b[248:]),
	}
	copy(c.DistributionHash[:], b[56:88])
	copy(c.Payer[:], b[88:120])
	copy(c.Payee[:], b[120:152])
	copy(c.AuthorizedSigner[:], b[152:184])
	copy(c.Mint[:], b[184:216])
	copy(c.RentPayer[:], b[216:248])
	return c, nil
}

// Refundable is what the payer would get back if the channel closed now.
func (c *Channel) Refundable() uint64 {
	if c.Settled >= c.Deposit {
		return 0
	}
	return c.Deposit - c.Settled
}

// VoucherSize is the signed voucher message's size.
const VoucherSize = 50

// VoucherMessage is the 50 bytes an authorized signer signs: magic "V"+v1,
// the channel, the cumulative amount and an expiry (zero for none).
func VoucherMessage(channel solana.PublicKey, cumulative uint64, expiresAt int64) []byte {
	m := make([]byte, 0, VoucherSize)
	m = append(m, 0x56, 0x01)
	m = append(m, channel[:]...)
	m = binary.LittleEndian.AppendUint64(m, cumulative)
	m = binary.LittleEndian.AppendUint64(m, uint64(expiresAt))
	return m
}

// Voucher is a signed cumulative authorization.
type Voucher struct {
	Channel    solana.PublicKey
	Cumulative uint64
	ExpiresAt  int64
	Signer     solana.PublicKey
	Signature  [64]byte
}

// SignVoucher signs a voucher with the channel's authorized signer.
func SignVoucher(k *solana.Keypair, channel solana.PublicKey, cumulative uint64, expiresAt time.Time) Voucher {
	var exp int64
	if !expiresAt.IsZero() {
		exp = expiresAt.Unix()
	}
	msg := VoucherMessage(channel, cumulative, exp)
	return Voucher{Channel: channel, Cumulative: cumulative, ExpiresAt: exp, Signer: k.PublicKey(), Signature: k.Sign(msg)}
}

// Verify checks the voucher's signature.
func (v Voucher) Verify() bool {
	return ed25519.Verify(v.Signer[:], VoucherMessage(v.Channel, v.Cumulative, v.ExpiresAt), v.Signature[:])
}

// Instruction is the canonical Ed25519 precompile instruction that must
// immediately precede settle or settle_and_seal.
func (v Voucher) Instruction() (solana.Instruction, error) {
	return solana.Ed25519Verify(v.Signer, v.Signature, VoucherMessage(v.Channel, v.Cumulative, v.ExpiresAt))
}
