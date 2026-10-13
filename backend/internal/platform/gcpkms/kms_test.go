package gcpkms

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

const version = "projects/p/locations/global/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1"

// fakeKMS serves one Ed25519 key the way Cloud KMS does.
type fakeKMS struct {
	pub       ed25519.PublicKey
	priv      ed25519.PrivateKey
	algorithm string
	// corrupt flips a bit of every signature; name overrides the "name" echoed back.
	corrupt bool
	name    string
	// failFirst answers this many requests with this status before behaving.
	failFirst int32
	failWith  int
	calls     atomic.Int32
	sawRaw    atomic.Bool
}

func newFakeKMS() *fakeKMS {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return &fakeKMS{pub: pub, priv: priv, algorithm: "EC_SIGN_ED25519", name: version, failWith: http.StatusServiceUnavailable}
}

func (f *fakeKMS) client(t *testing.T) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n := f.calls.Add(1); n <= f.failFirst {
			w.WriteHeader(f.failWith)
			_, _ = w.Write([]byte("secret-service-token"))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/publicKey") {
			der, _ := x509.MarshalPKIXPublicKey(f.pub)
			_ = json.NewEncoder(w).Encode(map[string]string{"pem": string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), "algorithm": f.algorithm})
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		msg, _ := base64.StdEncoding.DecodeString(in["data"])
		f.sawRaw.Store(string(msg) == "raw Solana message")
		sig := ed25519.Sign(f.priv, msg)
		if f.corrupt {
			sig[0] ^= 1
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"name": f.name, "signature": base64.StdEncoding.EncodeToString(sig)})
	}))
	t.Cleanup(srv.Close)
	return &Client{http: srv.Client(), base: srv.URL + "/", backoff: func(int) time.Duration { return 0 }}
}

func TestSignsRawMessagesAndRejectsBadSignatures(t *testing.T) {
	f := newFakeKMS()
	signer, err := f.client(t).Signer(context.Background(), version)
	if err != nil {
		t.Fatal(err)
	}
	if !signer.IsRemote() {
		t.Fatal("a KMS key is a remote key")
	}
	var want solana.PublicKey
	copy(want[:], f.pub)
	if signer.PublicKey() != want {
		t.Fatalf("the signer's address is the key's: %s", signer.PublicKey())
	}
	sig, err := signer.SignChecked([]byte("raw Solana message"))
	if err != nil || !ed25519.Verify(f.pub, []byte("raw Solana message"), sig[:]) {
		t.Fatalf("a valid signature: %v", err)
	}
	if !f.sawRaw.Load() {
		t.Fatal("the message was hashed or changed on its way to KMS: Ed25519 signs the raw bytes")
	}
	f.corrupt = true
	if _, err := signer.SignChecked([]byte("raw Solana message")); err == nil {
		t.Fatal("a signature that does not verify must never be accepted")
	}
}

// A remote key signs a real transaction through the same call the rails use.
func TestARemoteKeySignsATransaction(t *testing.T) {
	f := newFakeKMS()
	signer, err := f.client(t).Signer(context.Background(), version)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := solana.CompileV0(signer.PublicKey(), []solana.Instruction{solana.SetComputeUnitLimit(1)}, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	tx := solana.NewTransaction(msg)
	if err := tx.PartialSign(signer); err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(f.pub, msg.Serialize(), tx.Signatures[0][:]) {
		t.Fatal("the transaction's signature should verify against the KMS key")
	}
	// When KMS says no, the transaction is not signed and the caller is told.
	f.failFirst, f.failWith = 1000, http.StatusForbidden
	f.calls.Store(0)
	if err := solana.NewTransaction(msg).PartialSign(signer); err == nil {
		t.Fatal("a refusal from KMS must be an error, not a panic or a blank signature")
	}
}

func TestOnlyOneExactEd25519KeyVersionIsAccepted(t *testing.T) {
	ctx := context.Background()
	f := newFakeKMS()
	c := f.client(t)
	for name, v := range map[string]string{
		"a key with no version":  "projects/p/locations/global/keyRings/r/cryptoKeys/k",
		"nothing":                "",
		"a path with extra bits": version + "/extra",
		"a url":                  "https://example.com/" + version,
	} {
		if _, err := c.Signer(ctx, v); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	f.algorithm = "EC_SIGN_P256_SHA256"
	if _, err := c.Signer(ctx, version); err == nil {
		t.Error("a key that is not pure Ed25519 can't sign Solana transactions")
	}
	f.algorithm = "EC_SIGN_ED25519"
	signer, err := c.Signer(ctx, version)
	if err != nil {
		t.Fatal(err)
	}
	f.name = "projects/p/locations/global/keyRings/r/cryptoKeys/k/cryptoKeyVersions/2"
	if _, err := signer.SignChecked([]byte("m")); err == nil {
		t.Error("an answer from another key version must be refused")
	}
}

func TestErrorsDoNotExposeResponseSecrets(t *testing.T) {
	f := newFakeKMS()
	f.failFirst, f.failWith = 1000, http.StatusForbidden
	_, err := f.client(t).Signer(context.Background(), version)
	if err == nil || strings.Contains(err.Error(), "secret-service-token") {
		t.Fatalf("an error must not carry a response body (or succeed): %v", err)
	}
}

func TestTransientFailuresAreRetriedAndRefusalsAreNot(t *testing.T) {
	ctx := context.Background()
	t.Run("two unavailable answers, then the key", func(t *testing.T) {
		f := newFakeKMS()
		f.failFirst = 2
		if _, err := f.client(t).Signer(ctx, version); err != nil {
			t.Fatalf("a brief outage should not fail a start: %v", err)
		}
		if got := f.calls.Load(); got != 3 {
			t.Fatalf("calls = %d, want 3", got)
		}
	})
	t.Run("an outage that lasts is given up on", func(t *testing.T) {
		f := newFakeKMS()
		f.failFirst = 1000
		if _, err := f.client(t).Signer(ctx, version); err == nil {
			t.Fatal("want an error")
		}
		if got := f.calls.Load(); got != attempts {
			t.Fatalf("calls = %d, want %d", got, attempts)
		}
	})
	t.Run("a refusal is final", func(t *testing.T) {
		f := newFakeKMS()
		f.failFirst, f.failWith = 1000, http.StatusForbidden
		if _, err := f.client(t).Signer(ctx, version); err == nil {
			t.Fatal("want an error")
		}
		if got := f.calls.Load(); got != 1 {
			t.Fatalf("a permission error is not retried: calls = %d", got)
		}
	})
}
