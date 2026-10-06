package solana

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
)

// Ed25519Program is the native signature-verification precompile.
var Ed25519Program = MustPublicKey("Ed25519SigVerify111111111111111111111111111")

// InstructionsSysvar lets a program read the other instructions of its
// transaction (how a program sees what the Ed25519 precompile verified).
var InstructionsSysvar = MustPublicKey("Sysvar1nstructions1111111111111111111111111")

// RentSysvar is the rent sysvar account.
var RentSysvar = MustPublicKey("SysvarRent111111111111111111111111111111111")

// Ed25519Verify builds the canonical single-signature Ed25519 precompile
// instruction: [num=1, pad=0, offsets(14), pubkey(32), signature(64), message],
// with every *_instruction_index set to u16::MAX ("this instruction"), the
// layout programs that read it through the instructions sysvar expect.
func Ed25519Verify(pub PublicKey, sig [64]byte, msg []byte) (Instruction, error) {
	const (
		header    = 2
		offsets   = 14
		pubOff    = header + offsets
		sigOff    = pubOff + 32
		msgOff    = sigOff + 64
		thisIndex = 0xFFFF
	)
	if len(msg) > 0xFFFF-msgOff {
		return Instruction{}, errors.New("solana: ed25519 message too long")
	}
	if !ed25519.Verify(pub[:], msg, sig[:]) {
		return Instruction{}, errors.New("solana: the signature doesn't verify; the precompile would fail the transaction")
	}
	data := make([]byte, msgOff+len(msg))
	data[0], data[1] = 1, 0
	le := binary.LittleEndian
	le.PutUint16(data[2:], sigOff)
	le.PutUint16(data[4:], thisIndex)
	le.PutUint16(data[6:], pubOff)
	le.PutUint16(data[8:], thisIndex)
	le.PutUint16(data[10:], msgOff)
	le.PutUint16(data[12:], uint16(len(msg)))
	le.PutUint16(data[14:], thisIndex)
	copy(data[pubOff:], pub[:])
	copy(data[sigOff:], sig[:])
	copy(data[msgOff:], msg)
	return Instruction{ProgramID: Ed25519Program, Data: data}, nil
}
