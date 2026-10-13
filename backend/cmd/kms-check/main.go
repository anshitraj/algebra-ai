// Command kms-check tries a Cloud KMS key as Algebra's payer before a server
// is pointed at it: it prints the key's Solana address (the one to fund and to
// pin), has KMS sign test messages and checks the signatures, and confirms
// that a wrong key name is refused. It talks to the real service.
//
//	go run ./cmd/kms-check -version projects/…/cryptoKeyVersions/1        read, sign, verify
//	go run ./cmd/kms-check -version projects/…/cryptoKeyVersions/1 -tx    and send one memo transaction on devnet
//
// Credentials are the host's application default credentials, as for the
// server: on another host, GOOGLE_APPLICATION_CREDENTIALS names the file.
// Without -tx nothing touches a chain. With -tx the key pays the fee of one
// devnet transaction, so it needs a little devnet SOL (the faucet is asked
// first). There is no mainnet mode.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/platform/gcpkms"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

const devnetRPC = "https://api.devnet.solana.com"

var failed bool

func check(name string, ok bool, detail string) {
	mark := "ok  "
	if !ok {
		mark, failed = "FAIL", true
	}
	fmt.Printf("%s  %s", mark, name)
	if detail != "" {
		fmt.Printf("  (%s)", detail)
	}
	fmt.Println()
}

func main() {
	version := flag.String("version", "", "the CryptoKeyVersion resource name")
	sendTx := flag.Bool("tx", false, "also send one memo transaction on devnet")
	flag.Parse()
	if *version == "" {
		fmt.Fprintln(os.Stderr, "usage: kms-check -version projects/…/locations/…/keyRings/…/cryptoKeys/…/cryptoKeyVersions/N [-tx]")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	client, err := gcpkms.New(ctx)
	if err != nil {
		fmt.Println("FAIL  credentials:", err)
		os.Exit(1)
	}

	// 1. The public key, and so the Solana address.
	started := time.Now()
	key, err := client.Signer(ctx, *version)
	if err != nil {
		fmt.Println("FAIL  read the key:", err)
		os.Exit(1)
	}
	pk := key.PublicKey()
	check("the key is read and is Ed25519", true, fmt.Sprintf("address %s, %dms", pk, time.Since(started).Milliseconds()))
	check("it is a remote signer: no private half in this process", key.IsRemote(), "")
	check("the address is a point on the curve (a wallet, not a program address)", solana.IsOnCurve(pk), "")

	// 2. Reading it again gives the same address.
	again, err := client.Signer(ctx, *version)
	check("a second read gives the same address", err == nil && again.PublicKey() == pk, "")

	// 3. Signatures verify against the public key, and Ed25519 is deterministic.
	msg := make([]byte, 180)
	_, _ = rand.Read(msg)
	started = time.Now()
	sig, err := key.SignChecked(msg)
	took := time.Since(started)
	check("KMS signs a message", err == nil, fmt.Sprintf("%dms", took.Milliseconds()))
	check("the signature verifies against the key's public half", err == nil && ed25519.Verify(pk[:], msg, sig[:]), "")
	sig2, err2 := key.SignChecked(msg)
	check("signing the same message again gives the same signature", err2 == nil && sig2 == sig, "")
	other := append([]byte{}, msg...)
	other[0] ^= 1
	check("the signature does not verify for a different message", !ed25519.Verify(pk[:], other, sig[:]), "")

	// 4. Messages the size of a real transaction (a v0 message can be 1232 bytes).
	big := make([]byte, 1232)
	_, _ = rand.Read(big)
	bsig, err := key.SignChecked(big)
	check("a transaction-sized message (1232 bytes) is signed and verifies", err == nil && ed25519.Verify(pk[:], big, bsig[:]), "")

	// 5. What must be refused.
	keyName, _, _ := strings.Cut(*version, "/cryptoKeyVersions/")
	_, err = client.Signer(ctx, keyName)
	check("a key name without a version is refused", err != nil, errText(err))
	_, err = client.Signer(ctx, keyName+"/cryptoKeyVersions/999999")
	check("a version that does not exist is refused", err != nil, errText(err))
	check("the refusal carries no response body", err != nil && len(err.Error()) < 160 && !strings.Contains(err.Error(), "{"), "")

	if *sendTx {
		transaction(ctx, key)
	}
	if failed {
		os.Exit(1)
	}
}

func errText(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
}

// transaction sends one memo transaction on devnet with the KMS key as the
// fee payer and only signer: the same PartialSign path every payment uses.
func transaction(ctx context.Context, key *solana.Keypair) {
	rpc := solana.NewRPC(devnetRPC, nil)
	pk := key.PublicKey()
	bal, err := rpc.GetBalance(ctx, pk, solana.Confirmed)
	if err != nil {
		check("devnet balance is read", false, err.Error())
		return
	}
	if bal < 10_000 {
		if sig, err := rpc.RequestAirdrop(ctx, pk, 50_000_000); err != nil {
			check("the devnet faucet gives fee money", false, "refused: "+err.Error())
			fmt.Printf("      send a little devnet SOL to %s and run again\n", pk)
			return
		} else if _, err := rpc.ConfirmSignature(ctx, sig, solana.Confirmed, 0); err != nil {
			check("the devnet faucet gives fee money", false, err.Error())
			return
		}
		bal, _ = rpc.GetBalance(ctx, pk, solana.Confirmed)
	}
	check("the key has devnet SOL for fees", bal >= 10_000, fmt.Sprintf("%d lamports", bal))

	memo, err := solana.Memo("algebra kms check " + time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		check("memo instruction", false, err.Error())
		return
	}
	bh, err := rpc.GetLatestBlockhash(ctx, solana.Confirmed)
	if err != nil {
		check("blockhash", false, err.Error())
		return
	}
	m, err := solana.CompileV0(pk, []solana.Instruction{memo}, bh.Blockhash)
	if err != nil {
		check("the transaction compiles", false, err.Error())
		return
	}
	tx := solana.NewTransaction(m)
	if err := tx.PartialSign(key); err != nil {
		check("KMS signs the transaction", false, err.Error())
		return
	}
	check("KMS signs the transaction, and the signature verifies", tx.VerifySignature(pk), "")
	sig, err := rpc.SendTransaction(ctx, tx.Base64(), solana.SendOptions{})
	if err != nil {
		check("devnet accepts the transaction", false, err.Error())
		return
	}
	_, err = rpc.ConfirmSignature(ctx, sig, solana.Confirmed, 0)
	check("the transaction is confirmed on devnet", err == nil, "https://solscan.io/tx/"+sig+"?cluster=devnet")
}
