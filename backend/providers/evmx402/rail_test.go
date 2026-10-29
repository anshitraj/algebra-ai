package evmx402

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/platform/evm"
	"github.com/project-algebra/algebra/providers/x402"
)

const sepoliaUSDC = "0x036cbd53842c5426634e7929541ec2318f3dcf7e"

// node is a fake Base Sepolia JSON-RPC node: balances, used authorizations,
// block times and AuthorizationUsed logs.
type node struct {
	mu        sync.Mutex
	chainID   int64
	balance   *big.Int
	used      map[string]bool // nonce hex -> used at latest
	finalUsed map[string]bool // nonce hex -> used at finalized
	latest    uint64
	finalTime int64
	txFor     map[string]string // nonce hex -> tx hash
}

func (n *node) handler(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(body, &req)
	reply := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": v}) }
	switch req.Method {
	case "eth_chainId":
		reply(fmt.Sprintf("0x%x", n.chainID))
	case "eth_getBalance":
		reply("0x0")
	case "eth_getBlockByNumber":
		var tag string
		_ = json.Unmarshal(req.Params[0], &tag)
		ts := time.Now().Unix()
		if tag == "finalized" {
			ts = n.finalTime
		}
		reply(map[string]string{"number": fmt.Sprintf("0x%x", n.latest), "timestamp": fmt.Sprintf("0x%x", ts)})
	case "eth_call":
		var msg struct {
			Data string `json:"data"`
		}
		var tag string
		_ = json.Unmarshal(req.Params[0], &msg)
		_ = json.Unmarshal(req.Params[1], &tag)
		data := strings.TrimPrefix(msg.Data, "0x")
		switch data[:8] {
		case "70a08231": // balanceOf
			reply("0x" + hex.EncodeToString(pad(n.balance)))
		case "e94a0102": // authorizationState
			nonce := "0x" + data[8+64:]
			used := n.used[nonce]
			if tag == "finalized" {
				used = n.finalUsed[nonce]
			}
			if used {
				reply("0x" + strings.Repeat("0", 63) + "1")
			} else {
				reply("0x" + strings.Repeat("0", 64))
			}
		default:
			reply("0x")
		}
	case "eth_getLogs":
		var f struct {
			Topics []string `json:"topics"`
		}
		_ = json.Unmarshal(req.Params[0], &f)
		if tx, ok := n.txFor[f.Topics[2]]; ok {
			reply([]map[string]any{{"address": sepoliaUSDC, "topics": f.Topics, "data": "0x", "transactionHash": tx, "blockNumber": "0x10", "removed": false}})
			return
		}
		reply([]any{})
	default:
		reply(nil)
	}
}

func pad(v *big.Int) []byte { b := make([]byte, 32); v.FillBytes(b); return b }

func rig(t *testing.T) (*Rail, *node, *evm.Key) {
	t.Helper()
	n := &node{chainID: 84532, balance: big.NewInt(5_000_000), used: map[string]bool{}, finalUsed: map[string]bool{}, latest: 1000, txFor: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(n.handler))
	t.Cleanup(srv.Close)
	b, _ := hex.DecodeString("4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318")
	k, _ := evm.KeyFromBytes(b)
	r, err := New(Config{Network: "base-sepolia", RPC: evm.NewRPC(srv.URL), Key: k})
	if err != nil {
		t.Fatal(err)
	}
	return r, n, k
}

const seller = "0x8cbd7d9d0cf6ddb2b3b6e0e5c4b1a2f0e9d8c7b6"

func challenge(amount, asset, payTo string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"x402Version":2,"accepts":[{"scheme":"exact","network":"eip155:84532","amount":%q,"payTo":%q,"asset":%q,"maxTimeoutSeconds":120,"extra":{"name":"USDC","version":"2"}}]}`, amount, payTo, asset))
}

// The payment is a transferWithAuthorization that the payer alone signed,
// for exactly the price, to the seller, under USDC's real domain.
func TestAuthorize_SignsATransferAuthorizationForExactlyThePrice(t *testing.T) {
	r, _, k := rig(t)
	auth, err := r.Authorize(context.Background(), &econ.Reservation{HoldMinor: 3_000}, app.PaymentRequest{Requirements: challenge("1000", sepoliaUSDC, seller)})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Header != x402.HeaderPaymentV2 || auth.AmountMinor != 1_000 {
		t.Fatalf("authority: %+v", auth)
	}
	pp, err := x402.DecodePayment(auth.Value)
	if err != nil {
		t.Fatal(err)
	}
	var p authorizationPayload
	if err := json.Unmarshal(pp.Payload, &p); err != nil {
		t.Fatal(err)
	}
	a := p.Authorization
	if a.From != k.Hex() || a.To != seller || a.Value != "1000" || len(pp.Accepted) == 0 {
		t.Fatalf("authorization: %+v", a)
	}
	after, _ := new(big.Int).SetString(a.ValidAfter, 10)
	before, _ := new(big.Int).SetString(a.ValidBefore, 10)
	if before.Int64()-time.Now().Unix() > 121 || before.Int64() <= time.Now().Unix() || after.Int64() >= time.Now().Unix() {
		t.Errorf("valid from a minute ago until the seller's two minutes: %s..%s", a.ValidAfter, a.ValidBefore)
	}
	nonce, _ := evm.Hex32(a.Nonce)
	to, _ := evm.ParseAddress(seller)
	structHash, _ := evm.TransferAuthorization{From: k.Address(), To: to, Value: big.NewInt(1000), ValidAfter: after, ValidBefore: before, Nonce: nonce}.Hash()
	sig, _ := hex.DecodeString(strings.TrimPrefix(p.Signature, "0x"))
	if who, err := evm.Recover(evm.TypedDigest(r.Domain(), structHash), sig); err != nil || who != k.Address() {
		t.Fatalf("the signature is the payer's over USDC's domain: %x %v", who, err)
	}
	ev := auth.Evidence
	if ev.PaymentID != a.Nonce || ev.Payer != k.Hex() || ev.PayTo != seller || ev.ValidBefore != before.Int64() || ev.FromBlock != 1000 || !ev.Test || ev.Rail != "x402-base-sepolia" {
		t.Errorf("evidence: %+v", ev)
	}
}

func TestAuthorize_RefusesWhatItWontPay(t *testing.T) {
	r, n, k := rig(t)
	ctx := context.Background()
	cases := map[string]json.RawMessage{
		"another token":         challenge("1000", "0x1111111111111111111111111111111111111111", seller),
		"more than the hold":    challenge("5000", sepoliaUSDC, seller),
		"more than the ceiling": challenge("2000000", sepoliaUSDC, seller),
		"pay itself":            challenge("1000", sepoliaUSDC, k.Hex()),
		"the zero address":      challenge("1000", sepoliaUSDC, "0x0000000000000000000000000000000000000000"),
		"another chain":         json.RawMessage(`{"x402Version":2,"accepts":[{"scheme":"exact","network":"eip155:8453","amount":"1000","payTo":"` + seller + `","asset":"0x833589fcd6edb6e08f4c7c32d4f71b54bda02913"}]}`),
	}
	for name, req := range cases {
		hold := int64(3_000)
		if name == "more than the ceiling" {
			hold = 5_000_000
		}
		if _, err := r.Authorize(ctx, &econ.Reservation{HoldMinor: hold}, app.PaymentRequest{Requirements: req}); err == nil {
			t.Errorf("%s: signed", name)
		}
	}
	n.balance = big.NewInt(500)
	if _, err := r.Authorize(ctx, &econ.Reservation{HoldMinor: 3_000}, app.PaymentRequest{Requirements: challenge("1000", sepoliaUSDC, seller)}); err == nil {
		t.Error("an empty wallet doesn't sign")
	}
	if _, err := r.Authorize(ctx, &econ.Reservation{HoldMinor: 3_000}, app.PaymentRequest{Requirements: challenge("1000", sepoliaUSDC, seller), FundedByPass: true}); err != nil {
		t.Errorf("a payment the vault funds first is signed: %v", err)
	}
}

// Settlement: used is paid; unused is pending until the deadline has passed
// at a finalized block, then it can never be paid.
func TestSettlement_FromAuthorizationState(t *testing.T) {
	r, n, k := rig(t)
	ctx := context.Background()
	auth, err := r.Authorize(ctx, &econ.Reservation{HoldMinor: 3_000}, app.PaymentRequest{Requirements: challenge("1000", sepoliaUSDC, seller)})
	if err != nil {
		t.Fatal(err)
	}
	ev := auth.Evidence
	n.finalTime = time.Now().Unix()
	if st, _ := r.Settlement(ctx, ev); st.Status != app.SettlementPending {
		t.Fatalf("unused and still valid: %+v", st)
	}
	n.used[ev.PaymentID], n.txFor[ev.PaymentID] = true, "0xabc"
	if st, _ := r.Settlement(ctx, ev); st.Status != app.SettlementSettled || st.Transaction != "0xabc" || st.AmountMinor != 1000 {
		t.Fatalf("used: %+v", st)
	}
	delete(n.used, ev.PaymentID)
	n.finalTime = ev.ValidBefore + 1
	if st, _ := r.Settlement(ctx, ev); st.Status != app.SettlementNotSettled {
		t.Fatalf("expired at a finalized block and never used: %+v", st)
	}
	foreign := ev
	foreign.Payer = "0x1111111111111111111111111111111111111111"
	if st, _ := r.Settlement(ctx, foreign); st.Status != app.SettlementUnknown {
		t.Errorf("another wallet's payment says nothing: %+v", st)
	}
	_ = k
}

// The node must serve the chain the rail is for.
func TestStatus_RefusesAnotherChain(t *testing.T) {
	r, n, _ := rig(t)
	if st, err := r.Status(context.Background()); err != nil || st.USDCMinor != 5_000_000 {
		t.Fatalf("status: %+v %v", st, err)
	}
	n.chainID = 8453
	if _, err := r.Status(context.Background()); err == nil {
		t.Error("a mainnet node for a testnet rail is refused")
	}
}

// Live (EVMX402_LIVE=1): the real Base Sepolia USDC contract checks our
// signature. A read-only call of transferWithAuthorization from an unfunded
// payer fails on the balance, which USDC checks only after the signature: so
// the signature is valid. Nothing is sent.
func TestLive_BaseSepoliaUSDCAcceptsTheSignature(t *testing.T) {
	liveCheck(t, "base-sepolia", "https://sepolia.base.org", sepoliaUSDC, challenge("1000", sepoliaUSDC, seller))
}

// The same on Arc testnet, where USDC is the native token behind an ERC-20.
func TestLive_ArcTestnetUSDCAcceptsTheSignature(t *testing.T) {
	arc := "0x3600000000000000000000000000000000000000"
	req := json.RawMessage(`{"x402Version":2,"accepts":[{"scheme":"exact","network":"eip155:5042002","amount":"1000","payTo":"` + seller + `","asset":"` + arc + `","maxTimeoutSeconds":120}]}`)
	liveCheck(t, "arc-testnet", "https://rpc.testnet.arc.network", arc, req)
}

func liveCheck(t *testing.T, network, url, token string, req json.RawMessage) {
	if os.Getenv("EVMX402_LIVE") != "1" {
		t.Skip("set EVMX402_LIVE=1 to ask the real node")
	}
	sepoliaUSDC := token
	k, _ := evm.GenerateKey()
	rpc := evm.NewRPC(url)
	r, _ := New(Config{Network: network, RPC: rpc, Key: k})
	auth, _, err := r.DryRun(context.Background(), &econ.Reservation{HoldMinor: 3_000}, app.PaymentRequest{Requirements: req})
	if err != nil {
		t.Fatal(err)
	}
	pp, _ := x402.DecodePayment(auth.Value)
	var p authorizationPayload
	_ = json.Unmarshal(pp.Payload, &p)
	after, _ := new(big.Int).SetString(p.Authorization.ValidAfter, 10)
	before, _ := new(big.Int).SetString(p.Authorization.ValidBefore, 10)
	nonce, _ := evm.Hex32(p.Authorization.Nonce)
	to, _ := evm.ParseAddress(seller)
	sig, _ := hex.DecodeString(strings.TrimPrefix(p.Signature, "0x"))
	data, err := evm.TransferWithAuthorizationCalldata(evm.TransferAuthorization{From: k.Address(), To: to, Value: big.NewInt(1000), ValidAfter: after, ValidBefore: before, Nonce: nonce}, sig)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rpc.Call(context.Background(), seller, sepoliaUSDC, data, "latest")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "balance") || strings.Contains(strings.ToLower(err.Error()), "signature") {
		t.Fatalf("want a balance revert (the signature passed), got %v", err)
	}
	// A signature over anything else is refused as invalid, before the balance.
	sig[10] ^= 0xff
	bad, _ := evm.TransferWithAuthorizationCalldata(evm.TransferAuthorization{From: k.Address(), To: to, Value: big.NewInt(1000), ValidAfter: after, ValidBefore: before, Nonce: nonce}, sig)
	if _, err := rpc.Call(context.Background(), seller, sepoliaUSDC, bad, "latest"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "signature") {
		t.Fatalf("a tampered signature is refused as a signature: %v", err)
	}
}

// Live (EVMX402_LIVE=1): the real USDC contracts accept a permit signed as
// the vault's browser signs it. A read-only call of permit() succeeds only if
// the signature is the owner's over USDC's own domain; nothing is sent.
func TestLive_USDCAcceptsTheVaultsPermit(t *testing.T) {
	if os.Getenv("EVMX402_LIVE") != "1" {
		t.Skip("set EVMX402_LIVE=1 to ask the real nodes")
	}
	for network, url := range map[string]string{"base-sepolia": "https://sepolia.base.org", "arc-testnet": "https://rpc.testnet.arc.network"} {
		owner, _ := evm.GenerateKey()
		payer, _ := evm.GenerateKey()
		rpc := evm.NewRPC(url)
		r, _ := New(Config{Network: network, RPC: rpc, Key: payer})
		p := evm.Permit{Owner: owner.Address(), Spender: payer.Address(), Value: big.NewInt(5_000_000), Nonce: big.NewInt(0), Deadline: big.NewInt(time.Now().Add(24 * time.Hour).Unix())}
		h, _ := p.Hash()
		sig, _ := owner.Sign(evm.TypedDigest(r.Domain(), h))
		data, _ := evm.PermitCalldata(p, sig)
		if _, err := rpc.Call(context.Background(), payer.Hex(), r.USDC(), data, "latest"); err != nil {
			t.Fatalf("%s: the contract refused a valid permit: %v", network, err)
		}
		sig[5] ^= 0xff
		bad, _ := evm.PermitCalldata(p, sig)
		if _, err := rpc.Call(context.Background(), payer.Hex(), r.USDC(), bad, "latest"); err == nil {
			t.Fatalf("%s: a tampered permit was accepted", network)
		}
	}
}
