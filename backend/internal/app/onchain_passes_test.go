package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/spendpass"
)

type memOnchainStore struct {
	bindings map[string]*OnchainBinding
	pulls    map[string]*OnchainPull
}

func newMemOnchainStore() *memOnchainStore {
	return &memOnchainStore{bindings: map[string]*OnchainBinding{}, pulls: map[string]*OnchainPull{}}
}

func (m *memOnchainStore) OnchainBinding(_ context.Context, passID string) (*OnchainBinding, error) {
	if b, ok := m.bindings[passID]; ok {
		c := *b
		return &c, nil
	}
	return nil, fmt.Errorf("%w: none", shared.ErrNotFound)
}
func (m *memOnchainStore) SaveOnchainBinding(_ context.Context, b *OnchainBinding) error {
	c := *b
	m.bindings[b.PassID] = &c
	return nil
}
func (m *memOnchainStore) DeleteOnchainBinding(_ context.Context, passID string) error {
	delete(m.bindings, passID)
	return nil
}
func (m *memOnchainStore) OnchainPull(_ context.Context, id string) (*OnchainPull, error) {
	if p, ok := m.pulls[id]; ok {
		c := *p
		return &c, nil
	}
	return nil, fmt.Errorf("%w: none", shared.ErrNotFound)
}
func (m *memOnchainStore) InsertOnchainPull(_ context.Context, p *OnchainPull) error {
	if _, ok := m.pulls[p.ReservationID]; ok {
		return shared.ErrConflict
	}
	c := *p
	m.pulls[p.ReservationID] = &c
	return nil
}
func (m *memOnchainStore) UpdateOnchainPull(_ context.Context, p *OnchainPull, from string) error {
	cur, ok := m.pulls[p.ReservationID]
	if !ok || cur.State != from {
		return shared.ErrConflict
	}
	c := *p
	m.pulls[p.ReservationID] = &c
	return nil
}
func (m *memOnchainStore) OpenOnchainPulls(context.Context, int) ([]OnchainPull, error) {
	return nil, nil
}
func (m *memOnchainStore) OnchainPullsForPass(context.Context, string, int) ([]OnchainPull, error) {
	return nil, nil
}

func testNetwork(t *testing.T, required bool) *OnchainNetwork {
	t.Helper()
	kp, err := solana.NewKeypair()
	if err != nil {
		t.Fatal(err)
	}
	prog, err := spendpass.New("")
	if err != nil {
		t.Fatal(err)
	}
	return &OnchainNetwork{Network: "solana-devnet", Cluster: "devnet", RailName: "x402-solana-devnet", Program: prog,
		RPC: solana.NewRPC("http://127.0.0.1:1", nil), Payer: kp, Mint: solana.MustPublicKey("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"), Required: required}
}

func TestPassesOnEitherDeploymentAreHonoured(t *testing.T) {
	n := testNetwork(t, false)
	anchor, _ := spendpass.New(spendpass.AnchorProgramID)
	n.Accepted = []spendpass.Program{n.Program, anchor}
	if p, err := bindingProgram(n, &OnchainBinding{ProgramID: spendpass.AnchorProgramID}); err != nil || p.ID != anchor.ID {
		t.Fatalf("an Anchor-made pass: %v %v", p, err)
	}
	if p, err := bindingProgram(n, &OnchainBinding{ProgramID: spendpass.PinocchioProgramID}); err != nil || p.ID != n.Program.ID {
		t.Fatalf("a Pinocchio-made pass: %v %v", p, err)
	}
	if _, err := bindingProgram(n, &OnchainBinding{ProgramID: "11111111111111111111111111111111"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("another program: %v", err)
	}
}

func TestPassNumberIsStableAndPerPass(t *testing.T) {
	a, b := PassNumber("pass_1"), PassNumber("pass_2")
	if a == b || a != PassNumber("pass_1") {
		t.Fatalf("pass numbers: %d %d", a, b)
	}
}

func TestFundLeavesOtherRailsAndOffChainPassesAlone(t *testing.T) {
	store := newMemOnchainStore()
	svc := NewOnchainPassService(store, nil, nil, nil, []*OnchainNetwork{testNetwork(t, false)})
	// A rail the service doesn't manage (the sandbox): nothing to do.
	if p, err := svc.Fund(context.Background(), "pass_1", &econ.Reservation{ID: "r1", Rail: "sandbox"}, 1000); p != nil || err != nil {
		t.Fatalf("sandbox rail: %v %v", p, err)
	}
	// A pass that isn't on chain, where that is allowed: paid as before.
	if p, err := svc.Fund(context.Background(), "pass_1", &econ.Reservation{ID: "r1", Rail: "x402-solana-devnet"}, 1000); p != nil || err != nil {
		t.Fatalf("off-chain pass: %v %v", p, err)
	}
}

func TestRequiredNetworkRefusesAPassThatIsntOnChain(t *testing.T) {
	svc := NewOnchainPassService(newMemOnchainStore(), nil, nil, nil, []*OnchainNetwork{testNetwork(t, true)})
	_, err := svc.Fund(context.Background(), "pass_1", &econ.Reservation{ID: "r1", Rail: "x402-solana-devnet"}, 1000)
	var denied *AuthorityDenied
	if !errors.As(err, &denied) || denied.ReasonCodes[0] != DenyOnchainRequired {
		t.Fatalf("got %v", err)
	}
}

func TestAPassOnAnotherClusterIsRefused(t *testing.T) {
	store := newMemOnchainStore()
	store.bindings["pass_1"] = &OnchainBinding{PassID: "pass_1", Network: "solana", Address: "11111111111111111111111111111111"}
	svc := NewOnchainPassService(store, nil, nil, nil, []*OnchainNetwork{testNetwork(t, false)})
	_, err := svc.Fund(context.Background(), "pass_1", &econ.Reservation{ID: "r1", Rail: "x402-solana-devnet"}, 1000)
	var denied *AuthorityDenied
	if !errors.As(err, &denied) || denied.ReasonCodes[0] != DenyOnchainOtherNetwork {
		t.Fatalf("got %v", err)
	}
}

func TestCheckIgnoresPassesThatArentOnChain(t *testing.T) {
	svc := NewOnchainPassService(newMemOnchainStore(), nil, nil, nil, []*OnchainNetwork{testNetwork(t, false)})
	if err := svc.Check(context.Background(), "pass_1"); err != nil {
		t.Fatal(err)
	}
}

func TestDenialsNameTheOnChainRule(t *testing.T) {
	cases := map[error]string{
		spendpass.ErrFrozen:  DenyOnchainFrozen,
		spendpass.ErrRevoked: DenyOnchainRevoked,
		spendpass.ErrExpired: DenyOnchainExpired,
		fmt.Errorf("%w: too much", spendpass.ErrOverLimit):            DenyOnchainLimit,
		spendpass.Explain(errors.New("custom program error: 0x1775")): DenyOnchainLimit, // OverBudget
		spendpass.Explain(errors.New("custom program error: 0x1771")): DenyOnchainFrozen,
	}
	for err, want := range cases {
		var denied *AuthorityDenied
		if d := denyFor(err); !errors.As(d, &denied) || denied.ReasonCodes[0] != want {
			t.Errorf("%v: got %v, want %s", err, d, want)
		}
	}
	if denyFor(errors.New("connection refused")) != nil {
		t.Error("an outage isn't a denial")
	}
}

func TestPullStateMovesOnlyFromWhereItWas(t *testing.T) {
	store := newMemOnchainStore()
	p := &OnchainPull{ReservationID: "r1", State: PullPulled}
	if err := store.InsertOnchainPull(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	p.State = PullRefunding
	if err := store.UpdateOnchainPull(context.Background(), p, PullPulled); err != nil {
		t.Fatal(err)
	}
	// A second process that also read PULLED can't claim the refund again.
	if err := store.UpdateOnchainPull(context.Background(), p, PullPulled); !errors.Is(err, shared.ErrConflict) {
		t.Fatalf("second claim: %v", err)
	}
}
