package solana

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
)

// Message is a compiled v0 transaction message with static account keys only
// (no address lookup tables): every account an instruction touches is named in
// the message, which is what an x402 sponsor needs to inspect.
type Message struct {
	NumRequiredSignatures       uint8
	NumReadonlySignedAccounts   uint8
	NumReadonlyUnsignedAccounts uint8
	AccountKeys                 []PublicKey
	RecentBlockhash             [32]byte
	Instructions                []CompiledInstruction
}

// CompiledInstruction refers to accounts by index into Message.AccountKeys.
type CompiledInstruction struct {
	ProgramIDIndex uint8
	AccountIndexes []uint8
	Data           []byte
}

// Transaction is a message and its signatures, one per required signer in
// account order. A signature of 64 zero bytes is a slot not signed yet.
type Transaction struct {
	Signatures [][64]byte
	Message    Message
}

// CompileV0 turns instructions into a message with `feePayer` first.
//
// Accounts are ordered the way the runtime requires: signers before
// non-signers, and within each, writable before read-only. Where the same
// account appears more than once its flags are merged. Program IDs are
// read-only non-signer accounts.
func CompileV0(feePayer PublicKey, instrs []Instruction, blockhash [32]byte) (*Message, error) {
	type flags struct{ signer, writable bool }
	order := []PublicKey{feePayer}
	meta := map[PublicKey]*flags{feePayer: {signer: true, writable: true}}
	touch := func(pk PublicKey, signer, writable bool) {
		f, ok := meta[pk]
		if !ok {
			f = &flags{}
			meta[pk] = f
			order = append(order, pk)
		}
		f.signer = f.signer || signer
		f.writable = f.writable || writable
	}
	for _, in := range instrs {
		for _, a := range in.Accounts {
			touch(a.Pubkey, a.IsSigner, a.IsWritable)
		}
		touch(in.ProgramID, false, false)
	}
	var sw, sr, nw, nr []PublicKey
	for _, pk := range order {
		f := meta[pk]
		switch {
		case f.signer && f.writable:
			sw = append(sw, pk)
		case f.signer:
			sr = append(sr, pk)
		case f.writable:
			nw = append(nw, pk)
		default:
			nr = append(nr, pk)
		}
	}
	keys := append(append(append(append([]PublicKey{}, sw...), sr...), nw...), nr...)
	if len(keys) > 64 {
		return nil, errors.New("solana: too many accounts for one transaction")
	}
	idx := make(map[PublicKey]uint8, len(keys))
	for i, k := range keys {
		idx[k] = uint8(i)
	}
	m := &Message{
		NumRequiredSignatures: uint8(len(sw) + len(sr)), NumReadonlySignedAccounts: uint8(len(sr)),
		NumReadonlyUnsignedAccounts: uint8(len(nr)), AccountKeys: keys, RecentBlockhash: blockhash,
	}
	for _, in := range instrs {
		ci := CompiledInstruction{ProgramIDIndex: idx[in.ProgramID], Data: in.Data}
		for _, a := range in.Accounts {
			ci.AccountIndexes = append(ci.AccountIndexes, idx[a.Pubkey])
		}
		m.Instructions = append(m.Instructions, ci)
	}
	return m, nil
}

// shortvec appends a Solana "compact-u16" length.
func shortvec(buf []byte, n int) []byte {
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n == 0 {
			return append(buf, b)
		}
		buf = append(buf, b|0x80)
	}
}

// Serialize writes the v0 message wire format.
func (m *Message) Serialize() []byte {
	buf := []byte{0x80, m.NumRequiredSignatures, m.NumReadonlySignedAccounts, m.NumReadonlyUnsignedAccounts}
	buf = shortvec(buf, len(m.AccountKeys))
	for _, k := range m.AccountKeys {
		buf = append(buf, k[:]...)
	}
	buf = append(buf, m.RecentBlockhash[:]...)
	buf = shortvec(buf, len(m.Instructions))
	for _, in := range m.Instructions {
		buf = append(buf, in.ProgramIDIndex)
		buf = shortvec(buf, len(in.AccountIndexes))
		buf = append(buf, in.AccountIndexes...)
		buf = shortvec(buf, len(in.Data))
		buf = append(buf, in.Data...)
	}
	return shortvec(buf, 0) // no address table lookups
}

// NewTransaction pairs a message with empty signature slots.
func NewTransaction(m *Message) *Transaction {
	return &Transaction{Message: *m, Signatures: make([][64]byte, m.NumRequiredSignatures)}
}

// PartialSign signs the message for one required signer, leaving the other
// slots (for x402, the sponsor's) empty.
func (t *Transaction) PartialSign(k *Keypair) error {
	pk := k.PublicKey()
	for i := 0; i < int(t.Message.NumRequiredSignatures) && i < len(t.Message.AccountKeys); i++ {
		if t.Message.AccountKeys[i] == pk {
			t.Signatures[i] = k.Sign(t.Message.Serialize())
			return nil
		}
	}
	return errors.New("solana: this key is not a required signer of the transaction")
}

// Serialize writes the transaction wire format.
func (t *Transaction) Serialize() []byte {
	buf := shortvec(nil, len(t.Signatures))
	for _, s := range t.Signatures {
		buf = append(buf, s[:]...)
	}
	return append(buf, t.Message.Serialize()...)
}

// Base64 is the form RPC nodes and x402 carry a transaction in.
func (t *Transaction) Base64() string { return base64.StdEncoding.EncodeToString(t.Serialize()) }

// SignerSignature returns the signature slot for a required signer, and
// whether that slot has been signed.
func (t *Transaction) SignerSignature(pk PublicKey) ([64]byte, bool) {
	for i := 0; i < int(t.Message.NumRequiredSignatures) && i < len(t.Message.AccountKeys); i++ {
		if t.Message.AccountKeys[i] == pk {
			return t.Signatures[i], t.Signatures[i] != [64]byte{}
		}
	}
	return [64]byte{}, false
}

// VerifySignature checks one signer's signature over the message.
func (t *Transaction) VerifySignature(pk PublicKey) bool {
	sig, ok := t.SignerSignature(pk)
	return ok && ed25519.Verify(pk[:], t.Message.Serialize(), sig[:])
}

// --- decoding (tests, the dry-run tool, and sanity checks on what we sign) ---

type reader struct {
	b   []byte
	off int
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || n < 0 || r.off+n > len(r.b) {
		r.err = errors.New("solana: transaction is truncated")
		return nil
	}
	out := r.b[r.off : r.off+n]
	r.off += n
	return out
}

func (r *reader) byte() byte {
	if b := r.take(1); b != nil {
		return b[0]
	}
	return 0
}

func (r *reader) shortvec() int {
	n, shift := 0, 0
	for i := 0; i < 3; i++ {
		b := r.byte()
		n |= int(b&0x7f) << shift
		if b&0x80 == 0 {
			return n
		}
		shift += 7
	}
	r.err = errors.New("solana: bad compact length")
	return 0
}

// DecodeTransaction reads a serialized transaction: v0 or legacy, with static
// account keys. A message that uses address lookup tables is refused, since
// it can't be read without them.
func DecodeTransaction(raw []byte) (*Transaction, error) {
	r := &reader{b: raw}
	nsig := r.shortvec()
	t := &Transaction{}
	for i := 0; i < nsig && r.err == nil; i++ {
		var s [64]byte
		copy(s[:], r.take(64))
		t.Signatures = append(t.Signatures, s)
	}
	first := r.byte()
	versioned := first&0x80 != 0
	if versioned {
		if v := first & 0x7f; v != 0 {
			return nil, fmt.Errorf("solana: unsupported transaction version %d", v)
		}
		first = r.byte()
	}
	m := &t.Message
	m.NumRequiredSignatures, m.NumReadonlySignedAccounts, m.NumReadonlyUnsignedAccounts = first, r.byte(), r.byte()
	nkeys := r.shortvec()
	for i := 0; i < nkeys && r.err == nil; i++ {
		var k PublicKey
		copy(k[:], r.take(32))
		m.AccountKeys = append(m.AccountKeys, k)
	}
	copy(m.RecentBlockhash[:], r.take(32))
	nins := r.shortvec()
	for i := 0; i < nins && r.err == nil; i++ {
		ci := CompiledInstruction{ProgramIDIndex: r.byte()}
		ci.AccountIndexes = append([]uint8{}, r.take(r.shortvec())...)
		ci.Data = append([]byte{}, r.take(r.shortvec())...)
		m.Instructions = append(m.Instructions, ci)
	}
	if versioned && r.err == nil {
		if lookups := r.shortvec(); lookups != 0 {
			return nil, errors.New("solana: transactions with address lookup tables are not supported")
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.off != len(raw) {
		return nil, errors.New("solana: trailing bytes after the transaction")
	}
	if len(t.Signatures) != int(m.NumRequiredSignatures) {
		return nil, errors.New("solana: signature count doesn't match the message header")
	}
	for _, ci := range m.Instructions {
		if int(ci.ProgramIDIndex) >= len(m.AccountKeys) {
			return nil, errors.New("solana: an instruction names a program that isn't in the message")
		}
		for _, ai := range ci.AccountIndexes {
			if int(ai) >= len(m.AccountKeys) {
				return nil, errors.New("solana: an instruction names an account that isn't in the message")
			}
		}
	}
	return t, nil
}

// DecodeTransactionBase64 reads a base64 transaction.
func DecodeTransactionBase64(s string) (*Transaction, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, errors.New("solana: the transaction isn't base64")
	}
	return DecodeTransaction(raw)
}

// IsSigner reports whether the account at index i must sign.
func (m *Message) IsSigner(i int) bool { return i < int(m.NumRequiredSignatures) }

// ProgramOf returns the program an instruction calls.
func (m *Message) ProgramOf(ci CompiledInstruction) PublicKey {
	return m.AccountKeys[ci.ProgramIDIndex]
}

// InstructionAccounts resolves an instruction's account indexes to keys.
func (m *Message) InstructionAccounts(ci CompiledInstruction) []PublicKey {
	out := make([]PublicKey, len(ci.AccountIndexes))
	for i, ai := range ci.AccountIndexes {
		out[i] = m.AccountKeys[ai]
	}
	return out
}
