// Command spend-pass drives Algebra's Spend Pass program on Solana from
// keypair files: make a pass, fund it, freeze it, pull from it, and read it.
// It is for operators and demos; people use their own wallet in the console.
//
//	go run ./cmd/spend-pass -cluster devnet show    -owner OWNER.json -id 1
//	go run ./cmd/spend-pass -cluster devnet create  -owner OWNER.json -agent-address PAYER -id 1 -per-call 0.01 -budget 1 -days 30 [-window-secs 3600 -window-cap 0.2]
//	go run ./cmd/spend-pass -cluster devnet deposit -owner OWNER.json -id 1 -amount 1
//	go run ./cmd/spend-pass -cluster devnet freeze|unfreeze|revoke|close -owner OWNER.json -id 1
//	go run ./cmd/spend-pass -cluster devnet withdraw -owner OWNER.json -id 1 -amount 0.5
//	go run ./cmd/spend-pass -cluster devnet pull    -agent AGENT.json -pass PASS -amount 0.002
//	go run ./cmd/spend-pass -cluster devnet refund  -agent AGENT.json -pass PASS -amount 0.002
//	go run ./cmd/spend-pass -cluster devnet sign-send -owner OWNER.json -tx BASE64
//
// sign-send stands in for a person's wallet: it signs a transaction the API
// prepared (POST /me/passes/{id}/onchain/prepare or /tx) and sends it.
//
// The destination of every pull is the agent's own USDC account, which is
// how Algebra uses a pass: the agent is the payer.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/spendpass"
)

var rpcURLs = map[string]string{
	"devnet":  "https://api.devnet.solana.com",
	"mainnet": "https://api.mainnet-beta.solana.com",
}

func main() {
	fs := flag.NewFlagSet("spend-pass", flag.ExitOnError)
	cluster := fs.String("cluster", "devnet", "devnet or mainnet")
	rpcURL := fs.String("rpc", "", "RPC URL (default: the cluster's public endpoint)")
	programID := fs.String("program", "", "program id (default: "+spendpass.DefaultProgramID+")")
	ownerFile := fs.String("owner", "", "owner keypair file")
	agentFile := fs.String("agent", "", "agent keypair file (pull, refund)")
	agentAddr := fs.String("agent-address", "", "create: the agent's address (Algebra's payer)")
	passAddr := fs.String("pass", "", "pass address (pull, refund, show)")
	id := fs.Uint64("id", 1, "the owner's number for the pass")
	amount := fs.String("amount", "", "USDC amount, e.g. 0.25")
	perCall := fs.String("per-call", "0.01", "create: most one pull may take, USDC")
	budget := fs.String("budget", "1", "create: most all pulls may take, USDC")
	days := fs.Int("days", 30, "create: days until the pass expires")
	windowSecs := fs.Int64("window-secs", 0, "create: spending window length in seconds (0: none)")
	windowCap := fs.String("window-cap", "0", "create: most a window may take, USDC")
	price := fs.Uint64("priority", 10_000, "priority fee, micro-lamports per compute unit")
	txB64 := fs.String("tx", "", "sign-send: the unsigned transaction, base64")
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: spend-pass [flags] create|deposit|show|pull|refund|freeze|unfreeze|withdraw|revoke|close")
		os.Exit(2)
	}
	// Flags may come before or after the command.
	args := os.Args[1:]
	cmd := ""
	for i, a := range args {
		if !strings.HasPrefix(a, "-") && (i == 0 || !needsValue(args[i-1])) {
			cmd = a
			args = append(append([]string{}, args[:i]...), args[i+1:]...)
			break
		}
	}
	_ = fs.Parse(args)

	network := map[string]string{"devnet": chain.SolanaDevnet, "mainnet": chain.Solana}[*cluster]
	if network == "" {
		fail(fmt.Errorf("-cluster must be devnet or mainnet"))
	}
	if *rpcURL == "" {
		*rpcURL = rpcURLs[*cluster]
	}
	mintStr, _ := chain.AssetAddress(network, "USDC")
	mint := solana.MustPublicKey(mintStr)
	prog, err := spendpass.New(*programID)
	fail(err)
	rpc := solana.NewRPC(*rpcURL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	genesis, err := rpc.GetGenesisHash(ctx)
	fail(err)
	if want := map[string]string{"devnet": solana.DevnetGenesisHash, "mainnet": solana.MainnetGenesisHash}[*cluster]; genesis != want {
		fail(fmt.Errorf("the RPC node serves another cluster (%s)", genesis))
	}
	usdc := func(s string) uint64 {
		v, err := chain.ParseUnits(s, chain.USDCDecimals)
		fail(err)
		if v < 0 {
			fail(errors.New("amounts can't be negative"))
		}
		return uint64(v)
	}
	owner := func() *solana.Keypair { return keypair(*ownerFile, "-owner") }
	pass := func() solana.PublicKey {
		if *passAddr != "" {
			return solana.MustPublicKey(*passAddr)
		}
		pk, err := prog.PassAddress(owner().PublicKey(), *id)
		fail(err)
		return pk
	}
	send := func(payer *solana.Keypair, ixs ...solana.Instruction) {
		s, err := spendpass.Sign(ctx, rpc, payer, nil, *price, ixs...)
		fail(err)
		fail(spendpass.SendWithin(ctx, rpc, s, solana.Confirmed, 90*time.Second))
		fmt.Println("signature:", s.Signature)
		fmt.Println("explorer: ", explorer(*cluster, "tx/"+s.Signature))
	}
	intent := func(label string) [32]byte {
		return sha256.Sum256([]byte("spend-pass-cli:" + label + ":" + time.Now().UTC().Format(time.RFC3339Nano)))
	}

	switch cmd {
	case "create":
		o := owner()
		agent := solana.MustPublicKey(*agentAddr)
		dest, err := solana.AssociatedTokenAddress(agent, mint, solana.TokenProgram)
		fail(err)
		t := spendpass.Terms{PerCallCap: usdc(*perCall), TotalBudget: usdc(*budget), WindowSecs: *windowSecs, WindowCap: usdc(*windowCap),
			ExpiresAt: time.Now().Add(time.Duration(*days) * 24 * time.Hour).Unix()}
		fail(t.Validate(time.Now().Unix()))
		ata, err := spendpass.CreateATAIdempotent(o.PublicKey(), agent, mint)
		fail(err)
		ix, addr, err := prog.CreatePass(o.PublicKey(), agent, mint, dest, *id, t)
		fail(err)
		send(o, ata, ix)
		fmt.Println("pass:     ", addr)
	case "deposit", "withdraw", "close":
		o := owner()
		ownerToken, err := solana.AssociatedTokenAddress(o.PublicKey(), mint, solana.TokenProgram)
		fail(err)
		var ix solana.Instruction
		switch cmd {
		case "deposit":
			ix, err = prog.Deposit(o.PublicKey(), pass(), mint, ownerToken, usdc(*amount))
		case "withdraw":
			ix, err = prog.Withdraw(o.PublicKey(), pass(), mint, ownerToken, usdc(*amount))
		default:
			ix, err = prog.ClosePass(o.PublicKey(), pass(), mint, ownerToken)
		}
		fail(err)
		send(o, ix)
	case "freeze", "unfreeze":
		o := owner()
		send(o, prog.SetFrozen(o.PublicKey(), pass(), cmd == "freeze"))
	case "revoke":
		o := owner()
		send(o, prog.Revoke(o.PublicKey(), pass()))
	case "pull", "refund":
		a := keypair(*agentFile, "-agent")
		dest, err := solana.AssociatedTokenAddress(a.PublicKey(), mint, solana.TokenProgram)
		fail(err)
		var ix solana.Instruction
		if cmd == "pull" {
			ix, err = prog.Pull(a.PublicKey(), pass(), mint, dest, usdc(*amount), intent("pull"))
		} else {
			ix, err = prog.Refund(a.PublicKey(), pass(), mint, dest, usdc(*amount), intent("refund"))
		}
		fail(err)
		send(a, ix)
	case "sign-send":
		o := owner()
		tx, err := solana.DecodeTransactionBase64(*txB64)
		fail(err)
		if len(tx.Message.AccountKeys) == 0 || tx.Message.AccountKeys[0] != o.PublicKey() {
			fail(errors.New("that transaction isn't paid by this owner"))
		}
		fail(tx.PartialSign(o))
		sig, _ := tx.SignerSignature(o.PublicKey())
		signed := &spendpass.Signed{Base64: tx.Base64(), Signature: solana.EncodeBase58(sig[:])}
		fail(spendpass.SendWithin(ctx, rpc, signed, solana.Confirmed, 90*time.Second))
		fmt.Println("signature:", signed.Signature)
		fmt.Println("explorer: ", explorer(*cluster, "tx/"+signed.Signature))
		if *passAddr == "" {
			return
		}
	case "show":
	default:
		fail(fmt.Errorf("unknown command %q", cmd))
	}
	show(ctx, rpc, *cluster, pass(), mint)
}

func show(ctx context.Context, rpc *solana.RPC, cluster string, pass, mint solana.PublicKey) {
	acct, err := rpc.GetAccountInfo(ctx, pass, solana.Confirmed)
	fail(err)
	if acct == nil {
		fmt.Println("pass", pass, "doesn't exist (never made, or closed)")
		return
	}
	st, err := spendpass.Decode(acct.Data)
	fail(err)
	vault, _ := spendpass.Vault(pass, mint)
	bal, err := rpc.GetTokenAccountBalance(ctx, vault, solana.Confirmed)
	fail(err)
	out := map[string]any{
		"pass": pass.String(), "owner": st.Owner.String(), "agent": st.Agent.String(), "destination": st.Destination.String(),
		"vault": vault.String(), "vault_usdc": chain.FormatUnits(int64(bal.Amount), chain.USDCDecimals),
		"per_call_cap_usdc": chain.FormatUnits(int64(st.PerCallCap), chain.USDCDecimals),
		"budget_usdc":       chain.FormatUnits(int64(st.TotalBudget), chain.USDCDecimals),
		"spent_usdc":        chain.FormatUnits(int64(st.Spent), chain.USDCDecimals),
		"pulls":             st.Pulls, "frozen": st.Frozen, "revoked": st.Revoked,
		"expires_at": time.Unix(st.ExpiresAt, 0).UTC().Format(time.RFC3339),
		"explorer":   explorer(cluster, "address/"+pass.String()),
	}
	if st.WindowSecs > 0 {
		out["window_secs"], out["window_cap_usdc"] = st.WindowSecs, chain.FormatUnits(int64(st.WindowCap), chain.USDCDecimals)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}

func explorer(cluster, path string) string {
	u := "https://explorer.solana.com/" + path
	if cluster == "devnet" {
		u += "?cluster=devnet"
	}
	return u
}

func needsValue(flagArg string) bool {
	if strings.Contains(flagArg, "=") || !strings.HasPrefix(flagArg, "-") {
		return false
	}
	return true
}

func keypair(path, flagName string) *solana.Keypair {
	if path == "" {
		fail(fmt.Errorf("give %s a keypair file", flagName))
	}
	b, err := os.ReadFile(path)
	fail(err)
	k, err := solana.ParseKeypair(string(b))
	fail(err)
	return k
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "spend-pass:", err)
		os.Exit(1)
	}
}
