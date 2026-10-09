// Package solana is a small, dependency-free Solana client: keys, program
// derived addresses, the v0 transaction wire format, the few instructions
// Algebra needs (compute budget, SPL token transfer, memo) and a JSON-RPC
// client. It is deliberately not a general SDK: it builds and reads exactly
// what an x402 payment is made of, so that code is small enough to audit.
package solana

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var (
	bigRadix = big.NewInt(58)
	bigZero  = big.NewInt(0)
	indexes  = func() [256]int8 {
		var t [256]int8
		for i := range t {
			t[i] = -1
		}
		for i := 0; i < len(alphabet); i++ {
			t[alphabet[i]] = int8(i)
		}
		return t
	}()
)

// EncodeBase58 encodes bytes in the Bitcoin base58 alphabet, which Solana
// uses for addresses and signatures. Leading zero bytes become leading '1's.
func EncodeBase58(b []byte) string {
	n := new(big.Int).SetBytes(b)
	var out []byte
	mod := new(big.Int)
	for n.Cmp(bigZero) > 0 {
		n.DivMod(n, bigRadix, mod)
		out = append(out, alphabet[mod.Int64()])
	}
	for _, c := range b {
		if c != 0 {
			break
		}
		out = append(out, alphabet[0])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// DecodeBase58 is the inverse of EncodeBase58.
func DecodeBase58(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("solana: empty base58 string")
	}
	n := new(big.Int)
	for i := 0; i < len(s); i++ {
		d := indexes[s[i]]
		if d < 0 {
			return nil, fmt.Errorf("solana: %q is not base58 (bad character at %d)", truncate(s, 16), i)
		}
		n.Mul(n, bigRadix)
		n.Add(n, big.NewInt(int64(d)))
	}
	zeros := 0
	for zeros < len(s) && s[zeros] == alphabet[0] {
		zeros++
	}
	body := n.Bytes()
	out := make([]byte, zeros+len(body))
	copy(out[zeros:], body)
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return strings.TrimSpace(s[:n]) + "…"
	}
	return s
}
