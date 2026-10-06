// Command x402-dryrun shows what Algebra WOULD pay for a real x402 resource,
// without paying. It asks the provider for its price, picks the option
// Algebra can pay, builds and signs the payment transaction exactly as the
// rail would, and has the Solana node simulate it. Nothing is sent: the signed
// payment is discarded.
//
//	go run ./cmd/x402-dryrun -url https://api.example.com/resource
//	go run ./cmd/x402-dryrun -url https://api.example.com/risk -method POST -body '{"mint":"So111..."}'
//
// Use it before pointing Algebra at a provider for the first time: it
// confirms the provider's terms are ones the rail accepts, that the wallet
// could pay them, and that the transaction is well-formed. With a funded
// wallet the simulation succeeds; with an unfunded one it fails at the
// transfer, which still proves everything before it.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/providers/solanax402"
	"github.com/project-algebra/algebra/providers/x402"
	"github.com/project-algebra/algebra/providers/x402client"
)

func main() {
	url := flag.String("url", "", "the x402 resource to price")
	method := flag.String("method", "", "HTTP method (default: GET with no body, POST with one)")
	body := flag.String("body", "", "request input as JSON")
	flag.Parse()
	if *url == "" {
		fmt.Fprintln(os.Stderr, "give -url")
		os.Exit(2)
	}
	if err := run(*url, *method, *body); err != nil {
		fmt.Fprintln(os.Stderr, "\nStopped:", err)
		os.Exit(1)
	}
}

func run(url, method, body string) error {
	sc, err := config.SolanaFromEnv()
	if err != nil {
		return err
	}
	if sc.Cluster == "" {
		return fmt.Errorf("SOLANA_CLUSTER is not set; see .env.example")
	}
	text := sc.Keypair
	if sc.KeypairFile != "" {
		b, err := os.ReadFile(sc.KeypairFile)
		if err != nil {
			return err
		}
		text = string(b)
	}
	kp, err := solana.ParseKeypair(text)
	if err != nil {
		return fmt.Errorf("the wallet couldn't be loaded: %w", err)
	}
	rpcURL := sc.RPCURL
	if rpcURL == "" {
		rpcURL = map[string]string{"devnet": "https://api.devnet.solana.com", "mainnet": "https://api.mainnet-beta.solana.com"}[sc.Cluster]
	}
	rpc := solana.NewRPC(rpcURL, nil)
	rail, err := solanax402.New(solanax402.Config{Cluster: sc.Cluster, RPC: rpc, Signer: kp, MaxPaymentMinor: sc.MaxPaymentMinor})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	fmt.Printf("Cluster %s, wallet %s\n", sc.Cluster, kp.PublicKey())
	st, err := rail.Status(ctx)
	if err != nil {
		return err
	}
	if st.USDCMinor == nil {
		fmt.Println("Wallet USDC: no USDC account yet (the simulation will fail at the transfer)")
	} else {
		fmt.Printf("Wallet USDC: %s\n", chain.FormatUnits(int64(*st.USDCMinor), chain.USDCDecimals))
	}

	// 1. Ask the provider for its price: an ordinary unpaid request.
	var input json.RawMessage
	if body != "" {
		input = json.RawMessage(body)
	}
	cand, err := routing.Candidate{
		Capability: "dryrun.probe", Provider: "dryrun", ExecutionType: routing.ExecX402, Endpoint: url, Method: method,
		Network: rail.Network(), Sources: []routing.DiscoverySource{routing.SourceWeb},
	}.Normalize()
	if err != nil {
		return err
	}
	runner := x402client.New(x402client.Config{
		HTTP: safehttp.New(safehttp.Options{}), Networks: map[string]string{rail.Network(): rail.Name()},
	})
	fmt.Printf("\nAsking %s for its price (an unpaid request)...\n", cand.Endpoint)
	q, err := runner.Quote(ctx, cand, input)
	if err != nil {
		return err
	}
	sel, err := x402.SelectRaw(q.Requirements, "exact", rail.Network(), networkCAIP(rail.Network()))
	if err != nil {
		return err
	}
	fmt.Println("\nWhat the provider asks:")
	fmt.Printf("  price:        %s USDC (%d micro-USDC)\n", chain.FormatUnits(q.Cost.ProviderMinor, chain.USDCDecimals), q.Cost.ProviderMinor)
	fmt.Printf("  network:      %s\n", q.Network)
	fmt.Printf("  pays to:      %s\n", q.PayTo)
	fmt.Printf("  asset:        %s (Circle's USDC: checked)\n", q.AssetAddress)
	fmt.Printf("  sponsor:      %s (pays the fees)\n", sel.Requirements.ExtraField("feePayer"))
	fmt.Printf("  x402 version: %d\n", sel.Version)
	if m := sel.Requirements.ExtraField("memo"); m != "" {
		fmt.Printf("  seller memo:  %q\n", m)
	}

	// 2. Build and sign the payment exactly as the rail would.
	fmt.Println("\nBuilding and signing the payment (not sending it)...")
	auth, problems, err := rail.DryRun(ctx, &econ.Reservation{ID: "dryrun", HoldMinor: q.Cost.ProviderMinor},
		app.PaymentRequest{Requirements: q.Requirements, Resource: url})
	if err != nil {
		return fmt.Errorf("Algebra would REFUSE to pay this: %w", err)
	}
	for _, problem := range problems {
		fmt.Println("  would be refused when real:", strings.TrimPrefix(problem, "solanax402: "))
	}
	p, err := x402.DecodePayment(auth.Value)
	if err != nil {
		return err
	}
	var inner struct {
		Transaction string `json:"transaction"`
	}
	if err := json.Unmarshal(p.Payload, &inner); err != nil {
		return err
	}
	tx, err := solana.DecodeTransactionBase64(inner.Transaction)
	if err != nil {
		return err
	}
	fmt.Printf("  header:       %s\n", auth.Header)
	fmt.Printf("  payment id:   %s (the payer's signature: how the payment is found on chain)\n", shorten(auth.Evidence.PaymentID))
	fmt.Printf("  expires:      block height %d\n", auth.Evidence.ValidUntilHeight)
	fmt.Printf("  instructions: %d (compute limit, compute price, TransferChecked, memo)\n", len(tx.Message.Instructions))

	// 3. Have the node run it, without sending it.
	fmt.Println("\nSimulating on the node (nothing is submitted)...")
	sim, err := rpc.SimulateTransaction(ctx, inner.Transaction, solana.Confirmed)
	if err != nil {
		return err
	}
	if sim.Err == nil {
		fmt.Printf("  OK: the transaction would run (%d compute units).\n", sim.UnitsConsumed)
	} else {
		e, _ := json.Marshal(sim.Err)
		fmt.Printf("  the node reports: %s\n", e)
		if st.USDCMinor == nil || *st.USDCMinor < uint64(q.Cost.ProviderMinor) {
			fmt.Println("  (expected: the wallet doesn't hold the USDC yet. Everything before the transfer ran.)")
		}
	}
	for _, l := range sim.Logs {
		if strings.Contains(l, "failed") || strings.Contains(l, "Error") {
			fmt.Println("  log:", l)
		}
	}
	fmt.Println("\nNOTHING WAS SENT. The signed payment was discarded.")
	return nil
}

func networkCAIP(n string) string {
	if n == chain.SolanaDevnet {
		return "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1"
	}
	return "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"
}

func shorten(s string) string {
	if len(s) > 20 {
		return s[:10] + "…" + s[len(s)-6:]
	}
	return s
}
