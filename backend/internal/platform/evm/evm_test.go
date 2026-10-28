package evm

import (
	"strings"
	"testing"
)

// Fixtures made by the browser wallet (frontend/lib/wallet) from the
// well-known "abandon … about" phrase, whose first EVM address is 0x9858….
const (
	fixtureAddress = "0x9858effd232b4033e47d90003d41ec34ecaeda94"
	fixtureMessage = "Algebra vault ownership\nAccount: user_test\nSolana: HAgk14JpMQLgt6rVgv7cBQFJWFto5Dqxi472uT3DKpqk\nEVM: 0x9858EfFD232B4033E47d90003D41EC34EcaEda94\nIssued: 2026-10-29T10:00:00.000Z"
	fixtureSig     = "0xef49bb5ebf3bf1d3dd0ac70d335278658ee2bd137d8f1fc50c61c5c84c56cc95353f1fab79c918f39d0598667da90715b91a7db7eb09b62cbb5aed7c3b0396711c"
	fixtureRaw     = "0x02f8b183014a3403830f4240847735940082fde894036cbd53842c5426634e7929541ec2318f3dcf7e80b844a9059cbb0000000000000000000000005aaeb6053f3e94c9b9a09f33669435e7ef1beaed000000000000000000000000000000000000000000000000000000000016e360c001a0b7c9f00ae4873f45701941d86dbd20111be567f2ea23d303ffc29e55cef9f327a02f0d19d4c01945215bd5eb562dfe8ff5c3c50fdc81d1a89249cabc70f9d05d72"
	fixtureHash    = "0x2ea686644356761188e2ebb77cc4bf65346be659abe88b03ea9f2d447b7752ef"
)

func TestPersonalSigner_RecoversTheBrowsersAddress(t *testing.T) {
	got, err := PersonalSigner(fixtureMessage, fixtureSig)
	if err != nil || AddressHex(got) != fixtureAddress {
		t.Fatalf("got %s %v", AddressHex(got), err)
	}
	// One changed character is someone else's signature.
	if got, _ := PersonalSigner(strings.Replace(fixtureMessage, "user_test", "user_tesu", 1), fixtureSig); AddressHex(got) == fixtureAddress {
		t.Fatal("an edited message still recovers the owner")
	}
	if _, err := PersonalSigner(fixtureMessage, "0x1234"); err == nil {
		t.Fatal("a short signature is refused")
	}
}

func TestDecodeSignedEIP1559_ReadsChainRecipientAndSender(t *testing.T) {
	tx, err := DecodeSignedEIP1559(fixtureRaw)
	if err != nil {
		t.Fatal(err)
	}
	if tx.ChainID.Int64() != 84532 || AddressHex(tx.From) != fixtureAddress || AddressHex(tx.To) != "0x036cbd53842c5426634e7929541ec2318f3dcf7e" || tx.Hash != fixtureHash {
		t.Fatalf("%+v from=%s to=%s", tx, AddressHex(tx.From), AddressHex(tx.To))
	}
	for _, bad := range []string{"0x01f8", "0x02", fixtureRaw[:len(fixtureRaw)-2], "nothex"} {
		if _, err := DecodeSignedEIP1559(bad); err == nil {
			t.Errorf("%q decoded", bad)
		}
	}
}
