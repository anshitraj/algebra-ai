// Command release-check reads chain state. It never signs or sends a transaction.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/gcpkms"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/internal/platform/solana/spendpass"
)

var loader = solana.MustPublicKey("BPFLoaderUpgradeab1e11111111111111111111111")

func programData(d []byte) (slot uint64, authority string, code []byte, err error) {
	if len(d) < 45 || binary.LittleEndian.Uint32(d) != 3 || d[12] > 1 {
		return 0, "", nil, errors.New("invalid upgradeable ProgramData")
	}
	authority = "immutable"
	if d[12] == 1 {
		var key solana.PublicKey
		copy(key[:], d[13:45])
		authority = key.String()
	}
	return binary.LittleEndian.Uint64(d[4:12]), authority, d[45:], nil
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("release-check", flag.ContinueOnError)
	cluster := fs.String("cluster", "mainnet", "mainnet or devnet")
	rpcURL := fs.String("rpc", "", "RPC URL; defaults to cluster public node")
	program := fs.String("program", spendpass.DefaultProgramID, "program address")
	payer := fs.String("payer", "", "public payer address (no private key)")
	kmsVersion := fs.String("kms-key-version", "", "optional: resolve public payer address through ADC/KMS")
	authority := fs.String("upgrade-authority", "", "expected multisig authority address, or immutable")
	binaryFile := fs.String("binary", "", "reviewed .so file to compare byte-for-byte with deployment")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cluster != "mainnet" && *cluster != "devnet" {
		return errors.New("cluster must be mainnet or devnet")
	}
	if *authority == "" || *binaryFile == "" || (*payer == "" && *kmsVersion == "") {
		return errors.New("binary, expected upgrade-authority, and payer or kms-key-version are required")
	}
	if *rpcURL == "" {
		*rpcURL = "https://api.mainnet-beta.solana.com"
		if *cluster == "devnet" {
			*rpcURL = "https://api.devnet.solana.com"
		}
	}
	if *kmsVersion != "" {
		c, err := gcpkms.New(ctx)
		if err != nil {
			return err
		}
		signer, err := c.Signer(ctx, *kmsVersion)
		if err != nil {
			return err
		}
		address := signer.PublicKey().String()
		if *payer != "" && *payer != address {
			return errors.New("payer does not match KMS public key")
		}
		*payer = address
	}
	pk, err := solana.ParsePublicKey(*payer)
	if err != nil {
		return errors.New("invalid payer")
	}
	prog, err := solana.ParsePublicKey(*program)
	if err != nil {
		return errors.New("invalid program")
	}
	if *authority == *payer {
		return errors.New("upgrade authority must be separate from the API payer")
	}
	if *authority != "immutable" {
		if _, err := solana.ParsePublicKey(*authority); err != nil {
			return errors.New("invalid upgrade authority")
		}
	}
	artifact, err := os.ReadFile(*binaryFile)
	if err != nil {
		return err
	}
	if len(artifact) < 4 || !bytes.Equal(artifact[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return errors.New("binary is not an ELF program")
	}
	rpc := solana.NewRPC(*rpcURL, nil)
	genesis, err := rpc.GetGenesisHash(ctx)
	if err != nil {
		return errors.New("RPC genesis lookup failed")
	}
	want := solana.MainnetGenesisHash
	network := chain.Solana
	if *cluster == "devnet" {
		want, network = solana.DevnetGenesisHash, chain.SolanaDevnet
	}
	if genesis != want {
		return errors.New("RPC serves a different cluster")
	}
	a, err := rpc.GetAccountInfo(ctx, prog, solana.Finalized)
	if err != nil {
		return errors.New("program lookup failed")
	}
	if a == nil {
		return errors.New("program is not deployed on " + *cluster)
	}
	if !a.Executable || a.Owner != loader || len(a.Data) != 36 || binary.LittleEndian.Uint32(a.Data) != 2 {
		return errors.New("program is not an upgradeable-loader executable")
	}
	var dataAddress solana.PublicKey
	copy(dataAddress[:], a.Data[4:36])
	d, err := rpc.GetAccountInfo(ctx, dataAddress, solana.Finalized)
	if err != nil || d == nil || d.Owner != loader {
		return errors.New("program data lookup failed")
	}
	slot, actualAuthority, code, err := programData(d.Data)
	if err != nil {
		return err
	}
	if actualAuthority != *authority {
		return fmt.Errorf("upgrade authority differs: chain has %s", actualAuthority)
	}
	if len(code) < len(artifact) || !bytes.Equal(code[:len(artifact)], artifact) || !bytes.Equal(code[len(artifact):], make([]byte, len(code)-len(artifact))) {
		return errors.New("deployed binary differs from reviewed artifact")
	}
	lamports, err := rpc.GetBalance(ctx, pk, solana.Finalized)
	if err != nil {
		return errors.New("payer balance lookup failed")
	}
	mintAddress, _ := chain.AssetAddress(network, "USDC")
	token, err := solana.AssociatedTokenAddress(pk, solana.MustPublicKey(mintAddress), solana.TokenProgram)
	if err != nil {
		return err
	}
	ata, err := rpc.GetAccountInfo(ctx, token, solana.Finalized)
	if err != nil {
		return errors.New("payer ATA lookup failed")
	}
	if lamports == 0 || ata == nil || ata.Owner != solana.TokenProgram {
		return errors.New("payer needs SOL and an initialized USDC ATA; USDC is supplied by restricted vaults")
	}
	sum := sha256.Sum256(artifact)
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"cluster": *cluster, "program": *program, "program_data": dataAddress.String(), "deployment_slot": slot, "upgrade_authority": actualAuthority, "binary_sha256": hex.EncodeToString(sum[:]), "payer": *payer, "payer_lamports": lamports, "payer_usdc_ata": token.String(), "chain_preflight": "passed", "external_review_and_live_payment": "require separate release evidence"})
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release-check:", err)
		os.Exit(1)
	}
}
