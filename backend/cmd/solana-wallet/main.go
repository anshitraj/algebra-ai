// Command solana-wallet shows the wallet Algebra pays from, or creates a new
// one. It only reads from the chain.
//
//	go run ./cmd/solana-wallet                      status of the configured wallet
//	go run ./cmd/solana-wallet -new -out FILE       write a NEW keypair file (never overwrites)
//
// Settings come from the environment or a .env file: SOLANA_CLUSTER,
// SOLANA_RPC_URL, SOLANA_KEYPAIR_FILE (see .env.example).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/providers/solanax402"
)

func main() {
	create := flag.Bool("new", false, "create a new keypair file instead of showing the configured wallet")
	out := flag.String("out", "", "with -new: where to write the keypair (it will not overwrite an existing file)")
	flag.Parse()

	if *create {
		os.Exit(newWallet(*out))
	}
	os.Exit(status())
}

func newWallet(path string) int {
	if path == "" {
		fmt.Fprintln(os.Stderr, "give -out a path, for example .data/solana-devnet.json")
		return 2
	}
	kp, err := solana.NewKeypair()
	if err != nil {
		fmt.Fprintln(os.Stderr, "couldn't generate a key:", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// O_EXCL: never overwrite a wallet, which would destroy its funds.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "not written:", err)
		return 1
	}
	_, werr := f.WriteString(kp.ExportKeygenJSON())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		fmt.Fprintln(os.Stderr, "couldn't write the key file:", werr)
		return 1
	}
	fmt.Printf("New wallet written to %s (keep it private; it is the only copy).\n\n", path)
	fmt.Printf("Address: %s\n\n", kp.PublicKey())
	fmt.Println("To use it, set in your environment or .env:")
	fmt.Println("  SOLANA_CLUSTER=devnet")
	fmt.Printf("  SOLANA_KEYPAIR_FILE=%s\n\n", path)
	fmt.Println("Fund it with devnet USDC at https://faucet.circle.com (choose Solana Devnet), pasting the address above.")
	fmt.Println("It needs no SOL: the provider's sponsor pays transaction fees.")
	return 0
}

func status() int {
	sc, err := config.SolanaFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if sc.Cluster == "" {
		fmt.Fprintln(os.Stderr, "SOLANA_CLUSTER is not set; nothing to show. Create a wallet with: go run ./cmd/solana-wallet -new -out .data/solana-devnet.json")
		return 2
	}
	text := sc.Keypair
	if sc.KeypairFile != "" {
		b, err := os.ReadFile(sc.KeypairFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		text = string(b)
	}
	kp, err := solana.ParseKeypair(text)
	if err != nil {
		fmt.Fprintln(os.Stderr, "the wallet couldn't be loaded:", err)
		return 1
	}
	url := sc.RPCURL
	if url == "" {
		url = map[string]string{"devnet": "https://api.devnet.solana.com", "mainnet": "https://api.mainnet-beta.solana.com"}[sc.Cluster]
	}
	rail, err := solanax402.New(solanax402.Config{Cluster: sc.Cluster, RPC: solana.NewRPC(url, nil), Signer: kp, MaxPaymentMinor: sc.MaxPaymentMinor})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := rail.Status(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "couldn't check the wallet:", err)
		return 1
	}
	fmt.Printf("Cluster:           %s (%s)\n", sc.Cluster, url)
	fmt.Printf("Wallet address:    %s\n", st.Address)
	fmt.Printf("SOL:               %s (not needed; the sponsor pays fees)\n", chain.FormatUnits(int64(st.SOLLamports), 9))
	fmt.Printf("USDC account:      %s\n", st.USDCAccount)
	if st.USDCMinor == nil {
		fmt.Println("USDC balance:      no USDC account yet. Send USDC to the wallet address above to create it.")
	} else {
		fmt.Printf("USDC balance:      %s\n", chain.FormatUnits(int64(*st.USDCMinor), chain.USDCDecimals))
	}
	fmt.Printf("Per-payment limit: %s USDC (SOLANA_MAX_PAYMENT_USDC)\n", chain.FormatUnits(st.MaxPaymentMinor, chain.USDCDecimals))
	return 0
}
