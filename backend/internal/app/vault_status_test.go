package app

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/spendpass"
)

// vaultNode is a node that serves one Spend Pass account, its vault's balance
// and the payer's SOL: just what a vault check reads.
type vaultNode struct {
	mu       sync.Mutex
	program  solana.PublicKey
	pass     solana.PublicKey
	data     []byte // nil: the pass account is closed
	vault    uint64
	lamports uint64
	calls    map[string]int
}

func (v *vaultNode) serve(t *testing.T) string {
	t.Helper()
	v.calls = map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params []any           `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		v.mu.Lock()
		defer v.mu.Unlock()
		v.calls[req.Method]++
		ctx := map[string]any{"slot": 1}
		var result any
		switch req.Method {
		case "getAccountInfo":
			if addr, _ := req.Params[0].(string); addr == v.pass.String() && v.data != nil {
				result = map[string]any{"context": ctx, "value": map[string]any{
					"lamports": 1, "owner": v.program.String(), "executable": false, "data": []string{base64.StdEncoding.EncodeToString(v.data), "base64"},
				}}
			} else {
				result = map[string]any{"context": ctx, "value": nil}
			}
		case "getTokenAccountBalance":
			result = map[string]any{"context": ctx, "value": map[string]any{"amount": u64s(v.vault), "decimals": 6}}
		case "getBalance":
			result = map[string]any{"context": ctx, "value": v.lamports}
		default:
			result = nil
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func u64s(n uint64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// passBytes lays a pass out as the program stores it (the first byte is the
// account discriminator, 1).
func passBytes(st spendpass.State) []byte {
	b := make([]byte, spendpass.AccountSize)
	b[0] = 1
	r := b[1:]
	put := func(k solana.PublicKey) { copy(r, k[:]); r = r[32:] }
	u := func(n uint64) { binary.LittleEndian.PutUint64(r, n); r = r[8:] }
	put(st.Owner)
	put(st.Agent)
	put(st.Mint)
	put(st.Destination)
	u(st.ID)
	u(st.PerCallCap)
	u(st.TotalBudget)
	u(st.Spent)
	u(uint64(st.WindowSecs))
	u(st.WindowCap)
	u(uint64(st.WindowStart))
	u(st.WindowSpent)
	u(uint64(st.ExpiresAt))
	u(uint64(st.CreatedAt))
	u(st.Pulls)
	r = r[32:] // last intent
	if st.Frozen {
		r[0] = 1
	}
	if st.Revoked {
		r[1] = 1
	}
	return b
}

type vaultFixture struct {
	svc  *OnchainPassService
	n    *OnchainNetwork
	node *vaultNode
	st   spendpass.State
}

// newVaultFixture binds "pass_1" to a healthy pass on a node: made for this
// server's payer, funded with 500,000 micro-USDC, a 200,000 per-call cap.
func newVaultFixture(t *testing.T, required bool) *vaultFixture {
	t.Helper()
	n := testNetwork(t, required)
	owner, _ := solana.NewKeypair()
	pass := solana.MustPublicKey("11111111111111111111111111111112")
	dest, _ := solana.AssociatedTokenAddress(n.Payer.PublicKey(), n.Mint, solana.TokenProgram)
	now := time.Now()
	st := spendpass.State{Owner: owner.PublicKey(), Agent: n.Payer.PublicKey(), Mint: n.Mint, Destination: dest,
		PerCallCap: 200_000, TotalBudget: 1_000_000, ExpiresAt: now.Add(24 * time.Hour).Unix(), CreatedAt: now.Add(-time.Hour).Unix()}
	node := &vaultNode{program: n.Program.ID, pass: pass, data: passBytes(st), vault: 500_000, lamports: 5 * payerFeeFloor}
	n.RPC = solana.NewRPC(node.serve(t), nil)
	store := newMemOnchainStore()
	store.bindings["pass_1"] = &OnchainBinding{PassID: "pass_1", Network: n.Network, Address: pass.String(), OwnerWallet: owner.PublicKey().String()}
	return &vaultFixture{svc: NewOnchainPassService(store, nil, nil, nil, []*OnchainNetwork{n}), n: n, node: node, st: st}
}

const payerFeeFloor = 20_000_000

func (f *vaultFixture) set(mut func(*spendpass.State)) {
	mut(&f.st)
	f.node.mu.Lock()
	f.node.data = passBytes(f.st)
	f.node.mu.Unlock()
}

func TestVaultStatusOfAnUnboundPass(t *testing.T) {
	ctx := context.Background()
	required := NewOnchainPassService(newMemOnchainStore(), nil, nil, nil, []*OnchainNetwork{testNetwork(t, true)})
	if got := required.VaultStatus(ctx, "pass_1", "solana-devnet"); !got.Required || got.For(1000) != DenyOnchainRequired {
		t.Fatalf("a pass with no vault where one is required: %+v", got)
	}
	optional := NewOnchainPassService(newMemOnchainStore(), nil, nil, nil, []*OnchainNetwork{testNetwork(t, false)})
	if got := optional.VaultStatus(ctx, "pass_1", "solana-devnet"); got.Required || got.For(1000) != "" {
		t.Fatalf("no vault, none required: %+v", got)
	}
	// The sandbox has no vault and no requirement, and neither does no service at all.
	if got := required.VaultStatus(ctx, "pass_1", "sandbox"); got.Required || got.For(1000) != "" {
		t.Fatalf("the sandbox: %+v", got)
	}
	var none *OnchainPassService
	if got := none.VaultStatus(ctx, "pass_1", "solana-devnet"); got.Required || got.For(1000) != "" {
		t.Fatalf("no on-chain passes at all: %+v", got)
	}
}

func TestVaultStatusJudgesAPaymentBeforeAnyoneApproves(t *testing.T) {
	f := newVaultFixture(t, true)
	ctx := context.Background()
	v := f.svc.VaultStatus(ctx, "pass_1", "solana-devnet")
	if v.Code != "" || !v.Funded || v.FundedMinor != 500_000 {
		t.Fatalf("a healthy funded vault: %+v", v)
	}
	for amount, want := range map[int64]string{
		0:       "", // an unlisted price: only "can it pay at all"
		100_000: "",
		200_000: "",
		200_001: DenyOnchainLimit, // over the per-call cap the program enforces
		300_000: DenyOnchainLimit, // still over the per-call cap
	} {
		if got := v.For(amount); got != want {
			t.Errorf("For(%d) = %q, want %q", amount, got, want)
		}
	}
	// Under the cap but more than the vault holds.
	f.node.vault = 50_000
	if got := f.svc.VaultStatus(ctx, "pass_1", "solana-devnet").For(100_000); got != DenyOnchainUnfunded {
		t.Errorf("a vault holding less than the price: %q", got)
	}
}

func TestVaultStatusNamesWhyAVaultCannotPayAtAll(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		mut  func(*spendpass.State)
		want string
	}{
		"frozen":  {func(s *spendpass.State) { s.Frozen = true }, DenyOnchainFrozen},
		"revoked": {func(s *spendpass.State) { s.Revoked = true }, DenyOnchainRevoked},
		"expired": {func(s *spendpass.State) { s.ExpiresAt = time.Now().Add(-time.Minute).Unix() }, DenyOnchainExpired},
		"made for an earlier payer key": {func(s *spendpass.State) {
			other, _ := solana.NewKeypair()
			s.Agent = other.PublicKey()
		}, DenyOnchainAgentChanged},
		"paying into someone else's account": {func(s *spendpass.State) {
			other, _ := solana.NewKeypair()
			s.Destination = other.PublicKey()
		}, DenyOnchainAgentChanged},
	} {
		t.Run(name, func(t *testing.T) {
			f := newVaultFixture(t, true)
			f.set(c.mut)
			if got := f.svc.VaultStatus(ctx, "pass_1", "solana-devnet"); got.Code != c.want || got.For(10_000) != c.want {
				t.Fatalf("got %+v, want code %s", got, c.want)
			}
		})
	}
	t.Run("closed by its owner", func(t *testing.T) {
		f := newVaultFixture(t, true)
		f.node.data = nil
		if got := f.svc.VaultStatus(ctx, "pass_1", "solana-devnet"); got.Code != DenyOnchainMissing {
			t.Fatalf("got %+v", got)
		}
	})
}

// Fund is the authority: a vault made for another payer key is refused before
// anything is pulled, with a reason that says what to do.
func TestFundRefusesAVaultMadeForAnotherPayerKey(t *testing.T) {
	f := newVaultFixture(t, true)
	other, _ := solana.NewKeypair()
	f.set(func(s *spendpass.State) { s.Agent = other.PublicKey() })
	_, err := f.svc.Fund(context.Background(), "pass_1", &econ.Reservation{ID: "r1", Rail: "x402-solana-devnet"}, 1000)
	var denied *AuthorityDenied
	if !errors.As(err, &denied) || denied.ReasonCodes[0] != DenyOnchainAgentChanged {
		t.Fatalf("got %v", err)
	}
}

func TestNetworksSayWhetherThePayerCanPayFees(t *testing.T) {
	f := newVaultFixture(t, true)
	f.n.SignerMode = "generated"
	ctx := context.Background()
	got := f.svc.Networks(ctx)
	if len(got) != 1 || got[0].SignerMode != "generated" || !got[0].SignerReady {
		t.Fatalf("a payer with SOL: %+v", got)
	}
	// Asked again at once, it is answered from memory: this runs on every page load.
	f.svc.Networks(ctx)
	if f.node.calls["getBalance"] != 1 {
		t.Fatalf("the balance was read %d times", f.node.calls["getBalance"])
	}
	// Out of SOL, a minute later: not ready, so the console can say so.
	f.node.lamports = 1000
	f.svc.now = func() time.Time { return time.Now().Add(time.Minute) }
	if got := f.svc.Networks(ctx); got[0].SignerReady {
		t.Fatalf("a payer with no SOL must not be ready: %+v", got)
	}
}
