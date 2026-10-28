// Package evm is what Algebra needs from EVM chains (Base, Arc) for the
// owner's vault: a small JSON-RPC client (balances, the nonce and fees a
// browser-built transaction needs, relaying it), EIP-191 signature checks,
// and reading the sender of a signed EIP-1559 transaction. It signs nothing:
// the owner's key never leaves their browser (docs/WALLET.md).
package evm

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// Keccak256 is Ethereum's hash.
func Keccak256(parts ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// ParseAddress reads a 0x address. Case is not checked here: the address is
// compared in lower case everywhere.
func ParseAddress(s string) ([20]byte, error) {
	var a [20]byte
	s = strings.TrimSpace(s)
	if len(s) != 42 || !strings.HasPrefix(s, "0x") {
		return a, errors.New("evm: an address is 0x and 40 hex digits")
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return a, errors.New("evm: an address is 0x and 40 hex digits")
	}
	copy(a[:], b)
	return a, nil
}

// AddressHex is an address in lower case.
func AddressHex(a [20]byte) string { return "0x" + hex.EncodeToString(a[:]) }

// addressOf is the address of a public key.
func addressOf(pub *secp256k1.PublicKey) [20]byte {
	var a [20]byte
	copy(a[:], Keccak256(pub.SerializeUncompressed()[1:])[12:])
	return a
}

// recover returns the address that made a 65-byte r||s||v signature over
// digest; v is 0/1 or 27/28.
func recover(digest, sig []byte) ([20]byte, error) {
	if len(sig) != 65 {
		return [20]byte{}, errors.New("evm: a signature is 65 bytes")
	}
	v := sig[64]
	if v >= 27 {
		v -= 27
	}
	if v > 1 {
		return [20]byte{}, errors.New("evm: the signature's recovery byte is invalid")
	}
	// decred's compact form: header (27 + recovery id), then r and s.
	compact := append([]byte{27 + v}, sig[:64]...)
	pub, _, err := ecdsa.RecoverCompact(compact, digest)
	if err != nil {
		return [20]byte{}, fmt.Errorf("evm: recovering the signer: %w", err)
	}
	return addressOf(pub), nil
}

// PersonalSigner is the address that signed message with EIP-191
// personal_sign; signature is 0x-prefixed hex.
func PersonalSigner(message, signature string) ([20]byte, error) {
	sig, err := hex.DecodeString(strings.TrimPrefix(signature, "0x"))
	if err != nil {
		return [20]byte{}, errors.New("evm: the signature isn't hex")
	}
	digest := Keccak256([]byte("\x19Ethereum Signed Message:\n"+strconv.Itoa(len(message))), []byte(message))
	return recover(digest, sig)
}

// SignedTx is what Algebra reads from a signed type-2 transaction before it
// relays it.
type SignedTx struct {
	ChainID *big.Int
	From    [20]byte
	To      [20]byte
	Hash    string
}

// DecodeSignedEIP1559 reads a raw signed EIP-1559 transaction (0x02 || rlp):
// its chain, its recipient, who signed it (recovered from the signature) and
// its hash.
func DecodeSignedEIP1559(raw string) (*SignedTx, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(raw, "0x"))
	if err != nil || len(b) < 2 || b[0] != 0x02 {
		return nil, errors.New("evm: not a signed EIP-1559 transaction")
	}
	items, rest, err := rlpList(b[1:])
	if err != nil || len(rest) != 0 || len(items) != 12 {
		return nil, errors.New("evm: the transaction doesn't decode as EIP-1559")
	}
	for _, it := range items[:8] {
		if it.list {
			return nil, errors.New("evm: the transaction doesn't decode as EIP-1559")
		}
	}
	if !items[8].list || items[9].list || items[10].list || items[11].list || len(items[5].body) != 20 {
		return nil, errors.New("evm: the transaction doesn't decode as EIP-1559")
	}
	// What the sender signed: 0x02 || rlp(first nine fields).
	var payload []byte
	for _, it := range items[:9] {
		payload = append(payload, it.raw...)
	}
	digest := Keccak256([]byte{0x02}, rlpListHeader(len(payload)), payload)
	if len(items[10].body) > 32 || len(items[11].body) > 32 || len(items[9].body) > 1 {
		return nil, errors.New("evm: the transaction's signature is malformed")
	}
	sig := make([]byte, 65)
	copy(sig[32-len(items[10].body):32], items[10].body)
	copy(sig[64-len(items[11].body):64], items[11].body)
	if len(items[9].body) == 1 {
		sig[64] = items[9].body[0]
	}
	from, err := recover(digest, sig)
	if err != nil {
		return nil, err
	}
	out := &SignedTx{ChainID: new(big.Int).SetBytes(items[0].body), From: from, Hash: "0x" + hex.EncodeToString(Keccak256(b))}
	copy(out.To[:], items[5].body)
	return out, nil
}

type rlpItem struct {
	list bool
	body []byte // a string's bytes, or a list's payload
	raw  []byte // the whole encoding
}

func rlpList(b []byte) ([]rlpItem, []byte, error) {
	it, rest, err := rlpNext(b)
	if err != nil {
		return nil, nil, err
	}
	if !it.list {
		return nil, nil, errors.New("rlp: not a list")
	}
	var items []rlpItem
	payload := it.body
	for len(payload) > 0 {
		var x rlpItem
		if x, payload, err = rlpNext(payload); err != nil {
			return nil, nil, err
		}
		items = append(items, x)
	}
	return items, rest, nil
}

func rlpNext(b []byte) (rlpItem, []byte, error) {
	if len(b) == 0 {
		return rlpItem{}, nil, errors.New("rlp: empty")
	}
	p := b[0]
	span := func(off, n int, list bool) (rlpItem, []byte, error) {
		if n < 0 || off+n > len(b) {
			return rlpItem{}, nil, errors.New("rlp: truncated")
		}
		return rlpItem{list: list, body: b[off : off+n], raw: b[:off+n]}, b[off+n:], nil
	}
	long := func(lenOfLen int, list bool) (rlpItem, []byte, error) {
		if lenOfLen > 4 || 1+lenOfLen > len(b) {
			return rlpItem{}, nil, errors.New("rlp: bad length")
		}
		n := 0
		for _, c := range b[1 : 1+lenOfLen] {
			n = n<<8 | int(c)
		}
		return span(1+lenOfLen, n, list)
	}
	switch {
	case p < 0x80:
		return rlpItem{body: b[:1], raw: b[:1]}, b[1:], nil
	case p <= 0xb7:
		return span(1, int(p-0x80), false)
	case p < 0xc0:
		return long(int(p-0xb7), false)
	case p <= 0xf7:
		return span(1, int(p-0xc0), true)
	default:
		return long(int(p-0xf7), true)
	}
}

func rlpListHeader(n int) []byte {
	if n <= 55 {
		return []byte{byte(0xc0 + n)}
	}
	var l []byte
	for v := n; v > 0; v >>= 8 {
		l = append([]byte{byte(v)}, l...)
	}
	return append([]byte{byte(0xf7 + len(l))}, l...)
}

// RPC is a JSON-RPC client for one chain. Its URL comes from configuration,
// never from a request.
type RPC struct {
	url  string
	http *http.Client
}

func NewRPC(url string) *RPC {
	return &RPC{url: url, http: &http.Client{Timeout: 10 * time.Second}}
}

func (r *RPC) call(ctx context.Context, method string, params []any, out any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("evm: %s: %w", method, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("evm: %s: %w", method, err)
	}
	var doc struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("evm: %s answered HTTP %d with no JSON-RPC reply", method, resp.StatusCode)
	}
	if doc.Error != nil {
		return fmt.Errorf("evm: %s: %s", method, doc.Error.Message)
	}
	return json.Unmarshal(doc.Result, out)
}

func quantity(s string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(strings.TrimPrefix(s, "0x"), 16)
	if !ok {
		if s == "0x" || s == "0x0" {
			return new(big.Int), nil
		}
		return nil, fmt.Errorf("evm: %q isn't a quantity", s)
	}
	return n, nil
}

func (r *RPC) quantity(ctx context.Context, method string, params []any) (*big.Int, error) {
	var s string
	if err := r.call(ctx, method, params, &s); err != nil {
		return nil, err
	}
	return quantity(s)
}

// NativeBalance is an address's balance of the chain's own coin, in wei.
func (r *RPC) NativeBalance(ctx context.Context, owner string) (*big.Int, error) {
	return r.quantity(ctx, "eth_getBalance", []any{owner, "latest"})
}

// TokenBalance is an address's ERC-20 balance.
func (r *RPC) TokenBalance(ctx context.Context, token, owner string) (*big.Int, error) {
	a, err := ParseAddress(owner)
	if err != nil {
		return nil, err
	}
	data := "0x70a08231" + strings.Repeat("0", 24) + hex.EncodeToString(a[:])
	return r.quantity(ctx, "eth_call", []any{map[string]string{"to": token, "data": data}, "latest"})
}

// TxParams is what a browser needs to build a type-2 transaction.
type TxParams struct {
	ChainID              *big.Int `json:"chain_id"`
	Nonce                *big.Int `json:"nonce"`
	MaxPriorityFeePerGas *big.Int `json:"max_priority_fee_per_gas"`
	MaxFeePerGas         *big.Int `json:"max_fee_per_gas"`
	GasLimit             *big.Int `json:"gas_limit"`
}

// Params reads the pending nonce, the fees (twice the latest base fee plus
// the suggested tip) and the gas the call needs (estimated, plus a fifth).
func (r *RPC) Params(ctx context.Context, from, to, data string) (*TxParams, error) {
	chainID, err := r.quantity(ctx, "eth_chainId", nil)
	if err != nil {
		return nil, err
	}
	nonce, err := r.quantity(ctx, "eth_getTransactionCount", []any{from, "pending"})
	if err != nil {
		return nil, err
	}
	tip, err := r.quantity(ctx, "eth_maxPriorityFeePerGas", nil)
	if err != nil {
		tip = big.NewInt(1_000_000) // 0.001 gwei: where the node has no suggestion
	}
	var block struct {
		BaseFee string `json:"baseFeePerGas"`
	}
	if err := r.call(ctx, "eth_getBlockByNumber", []any{"latest", false}, &block); err != nil {
		return nil, err
	}
	base, err := quantity(block.BaseFee)
	if err != nil {
		return nil, err
	}
	gas, err := r.quantity(ctx, "eth_estimateGas", []any{map[string]string{"from": from, "to": to, "data": data}})
	if err != nil {
		return nil, err
	}
	gas.Mul(gas, big.NewInt(6)).Div(gas, big.NewInt(5))
	maxFee := new(big.Int).Add(new(big.Int).Mul(base, big.NewInt(2)), tip)
	return &TxParams{ChainID: chainID, Nonce: nonce, MaxPriorityFeePerGas: tip, MaxFeePerGas: maxFee, GasLimit: gas}, nil
}

// SendRaw relays a signed transaction and returns its hash.
func (r *RPC) SendRaw(ctx context.Context, raw string) (string, error) {
	var hash string
	if err := r.call(ctx, "eth_sendRawTransaction", []any{raw}, &hash); err != nil {
		return "", err
	}
	return hash, nil
}
