package solana

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

// accountJSON is an account as a node returns it with base64 data.
type accountJSON struct {
	Lamports   uint64   `json:"lamports"`
	Owner      string   `json:"owner"`
	Data       []string `json:"data"`
	Executable bool     `json:"executable"`
}

func (a *accountJSON) info() (*AccountInfo, error) {
	if a == nil {
		return nil, nil
	}
	owner, err := ParsePublicKey(a.Owner)
	if err != nil {
		return nil, err
	}
	info := &AccountInfo{Lamports: a.Lamports, Owner: owner, Executable: a.Executable}
	if len(a.Data) > 0 {
		if info.Data, err = base64.StdEncoding.DecodeString(a.Data[0]); err != nil {
			return nil, errors.New("solana: the node returned malformed account data")
		}
	}
	return info, nil
}

// maxMultiple is the most accounts one getMultipleAccounts call may ask for.
const maxMultiple = 100

// GetMultipleAccounts reads several accounts in one call. The answer lines up
// with the keys; an account that doesn't exist is nil.
func (r *RPC) GetMultipleAccounts(ctx context.Context, keys []PublicKey, commitment string) ([]*AccountInfo, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if len(keys) > maxMultiple {
		return nil, fmt.Errorf("solana: at most %d accounts can be read at once", maxMultiple)
	}
	addrs := make([]string, len(keys))
	for i, k := range keys {
		addrs[i] = k.String()
	}
	var out struct {
		Value []*accountJSON `json:"value"`
	}
	if err := r.call(ctx, "getMultipleAccounts", []any{addrs, map[string]any{"encoding": "base64", "commitment": commitment}}, &out); err != nil {
		return nil, err
	}
	if len(out.Value) != len(keys) {
		return nil, errors.New("solana: the node answered for a different number of accounts than were asked for")
	}
	infos := make([]*AccountInfo, len(keys))
	for i, a := range out.Value {
		var err error
		if infos[i], err = a.info(); err != nil {
			return nil, err
		}
	}
	return infos, nil
}

// OwnedTokenAccount is a token account found by its owner.
type OwnedTokenAccount struct {
	Address PublicKey
	Info    AccountInfo
}

// GetTokenAccountsByOwner lists the token accounts an owner has under one
// token program.
func (r *RPC) GetTokenAccountsByOwner(ctx context.Context, owner, program PublicKey, commitment string) ([]OwnedTokenAccount, error) {
	var out struct {
		Value []struct {
			Pubkey  string      `json:"pubkey"`
			Account accountJSON `json:"account"`
		} `json:"value"`
	}
	params := []any{owner.String(), map[string]any{"programId": program.String()}, map[string]any{"encoding": "base64", "commitment": commitment}}
	if err := r.call(ctx, "getTokenAccountsByOwner", params, &out); err != nil {
		return nil, err
	}
	accounts := make([]OwnedTokenAccount, 0, len(out.Value))
	for _, v := range out.Value {
		addr, err := ParsePublicKey(v.Pubkey)
		if err != nil {
			return nil, err
		}
		info, err := v.Account.info()
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, OwnedTokenAccount{Address: addr, Info: *info})
	}
	return accounts, nil
}

// TokenAccountSize is the length of a plain SPL token account. Token-2022
// accounts may carry extensions after it; the first 165 bytes are the same.
const TokenAccountSize = 165

// Token account states.
const (
	TokenAccountUninitialized byte = iota
	TokenAccountInitialized
	TokenAccountFrozen
)

// TokenAccount is an SPL token account, every field of it: a swap is judged by
// whether any of them, not only the balance, changed in a way it shouldn't.
type TokenAccount struct {
	Mint, Owner     PublicKey
	Amount          uint64
	Delegate        *PublicKey
	State           byte
	IsNative        *uint64
	DelegatedAmount uint64
	CloseAuthority  *PublicKey
}

// ParseTokenAccount reads an SPL token account's data.
func ParseTokenAccount(data []byte) (*TokenAccount, error) {
	if len(data) < TokenAccountSize {
		return nil, errors.New("solana: not a token account: too short")
	}
	t := &TokenAccount{
		Amount:          binary.LittleEndian.Uint64(data[64:72]),
		State:           data[108],
		DelegatedAmount: binary.LittleEndian.Uint64(data[121:129]),
	}
	copy(t.Mint[:], data[0:32])
	copy(t.Owner[:], data[32:64])
	if binary.LittleEndian.Uint32(data[72:76]) == 1 {
		var d PublicKey
		copy(d[:], data[76:108])
		t.Delegate = &d
	}
	if binary.LittleEndian.Uint32(data[109:113]) == 1 {
		n := binary.LittleEndian.Uint64(data[113:121])
		t.IsNative = &n
	}
	if binary.LittleEndian.Uint32(data[129:133]) == 1 {
		var c PublicKey
		copy(c[:], data[133:165])
		t.CloseAuthority = &c
	}
	return t, nil
}

// SameExceptAmount reports whether two states of a token account differ in
// nothing but the amount: same mint, owner, delegate, state and close
// authority. A delegate or an owner that changed would let someone else take
// the balance later, which no balance check can see.
func (t *TokenAccount) SameExceptAmount(o *TokenAccount) bool {
	if t.Mint != o.Mint || t.Owner != o.Owner || t.State != o.State || t.DelegatedAmount != o.DelegatedAmount {
		return false
	}
	return samePtr(t.Delegate, o.Delegate) && samePtr(t.CloseAuthority, o.CloseAuthority) && sameNative(t.IsNative, o.IsNative)
}

func samePtr(a, b *PublicKey) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameNative(a, b *uint64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Encode is the account's 165-byte form, for tests.
func (t *TokenAccount) Encode() []byte {
	data := make([]byte, TokenAccountSize)
	copy(data[0:32], t.Mint[:])
	copy(data[32:64], t.Owner[:])
	binary.LittleEndian.PutUint64(data[64:72], t.Amount)
	if t.Delegate != nil {
		binary.LittleEndian.PutUint32(data[72:76], 1)
		copy(data[76:108], t.Delegate[:])
	}
	data[108] = t.State
	if t.IsNative != nil {
		binary.LittleEndian.PutUint32(data[109:113], 1)
		binary.LittleEndian.PutUint64(data[113:121], *t.IsNative)
	}
	binary.LittleEndian.PutUint64(data[121:129], t.DelegatedAmount)
	if t.CloseAuthority != nil {
		binary.LittleEndian.PutUint32(data[129:133], 1)
		copy(data[133:165], t.CloseAuthority[:])
	}
	return data
}
