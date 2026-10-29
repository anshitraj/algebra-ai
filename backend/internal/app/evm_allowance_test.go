package app

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/internal/platform/evm"
)

// usdcChain is a fake EVM node with USDC on it: balances, allowances,
// permit nonces, and the effects of the payer's permit, transferFrom and
// transfer transactions, each mined at once unless held.
type usdcChain struct {
	mu        sync.Mutex
	balance   map[[20]byte]*big.Int
	allowance map[[2][20]byte]*big.Int
	nonces    map[[20]byte]*big.Int
	receipts  map[string]string
	hold      bool // leave sent transactions unmined
	sent      int
}

func newUSDCChain() *usdcChain {
	return &usdcChain{balance: map[[20]byte]*big.Int{}, allowance: map[[2][20]byte]*big.Int{}, nonces: map[[20]byte]*big.Int{}, receipts: map[string]string{}}
}

func (c *usdcChain) get(m map[[20]byte]*big.Int, a [20]byte) *big.Int {
	if v, ok := m[a]; ok {
		return v
	}
	return new(big.Int)
}

func (c *usdcChain) Uint(_ context.Context, _ string, sig string, args ...[20]byte) (*big.Int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch sig {
	case "balanceOf(address)":
		return new(big.Int).Set(c.get(c.balance, args[0])), nil
	case "nonces(address)":
		return new(big.Int).Set(c.get(c.nonces, args[0])), nil
	case "allowance(address,address)":
		if v, ok := c.allowance[[2][20]byte{args[0], args[1]}]; ok {
			return new(big.Int).Set(v), nil
		}
		return new(big.Int), nil
	}
	return nil, fmt.Errorf("unknown %s", sig)
}

func (c *usdcChain) Params(context.Context, string, string, string) (*evm.TxParams, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &evm.TxParams{ChainID: big.NewInt(84532), Nonce: big.NewInt(int64(c.sent)), MaxPriorityFeePerGas: big.NewInt(1), MaxFeePerGas: big.NewInt(2), GasLimit: big.NewInt(90_000)}, nil
}

func word(b []byte, i int) []byte  { return b[4+32*i : 4+32*(i+1)] }
func addrOf(w []byte) (a [20]byte) { copy(a[:], w[12:]); return a }

func (c *usdcChain) SendRaw(_ context.Context, raw string) (string, error) {
	tx, err := evm.DecodeSignedEIP1559(raw)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent++
	if c.hold {
		return tx.Hash, nil
	}
	ok := true
	d := tx.Data
	switch hex.EncodeToString(d[:4]) {
	case hex.EncodeToString(evm.Keccak256([]byte("permit(address,address,uint256,uint256,uint8,bytes32,bytes32)"))[:4]):
		owner, spender := addrOf(word(d, 0)), addrOf(word(d, 1))
		c.allowance[[2][20]byte{owner, spender}] = new(big.Int).SetBytes(word(d, 2))
		c.nonces[owner] = new(big.Int).Add(c.get(c.nonces, owner), big.NewInt(1))
	case hex.EncodeToString(evm.Keccak256([]byte("transferFrom(address,address,uint256)"))[:4]):
		from, to, v := addrOf(word(d, 0)), addrOf(word(d, 1)), new(big.Int).SetBytes(word(d, 2))
		key := [2][20]byte{from, tx.From}
		if c.get(c.balance, from).Cmp(v) < 0 || c.allowance[key] == nil || c.allowance[key].Cmp(v) < 0 {
			ok = false
			break
		}
		c.allowance[key] = new(big.Int).Sub(c.allowance[key], v)
		c.balance[from] = new(big.Int).Sub(c.get(c.balance, from), v)
		c.balance[to] = new(big.Int).Add(c.get(c.balance, to), v)
	case hex.EncodeToString(evm.Keccak256([]byte("transfer(address,uint256)"))[:4]):
		to, v := addrOf(word(d, 0)), new(big.Int).SetBytes(word(d, 1))
		if c.get(c.balance, tx.From).Cmp(v) < 0 {
			ok = false
			break
		}
		c.balance[tx.From] = new(big.Int).Sub(c.get(c.balance, tx.From), v)
		c.balance[to] = new(big.Int).Add(c.get(c.balance, to), v)
	default:
		ok = false
	}
	c.receipts[tx.Hash] = map[bool]string{true: "0x1", false: "0x0"}[ok]
	return tx.Hash, nil
}

func (c *usdcChain) TransactionReceipt(_ context.Context, hash string) (*evm.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st, ok := c.receipts[hash]; ok {
		return &evm.Receipt{Status: st}, nil
	}
	return nil, nil
}

type fixedReservations struct{ rs []econ.Reservation }

func (f *fixedReservations) Reservations(context.Context, string) ([]econ.Reservation, error) {
	return f.rs, nil
}

type evmRig struct {
	svc         *EVMAllowanceService
	chain       *usdcChain
	owner       *evm.Key
	payer       *evm.Key
	net         *EVMNetwork
	store       *memAllowances
	pulls       *memOnchainStore
	reservation *fixedReservations
}

func newEVMRig(t *testing.T) *evmRig {
	t.Helper()
	owner, _ := evm.GenerateKey()
	payer, _ := evm.GenerateKey()
	token, _ := evm.ParseAddress("0x036cbd53842c5426634e7929541ec2318f3dcf7e")
	c := newUSDCChain()
	c.balance[owner.Address()] = big.NewInt(2_000_000)
	n := &EVMNetwork{Network: "base-sepolia", RailName: "x402-base-sepolia", RPC: c, Payer: payer, Token: token,
		Domain: evm.Domain{Name: "USDC", Version: "2", ChainID: big.NewInt(84532), VerifyingContract: token}}
	vaults := &memVaults{m: map[string]*OwnerVaultRecord{"user_1": {UserID: "user_1", EVMAddress: owner.Hex(), SolanaAddress: "So1"}}}
	r := &evmRig{chain: c, owner: owner, payer: payer, net: n, store: &memAllowances{m: map[string]*VaultAllowance{}}, pulls: newMemOnchainStore(), reservation: &fixedReservations{}}
	r.svc = NewEVMAllowanceService(r.store, vaults, onePass{spendpass.Pass{ID: "pass_1", UserID: "user_1"}}, r.pulls, r.reservation, []*EVMNetwork{n}, 0, nil)
	r.svc.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	r.svc.wait, r.svc.poll = 50*time.Millisecond, 5*time.Millisecond
	return r
}

// sign is what the owner's browser does: an EIP-2612 permit for value.
func (r *evmRig) sign(t *testing.T, key *evm.Key, spender [20]byte, value int64, p *PermitParams) string {
	t.Helper()
	nonce, _ := new(big.Int).SetString(p.Nonce, 10)
	h, err := evm.Permit{Owner: key.Address(), Spender: spender, Value: big.NewInt(value), Nonce: nonce, Deadline: big.NewInt(p.Deadline)}.Hash()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := key.Sign(evm.TypedDigest(r.net.Domain, h))
	if err != nil {
		t.Fatal(err)
	}
	return "0x" + hex.EncodeToString(sig)
}

func TestEVMAllowance_PermitSetsAndStopsTheLimit(t *testing.T) {
	ctx := context.Background()
	r := newEVMRig(t)
	p, err := r.svc.Prepare(ctx, "user_1", "base-sepolia")
	if err != nil || p.Spender != r.payer.Hex() || p.Owner != r.owner.Hex() || p.Nonce != "0" || p.Name != "USDC" || p.ChainID != 84532 {
		t.Fatalf("prepare: %+v %v", p, err)
	}
	res, err := r.svc.Submit(ctx, "user_1", PermitSubmit{Network: "base-sepolia", ValueMinor: 5_000, Deadline: p.Deadline, Signature: r.sign(t, r.owner, r.payer.Address(), 5_000, p)})
	if err != nil || res.Pending || !res.Allowance.Active || res.Allowance.LeftMinor != 5_000 || res.Allowance.BalanceMinor != 2_000_000 {
		t.Fatalf("set: %+v %v", res, err)
	}
	// What it refuses: a permit for another spender, another value, a key that isn't the vault's, too much.
	p, _ = r.svc.Prepare(ctx, "user_1", "base-sepolia")
	other, _ := evm.GenerateKey()
	bad := []PermitSubmit{
		{Network: "base-sepolia", ValueMinor: 5_000, Deadline: p.Deadline, Signature: r.sign(t, r.owner, other.Address(), 5_000, p)},
		{Network: "base-sepolia", ValueMinor: 9_000, Deadline: p.Deadline, Signature: r.sign(t, r.owner, r.payer.Address(), 5_000, p)},
		{Network: "base-sepolia", ValueMinor: 5_000, Deadline: p.Deadline, Signature: r.sign(t, other, r.payer.Address(), 5_000, p)},
		{Network: "base-sepolia", ValueMinor: 2_000_000_000, Deadline: p.Deadline, Signature: r.sign(t, r.owner, r.payer.Address(), 2_000_000_000, p)},
		{Network: "base-sepolia", ValueMinor: 5_000, Deadline: time.Now().Add(time.Minute).Unix(), Signature: r.sign(t, r.owner, r.payer.Address(), 5_000, p)},
	}
	for i, b := range bad {
		if _, err := r.svc.Submit(ctx, "user_1", b); err == nil {
			t.Errorf("bad permit %d was submitted", i)
		}
	}
	// Stop: a permit for 0.
	res, err = r.svc.Submit(ctx, "user_1", PermitSubmit{Network: "base-sepolia", ValueMinor: 0, Deadline: p.Deadline, Signature: r.sign(t, r.owner, r.payer.Address(), 0, p)})
	if err != nil || res.Allowance.Active || res.Allowance.LeftMinor != 0 {
		t.Fatalf("stop: %+v %v", res, err)
	}
	if rec, _ := r.store.VaultAllowance(ctx, "user_1", "base-sepolia"); rec.State != AllowanceRevoked {
		t.Errorf("recorded as stopped: %+v", rec)
	}
}

func TestEVMAllowance_PullBeforePayAndRefund(t *testing.T) {
	ctx := context.Background()
	r := newEVMRig(t)
	p, _ := r.svc.Prepare(ctx, "user_1", "base-sepolia")
	if _, err := r.svc.Submit(ctx, "user_1", PermitSubmit{Network: "base-sepolia", ValueMinor: 5_000, Deadline: p.Deadline, Signature: r.sign(t, r.owner, r.payer.Address(), 5_000, p)}); err != nil {
		t.Fatal(err)
	}
	if on, _ := r.svc.Funding(ctx, "pass_1", "x402-base-sepolia"); !on {
		t.Fatal("payments on Base Sepolia come from the vault")
	}
	rv := &econ.Reservation{ID: "rsv_1", IntentID: "eint_1", Rail: "x402-base-sepolia", HoldMinor: 3_000}
	pull, err := r.svc.Fund(ctx, "pass_1", rv, 1_000)
	if err != nil || pull.State != PullPulled || pull.Source != PullSourceVaultEVM {
		t.Fatalf("pull: %+v %v", pull, err)
	}
	if r.chain.balance[r.payer.Address()].Int64() != 1_000 || r.chain.balance[r.owner.Address()].Int64() != 1_999_000 {
		t.Fatalf("1000 moved from the vault to the payer: %v %v", r.chain.balance[r.payer.Address()], r.chain.balance[r.owner.Address()])
	}
	if again, err := r.svc.Fund(ctx, "pass_1", rv, 1_000); err != nil || again.PullSignature != pull.PullSignature {
		t.Errorf("funding the same attempt again reuses its pull: %v", err)
	}
	// The limit holds: 4000 left, 4500 asked.
	var denied *AuthorityDenied
	if _, err := r.svc.Fund(ctx, "pass_1", &econ.Reservation{ID: "rsv_2", IntentID: "eint_2", Rail: "x402-base-sepolia", HoldMinor: 5_000}, 4_500); !errors.As(err, &denied) || denied.ReasonCodes[0] != DenyVaultAllowanceLow {
		t.Fatalf("over what is left: %v", err)
	}
	// The attempt ended without its payment being handed over: everything goes back.
	r.reservation.rs = []econ.Reservation{{ID: "rsv_1", IntentID: "eint_1", State: econ.ReservationReleased}}
	if _, err := r.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.pulls.OnchainPull(ctx, "rsv_1"); got.State != PullRefunding || got.RefundSignature == "" {
		t.Fatalf("refund sent: %+v", got)
	}
	if _, err := r.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.pulls.OnchainPull(ctx, "rsv_1"); got.State != PullRefunded || *got.RefundMinor != 1_000 {
		t.Fatalf("refunded: %+v", got)
	}
	if r.chain.balance[r.owner.Address()].Int64() != 2_000_000 {
		t.Errorf("the vault has its USDC back: %v", r.chain.balance[r.owner.Address()])
	}
}

func TestEVMAllowance_AnUnminedPullReleasesNothing(t *testing.T) {
	ctx := context.Background()
	r := newEVMRig(t)
	p, _ := r.svc.Prepare(ctx, "user_1", "base-sepolia")
	if _, err := r.svc.Submit(ctx, "user_1", PermitSubmit{Network: "base-sepolia", ValueMinor: 5_000, Deadline: p.Deadline, Signature: r.sign(t, r.owner, r.payer.Address(), 5_000, p)}); err != nil {
		t.Fatal(err)
	}
	r.chain.hold = true
	_, err := r.svc.Fund(ctx, "pass_1", &econ.Reservation{ID: "rsv_9", IntentID: "eint_9", Rail: "x402-base-sepolia", HoldMinor: 3_000}, 1_000)
	if err == nil || !strings.Contains(err.Error(), "isn't confirmed") {
		t.Fatalf("a pull not mined in time stops the payment: %v", err)
	}
	if got, _ := r.pulls.OnchainPull(ctx, "rsv_9"); got == nil || got.State != PullSending {
		t.Fatalf("the pull is recorded for the reconciler: %+v", got)
	}
}
