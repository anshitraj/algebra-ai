package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/evm"
	"github.com/project-algebra/algebra/providers/x402"
)

// Demo sellers on Base Sepolia and Arc testnet. A payment is an EIP-3009
// transferWithAuthorization the buyer signed; the seller checks it and submits
// it itself, paying the gas (Base Sepolia ETH; on Arc, USDC is the gas), and
// answers only once the transaction is mined and succeeded. Until the seller's
// wallet holds gas, every payment is refused and nothing moves.

// evmChains are the test chains the demo sells on.
var evmChains = map[string]struct {
	caip, rpc string
	chainID   int64
}{
	chain.BaseSepolia: {caip: "eip155:84532", rpc: "https://sepolia.base.org", chainID: 84532},
	chain.ArcTestnet:  {caip: "eip155:5042002", rpc: "https://rpc.testnet.arc.network", chainID: 5042002},
}

// evmPersonas sell on Base Sepolia and Arc: a cheap and a dear token price on
// each, so the router compares them with the Solana ones.
var evmPersonas = []persona{
	{Name: "basebeam", Title: "BaseBeam Prices (demo, Base Sepolia)", Class: "token.price", ListedMinor: 900, AskMinor: 900, Scheme: "exact", Method: "GET", Network: chain.BaseSepolia, Delay: 200 * time.Millisecond},
	{Name: "arcade", Title: "Arcade Prices (demo, Arc testnet)", Class: "token.price", ListedMinor: 1_100, AskMinor: 1_100, Scheme: "exact", Method: "GET", Network: chain.ArcTestnet, Delay: 100 * time.Millisecond},
	{Name: "basecast", Title: "BaseCast Weather (demo, Base Sepolia)", Class: "weather.forecast", ListedMinor: 1_100, AskMinor: 1_100, Scheme: "exact", Method: "POST", Network: chain.BaseSepolia},
}

// evmSeller is the demo's EVM wallet and its nodes.
type evmSeller struct {
	key  *evm.Key
	rpcs map[string]*evm.RPC
}

// loadEVMSeller reads the EVM seller's key from DEMO_PROVIDER_EVM_KEY, or
// from demo-provider-evm.key in dir (next to the Solana wallet), making one
// the first time.
func loadEVMSeller(dir string) (*evmSeller, error) {
	var b []byte
	if v := strings.TrimSpace(os.Getenv("DEMO_PROVIDER_EVM_KEY")); v != "" {
		var err error
		if b, err = hex.DecodeString(strings.TrimPrefix(v, "0x")); err != nil {
			return nil, errors.New("DEMO_PROVIDER_EVM_KEY isn't hex")
		}
	} else {
		path := filepath.Join(dir, "demo-provider-evm.key")
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			if b, err = hex.DecodeString(strings.TrimSpace(string(raw))); err != nil {
				return nil, fmt.Errorf("%s isn't a hex key", path)
			}
		case os.IsNotExist(err):
			k, err := evm.GenerateKey()
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, []byte(hex.EncodeToString(k.Bytes())), 0o600); err != nil {
				return nil, err
			}
			log.Printf("created a new EVM seller wallet at %s", path)
			b = k.Bytes()
		default:
			return nil, err
		}
	}
	k, err := evm.KeyFromBytes(b)
	if err != nil {
		return nil, err
	}
	s := &evmSeller{key: k, rpcs: map[string]*evm.RPC{}}
	for n, c := range evmChains {
		url := c.rpc
		if v := strings.TrimSpace(os.Getenv("DEMO_PROVIDER_" + strings.ToUpper(strings.ReplaceAll(n, "-", "_")) + "_RPC")); v != "" {
			url = v
		}
		s.rpcs[n] = evm.NewRPC(url)
	}
	return s, nil
}

func usdcOn(network string) [20]byte {
	addr, _ := chain.AssetAddress(network, "USDC")
	a, _ := evm.ParseAddress(strings.ToLower(addr))
	return a
}

func domainOn(network string) evm.Domain {
	return evm.Domain{Name: "USDC", Version: "2", ChainID: big.NewInt(evmChains[network].chainID), VerifyingContract: usdcOn(network)}
}

// evmRequirements is an EVM persona's payment option.
func (s *server) evmRequirements(p persona) map[string]any {
	return map[string]any{
		"scheme": "exact", "network": evmChains[p.Network].caip, "amount": strconv.FormatInt(p.AskMinor, 10),
		"asset": evm.AddressHex(usdcOn(p.Network)), "payTo": s.evm.key.Hex(), "maxTimeoutSeconds": 120,
		"extra": map[string]any{"name": "USDC", "version": "2"},
	}
}

// payExactEVM checks a transferWithAuthorization for exactly the price to
// this seller, signed by its from, still valid; then submits it.
func (s *server) payExactEVM(ctx context.Context, p persona, header string) (tx, payer string, err error) {
	pay, err := x402.DecodePayment(header)
	if err != nil {
		return "", "", err
	}
	var inner struct {
		Signature     string `json:"signature"`
		Authorization struct {
			From, To, Value, ValidAfter, ValidBefore, Nonce string
		} `json:"authorization"`
	}
	if err := json.Unmarshal(pay.Payload, &inner); err != nil || inner.Signature == "" {
		return "", "", errors.New("the payload carries no authorization")
	}
	a := inner.Authorization
	from, err1 := evm.ParseAddress(strings.ToLower(a.From))
	to, err2 := evm.ParseAddress(strings.ToLower(a.To))
	nonce, err3 := evm.Hex32(a.Nonce)
	value, ok1 := new(big.Int).SetString(a.Value, 10)
	after, ok2 := new(big.Int).SetString(a.ValidAfter, 10)
	before, ok3 := new(big.Int).SetString(a.ValidBefore, 10)
	sig, err4 := hex.DecodeString(strings.TrimPrefix(inner.Signature, "0x"))
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || !ok1 || !ok2 || !ok3 || len(sig) != 65 {
		return "", "", errors.New("the authorization is malformed")
	}
	now := time.Now().Unix()
	switch {
	case to != s.evm.key.Address():
		return "", "", errors.New("the authorization pays someone else")
	case value.Cmp(big.NewInt(p.AskMinor)) != 0:
		return "", "", fmt.Errorf("the authorization is for %s, the price is %d", value, p.AskMinor)
	case after.Int64() > now || before.Int64() < now+10:
		return "", "", errors.New("the authorization isn't valid now")
	}
	auth := evm.TransferAuthorization{From: from, To: to, Value: value, ValidAfter: after, ValidBefore: before, Nonce: nonce}
	h, err := auth.Hash()
	if err != nil {
		return "", "", err
	}
	if who, err := evm.Recover(evm.TypedDigest(domainOn(p.Network), h), sig); err != nil || who != from {
		return "", "", errors.New("the authorization isn't signed by its from")
	}
	if !s.claim(a.Nonce) {
		return "", "", errors.New("this payment is already being settled")
	}
	data, err := evm.TransferWithAuthorizationCalldata(auth, sig)
	if err != nil {
		return "", "", err
	}
	rpc := s.evm.rpcs[p.Network]
	token := evm.AddressHex(usdcOn(p.Network))
	params, err := rpc.Params(ctx, s.evm.key.Hex(), token, "0x"+hex.EncodeToString(data))
	if err != nil {
		return "", "", fmt.Errorf("the seller can't submit the payment (its wallet %s may need gas on %s): %w", s.evm.key.Hex(), p.Network, err)
	}
	raw, hash, err := s.evm.key.SignTx(evm.Tx1559{ChainID: params.ChainID, Nonce: params.Nonce, Tip: params.MaxPriorityFeePerGas, MaxFee: params.MaxFeePerGas, Gas: params.GasLimit, To: usdcOn(p.Network), Data: data})
	if err != nil {
		return "", "", err
	}
	if _, err := rpc.SendRaw(ctx, raw); err != nil {
		return "", "", fmt.Errorf("submitting the payment: %w", err)
	}
	for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		rc, err := rpc.TransactionReceipt(ctx, hash)
		if err == nil && rc != nil {
			if rc.Status != "0x1" {
				return "", "", fmt.Errorf("the payment %s was mined and failed", hash)
			}
			return hash, evm.AddressHex(from), nil
		}
	}
	return "", "", fmt.Errorf("the payment %s wasn't mined in time", hash)
}
