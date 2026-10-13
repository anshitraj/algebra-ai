package payersigner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// memStore is a Store with the database's one-row-per-cluster behaviour.
type memStore struct {
	mu     sync.Mutex
	rows   map[string]*Stored
	passes bool
	saves  int
}

func newMemStore() *memStore { return &memStore{rows: map[string]*Stored{}} }

func (m *memStore) Signer(_ context.Context, cluster string) (*Stored, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[cluster]
	if !ok {
		return nil, fmt.Errorf("%w: none", shared.ErrNotFound)
	}
	c := *r
	return &c, nil
}

func (m *memStore) SaveSigner(_ context.Context, s *Stored) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[s.Cluster]; ok {
		return false, nil
	}
	c := *s
	m.rows[s.Cluster] = &c
	m.saves++
	return true, nil
}

func (m *memStore) HasPasses(context.Context, string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.passes, nil
}

// fakeMaster is plainly not a real key.
func fakeMaster(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func deps(t *testing.T, st Store, master byte) Deps {
	t.Helper()
	enc, err := NewEncryptor(fakeMaster(master))
	if err != nil {
		t.Fatal(err)
	}
	return Deps{Store: st, Enc: enc}
}

var devnet = Source{Cluster: "devnet", Network: "solana-devnet", Generate: true}

func TestGeneratedSignerSurvivesARestart(t *testing.T) {
	st := newMemStore()
	first, err := Resolve(context.Background(), devnet, deps(t, st, 'a'))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Mode != ModeGenerated {
		t.Fatalf("first start: %+v", first)
	}
	// A restart is a new process with the same database and master key.
	second, err := Resolve(context.Background(), devnet, deps(t, st, 'a'))
	if err != nil {
		t.Fatal(err)
	}
	if second.Created {
		t.Fatal("a restart must not make a new key")
	}
	if first.Key.PublicKey() != second.Key.PublicKey() {
		t.Fatalf("the address changed across a restart: %s then %s", first.Key.PublicKey(), second.Key.PublicKey())
	}
	if st.saves != 1 {
		t.Fatalf("stored %d keys", st.saves)
	}
	// And it signs: the reloaded key is the real one.
	msg := []byte("a message")
	sig := second.Key.Sign(msg)
	if sig == ([64]byte{}) {
		t.Fatal("no signature")
	}
}

func TestGeneratedSignerIsDevnetOnly(t *testing.T) {
	_, err := Resolve(context.Background(), Source{Cluster: "mainnet", Network: "solana", Generate: true}, deps(t, newMemStore(), 'a'))
	if err == nil || !strings.Contains(err.Error(), "devnet only") {
		t.Fatalf("a generated key on mainnet must be refused, got %v", err)
	}
}

func TestVaultsWithoutAStoredSignerRefuseToMakeANewOne(t *testing.T) {
	st := newMemStore()
	st.passes = true
	_, err := Resolve(context.Background(), devnet, deps(t, st, 'a'))
	if err == nil || !strings.Contains(err.Error(), "vaults exist") {
		t.Fatalf("vaults exist, the key is gone: want a refusal, got %v", err)
	}
	if st.saves != 0 {
		t.Fatal("a key was made anyway")
	}
}

func TestAWrongMasterKeyNeverMakesANewSigner(t *testing.T) {
	st := newMemStore()
	if _, err := Resolve(context.Background(), devnet, deps(t, st, 'a')); err != nil {
		t.Fatal(err)
	}
	stored := st.rows["devnet"].PublicKey
	_, err := Resolve(context.Background(), devnet, deps(t, st, 'b'))
	if err == nil || !strings.Contains(err.Error(), "NOT generated") {
		t.Fatalf("a key sealed under another master key must stop the start, got %v", err)
	}
	if !strings.Contains(err.Error(), stored) {
		t.Fatalf("the error should name the stored address %s: %v", stored, err)
	}
	if st.saves != 1 || st.rows["devnet"].PublicKey != stored {
		t.Fatal("the stored key was replaced")
	}
}

func TestATamperedRowIsRefused(t *testing.T) {
	st := newMemStore()
	if _, err := Resolve(context.Background(), devnet, deps(t, st, 'a')); err != nil {
		t.Fatal(err)
	}
	other, _ := solana.NewKeypair()
	st.rows["devnet"].PublicKey = other.PublicKey().String() // aad no longer matches
	if _, err := Resolve(context.Background(), devnet, deps(t, st, 'a')); err == nil {
		t.Fatal("a row whose address was changed must be refused")
	}
	st.rows["devnet"].PublicKey = "x"
	st.rows["devnet"].Sealed = []byte("not sealed")
	if _, err := Resolve(context.Background(), devnet, deps(t, st, 'a')); err == nil {
		t.Fatal("a row with a changed ciphertext must be refused")
	}
}

func TestThePinMustMatch(t *testing.T) {
	st := newMemStore()
	made, err := Resolve(context.Background(), devnet, deps(t, st, 'a'))
	if err != nil {
		t.Fatal(err)
	}
	pinned := devnet
	pinned.ExpectPubkey = made.Key.PublicKey().String()
	if _, err := Resolve(context.Background(), pinned, deps(t, st, 'a')); err != nil {
		t.Fatalf("the right pin: %v", err)
	}
	other, _ := solana.NewKeypair()
	pinned.ExpectPubkey = other.PublicKey().String()
	if _, err := Resolve(context.Background(), pinned, deps(t, st, 'a')); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("the wrong pin must stop the start, got %v", err)
	}
}

func TestAPinnedSignerThatIsMissingIsNotReplaced(t *testing.T) {
	st := newMemStore() // the row was lost
	pinned := devnet
	other, _ := solana.NewKeypair()
	pinned.ExpectPubkey = other.PublicKey().String()
	_, err := Resolve(context.Background(), pinned, deps(t, st, 'a'))
	if err == nil || !strings.Contains(err.Error(), "no signer is stored") {
		t.Fatalf("pinned but missing: want a refusal, got %v", err)
	}
	if st.saves != 0 {
		t.Fatal("a key was made to replace the pinned one")
	}
}

func TestServersStartingTogetherEndUpWithOneKey(t *testing.T) {
	st := newMemStore()
	const n = 8
	var wg sync.WaitGroup
	got := make([]string, n)
	created := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := Resolve(context.Background(), devnet, deps(t, st, 'a'))
			if err != nil {
				t.Error(err)
				return
			}
			got[i], created[i] = s.Key.PublicKey().String(), s.Created
		}(i)
	}
	wg.Wait()
	winners := 0
	for i := range got {
		if got[i] != got[0] {
			t.Fatalf("servers disagree on the key: %v", got)
		}
		if created[i] {
			winners++
		}
	}
	if winners != 1 || st.saves != 1 {
		t.Fatalf("%d servers say they made the key, %d were stored", winners, st.saves)
	}
}

func TestOneSourceOnly(t *testing.T) {
	_, err := Resolve(context.Background(), Source{Cluster: "devnet", Network: "solana-devnet", Generate: true, Keypair: "[1]"}, deps(t, newMemStore(), 'a'))
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("a keypair and a generated signer together: %v", err)
	}
	if _, err := Resolve(context.Background(), Source{Cluster: "devnet"}, deps(t, newMemStore(), 'a')); err == nil {
		t.Fatal("no source at all must be an error")
	}
}

func TestAnOperatorKeypairStillWorks(t *testing.T) {
	kp, _ := solana.NewKeypair()
	dir := t.TempDir()
	file := filepath.Join(dir, "k.json")
	if err := os.WriteFile(file, []byte(kp.ExportKeygenJSON()), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Resolve(context.Background(), Source{Cluster: "mainnet", Network: "solana", KeypairFile: file}, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != ModeEnv || s.Key.PublicKey() != kp.PublicKey() || s.Created {
		t.Fatalf("env key: %+v", s)
	}
}

// No failure path may put a key, sealed or not, in its message.
func TestErrorsNeverContainKeyMaterial(t *testing.T) {
	st := newMemStore()
	made, err := Resolve(context.Background(), devnet, deps(t, st, 'a'))
	if err != nil {
		t.Fatal(err)
	}
	secret := made.Key.ExportKeygenJSON()
	sealed := fmt.Sprintf("%v", st.rows["devnet"].Sealed)

	var errs []error
	_, e1 := Resolve(context.Background(), devnet, deps(t, st, 'b')) // wrong master key
	errs = append(errs, e1)
	st.rows["devnet"].Sealed = append([]byte(nil), st.rows["devnet"].Sealed...)
	st.rows["devnet"].Sealed[0] ^= 1
	_, e2 := Resolve(context.Background(), devnet, deps(t, st, 'a')) // altered ciphertext
	errs = append(errs, e2)
	bad := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(bad, []byte("[1,2,3,SECRETISH]"), 0o600)
	_, e3 := Resolve(context.Background(), Source{Cluster: "devnet", KeypairFile: bad}, Deps{}) // unreadable keypair
	errs = append(errs, e3)
	_, e4 := Resolve(context.Background(), Source{Cluster: "devnet", Keypair: "[1,2,3]"}, Deps{})
	errs = append(errs, e4)

	for i, e := range errs {
		if e == nil {
			t.Fatalf("case %d: expected an error", i)
		}
		for _, leak := range []string{secret, sealed, "SECRETISH", "1,2,3"} {
			if strings.Contains(e.Error(), leak) {
				t.Fatalf("case %d leaked %q: %v", i, leak, e)
			}
		}
	}
}

// --- fee top-ups ---

type fakeChain struct {
	balance   uint64
	airdrops  int
	refuse    bool
	confirmed bool
}

func (f *fakeChain) GetBalance(context.Context, solana.PublicKey, string) (uint64, error) {
	return f.balance, nil
}
func (f *fakeChain) RequestAirdrop(context.Context, solana.PublicKey, uint64) (string, error) {
	if f.refuse {
		return "", errors.New("rate limited")
	}
	f.airdrops++
	return "fakesig", nil
}
func (f *fakeChain) ConfirmSignature(context.Context, string, string, time.Duration) (*solana.SignatureStatus, error) {
	f.confirmed = true
	return &solana.SignatureStatus{}, nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestTopUpAsksTheFaucetOnlyWhenLow(t *testing.T) {
	kp, _ := solana.NewKeypair()
	low := &fakeChain{balance: 1_000}
	TopUp(context.Background(), low, kp.PublicKey(), quiet())
	if low.airdrops != 1 || !low.confirmed {
		t.Fatalf("a low balance should ask once and wait: %+v", low)
	}
	rich := &fakeChain{balance: 2 * topUpBelowLamports}
	TopUp(context.Background(), rich, kp.PublicKey(), quiet())
	if rich.airdrops != 0 {
		t.Fatal("a funded key shouldn't ask")
	}
	refused := &fakeChain{balance: 0, refuse: true}
	TopUp(context.Background(), refused, kp.PublicKey(), quiet()) // must not panic or block
}

func TestReadyNeedsTheFeeFloor(t *testing.T) {
	kp, _ := solana.NewKeypair()
	if _, ok, _ := Ready(context.Background(), &fakeChain{balance: FeeFloorLamports - 1}, kp.PublicKey()); ok {
		t.Fatal("just under the floor is not ready")
	}
	if _, ok, _ := Ready(context.Background(), &fakeChain{balance: FeeFloorLamports}, kp.PublicKey()); !ok {
		t.Fatal("at the floor is ready")
	}
}
