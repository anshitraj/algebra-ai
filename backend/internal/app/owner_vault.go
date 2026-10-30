package app

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/evm"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// The owner's vault (docs/WALLET.md): a wallet the owner's browser made and
// holds the keys of. Algebra records its two public addresses after checking
// that both keys signed the ownership message, reads their balances, gives
// the browser what it needs to build a withdrawal, and relays the signed
// transaction. It never holds, sees or receives a key.

// OwnerVaultRecord is an account's recorded vault addresses.
type OwnerVaultRecord struct {
	UserID        string    `json:"-"`
	SolanaAddress string    `json:"solana_address"`
	EVMAddress    string    `json:"evm_address"`
	ProofMessage  string    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// OwnerVaultStore keeps one vault per account.
type OwnerVaultStore interface {
	OwnerVault(ctx context.Context, userID string) (*OwnerVaultRecord, error) // shared.ErrNotFound if none
	// SaveOwnerVault records or replaces the account's vault; shared.ErrConflict
	// when another account has either address.
	SaveOwnerVault(ctx context.Context, r *OwnerVaultRecord) error
}

// VaultSolanaRPC is what the vault needs from a Solana node.
type VaultSolanaRPC interface {
	WalletRPC
	GetLatestBlockhash(ctx context.Context, commitment string) (solana.BlockhashInfo, error)
	GetAccountInfo(ctx context.Context, pk solana.PublicKey, commitment string) (*solana.AccountInfo, error)
	SendTransaction(ctx context.Context, txBase64 string, o solana.SendOptions) (string, error)
}

// VaultEVMRPC is what the vault needs from an EVM node.
type VaultEVMRPC interface {
	NativeBalance(ctx context.Context, owner string) (*big.Int, error)
	TokenBalance(ctx context.Context, token, owner string) (*big.Int, error)
	Params(ctx context.Context, from, to, data string) (*evm.TxParams, error)
	SendRaw(ctx context.Context, raw string) (string, error)
}

// VaultChain is one chain the vault holds USDC on.
type VaultChain struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // "solana" or "evm"
	Test    bool   `json:"test"`
	ChainID int64  `json:"chain_id,omitempty"`
	USDC    string `json:"usdc"`
	// Native is the coin that pays the network fee (on Arc, USDC itself).
	NativeSymbol   string `json:"native_symbol"`
	NativeDecimals int    `json:"native_decimals"`
	// Deposits: whether the console shows this chain's address to deposit to.
	// Off on mainnet until the design has been independently reviewed.
	Deposits bool `json:"deposits"`
	// Available: this server has a node for it.
	Available bool `json:"available"`
}

var vaultChains = []VaultChain{
	{ID: chain.SolanaDevnet, Name: "Solana", Kind: "solana", Test: true, NativeSymbol: "SOL", NativeDecimals: 9},
	{ID: chain.BaseSepolia, Name: "Base", Kind: "evm", Test: true, ChainID: 84532, NativeSymbol: "ETH", NativeDecimals: 18},
	{ID: chain.ArcTestnet, Name: "Arc", Kind: "evm", Test: true, ChainID: 5042002, NativeSymbol: "USDC", NativeDecimals: 18},
	{ID: chain.Solana, Name: "Solana", Kind: "solana", NativeSymbol: "SOL", NativeDecimals: 9},
	{ID: chain.Base, Name: "Base", Kind: "evm", ChainID: 8453, NativeSymbol: "ETH", NativeDecimals: 18},
	{ID: chain.Arc, Name: "Arc", Kind: "evm", ChainID: 5042, NativeSymbol: "USDC", NativeDecimals: 18},
}

// OwnershipProof is what the browser sends to record its vault.
type OwnershipProof struct {
	SolanaAddress   string `json:"solana_address"`
	EVMAddress      string `json:"evm_address"`
	Message         string `json:"message"`
	SolanaSignature string `json:"solana_signature"`
	EVMSignature    string `json:"evm_signature"`
}

// OwnershipMessage is the message both keys sign; the browser builds the
// same (frontend/lib/wallet/proof.ts).
func OwnershipMessage(userID, solanaAddress, evmAddress, issued string) string {
	return strings.Join([]string{"Algebra vault ownership", "Account: " + userID, "Solana: " + solanaAddress, "EVM: " + evmAddress, "Issued: " + issued}, "\n")
}

// ownershipFresh is how old a proof may be, and how far ahead of the server's clock.
const (
	ownershipFresh = 15 * time.Minute
	ownershipAhead = 2 * time.Minute
)

// Vault errors.
var (
	ErrVaultProof    = fmt.Errorf("%w: the vault's ownership proof doesn't check out", ErrInvalidRequest)
	ErrVaultNone     = fmt.Errorf("%w: this account has no vault yet", shared.ErrNotFound)
	ErrVaultChain    = fmt.Errorf("%w: that chain isn't one the vault uses here", ErrInvalidRequest)
	ErrVaultTaken    = fmt.Errorf("%w: that wallet is another account's vault", shared.ErrConflict)
	ErrVaultNotYours = fmt.Errorf("%w: that transaction isn't signed by this account's vault", ErrInvalidRequest)
)

// OwnerVaultService runs the vault.
type OwnerVaultService struct {
	store           OwnerVaultStore
	solana          map[string]VaultSolanaRPC
	evm             map[string]VaultEVMRPC
	mainnetDeposits bool
	limiter         RateLimiter
	now             func() time.Time

	mu    sync.Mutex
	cache map[string]cachedChainBalance
}

type cachedChainBalance struct {
	at time.Time
	b  ChainBalance
}

// NewOwnerVaultService reads chains through the given nodes, keyed by
// network. mainnetDeposits shows mainnet deposit addresses (off until review).
func NewOwnerVaultService(store OwnerVaultStore, sol map[string]VaultSolanaRPC, ev map[string]VaultEVMRPC, mainnetDeposits bool, limiter RateLimiter) *OwnerVaultService {
	if limiter == nil {
		limiter = newWindowCounter(time.Now)
	}
	return &OwnerVaultService{store: store, solana: sol, evm: ev, mainnetDeposits: mainnetDeposits, limiter: limiter, now: time.Now, cache: map[string]cachedChainBalance{}}
}

// Chains are the vault's chains as this server runs them.
func (s *OwnerVaultService) Chains() []VaultChain {
	out := make([]VaultChain, 0, len(vaultChains))
	for _, c := range vaultChains {
		c.USDC, _ = chain.AssetAddress(c.ID, "USDC")
		c.Available = s.solana[c.ID] != nil || s.evm[c.ID] != nil
		c.Deposits = c.Available && (c.Test || s.mainnetDeposits)
		out = append(out, c)
	}
	return out
}

func (s *OwnerVaultService) chain(id string) (VaultChain, error) {
	for _, c := range s.Chains() {
		if c.ID == id && c.Available {
			return c, nil
		}
	}
	return VaultChain{}, ErrVaultChain
}

// Get is the account's vault; ErrVaultNone if it has none.
func (s *OwnerVaultService) Get(ctx context.Context, userID string) (*OwnerVaultRecord, error) {
	r, err := s.store.OwnerVault(ctx, userID)
	if errors.Is(err, shared.ErrNotFound) {
		return nil, ErrVaultNone
	}
	return r, err
}

// Register records the account's vault after checking the proof: the message
// names this account and both addresses, it is fresh, the Solana key signed
// it (ed25519) and the EVM key signed it (EIP-191). A proof for new addresses
// replaces the old ones (the owner made or imported another wallet).
func (s *OwnerVaultService) Register(ctx context.Context, userID string, p OwnershipProof) (*OwnerVaultRecord, error) {
	solKey, err := solana.ParsePublicKey(p.SolanaAddress)
	if err != nil {
		return nil, fmt.Errorf("%w: the Solana address is invalid", ErrVaultProof)
	}
	evmAddr, err := evm.ParseAddress(p.EVMAddress)
	if err != nil {
		return nil, fmt.Errorf("%w: the EVM address is invalid", ErrVaultProof)
	}
	lines := strings.Split(p.Message, "\n")
	issuedAt, ok := strings.CutPrefix(lines[len(lines)-1], "Issued: ")
	if !ok || p.Message != OwnershipMessage(userID, p.SolanaAddress, p.EVMAddress, issuedAt) {
		return nil, fmt.Errorf("%w: the message isn't the one for this account", ErrVaultProof)
	}
	issued, err := time.Parse(time.RFC3339Nano, issuedAt)
	now := s.now()
	if err != nil || now.Sub(issued) > ownershipFresh || issued.Sub(now) > ownershipAhead {
		return nil, fmt.Errorf("%w: the proof is out of date; make it again", ErrVaultProof)
	}
	sig, err := solana.DecodeBase58(p.SolanaSignature)
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(solKey[:], []byte(p.Message), sig) {
		return nil, fmt.Errorf("%w: the Solana signature is wrong", ErrVaultProof)
	}
	signer, err := evm.PersonalSigner(p.Message, p.EVMSignature)
	if err != nil || signer != evmAddr {
		return nil, fmt.Errorf("%w: the EVM signature is wrong", ErrVaultProof)
	}
	rec := &OwnerVaultRecord{UserID: userID, SolanaAddress: solKey.String(), EVMAddress: evm.AddressHex(evmAddr), ProofMessage: p.Message, CreatedAt: now, UpdatedAt: now}
	if prev, err := s.store.OwnerVault(ctx, userID); err == nil {
		rec.CreatedAt = prev.CreatedAt
	}
	if err := s.store.SaveOwnerVault(ctx, rec); err != nil {
		if errors.Is(err, shared.ErrConflict) {
			return nil, ErrVaultTaken
		}
		return nil, err
	}
	return rec, nil
}

// ChainBalance is what the vault holds on one chain. Amounts are decimal
// strings of minor units (EVM balances don't fit an int64). OK is false when
// the node couldn't be read: then the amounts are unknown, not zero.
type ChainBalance struct {
	Chain        string `json:"chain"`
	Address      string `json:"address"`
	USDCMinor    string `json:"usdc_minor"`
	NativeMinor  string `json:"native_minor"`
	NativeSymbol string `json:"native_symbol"`
	OK           bool   `json:"ok"`
}

// Balances reads the vault on every available chain of one kind of network
// (test or real). force skips the few-second cache.
func (s *OwnerVaultService) Balances(ctx context.Context, userID string, test, force bool) ([]ChainBalance, error) {
	v, err := s.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []ChainBalance
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, c := range s.Chains() {
		if c.Test != test || !c.Available {
			continue
		}
		wg.Add(1)
		go func(c VaultChain) {
			defer wg.Done()
			b := s.balance(ctx, c, v, force)
			mu.Lock()
			out = append(out, b)
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	order := map[string]int{}
	for i, c := range vaultChains {
		order[c.ID] = i
	}
	slices.SortFunc(out, func(a, b ChainBalance) int { return order[a.Chain] - order[b.Chain] })
	return out, nil
}

func (s *OwnerVaultService) balance(ctx context.Context, c VaultChain, v *OwnerVaultRecord, force bool) ChainBalance {
	b := ChainBalance{Chain: c.ID, NativeSymbol: c.NativeSymbol, USDCMinor: "0", NativeMinor: "0"}
	if c.Kind == "solana" {
		b.Address = v.SolanaAddress
	} else {
		b.Address = v.EVMAddress
	}
	key := c.ID + "/" + b.Address
	s.mu.Lock()
	hit, ok := s.cache[key]
	s.mu.Unlock()
	if ok && !force && s.now().Sub(hit.at) < 8*time.Second {
		return hit.b
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if c.Kind == "solana" {
		owner, err := solana.ParsePublicKey(b.Address)
		mint, err2 := solana.ParsePublicKey(c.USDC)
		if err != nil || err2 != nil {
			return b
		}
		rpc := s.solana[c.ID]
		lamports, err := rpc.GetBalance(ctx, owner, solana.Confirmed)
		if err != nil {
			return b
		}
		b.NativeMinor = fmt.Sprint(lamports)
		ata, err := solana.AssociatedTokenAddress(owner, mint, solana.TokenProgram)
		if err != nil {
			return b
		}
		switch tb, err := rpc.GetTokenAccountBalance(ctx, ata, solana.Confirmed); {
		case errors.Is(err, solana.ErrAccountNotFound):
		case err != nil:
			return b
		default:
			b.USDCMinor = fmt.Sprint(tb.Amount)
		}
	} else {
		rpc := s.evm[c.ID]
		native, err := rpc.NativeBalance(ctx, b.Address)
		if err != nil {
			return b
		}
		usdc, err := rpc.TokenBalance(ctx, c.USDC, b.Address)
		if err != nil {
			return b
		}
		b.NativeMinor, b.USDCMinor = native.String(), usdc.String()
	}
	b.OK = true
	s.mu.Lock()
	s.cache[key] = cachedChainBalance{at: s.now(), b: b}
	s.mu.Unlock()
	return b
}

// WithdrawParams is what the browser needs to build a USDC withdrawal on one
// chain. Nothing in it says what the transaction does: the browser decides
// that, and signs only what it built.
type WithdrawParams struct {
	Chain string `json:"chain"`
	// Solana: a recent blockhash, and whether the recipient already has a USDC account.
	Blockhash                string `json:"blockhash,omitempty"`
	RecipientHasTokenAccount *bool  `json:"recipient_has_token_account,omitempty"`
	// EVM: the nonce, fees and gas for the transfer.
	EVM *evm.TxParams `json:"evm,omitempty"`
}

// PrepareWithdraw reads what a withdrawal of USDC to `to` needs on a chain.
func (s *OwnerVaultService) PrepareWithdraw(ctx context.Context, userID, chainID, to string, amountMinor *big.Int) (*WithdrawParams, error) {
	v, err := s.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	c, err := s.chain(chainID)
	if err != nil {
		return nil, err
	}
	if amountMinor == nil || amountMinor.Sign() <= 0 {
		return nil, fmt.Errorf("%w: the amount must be more than zero", ErrInvalidRequest)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := &WithdrawParams{Chain: c.ID}
	if c.Kind == "solana" {
		dest, err := solana.ParsePublicKey(to)
		if err != nil {
			return nil, fmt.Errorf("%w: that isn't a Solana address", ErrInvalidRequest)
		}
		mint, _ := solana.ParsePublicKey(c.USDC)
		rpc := s.solana[c.ID]
		bh, err := rpc.GetLatestBlockhash(ctx, solana.Confirmed)
		if err != nil {
			return nil, err
		}
		ata, err := solana.AssociatedTokenAddress(dest, mint, solana.TokenProgram)
		if err != nil {
			return nil, err
		}
		info, err := rpc.GetAccountInfo(ctx, ata, solana.Confirmed)
		if err != nil {
			return nil, err
		}
		has := info != nil
		out.Blockhash, out.RecipientHasTokenAccount = solana.PublicKey(bh.Blockhash).String(), &has
		return out, nil
	}
	dest, err := evm.ParseAddress(to)
	if err != nil {
		return nil, fmt.Errorf("%w: that isn't an EVM address", ErrInvalidRequest)
	}
	data := "0xa9059cbb" + strings.Repeat("0", 24) + hex.EncodeToString(dest[:]) + fmt.Sprintf("%064x", amountMinor)
	p, err := s.evm[c.ID].Params(ctx, v.EVMAddress, c.USDC, data)
	if err != nil {
		return nil, err
	}
	out.EVM = p
	return out, nil
}

// broadcastsPerHour bounds how many transactions one account can have relayed.
const broadcastsPerHour = 30

// Broadcast relays a transaction the owner's browser signed. Only one signed
// by this account's own vault, for this chain, is relayed.
func (s *OwnerVaultService) Broadcast(ctx context.Context, userID, chainID, raw string) (string, error) {
	v, err := s.Get(ctx, userID)
	if err != nil {
		return "", err
	}
	c, err := s.chain(chainID)
	if err != nil {
		return "", err
	}
	ok, _, err := s.limiter.Allow(ctx, "vault:broadcast:"+userID, broadcastsPerHour, time.Hour)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: too many transactions this hour; try again later", ErrInvalidRequest)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if c.Kind == "solana" {
		tx, err := solana.DecodeTransactionBase64(raw)
		if err != nil || len(tx.Signatures) != 1 || len(tx.Message.AccountKeys) == 0 || tx.Message.AccountKeys[0].String() != v.SolanaAddress {
			return "", ErrVaultNotYours
		}
		return s.solana[c.ID].SendTransaction(ctx, raw, solana.SendOptions{})
	}
	tx, err := evm.DecodeSignedEIP1559(raw)
	if err != nil || evm.AddressHex(tx.From) != v.EVMAddress || tx.ChainID.Int64() != c.ChainID {
		return "", ErrVaultNotYours
	}
	return s.evm[c.ID].SendRaw(ctx, raw)
}
