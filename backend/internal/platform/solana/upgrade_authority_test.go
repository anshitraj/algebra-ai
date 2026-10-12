package solana

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestProgramAccountsNameTheirUpgradeAuthority(t *testing.T) {
	dataAddr := MustPublicKey("7FmoWxPpe28Wz7Akeiwfv36sxGDAKvtY7gJTRUTiii7d")
	authority := MustPublicKey("GhpmTWT8ng3viaQtFNDF7T7RyrmemosWvEDetKmBEoPA")

	program := make([]byte, 36)
	binary.LittleEndian.PutUint32(program, 2)
	copy(program[4:], dataAddr[:])
	if got, err := programDataAddress(program); err != nil || got != dataAddr {
		t.Fatalf("program account: %v %v", got, err)
	}

	header := func(option byte, key *PublicKey, code int) []byte {
		b := make([]byte, 13)
		binary.LittleEndian.PutUint32(b, 3)
		binary.LittleEndian.PutUint64(b[4:], 509115745)
		b[12] = option
		if key != nil {
			b = append(b, key[:]...)
		}
		return append(b, make([]byte, code)...)
	}
	if got, err := programDataAuthority(header(1, &authority, 1000)); err != nil || got == nil || *got != authority {
		t.Fatalf("an upgradeable program names its authority: %v %v", got, err)
	}
	if got, err := programDataAuthority(header(0, nil, 1000)); err != nil || got != nil {
		t.Fatalf("an immutable program has no authority: %v %v", got, err)
	}

	// Anything else is not believed: a wrong state, a short account, a bad option.
	wrongState := header(1, &authority, 0)
	binary.LittleEndian.PutUint32(wrongState, 2)
	for name, data := range map[string][]byte{
		"program account read as program data": wrongState,
		"empty":                                nil,
		"truncated header":                     header(1, &authority, 0)[:12],
		"authority cut short":                  header(1, &authority, 0)[:30],
		"option byte that is neither":          header(7, nil, 40),
	} {
		if got, err := programDataAuthority(data); !errors.Is(err, ErrNotUpgradeable) || got != nil {
			t.Errorf("%s: %v %v, want ErrNotUpgradeable", name, got, err)
		}
	}
	for name, data := range map[string][]byte{"empty": nil, "short": program[:20], "program data read as a program": header(1, &authority, 0)} {
		if _, err := programDataAddress(data); !errors.Is(err, ErrNotUpgradeable) {
			t.Errorf("program account, %s: %v, want ErrNotUpgradeable", name, err)
		}
	}
}
