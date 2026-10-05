package solana

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// PublicKey is a 32-byte Solana address.
type PublicKey [32]byte

// ParsePublicKey reads a base58 address.
func ParsePublicKey(s string) (PublicKey, error) {
	var pk PublicKey
	b, err := DecodeBase58(strings.TrimSpace(s))
	if err != nil {
		return pk, err
	}
	if len(b) != 32 {
		return pk, fmt.Errorf("solana: an address is 32 bytes, %q decodes to %d", truncate(s, 16), len(b))
	}
	copy(pk[:], b)
	return pk, nil
}

// MustPublicKey is ParsePublicKey for constants.
func MustPublicKey(s string) PublicKey {
	pk, err := ParsePublicKey(s)
	if err != nil {
		panic(err)
	}
	return pk
}

func (p PublicKey) String() string { return EncodeBase58(p[:]) }

// IsZero reports the all-zero address (the system program).
func (p PublicKey) IsZero() bool { return p == PublicKey{} }

// Keypair signs for one address. Its secret never prints: every formatting
// verb shows the public key only, and it can't be marshalled to JSON, so a
// stray log line or a debug dump can't leak it.
type Keypair struct{ priv ed25519.PrivateKey }

// NewKeypair makes a random keypair (tests, and throwaway devnet wallets).
func NewKeypair() (*Keypair, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Keypair{priv: priv}, nil
}

// ParseKeypair reads a keypair in either form Solana tooling produces: the
// JSON array of 64 numbers that `solana-keygen` writes, or the base58 text of
// those 64 bytes that wallets export. It checks that the public half belongs
// to the secret half, so a corrupted key is refused rather than used.
func ParseKeypair(s string) (*Keypair, error) {
	s = strings.TrimSpace(s)
	var raw []byte
	if strings.HasPrefix(s, "[") {
		var nums []int
		if err := json.Unmarshal([]byte(s), &nums); err != nil {
			return nil, errors.New("solana: the keypair isn't a JSON array of numbers")
		}
		raw = make([]byte, len(nums))
		for i, n := range nums {
			if n < 0 || n > 255 {
				return nil, errors.New("solana: the keypair array has a value outside 0-255")
			}
			raw[i] = byte(n)
		}
	} else {
		var err error
		if raw, err = DecodeBase58(s); err != nil {
			return nil, errors.New("solana: the keypair isn't base58 or a JSON array")
		}
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("solana: a keypair is 64 bytes, this one is %d", len(raw))
	}
	priv := ed25519.PrivateKey(raw)
	if want := ed25519.NewKeyFromSeed(priv.Seed()).Public().(ed25519.PublicKey); string(want) != string(priv.Public().(ed25519.PublicKey)) {
		return nil, errors.New("solana: the keypair's public half doesn't match its secret half")
	}
	return &Keypair{priv: priv}, nil
}

// PublicKey is the address this keypair signs for.
func (k *Keypair) PublicKey() PublicKey {
	var pk PublicKey
	copy(pk[:], k.priv.Public().(ed25519.PublicKey))
	return pk
}

// Sign signs a message.
func (k *Keypair) Sign(msg []byte) [64]byte {
	var sig [64]byte
	copy(sig[:], ed25519.Sign(k.priv, msg))
	return sig
}

// ExportKeygenJSON returns the secret in the JSON-array form `solana-keygen`
// writes. It exists for one purpose, writing a key file for a new wallet, and
// is named so that it can't be called by accident. Never log, print or send
// its result.
func (k *Keypair) ExportKeygenJSON() string {
	nums := make([]string, len(k.priv))
	for i, b := range k.priv {
		nums[i] = strconv.Itoa(int(b))
	}
	return "[" + strings.Join(nums, ",") + "]"
}

// String, GoString and Format show the public key and nothing else.
func (k *Keypair) String() string   { return "Keypair(" + k.PublicKey().String() + ")" }
func (k *Keypair) GoString() string { return k.String() }
func (k *Keypair) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(k.String()))
}

// MarshalJSON refuses: a keypair is never serialised by accident.
func (k *Keypair) MarshalJSON() ([]byte, error) {
	return nil, errors.New("solana: a keypair can't be marshalled")
}

// --- program derived addresses ---

var (
	curveP = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	// curveD is the Edwards curve constant d = -121665/121666 mod p.
	curveD = func() *big.Int {
		num := new(big.Int).Neg(big.NewInt(121665))
		den := new(big.Int).ModInverse(big.NewInt(121666), curveP)
		return num.Mul(num, den).Mod(num, curveP)
	}()
	curveExp = new(big.Int).Rsh(new(big.Int).Sub(curveP, big.NewInt(1)), 1) // (p-1)/2
)

// IsOnCurve reports whether 32 bytes decode to a point on the ed25519 curve.
// An address is a program derived address only when it is NOT on the curve,
// so nobody holds a private key for it. The sign bit is ignored, as it is by
// the validator.
func IsOnCurve(b [32]byte) bool {
	// y is the little-endian integer in the low 255 bits.
	var be [32]byte
	for i := range b {
		be[31-i] = b[i]
	}
	be[0] &= 0x7f
	y := new(big.Int).SetBytes(be[:])
	y.Mod(y, curveP)

	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, curveP)
	u := new(big.Int).Sub(y2, big.NewInt(1))
	u.Mod(u, curveP)
	v := new(big.Int).Mul(curveD, y2)
	v.Add(v, big.NewInt(1)).Mod(v, curveP)
	// x² = u/v has a solution iff u/v is zero or a quadratic residue.
	r := new(big.Int).Mul(u, new(big.Int).ModInverse(v, curveP))
	r.Mod(r, curveP)
	if r.Sign() == 0 {
		return true
	}
	return new(big.Int).Exp(r, curveExp, curveP).Cmp(big.NewInt(1)) == 0
}

// CreateProgramAddress derives the address for exact seeds, failing if it
// lands on the curve.
func CreateProgramAddress(seeds [][]byte, program PublicKey) (PublicKey, error) {
	if len(seeds) > 16 {
		return PublicKey{}, errors.New("solana: at most 16 seeds")
	}
	h := sha256.New()
	for _, s := range seeds {
		if len(s) > 32 {
			return PublicKey{}, errors.New("solana: a seed is at most 32 bytes")
		}
		h.Write(s)
	}
	h.Write(program[:])
	h.Write([]byte("ProgramDerivedAddress"))
	var pk PublicKey
	copy(pk[:], h.Sum(nil))
	if IsOnCurve(pk) {
		return PublicKey{}, errors.New("solana: derived address is on the curve")
	}
	return pk, nil
}

// FindProgramAddress finds the canonical program derived address: the first
// bump seed, counting down from 255, that yields an off-curve address.
func FindProgramAddress(seeds [][]byte, program PublicKey) (PublicKey, uint8, error) {
	for bump := 255; bump >= 0; bump-- {
		withBump := append(append([][]byte{}, seeds...), []byte{byte(bump)})
		if pk, err := CreateProgramAddress(withBump, program); err == nil {
			return pk, uint8(bump), nil
		}
	}
	return PublicKey{}, 0, errors.New("solana: no viable bump seed")
}
