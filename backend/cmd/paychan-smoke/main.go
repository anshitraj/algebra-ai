// Command paychan-smoke runs x402 "upto" end to end on Solana devnet through the
// real payment-channels program, with a throwaway token so no USDC is needed:
// it mints a test token, opens a channel escrowing a 0.05 ceiling (the payer
// signs, the provider verifies, co-signs as fee payer and submits), settles
// 0.012345 from the provider's voucher, and prints the token balance changes:
// the provider paid, the rest refunded, in one transaction.
//
//	go run ./cmd/paychan-smoke   # wallets: solana-devnet.json and demo-provider.json in the data directory
//
// Both wallets need a little devnet SOL; it never touches mainnet.
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/project-algebra/algebra/internal/platform/datadir"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/providers/paychan"
	"github.com/project-algebra/algebra/providers/x402"
)

var rpc = solana.NewRPC("https://api.devnet.solana.com", &http.Client{Timeout: 30 * time.Second})

func key(path string) *solana.Keypair {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	k, err := solana.ParseKeypair(string(b))
	if err != nil {
		log.Fatal(err)
	}
	return k
}

func send(ctx context.Context, what string, feePayer solana.PublicKey, ixs []solana.Instruction, signers ...*solana.Keypair) string {
	bh, err := rpc.GetLatestBlockhash(ctx, solana.Confirmed)
	if err != nil {
		log.Fatal(err)
	}
	msg, err := solana.CompileV0(feePayer, ixs, bh.Blockhash)
	if err != nil {
		log.Fatal(err)
	}
	tx := solana.NewTransaction(msg)
	for _, s := range signers {
		if err := tx.PartialSign(s); err != nil {
			log.Fatal(err)
		}
	}
	return submit(ctx, what, tx)
}

func submit(ctx context.Context, what string, tx *solana.Transaction) string {
	sig, err := rpc.SendTransaction(ctx, tx.Base64(), solana.SendOptions{})
	if err != nil {
		log.Fatalf("%s: %v", what, err)
	}
	if _, err := rpc.ConfirmSignature(ctx, sig, solana.Confirmed, 0); err != nil {
		log.Fatalf("%s: %v", what, err)
	}
	fmt.Printf("%-22s https://explorer.solana.com/tx/%s?cluster=devnet\n", what, sig)
	return sig
}

func ataCreate(funder, owner, mint solana.PublicKey) solana.Instruction {
	ata, _ := solana.AssociatedTokenAddress(owner, mint, solana.TokenProgram)
	return solana.Instruction{ProgramID: solana.AssociatedTokenProgram, Data: []byte{1}, Accounts: []solana.AccountMeta{
		{Pubkey: funder, IsSigner: true, IsWritable: true}, {Pubkey: ata, IsWritable: true}, {Pubkey: owner}, {Pubkey: mint},
		{Pubkey: solana.SystemProgram}, {Pubkey: solana.TokenProgram}}}
}

func main() {
	payerPath := flag.String("payer", datadir.Path("solana-devnet.json"), "the paying wallet (Algebra's devnet wallet)")
	providerPath := flag.String("provider", datadir.Path("demo-provider.json"), "the provider's wallet: fee payer, payee and receiver authorizer")
	flag.Parse()
	ctx := context.Background()
	if g, _ := rpc.GetGenesisHash(ctx); g != solana.DevnetGenesisHash {
		log.Fatal("not devnet")
	}
	payer := key(*payerPath)
	sponsor := key(*providerPath)
	A, S := payer.PublicKey(), sponsor.PublicKey()

	// 1. A throwaway 6-decimal token, 1.0 of it to the payer.
	mint, _ := solana.NewKeypair()
	M := mint.PublicKey()
	rent, err := rpc.GetMinimumBalanceForRentExemption(ctx, 82)
	if err != nil {
		log.Fatal(err)
	}
	create := binary.LittleEndian.AppendUint32(nil, 0)
	create = binary.LittleEndian.AppendUint64(create, rent)
	create = binary.LittleEndian.AppendUint64(create, 82)
	create = append(create, solana.TokenProgram[:]...)
	initMint := append([]byte{20, 6}, A[:]...)
	initMint = append(initMint, 0)
	payerATA, _ := solana.AssociatedTokenAddress(A, M, solana.TokenProgram)
	mintTo := binary.LittleEndian.AppendUint64([]byte{7}, 1_000_000)
	treasury, _ := paychan.TreasuryOwner("solana-devnet")
	send(ctx, "mint + accounts", A, []solana.Instruction{
		{ProgramID: solana.SystemProgram, Data: create, Accounts: []solana.AccountMeta{{Pubkey: A, IsSigner: true, IsWritable: true}, {Pubkey: M, IsSigner: true, IsWritable: true}}},
		{ProgramID: solana.TokenProgram, Data: initMint, Accounts: []solana.AccountMeta{{Pubkey: M, IsWritable: true}}},
		ataCreate(A, A, M), ataCreate(A, S, M), ataCreate(A, treasury, M),
		{ProgramID: solana.TokenProgram, Data: mintTo, Accounts: []solana.AccountMeta{{Pubkey: M, IsWritable: true}, {Pubkey: payerATA, IsWritable: true}, {Pubkey: A, IsSigner: true}}},
	}, payer, mint)

	// 2. The provider's 402 terms: a 0.05 ceiling, settled by voucher.
	extra := fmt.Sprintf(`{"paymentFlow":"escrow","feePayer":"%s","receiverAuthorizer":"%s","withdrawDelay":900,"tokenProgram":"%s"}`, S, S, solana.TokenProgram)
	terms, err := paychan.ParseUptoTerms(x402.Requirements{Scheme: "upto", Network: "solana-devnet", Amount: "50000", Asset: M.String(), PayTo: S.String(), MaxTimeoutSeconds: 300, Extra: []byte(extra)})
	if err != nil {
		log.Fatal(err)
	}

	// 3. Algebra signs the open as payer (what the rail does for an upto option).
	slot, err := rpc.GetSlot(ctx, solana.Confirmed)
	if err != nil {
		log.Fatal(err)
	}
	bh, err := rpc.GetLatestBlockhash(ctx, solana.Confirmed)
	if err != nil {
		log.Fatal(err)
	}
	_, payload, err := paychan.BuildUptoOpen(terms, payer, bh.Blockhash, slot, time.Now(), 1)
	if err != nil {
		log.Fatal(err)
	}

	// 4. The provider verifies it, co-signs as fee payer and rent payer, submits.
	openTx, ch, err := paychan.VerifyUptoOpen(terms, payload)
	if err != nil {
		log.Fatalf("provider refused the open: %v", err)
	}
	if err := openTx.PartialSign(sponsor); err != nil {
		log.Fatal(err)
	}
	submit(ctx, "channel open (0.05)", openTx)
	info, _ := rpc.GetAccountInfo(ctx, ch, solana.Confirmed)
	c, err := paychan.DecodeChannel(info.Data)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("channel %s: %s, deposit %d, settled %d\n", ch, c.Status, c.Deposit, c.Settled)

	// 5. The work cost 0.012345: the provider settles that from its voucher;
	// distribute pays it and refunds the rest in the same transaction.
	actual := uint64(12_345)
	v := paychan.SignVoucher(sponsor, ch, actual, time.Unix(payload.ExpiresAt, 0))
	bh, _ = rpc.GetLatestBlockhash(ctx, solana.Confirmed)
	msg, err := paychan.SettleUptoTx("solana-devnet", terms, A, ch, &v, bh.Blockhash)
	if err != nil {
		log.Fatal(err)
	}
	stx := solana.NewTransaction(msg)
	if err := stx.PartialSign(sponsor); err != nil {
		log.Fatal(err)
	}
	sig := submit(ctx, "settle + distribute", stx)

	tb, err := rpc.GetTransactionBalances(ctx, sig, solana.Confirmed)
	if err != nil {
		log.Fatal(err)
	}
	escrow, _ := solana.AssociatedTokenAddress(ch, M, solana.TokenProgram)
	payToATA, _ := solana.AssociatedTokenAddress(S, M, solana.TokenProgram)
	fmt.Println("escrow change: ", tb.Deltas[escrow.String()].Change())
	fmt.Println("provider paid: ", tb.Deltas[payToATA.String()].Change())
	fmt.Println("payer refunded:", tb.Deltas[payerATA.String()].Change())
	fmt.Println("ceiling", payload.MaxAmount, "actual", strconv.FormatUint(actual, 10))
}
