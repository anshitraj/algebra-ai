package account

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// A login code is how someone signs in without a password: six digits,
// emailed to the address they typed, good for a few minutes and a few tries.
// Whoever reads the inbox can sign in, which is exactly what a password reset
// already allows, so a code adds no new way in.
const (
	LoginCodeDigits = 6
	// LoginCodeTTL is how long a code works.
	LoginCodeTTL = 10 * time.Minute
	// LoginCodeAttempts is how many wrong guesses a code survives: a six-digit
	// code has a million values, so five guesses are a 1 in 200,000 chance.
	LoginCodeAttempts = 5
	// LoginCodeCooldown is the least time between two codes to one address.
	LoginCodeCooldown = 30 * time.Second
)

// ErrLoginCodeWrong is a code that is live but not this one.
var ErrLoginCodeWrong = errors.New("that code didn't work: check the six digits, or ask for a new one")

// ErrLoginCodeGone is a code that is no longer live: never sent, expired, used,
// or guessed at too often. Asking for a new one is the way on.
var ErrLoginCodeGone = errors.New("that code has expired or was already used: ask for a new one")

// LoginCodeTooSoon is a refusal to send another code yet.
type LoginCodeTooSoon struct{ RetryAfter time.Duration }

func (e *LoginCodeTooSoon) Error() string {
	secs := int(e.RetryAfter.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("a code was just sent: wait %d seconds before asking for another", secs)
}

// codeSpace is how many six-digit codes there are.
var codeSpace = big.NewInt(1_000_000)

// NewLoginCode is a uniformly random six-digit code, leading zeros kept.
func NewLoginCode() (string, error) {
	n, err := rand.Int(rand.Reader, codeSpace)
	if err != nil {
		return "", fmt.Errorf("account: generating a login code: %w", err)
	}
	return fmt.Sprintf("%0*d", LoginCodeDigits, n.Int64()), nil
}

// ValidLoginCodeShape reports whether s is exactly six digits.
func ValidLoginCodeShape(s string) bool {
	if len(s) != LoginCodeDigits {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// LoginCodeAddressKey is what the code table is keyed by: a keyed hash of the
// normalized address, so the table never holds an address.
func LoginCodeAddressKey(key []byte, email string) string {
	return keyedHash(key, "address", email)
}

// LoginCodeHash is what is stored of a code: a keyed hash of the address and
// the code. Without the key a stolen table can't be searched for the million
// possible codes.
func LoginCodeHash(key []byte, email, code string) string {
	return keyedHash(key, "code", email+"\x00"+code)
}

func keyedHash(key []byte, purpose, value string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("algebra:login-code:" + purpose + ":"))
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
