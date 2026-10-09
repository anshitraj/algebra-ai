// Command demo-provider is a set of paid x402 APIs on Solana devnet, for
// showing Algebra's router and spend firewall against real payments without
// spending real money. Every response says it is a demo.
//
// It serves several "providers" of the same class of work (token.price) that
// differ the way real ones do, plus one usage-billed one:
//
//	alpha   $0.002, fast, honest
//	beta    $0.001, slow (about 1.2 s), honest
//	flaky   $0.0005 listed, always answers 503: down
//	greedy  $0.001 listed, but its 402 asks $0.004: overcharges its listing
//	trap    $25 per call: a honeypot price
//	meter   llm.chat over x402 "upto": escrows a $0.05 ceiling in a payment
//	        channel, settles what the prompt actually cost, refunds the rest
//
// It is its own facilitator: it checks each payment, co-signs it as fee payer
// and submits it to devnet, so its wallet needs a little devnet SOL (it asks
// the devnet faucet at start) and a USDC account (it creates one).
//
//	go run ./cmd/demo-provider   # its wallet: demo-provider.json in the data directory (.data, or ALGEBRA_DATA_DIR)
//	go run ./cmd/demo-provider -print-config   # ECONOMIC_PROVIDERS for the API
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/datadir"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8402", "where to listen")
	rpcURL := flag.String("rpc", "https://api.devnet.solana.com", "devnet RPC endpoint")
	keyPath := flag.String("key", datadir.Path("demo-provider.json"), "the provider's wallet (created if missing)")
	printConfig := flag.Bool("print-config", false, "print ECONOMIC_PROVIDERS for the API and exit")
	flag.Parse()

	base := "http://" + *addr
	if *printConfig {
		b, _ := json.Marshal(configured(base))
		fmt.Println(string(b))
		return
	}

	key, created, err := loadOrCreateKey(*keyPath)
	if err != nil {
		log.Fatalf("demo-provider: %v", err)
	}
	if created {
		log.Printf("created a new provider wallet at %s", *keyPath)
	}
	rpc := solana.NewRPC(*rpcURL, &http.Client{Timeout: 30 * time.Second})
	mint := solana.MustPublicKey(mustUSDC())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	if err := prepare(ctx, rpc, key, mint); err != nil {
		if errors.Is(err, errWrongCluster) {
			log.Fatalf("demo-provider: %v", err)
		}
		// Unpaid requests (prices, health probes) still work; payments need
		// the wallet funded. Try again every minute until it is.
		log.Printf("demo-provider: not ready to take payments yet: %v", err)
		go func() {
			for range time.Tick(time.Minute) {
				c, done := context.WithTimeout(context.Background(), time.Minute)
				err := prepare(c, rpc, key, mint)
				done()
				if err == nil {
					log.Printf("demo-provider: ready to take payments")
					return
				}
			}
		}()
	}
	cancel()

	s := &server{rpc: rpc, key: key, mint: mint, base: base, results: map[string]cached{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /personas", s.listPersonas)
	for _, p := range personas {
		mux.HandleFunc(p.path(), s.handle(p))
	}
	log.Printf("demo providers on %s, paying to %s on devnet", base, key.PublicKey())
	log.Printf("configure the API with: ECONOMIC_PROVIDERS='%s'", mustJSON(configured(base)))
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func mustUSDC() string {
	m, ok := chain.AssetAddress(chain.SolanaDevnet, "USDC")
	if !ok {
		panic("no devnet USDC")
	}
	return m
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func loadOrCreateKey(path string) (*solana.Keypair, bool, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		k, err := solana.ParseKeypair(string(b))
		return k, false, err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	k, err := solana.NewKeypair()
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if _, err := f.WriteString(k.ExportKeygenJSON()); err != nil {
		return nil, false, err
	}
	return k, true, nil
}

var errWrongCluster = errors.New("the RPC endpoint isn't devnet; this demo never runs elsewhere")

// prepare checks the cluster, tops up SOL from the devnet faucet if the
// wallet has almost none, and makes sure the wallet has a USDC account to be
// paid into.
func prepare(ctx context.Context, rpc *solana.RPC, key *solana.Keypair, mint solana.PublicKey) error {
	g, err := rpc.GetGenesisHash(ctx)
	if err != nil {
		return err
	}
	if g != solana.DevnetGenesisHash {
		return errWrongCluster
	}
	me := key.PublicKey()
	bal, err := rpc.GetBalance(ctx, me, solana.Confirmed)
	if err != nil {
		return err
	}
	if bal < 50_000_000 {
		log.Printf("wallet has %d lamports; asking the devnet faucet for 1 SOL", bal)
		sig, err := rpc.RequestAirdrop(ctx, me, 1_000_000_000)
		if err != nil {
			log.Printf("the faucet refused (%v); fund %s with devnet SOL at https://faucet.solana.com", err, me)
		} else if _, err := rpc.ConfirmSignature(ctx, sig, solana.Confirmed, 0); err != nil {
			log.Printf("airdrop not confirmed: %v", err)
		}
	}
	ata, err := solana.AssociatedTokenAddress(me, mint, solana.TokenProgram)
	if err != nil {
		return err
	}
	info, err := rpc.GetAccountInfo(ctx, ata, solana.Confirmed)
	if err != nil {
		return err
	}
	if info != nil {
		return nil
	}
	bh, err := rpc.GetLatestBlockhash(ctx, solana.Confirmed)
	if err != nil {
		return err
	}
	msg, err := solana.CompileV0(me, []solana.Instruction{createATAIdempotent(me, ata, me, mint)}, bh.Blockhash)
	if err != nil {
		return err
	}
	tx := solana.NewTransaction(msg)
	if err := tx.PartialSign(key); err != nil {
		return err
	}
	sig, err := rpc.SendTransaction(ctx, tx.Base64(), solana.SendOptions{})
	if err != nil {
		return fmt.Errorf("creating the USDC account (needs devnet SOL): %w", err)
	}
	if _, err := rpc.ConfirmSignature(ctx, sig, solana.Confirmed, 0); err != nil {
		return err
	}
	log.Printf("created USDC account %s (%s)", ata, sig)
	return nil
}

// createATAIdempotent is the associated token program's CreateIdempotent.
func createATAIdempotent(funder, ata, owner, mint solana.PublicKey) solana.Instruction {
	return solana.Instruction{ProgramID: solana.AssociatedTokenProgram, Data: []byte{1}, Accounts: []solana.AccountMeta{
		{Pubkey: funder, IsSigner: true, IsWritable: true},
		{Pubkey: ata, IsWritable: true},
		{Pubkey: owner},
		{Pubkey: mint},
		{Pubkey: solana.SystemProgram},
		{Pubkey: solana.TokenProgram},
	}}
}
