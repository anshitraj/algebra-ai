package solana

import (
	"encoding/binary"
	"errors"
)

// Well-known programs.
var (
	SystemProgram          = PublicKey{}
	TokenProgram           = MustPublicKey("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA")
	Token2022Program       = MustPublicKey("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	AssociatedTokenProgram = MustPublicKey("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	ComputeBudgetProgram   = MustPublicKey("ComputeBudget111111111111111111111111111111")
	MemoProgram            = MustPublicKey("MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr")
)

// Genesis hashes identify a cluster; an RPC endpoint that reports another one
// is not the cluster the operator thinks it is.
const (
	MainnetGenesisHash = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
	DevnetGenesisHash  = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"
)

// AccountMeta is an account an instruction touches.
type AccountMeta struct {
	Pubkey     PublicKey
	IsSigner   bool
	IsWritable bool
}

// Instruction is one call into a program.
type Instruction struct {
	ProgramID PublicKey
	Accounts  []AccountMeta
	Data      []byte
}

// AssociatedTokenAddress is the address of the token account that holds
// `mint` for `owner` under a token program: a program derived address.
func AssociatedTokenAddress(owner, mint, tokenProgram PublicKey) (PublicKey, error) {
	pk, _, err := FindProgramAddress([][]byte{owner[:], tokenProgram[:], mint[:]}, AssociatedTokenProgram)
	return pk, err
}

// SetComputeUnitLimit caps the compute a transaction may use.
func SetComputeUnitLimit(units uint32) Instruction {
	data := make([]byte, 5)
	data[0] = 2
	binary.LittleEndian.PutUint32(data[1:], units)
	return Instruction{ProgramID: ComputeBudgetProgram, Data: data}
}

// SetComputeUnitPrice sets the priority fee in micro-lamports per compute unit.
func SetComputeUnitPrice(microLamports uint64) Instruction {
	data := make([]byte, 9)
	data[0] = 3
	binary.LittleEndian.PutUint64(data[1:], microLamports)
	return Instruction{ProgramID: ComputeBudgetProgram, Data: data}
}

// TransferChecked moves `amount` of `mint` from one token account to another,
// signed by `authority`, and fails if the mint's decimals aren't `decimals`.
// Unlike a plain transfer it can't be redirected to a different token by a
// wrong account, which is why x402 requires it.
func TransferChecked(tokenProgram, source, mint, destination, authority PublicKey, amount uint64, decimals uint8) Instruction {
	data := make([]byte, 10)
	data[0] = 12
	binary.LittleEndian.PutUint64(data[1:], amount)
	data[9] = decimals
	return Instruction{
		ProgramID: tokenProgram,
		Accounts: []AccountMeta{
			{Pubkey: source, IsWritable: true},
			{Pubkey: mint},
			{Pubkey: destination, IsWritable: true},
			{Pubkey: authority, IsSigner: true},
		},
		Data: data,
	}
}

// Memo attaches UTF-8 text to a transaction.
func Memo(text string) (Instruction, error) {
	if len(text) == 0 || len(text) > 566 {
		return Instruction{}, errors.New("solana: a memo is 1 to 566 bytes")
	}
	return Instruction{ProgramID: MemoProgram, Data: []byte(text)}, nil
}
