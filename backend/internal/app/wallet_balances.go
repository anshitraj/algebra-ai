package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// WalletBalances reads what the signed-in person's own wallets hold on a
// cluster, so the console can show them an address to fund and what has
// arrived. It answers only for wallets already recorded as theirs
// (user_wallets): it is not a way to look up an address.

// WalletRPC is the node a cluster's balances are read from.
type WalletRPC interface {
	GetBalance(ctx context.Context, pk solana.PublicKey, commitment string) (uint64, error)
	GetTokenAccountBalance(ctx context.Context, pk solana.PublicKey, commitment string) (solana.TokenBalance, error)
}

// WalletBalance is one wallet's holdings on one cluster.
type WalletBalance struct {
	Address string `json:"address"`
	// Kind is "embedded" (Privy holds the key for the person) or "external".
	Kind string `json:"kind"`
	// SOLLamports and USDCMinor are what the cluster says. OK is false when
	// the node could not be read just now: then both are unknown, not zero.
	SOLLamports uint64 `json:"sol_lamports"`
	USDCMinor   int64  `json:"usdc_minor"`
	OK          bool   `json:"ok"`
}

// WalletsView is a person's wallets and their balances on one network.
type WalletsView struct {
	Network string          `json:"network"`
	Mint    string          `json:"usdc_mint"`
	Wallets []WalletBalance `json:"wallets"`
}

// WalletBalanceService answers WalletsFor. The per-address answers are kept
// for a few seconds, so a page that asks again doesn't ask the node again.
type WalletBalanceService struct {
	accounts *AccountService
	rpcs     map[string]WalletRPC // by network: chain.Solana, chain.SolanaDevnet
	now      func() time.Time
	ttl      time.Duration

	mu    sync.Mutex
	cache map[string]cachedBalance
}

type cachedBalance struct {
	at time.Time
	b  WalletBalance
}

// NewWalletBalanceService reads balances from rpcs, keyed by network.
func NewWalletBalanceService(accounts *AccountService, rpcs map[string]WalletRPC) *WalletBalanceService {
	return &WalletBalanceService{accounts: accounts, rpcs: rpcs, now: time.Now, ttl: 8 * time.Second, cache: map[string]cachedBalance{}}
}

// ErrNoWalletNetwork: the network isn't a Solana cluster this server reads.
var ErrNoWalletNetwork = fmt.Errorf("%w: wallet balances are available on Solana mainnet and devnet", ErrInvalidRequest)

// WalletsFor lists userID's Solana wallets with their balances on network.
// force skips the short cache (a person pressed refresh).
func (s *WalletBalanceService) WalletsFor(ctx context.Context, userID, network string, force bool) (*WalletsView, error) {
	network = chain.NormalizeNetwork(network)
	rpc := s.rpcs[network]
	mint, ok := chain.AssetAddress(network, "USDC")
	if rpc == nil || !ok {
		return nil, ErrNoWalletNetwork
	}
	mintKey, err := solana.ParsePublicKey(mint)
	if err != nil {
		return nil, err
	}
	owned, err := s.accounts.Wallets(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := &WalletsView{Network: network, Mint: mint, Wallets: []WalletBalance{}}
	for _, w := range owned {
		if w.Chain != "solana" {
			continue
		}
		out.Wallets = append(out.Wallets, s.balance(ctx, rpc, network, mintKey, w.Address, w.Kind, force))
	}
	return out, nil
}

func (s *WalletBalanceService) balance(ctx context.Context, rpc WalletRPC, network string, mint solana.PublicKey, address, kind string, force bool) WalletBalance {
	key := network + "/" + address
	s.mu.Lock()
	c, hit := s.cache[key]
	s.mu.Unlock()
	if hit && !force && s.now().Sub(c.at) < s.ttl {
		c.b.Kind = kind
		return c.b
	}
	b := WalletBalance{Address: address, Kind: kind}
	owner, err := solana.ParsePublicKey(address)
	if err != nil {
		return b
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	lamports, err := rpc.GetBalance(ctx, owner, solana.Confirmed)
	if err != nil {
		return b
	}
	b.SOLLamports = lamports
	token, err := solana.AssociatedTokenAddress(owner, mint, solana.TokenProgram)
	if err != nil {
		return b
	}
	switch bal, err := rpc.GetTokenAccountBalance(ctx, token, solana.Confirmed); {
	case errors.Is(err, solana.ErrAccountNotFound):
		// No USDC account yet: it holds none.
	case err != nil:
		return b
	default:
		b.USDCMinor = int64(bal.Amount)
	}
	b.OK = true
	s.mu.Lock()
	s.cache[key] = cachedBalance{at: s.now(), b: b}
	s.mu.Unlock()
	return b
}
