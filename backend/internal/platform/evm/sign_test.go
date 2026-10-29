package evm

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

func mustAddr(t *testing.T, s string) [20]byte {
	t.Helper()
	a, err := ParseAddress(strings.ToLower(s))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// The example in EIP-712 itself: the domain separator and the digest it gives.
func TestTypedData_SpecExample(t *testing.T) {
	d := Domain{Name: "Ether Mail", Version: "1", ChainID: big.NewInt(1), VerifyingContract: mustAddr(t, "0xCcCCccccCCCCcCCCCCCcCcCccCcCCCcCcccccccC")}
	if got := hex.EncodeToString(d.Separator()); got != "f2cee375fa42b42143804025fc449deafd50cc031ca257e0b194a650a912090f" {
		t.Fatalf("domain separator: %s", got)
	}
	person := Keccak256([]byte("Person(string name,address wallet)"))
	mail := Keccak256([]byte("Mail(Person from,Person to,string contents)Person(string name,address wallet)"))
	from := Keccak256(person, Keccak256([]byte("Cow")), AddressWord(mustAddr(t, "0xCD2a3d9F938E13CD947Ec05AbC7FE734Df8DD826")))
	to := Keccak256(person, Keccak256([]byte("Bob")), AddressWord(mustAddr(t, "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB")))
	msg := Keccak256(mail, from, to, Keccak256([]byte("Hello, Bob!")))
	if got := hex.EncodeToString(TypedDigest(d, msg)); got != "be609aee343fb3c4b28e1df9e632fca64fcfaede20f02e86244efddf30957bd2" {
		t.Fatalf("digest: %s", got)
	}
}

// USDC's domain on Arc testnet, as the contract itself returned it
// (DOMAIN_SEPARATOR() read from rpc.testnet.arc.network on 2026-10-29).
func TestTypedData_ArcTestnetUSDCDomain(t *testing.T) {
	d := Domain{Name: "USDC", Version: "2", ChainID: big.NewInt(5042002), VerifyingContract: mustAddr(t, "0x3600000000000000000000000000000000000000")}
	if got := hex.EncodeToString(d.Separator()); got != "361191522483d32a83e70ae7183b4b9629442c13a78bc9921d6f707911c8c6b0" {
		t.Fatalf("Arc testnet USDC domain separator: %s", got)
	}
}

// A well-known key and its address (web3.js's documentation example), a
// signature that recovers to it, and a signed transaction whose sender
// decodes back to it.
func TestKey_SignRecoverAndSendTransaction(t *testing.T) {
	b, _ := hex.DecodeString("4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318")
	k, err := KeyFromBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if k.Hex() != "0x2c7536e3605d9c16a7a3d7b1898e529396a65c23" {
		t.Fatalf("address: %s", k.Hex())
	}
	digest := Keccak256([]byte("anything"))
	sig, err := k.Sign(digest)
	if err != nil || len(sig) != 65 || (sig[64] != 27 && sig[64] != 28) {
		t.Fatalf("signature: %x %v", sig, err)
	}
	if got, err := Recover(digest, sig); err != nil || got != k.Address() {
		t.Fatalf("recovers to %x, %v", got, err)
	}
	to := mustAddr(t, "0x036CbD53842c5426634e7929541eC2318f3dCF7e")
	raw, hash, err := k.SignTx(Tx1559{ChainID: big.NewInt(84532), Nonce: big.NewInt(7), Tip: big.NewInt(1_000_000), MaxFee: big.NewInt(50_000_000), Gas: big.NewInt(80_000), To: to,
		Data: TransferFromCalldata(mustAddr(t, "0x1111111111111111111111111111111111111111"), k.Address(), big.NewInt(1000))})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := DecodeSignedEIP1559(raw)
	if err != nil {
		t.Fatal(err)
	}
	if tx.From != k.Address() || tx.To != to || tx.ChainID.Int64() != 84532 || tx.Hash != hash {
		t.Fatalf("decoded: %+v (hash %s)", tx, hash)
	}
}

func TestTransferAuthorization_RefusesValuesOutOfRange(t *testing.T) {
	a := TransferAuthorization{Value: big.NewInt(-1), ValidAfter: big.NewInt(0), ValidBefore: big.NewInt(1)}
	if _, err := a.Hash(); err == nil {
		t.Error("a negative value is refused, not wrapped")
	}
	if _, err := (Permit{Value: new(big.Int).Lsh(big.NewInt(1), 256), Nonce: big.NewInt(0), Deadline: big.NewInt(1)}).Hash(); err == nil {
		t.Error("2^256 is refused")
	}
}
