// Command solana-wallet shows the wallet Algebra pays from, or creates a new
// one. It only reads from the chain.
//
//	go run ./cmd/solana-wallet                      status of the configured wallet
//	go run ./cmd/solana-wallet -new -out FILE       write a NEW keypair file (never overwrites)
//	go run ./cmd/solana-wallet -generated           the devnet payer key Algebra generated and keeps sealed in the database
//	go run ./cmd/solana-wallet -generated -out FILE write that key to a file (devnet only; never overwrites)
//
// Settings come from the environment or a .env file: SOLANA_CLUSTER,
// SOLANA_RPC_URL, SOLANA_KEYPAIR_FILE (see .env.example); for -generated,
// DATABASE_URL and ALGEBRA_MASTER_KEY, the ones the server runs with.
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/payersigner"
	"github.com/project-algebra/algebra/internal/platform/postgres"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/providers/solanax402"
)

func main() {
	create := flag.Bool("new", false, "create a new keypair file instead of showing the configured wallet")
	out := flag.String("out", "", "with -new or -generated: where to write the keypair (it will not overwrite an existing file)")
	generated := flag.Bool("generated", false, "show (with -out, export) the devnet payer key Algebra generated and keeps sealed in the database")
	flag.Parse()

	if *generated {
		os.Exit(generatedSigner(*out))
	}
	if *create {
		os.Exit(newWallet(*out))
	}
	os.Exit(status())
}

// writeKey writes a key file that is never overwritten.
func writeKey(path string, kp *solana.Keypair) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// O_EXCL: never overwrite a wallet, which would destroy its funds.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(kp.ExportKeygenJSON())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// generatedSigner is the recovery tool for the payer key Algebra generated: it
// shows the address and balance, and with out writes the key to a file. It only
// opens what is stored; it never makes a key.
func generatedSigner(out string) int {
	_ = godotenv.Load()
	dbURL, master := os.Getenv("DATABASE_URL"), os.Getenv("ALGEBRA_MASTER_KEY")
	if dbURL == "" || master == "" {
		fmt.Fprintln(os.Stderr, "set DATABASE_URL and ALGEBRA_MASTER_KEY to the ones the server runs with")
		return 2
	}
	key, err := base64.StdEncoding.DecodeString(master)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ALGEBRA_MASTER_KEY is not valid base64")
		return 2
	}
	enc, err := payersigner.NewEncryptor(key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, err := postgres.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "couldn't reach the database:", err)
		return 1
	}
	defer db.Close()
	repo := postgres.NewSignerRepo(db)
	kp, row, err := payersigner.Load(ctx, "devnet", payersigner.Deps{Store: repo, Enc: enc})
	if err != nil {
		fmt.Fprintln(os.Stderr, "no devnet payer key could be opened:", err)
		return 1
	}
	url := os.Getenv("SOLANA_DEVNET_RPC_URL")
	if url == "" {
		url = "https://api.devnet.solana.com"
	}
	fmt.Printf("Generated devnet payer key, made %s\n", row.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Address:           %s\n", kp.PublicKey())
	if sol, err := solana.NewRPC(url, nil).GetBalance(ctx, kp.PublicKey(), solana.Confirmed); err == nil {
		ready := "ready"
		if sol < payersigner.FeeFloorLamports {
			ready = "TOO LOW: fund the address at https://faucet.solana.com (devnet)"
		}
		fmt.Printf("SOL:               %s (%s; it pays the fees of vault pulls and refunds)\n", chain.FormatUnits(int64(sol), 9), ready)
	}
	if has, err := repo.HasPasses(ctx, chain.SolanaDevnet); err == nil && has {
		fmt.Println("Vaults:            on-chain vaults exist for this key. A different key cannot pull from them.")
	}
	if out == "" {
		return 0
	}
	if err := writeKey(out, kp); err != nil {
		fmt.Fprintln(os.Stderr, "not written:", err)
		return 1
	}
	fmt.Printf("\nKey written to %s (devnet only; keep it private). To run a server with it instead of the database copy, set SOLANA_DEVNET_KEYPAIR_FILE and remove SOLANA_DEVNET_SIGNER; the address does not change.\n", out)
	return 0
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
	if err := writeKey(path, kp); err != nil {
		fmt.Fprintln(os.Stderr, "not written:", err)
		return 1
	}
	fmt.Printf("New wallet written to %s (keep it private; it is the only copy).\n\n", path)
	fmt.Printf("Address: %s\n\n", kp.PublicKey())
	fmt.Println("To use it, set in your environment or .env:")
	fmt.Println("  SOLANA_CLUSTER=devnet")
	fmt.Printf("  SOLANA_KEYPAIR_FILE=%s\n\n", path)
	fmt.Println("Fund it with devnet USDC at https://faucet.circle.com (choose Solana Devnet), pasting the address above.")
	fmt.Println("An x402 payment's fees are the provider's. If payments come from Spend Pass vaults, this wallet also")
	fmt.Println("pays the fees of pulls and refunds: give it a little devnet SOL at https://faucet.solana.com.")
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
	fmt.Printf("SOL:               %s (x402 fees are the provider's; vault pulls and refunds are paid from this)\n", chain.FormatUnits(int64(st.SOLLamports), 9))
	fmt.Printf("USDC account:      %s\n", st.USDCAccount)
	if st.USDCMinor == nil {
		fmt.Println("USDC balance:      no USDC account yet. Send USDC to the wallet address above to create it.")
	} else {
		fmt.Printf("USDC balance:      %s\n", chain.FormatUnits(int64(*st.USDCMinor), chain.USDCDecimals))
	}
	fmt.Printf("Per-payment limit: %s USDC (SOLANA_MAX_PAYMENT_USDC)\n", chain.FormatUnits(st.MaxPaymentMinor, chain.USDCDecimals))
	return 0
}
