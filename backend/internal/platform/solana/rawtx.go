package solana

import (
	"errors"
	"fmt"
)

// RawTransaction is what can be read of a serialized transaction without
// resolving its address lookup tables: the signature slots, the header and the
// static account keys. That is enough to know who pays and who must sign, and
// to sign the message untouched, which is all that is needed to sign a
// transaction somebody else built (a swap from an aggregator) after checking
// what it would do by simulating it. The instructions are not read: whatever
// they are, they are judged by their effect, never by their look.
//
// Only legacy and version 0 messages are understood. A version 1 message has
// another layout and is refused rather than guessed at.
type RawTransaction struct {
	raw []byte
	// sigStart is where the first signature slot begins; msgStart where the
	// message does.
	sigStart, msgStart int

	Version                     int // 0 for legacy (unversioned) and v0 alike
	Versioned                   bool
	NumSignatures               int
	NumRequiredSignatures       int
	NumReadonlySignedAccounts   int
	NumReadonlyUnsignedAccounts int
	// AccountKeys are the static keys. A v0 message may name more through
	// lookup tables, which are not resolved here.
	AccountKeys []PublicKey
}

// ParseRawTransaction reads the parts of a serialized transaction that signing
// needs. It refuses what it can't be sure of.
func ParseRawTransaction(raw []byte) (*RawTransaction, error) {
	r := &reader{b: raw}
	nsig := r.shortvec()
	if r.err != nil {
		return nil, r.err
	}
	t := &RawTransaction{raw: raw, NumSignatures: nsig, sigStart: r.off}
	if nsig <= 0 || nsig > 127 {
		return nil, errors.New("solana: a transaction has between 1 and 127 signatures")
	}
	r.take(64 * nsig)
	t.msgStart = r.off
	first := r.byte()
	if first&0x80 != 0 {
		t.Versioned = true
		if v := int(first & 0x7f); v != 0 {
			return nil, fmt.Errorf("solana: transaction version %d is not supported (version 0 and legacy are)", v)
		}
		first = r.byte()
	}
	t.NumRequiredSignatures = int(first)
	t.NumReadonlySignedAccounts = int(r.byte())
	t.NumReadonlyUnsignedAccounts = int(r.byte())
	nkeys := r.shortvec()
	if r.err != nil {
		return nil, r.err
	}
	if nkeys <= 0 || nkeys > 256 {
		return nil, errors.New("solana: a transaction names between 1 and 256 static accounts")
	}
	for i := 0; i < nkeys; i++ {
		var k PublicKey
		copy(k[:], r.take(32))
		t.AccountKeys = append(t.AccountKeys, k)
	}
	if r.err != nil {
		return nil, r.err
	}
	switch {
	case t.NumRequiredSignatures != nsig:
		return nil, errors.New("solana: the signature count doesn't match the message header")
	case t.NumRequiredSignatures > len(t.AccountKeys):
		return nil, errors.New("solana: the message requires more signers than it has accounts")
	}
	return t, nil
}

// FeePayer is the account that pays the fee: the first static key.
func (t *RawTransaction) FeePayer() PublicKey { return t.AccountKeys[0] }

// Message is the bytes a signer signs.
func (t *RawTransaction) Message() []byte { return t.raw[t.msgStart:] }

// Signers are the accounts that must sign, in the order of the signature slots.
func (t *RawTransaction) Signers() []PublicKey { return t.AccountKeys[:t.NumRequiredSignatures] }

// SignerIndex is the signature slot of a required signer.
func (t *RawTransaction) SignerIndex(pk PublicKey) (int, bool) {
	for i, k := range t.Signers() {
		if k == pk {
			return i, true
		}
	}
	return 0, false
}

// Sign returns the transaction with the key's signature in its slot, the other
// slots as they were, and the signature itself: the transaction's identity on
// chain when the key is the fee payer. The receiver is not changed.
func (t *RawTransaction) Sign(k *Keypair) (signed []byte, signature [64]byte, err error) {
	i, ok := t.SignerIndex(k.PublicKey())
	if !ok {
		return nil, signature, errors.New("solana: this key is not a required signer of the transaction")
	}
	signature = k.Sign(t.Message())
	signed = append([]byte(nil), t.raw...)
	copy(signed[t.sigStart+64*i:], signature[:])
	return signed, signature, nil
}
