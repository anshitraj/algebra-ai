package solana

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// remoteFor is a remote key backed by a local one, standing in for a signing service.
func remoteFor(t *testing.T, sign func(local *Keypair, m []byte) ([64]byte, error)) (*Keypair, *Keypair) {
	t.Helper()
	local, err := NewKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return NewRemoteSigner(local.PublicKey(), func(m []byte) ([64]byte, error) { return sign(local, m) }), local
}

func TestARemoteKeySignsAndItsSignaturesAreChecked(t *testing.T) {
	good, local := remoteFor(t, func(l *Keypair, m []byte) ([64]byte, error) { return l.Sign(m), nil })
	if !good.IsRemote() || good.PublicKey() != local.PublicKey() {
		t.Fatal("a remote key has its address and says it is remote")
	}
	sig, err := good.SignChecked([]byte("message"))
	pub := local.PublicKey()
	if err != nil || !ed25519.Verify(pub[:], []byte("message"), sig[:]) {
		t.Fatalf("a good signature: %v", err)
	}

	// A signature made by some other key, or garbled, is an error, not a transaction.
	other, _ := NewKeypair()
	wrong, _ := remoteFor(t, func(_ *Keypair, m []byte) ([64]byte, error) { return other.Sign(m), nil })
	if _, err := wrong.SignChecked([]byte("message")); err == nil {
		t.Fatal("a signature from another key must be refused")
	}
	// And a signing service that is down is an error the caller can handle.
	down, _ := remoteFor(t, func(*Keypair, []byte) ([64]byte, error) { return [64]byte{}, errors.New("unavailable") })
	if _, err := down.SignChecked([]byte("message")); err == nil {
		t.Fatal("an outage is an error")
	}
}

func TestALocalKeyStillSignsEitherWay(t *testing.T) {
	k, _ := NewKeypair()
	a := k.Sign([]byte("m"))
	b, err := k.SignChecked([]byte("m"))
	if err != nil || a != b || k.IsRemote() {
		t.Fatalf("a local key: %v", err)
	}
}

// The unchecked Sign cannot swallow a failure: it refuses a remote key outright,
// and a remote key can't be exported or printed.
func TestARemoteKeyCannotBeSignedWithUncheckedOrExported(t *testing.T) {
	k, _ := remoteFor(t, func(l *Keypair, m []byte) ([64]byte, error) { return l.Sign(m), nil })
	for name, f := range map[string]func(){
		"Sign":             func() { k.Sign([]byte("m")) },
		"ExportKeygenJSON": func() { k.ExportKeygenJSON() },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s must refuse a remote key", name)
				}
			}()
			f()
		}()
	}
	// Nothing prints more than the address.
	for _, s := range []string{k.String(), fmt.Sprintf("%v", k), fmt.Sprintf("%+v", k), fmt.Sprintf("%#v", k)} {
		if !strings.HasPrefix(s, "Keypair(") || !strings.Contains(s, k.PublicKey().String()) {
			t.Errorf("unexpected print: %q", s)
		}
	}
}
