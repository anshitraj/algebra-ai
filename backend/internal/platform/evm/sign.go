package evm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// Signing, for Algebra's own EVM payer (providers/evmx402): an x402 payment
// on Base or Arc is a USDC transferWithAuthorization (EIP-3009) the payer signs
// as EIP-712 typed data, and pulling from an owner's vault is a transaction the
// payer sends after the owner signed a permit (EIP-2612). An owner's own key
// still never reaches this package.

// Key is a secp256k1 private key and its address.
type Key struct {
	priv *secp256k1.PrivateKey
	addr [20]byte
}

// GenerateKey makes a new key from the system's randomness.
func GenerateKey() (*Key, error) {
	b := make([]byte, 32)
	for {
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		if k, err := KeyFromBytes(b); err == nil {
			return k, nil
		}
	}
}

// KeyFromBytes reads a 32-byte private key.
func KeyFromBytes(b []byte) (*Key, error) {
	if len(b) != 32 {
		return nil, errors.New("evm: a private key is 32 bytes")
	}
	var s secp256k1.ModNScalar
	if overflow := s.SetByteSlice(b); overflow || s.IsZero() {
		return nil, errors.New("evm: the private key is out of range")
	}
	priv := secp256k1.NewPrivateKey(&s)
	return &Key{priv: priv, addr: addressOf(priv.PubKey())}, nil
}

// Bytes is the private key, for sealing. Never log it.
func (k *Key) Bytes() []byte { return k.priv.Serialize() }

// Address is the key's address.
func (k *Key) Address() [20]byte { return k.addr }

// Hex is the key's address in lower case.
func (k *Key) Hex() string { return AddressHex(k.addr) }

// Sign signs a 32-byte digest: r || s || v, with v 27 or 28.
func (k *Key) Sign(digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, errors.New("evm: a digest is 32 bytes")
	}
	compact := ecdsa.SignCompact(k.priv, digest, false)
	// decred: header (27 + recovery id), r, s.
	out := make([]byte, 65)
	copy(out, compact[1:])
	out[64] = compact[0]
	return out, nil
}

// Recover is the address that made a 65-byte r||s||v signature over digest.
func Recover(digest, sig []byte) ([20]byte, error) { return recover(digest, sig) }

// --- ABI words ---

// Word is a uint256 as 32 big-endian bytes. Negative or oversized values are
// an error, never wrapped.
func Word(v *big.Int) ([]byte, error) {
	if v == nil || v.Sign() < 0 || v.BitLen() > 256 {
		return nil, errors.New("evm: a uint256 is between 0 and 2^256-1")
	}
	out := make([]byte, 32)
	v.FillBytes(out)
	return out, nil
}

func mustWord(v *big.Int) []byte {
	w, err := Word(v)
	if err != nil {
		panic(err)
	}
	return w
}

// AddressWord is an address left-padded to 32 bytes.
func AddressWord(a [20]byte) []byte {
	out := make([]byte, 32)
	copy(out[12:], a[:])
	return out
}

// --- EIP-712 ---

var domainType = Keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))

// Domain is an EIP-712 domain with the four fields USDC uses.
type Domain struct {
	Name              string
	Version           string
	ChainID           *big.Int
	VerifyingContract [20]byte
}

// Separator is the domain's hash (what DOMAIN_SEPARATOR() returns on chain).
func (d Domain) Separator() []byte {
	return Keccak256(domainType, Keccak256([]byte(d.Name)), Keccak256([]byte(d.Version)), mustWord(d.ChainID), AddressWord(d.VerifyingContract))
}

// TypedDigest is what is signed: keccak256(0x19 0x01 || domain || struct).
func TypedDigest(d Domain, structHash []byte) []byte {
	return Keccak256([]byte{0x19, 0x01}, d.Separator(), structHash)
}

var transferAuthType = Keccak256([]byte("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"))

// TransferAuthorization is EIP-3009's transferWithAuthorization: from lets
// anyone move value to to, once (nonce), between validAfter and validBefore.
type TransferAuthorization struct {
	From, To                       [20]byte
	Value, ValidAfter, ValidBefore *big.Int
	Nonce                          [32]byte
}

// Hash is the authorization's EIP-712 struct hash.
func (a TransferAuthorization) Hash() ([]byte, error) {
	for _, v := range []*big.Int{a.Value, a.ValidAfter, a.ValidBefore} {
		if _, err := Word(v); err != nil {
			return nil, err
		}
	}
	return Keccak256(transferAuthType, AddressWord(a.From), AddressWord(a.To), mustWord(a.Value), mustWord(a.ValidAfter), mustWord(a.ValidBefore), a.Nonce[:]), nil
}

var permitType = Keccak256([]byte("Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)"))

// Permit is EIP-2612: owner lets spender transfer up to value of its tokens,
// signed off chain, usable once (nonce) until deadline.
type Permit struct {
	Owner, Spender         [20]byte
	Value, Nonce, Deadline *big.Int
}

// Hash is the permit's EIP-712 struct hash.
func (p Permit) Hash() ([]byte, error) {
	for _, v := range []*big.Int{p.Value, p.Nonce, p.Deadline} {
		if _, err := Word(v); err != nil {
			return nil, err
		}
	}
	return Keccak256(permitType, AddressWord(p.Owner), AddressWord(p.Spender), mustWord(p.Value), mustWord(p.Nonce), mustWord(p.Deadline)), nil
}

// --- Calldata ---

func selector(sig string) []byte { return Keccak256([]byte(sig))[:4] }

// PermitCalldata is permit(owner, spender, value, deadline, v, r, s).
func PermitCalldata(p Permit, sig []byte) ([]byte, error) {
	if len(sig) != 65 {
		return nil, errors.New("evm: a signature is 65 bytes")
	}
	v := new(big.Int).SetUint64(uint64(sig[64]))
	out := selector("permit(address,address,uint256,uint256,uint8,bytes32,bytes32)")
	for _, w := range [][]byte{AddressWord(p.Owner), AddressWord(p.Spender), mustWord(p.Value), mustWord(p.Deadline), mustWord(v), sig[:32], sig[32:64]} {
		out = append(out, w...)
	}
	return out, nil
}

// TransferFromCalldata is transferFrom(from, to, value).
func TransferFromCalldata(from, to [20]byte, value *big.Int) []byte {
	out := selector("transferFrom(address,address,uint256)")
	out = append(out, AddressWord(from)...)
	out = append(out, AddressWord(to)...)
	return append(out, mustWord(value)...)
}

// TransferWithAuthorizationCalldata is transferWithAuthorization(from, to,
// value, validAfter, validBefore, nonce, v, r, s): what a seller submits.
func TransferWithAuthorizationCalldata(a TransferAuthorization, sig []byte) ([]byte, error) {
	if len(sig) != 65 {
		return nil, errors.New("evm: a signature is 65 bytes")
	}
	if _, err := a.Hash(); err != nil {
		return nil, err
	}
	v := new(big.Int).SetUint64(uint64(sig[64]))
	out := selector("transferWithAuthorization(address,address,uint256,uint256,uint256,bytes32,uint8,bytes32,bytes32)")
	for _, w := range [][]byte{AddressWord(a.From), AddressWord(a.To), mustWord(a.Value), mustWord(a.ValidAfter), mustWord(a.ValidBefore), a.Nonce[:], mustWord(v), sig[:32], sig[32:64]} {
		out = append(out, w...)
	}
	return out, nil
}

// TransferTopic is the Transfer(address,address,uint256) event's topic, and
// AuthorizationUsedTopic EIP-3009's AuthorizationUsed(address,bytes32).
var (
	TransferTopic          = "0x" + hex.EncodeToString(Keccak256([]byte("Transfer(address,address,uint256)")))
	AuthorizationUsedTopic = "0x" + hex.EncodeToString(Keccak256([]byte("AuthorizationUsed(address,bytes32)")))
)

// --- Transactions the payer sends ---

// Tx1559 is an EIP-1559 transaction to sign.
type Tx1559 struct {
	ChainID, Nonce, Tip, MaxFee, Gas *big.Int
	To                               [20]byte
	Value                            *big.Int
	Data                             []byte
}

// SignTx signs an EIP-1559 transaction and returns it raw (0x02 || rlp) with
// its hash.
func (k *Key) SignTx(t Tx1559) (raw, hash string, err error) {
	if t.Value == nil {
		t.Value = new(big.Int)
	}
	fields := [][]byte{rlpInt(t.ChainID), rlpInt(t.Nonce), rlpInt(t.Tip), rlpInt(t.MaxFee), rlpInt(t.Gas), rlpBytes(t.To[:]), rlpInt(t.Value), rlpBytes(t.Data), rlpListOf(nil)}
	unsigned := append([]byte{0x02}, rlpListOf(fields)...)
	sig, err := k.Sign(Keccak256(unsigned))
	if err != nil {
		return "", "", err
	}
	v := new(big.Int).SetUint64(uint64(sig[64] - 27))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:64])
	signed := append([]byte{0x02}, rlpListOf(append(fields, rlpInt(v), rlpInt(r), rlpInt(s)))...)
	return "0x" + hex.EncodeToString(signed), "0x" + hex.EncodeToString(Keccak256(signed)), nil
}

func rlpBytes(b []byte) []byte {
	if len(b) == 1 && b[0] < 0x80 {
		return []byte{b[0]}
	}
	return append(rlpLength(len(b), 0x80), b...)
}

func rlpInt(v *big.Int) []byte {
	if v == nil || v.Sign() == 0 {
		return []byte{0x80}
	}
	return rlpBytes(v.Bytes())
}

func rlpListOf(items [][]byte) []byte {
	var body []byte
	for _, it := range items {
		body = append(body, it...)
	}
	return append(rlpLength(len(body), 0xc0), body...)
}

func rlpLength(n int, offset byte) []byte {
	if n < 56 {
		return []byte{offset + byte(n)}
	}
	l := big.NewInt(int64(n)).Bytes()
	return append([]byte{offset + 55 + byte(len(l))}, l...)
}

// --- Reads the rail needs ---

// Call is eth_call at a block tag ("latest", "finalized"), returning the
// result's bytes. A revert comes back as an error carrying the node's message.
func (r *RPC) Call(ctx context.Context, from, to string, data []byte, block string) ([]byte, error) {
	msg := map[string]string{"to": to, "data": "0x" + hex.EncodeToString(data)}
	if from != "" {
		msg["from"] = from
	}
	var out string
	if err := r.call(ctx, "eth_call", []any{msg, block}, &out); err != nil {
		return nil, err
	}
	return hex.DecodeString(strings.TrimPrefix(out, "0x"))
}

// ChainID is the node's chain.
func (r *RPC) ChainID(ctx context.Context) (*big.Int, error) {
	return r.quantity(ctx, "eth_chainId", nil)
}

// AuthorizationState reports whether an EIP-3009 authorization has been used.
func (r *RPC) AuthorizationState(ctx context.Context, token string, authorizer [20]byte, nonce [32]byte, block string) (bool, error) {
	data := append(selector("authorizationState(address,bytes32)"), AddressWord(authorizer)...)
	data = append(data, nonce[:]...)
	out, err := r.Call(ctx, "", token, data, block)
	if err != nil {
		return false, err
	}
	if len(out) != 32 {
		return false, fmt.Errorf("evm: authorizationState answered %d bytes", len(out))
	}
	return out[31] == 1, nil
}

// Uint reads a view function that takes addresses and returns one uint256
// (nonces(owner), allowance(owner, spender), balanceOf(owner)).
func (r *RPC) Uint(ctx context.Context, token, signature string, args ...[20]byte) (*big.Int, error) {
	data := selector(signature)
	for _, a := range args {
		data = append(data, AddressWord(a)...)
	}
	out, err := r.Call(ctx, "", token, data, "latest")
	if err != nil {
		return nil, err
	}
	if len(out) != 32 {
		return nil, fmt.Errorf("evm: %s answered %d bytes", signature, len(out))
	}
	return new(big.Int).SetBytes(out), nil
}

// Block is a block's number and time.
type Block struct {
	Number    uint64
	Timestamp uint64
}

// BlockByTag reads "latest", "safe" or "finalized".
func (r *RPC) BlockByTag(ctx context.Context, tag string) (*Block, error) {
	var b struct {
		Number    string `json:"number"`
		Timestamp string `json:"timestamp"`
	}
	if err := r.call(ctx, "eth_getBlockByNumber", []any{tag, false}, &b); err != nil {
		return nil, err
	}
	n, err := quantity(b.Number)
	if err != nil {
		return nil, err
	}
	ts, err := quantity(b.Timestamp)
	if err != nil {
		return nil, err
	}
	return &Block{Number: n.Uint64(), Timestamp: ts.Uint64()}, nil
}

// Log is one event log.
type Log struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
	TxHash  string   `json:"transactionHash"`
	Block   string   `json:"blockNumber"`
	Removed bool     `json:"removed"`
}

// Logs is eth_getLogs for one contract and topics, between two blocks.
func (r *RPC) Logs(ctx context.Context, address string, topics []any, from, to string) ([]Log, error) {
	var out []Log
	err := r.call(ctx, "eth_getLogs", []any{map[string]any{"address": address, "topics": topics, "fromBlock": from, "toBlock": to}}, &out)
	return out, err
}

// Receipt is what Algebra reads from a mined transaction.
type Receipt struct {
	Status string `json:"status"`
	Block  string `json:"blockNumber"`
	Logs   []Log  `json:"logs"`
}

// TransactionReceipt is nil while the transaction is unmined.
func (r *RPC) TransactionReceipt(ctx context.Context, hash string) (*Receipt, error) {
	var out *Receipt
	if err := r.call(ctx, "eth_getTransactionReceipt", []any{hash}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Hex32 reads a 0x-prefixed 32-byte value.
func Hex32(s string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(s), "0x"))
	if err != nil || len(b) != 32 {
		return out, errors.New("evm: expected 32 bytes of hex")
	}
	copy(out[:], b)
	return out, nil
}

// BlockTag is a block number as a JSON-RPC tag.
func BlockTag(n uint64) string { return fmt.Sprintf("0x%x", n) }
