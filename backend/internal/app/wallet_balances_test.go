package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

type fakeWalletRPC struct {
	lamports  map[string]uint64
	usdc      map[string]uint64
	downFor   map[string]bool
	calls     int
	noAccount map[string]bool
}

func (f *fakeWalletRPC) GetBalance(_ context.Context, pk solana.PublicKey, _ string) (uint64, error) {
	f.calls++
	if f.downFor[pk.String()] {
		return 0, errors.New("node unreachable")
	}
	return f.lamports[pk.String()], nil
}

func (f *fakeWalletRPC) GetTokenAccountBalance(_ context.Context, pk solana.PublicKey, _ string) (solana.TokenBalance, error) {
	f.calls++
	for owner, amount := range f.usdc {
		o := solana.MustPublicKey(owner)
		ata, _ := solana.AssociatedTokenAddress(o, solana.MustPublicKey(devnetUSDC), solana.TokenProgram)
		if ata == pk {
			return solana.TokenBalance{Amount: amount, Decimals: 6}, nil
		}
	}
	return solana.TokenBalance{}, solana.ErrAccountNotFound
}

var devnetUSDC = func() string { m, _ := chain.AssetAddress(chain.SolanaDevnet, "USDC"); return m }()

type walletsOnly struct{ wallets []account.Wallet }

func (w walletsOnly) SaveUserWallets(context.Context, string, []account.Wallet, time.Time) error {
	return nil
}
func (w walletsOnly) ListUserWallets(context.Context, string) ([]account.Wallet, error) {
	return w.wallets, nil
}

func TestWalletBalances_OnlyTheirOwnWalletsAndWhatTheClusterSays(t *testing.T) {
	const embedded = "5t62QVXAP78GQbvzPjRwjEgbLMYLu3JMHKaRFEGXrXsC"
	const external = "9kEVg2noE8uubD6e1jMtEwumULEyNLgr7uYCXDVL4CLW"
	const someoneElse = "GhpmTWT8ng3viaQtFNDF7T7RyrmemosWvEDetKmBEoPA"
	rpc := &fakeWalletRPC{
		lamports: map[string]uint64{embedded: 2_500_000_000, someoneElse: 9},
		usdc:     map[string]uint64{embedded: 1_250_000, someoneElse: 15_000_000},
		downFor:  map[string]bool{},
	}
	accounts := &AccountService{}
	accounts.SetWalletStore(walletsOnly{wallets: []account.Wallet{
		{Chain: "solana", Address: embedded, Kind: account.WalletEmbedded, Source: "privy"},
		{Chain: "solana", Address: external, Kind: account.WalletExternal, Source: "privy"},
		{Chain: "ethereum", Address: "0xabc", Kind: account.WalletExternal, Source: "privy"},
	}})
	now := time.Now()
	svc := NewWalletBalanceService(accounts, map[string]WalletRPC{chain.SolanaDevnet: rpc})
	svc.now = func() time.Time { return now }

	v, err := svc.WalletsFor(context.Background(), "user_1", "solana-devnet", false)
	if err != nil {
		t.Fatal(err)
	}
	if v.Network != chain.SolanaDevnet || v.Mint != devnetUSDC || len(v.Wallets) != 2 {
		t.Fatalf("only the person's two Solana wallets, on devnet: %+v", v)
	}
	if w := v.Wallets[0]; w.Address != embedded || w.Kind != "embedded" || !w.OK || w.SOLLamports != 2_500_000_000 || w.USDCMinor != 1_250_000 {
		t.Fatalf("the embedded wallet's holdings: %+v", w)
	}
	// A wallet with no USDC account holds none, and is still a good reading.
	if w := v.Wallets[1]; w.Address != external || !w.OK || w.USDCMinor != 0 || w.SOLLamports != 0 {
		t.Fatalf("an empty wallet reads as empty: %+v", w)
	}
	for _, w := range v.Wallets {
		if w.Address == someoneElse {
			t.Fatal("another person's wallet must never appear")
		}
	}

	// Asked again at once: the node isn't asked again; force asks.
	before := rpc.calls
	if _, err := svc.WalletsFor(context.Background(), "user_1", "solana-devnet", false); err != nil || rpc.calls != before {
		t.Fatalf("a repeat within the cache window is answered from it: %d calls, err %v", rpc.calls-before, err)
	}
	if _, err := svc.WalletsFor(context.Background(), "user_1", "solana-devnet", true); err != nil || rpc.calls == before {
		t.Fatalf("a forced read asks the node: %d calls, err %v", rpc.calls-before, err)
	}

	// A node that can't be read is "unknown", never "zero".
	rpc.downFor[embedded] = true
	now = now.Add(time.Minute)
	v, err = svc.WalletsFor(context.Background(), "user_1", "solana-devnet", false)
	if err != nil || v.Wallets[0].OK || v.Wallets[0].USDCMinor != 0 {
		t.Fatalf("an unreadable wallet is not ok: %+v %v", v.Wallets[0], err)
	}

	// A network this server doesn't read, or a sandbox, is refused.
	for _, n := range []string{"sandbox", "base", "solana"} {
		if _, err := svc.WalletsFor(context.Background(), "user_1", n, false); !errors.Is(err, ErrNoWalletNetwork) {
			t.Errorf("%s: %v, want ErrNoWalletNetwork", n, err)
		}
	}
}
