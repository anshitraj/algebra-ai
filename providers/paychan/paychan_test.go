package paychan

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/providers/x402"
)

// A real open on devnet (slot 508188926): its accounts and instruction data,
// as the chain recorded them. The encoders must produce exactly these.
func TestOpenMatchesARealDevnetChannel(t *testing.T) {
	payer := solana.MustPublicKey("FvZ2jvMmVveZYNvsQEQbpkPnHtUuBWburtHAYAWW4SLg")
	payee := solana.MustPublicKey("2thyaZ4skNqYyMdR4HSkkQ1VPUCnzi884AahjmXy6fXu")
	mint := solana.MustPublicKey("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU")
	data, _ := hex.DecodeString("01dd79cd39a67ca4f8400d03000000000084030000f9584a1e0000000000000000")
	salt := uint64(0xf8a47ca639cd79dd)
	openSlot := uint64(0x1e4a58f9)

	if got := EventAuthority().String(); got != "75c2huVzDW1Eq4kD86by5392sjdJzuAT195XNgP2f2tX" {
		t.Fatalf("event authority %s", got)
	}
	ix, ch, err := Open(OpenParams{Payer: payer, RentPayer: payer, Payee: payee, Mint: mint, AuthorizedSigner: payer,
		Salt: salt, Deposit: 200_000, GracePeriod: 900, OpenSlot: openSlot})
	if err != nil {
		t.Fatal(err)
	}
	if ch.String() != "Exf7dxNdykdxtLTZQoitkjVRxbtRw4FhqXkUMxsbvG7J" {
		t.Fatalf("channel PDA %s", ch)
	}
	if hex.EncodeToString(ix.Data) != hex.EncodeToString(data) {
		t.Fatalf("data\n got %x\nwant %x", ix.Data, data)
	}
	want := []string{
		"FvZ2jvMmVveZYNvsQEQbpkPnHtUuBWburtHAYAWW4SLg", "FvZ2jvMmVveZYNvsQEQbpkPnHtUuBWburtHAYAWW4SLg",
		"2thyaZ4skNqYyMdR4HSkkQ1VPUCnzi884AahjmXy6fXu", "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU",
		"FvZ2jvMmVveZYNvsQEQbpkPnHtUuBWburtHAYAWW4SLg", "Exf7dxNdykdxtLTZQoitkjVRxbtRw4FhqXkUMxsbvG7J",
		"HoCt2EMsADWrFoHWVQobYPozo9p1Jp48W3ij8NSbuPyn", "78aYdf3W4CcKqseP24bBwRuZwftxgRGcceLSAG9DBkGu",
		"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", "11111111111111111111111111111111",
		"SysvarRent111111111111111111111111111111111", "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL",
		"75c2huVzDW1Eq4kD86by5392sjdJzuAT195XNgP2f2tX", "CHNLxYvVA28MJP9PrFuDXccuoGXAx7jBacfLEkahyGsX",
	}
	for i, w := range want {
		if ix.Accounts[i].Pubkey.String() != w {
			t.Errorf("account %d = %s, want %s", i, ix.Accounts[i].Pubkey, w)
		}
	}
}

func TestVoucherAndItsPrecompileInstruction(t *testing.T) {
	k, _ := solana.NewKeypair()
	ch := solana.MustPublicKey("Exf7dxNdykdxtLTZQoitkjVRxbtRw4FhqXkUMxsbvG7J")
	v := SignVoucher(k, ch, 42_500, time.Unix(1_800_000_000, 0))
	if !v.Verify() {
		t.Fatal("voucher doesn't verify")
	}
	m := VoucherMessage(ch, 42_500, 1_800_000_000)
	if len(m) != VoucherSize || m[0] != 0x56 || m[1] != 0x01 {
		t.Fatalf("message %x", m)
	}
	ix, err := v.Instruction()
	if err != nil {
		t.Fatal(err)
	}
	// The program accepts only the canonical 162-byte layout.
	if len(ix.Data) != 162 || ix.Data[0] != 1 || ix.ProgramID != solana.Ed25519Program {
		t.Fatalf("precompile ix: %d bytes", len(ix.Data))
	}
	v.Cumulative++
	if v.Verify() {
		t.Fatal("a changed amount must not verify")
	}
}

func TestDecodeChannelRoundTrip(t *testing.T) {
	b := make([]byte, ChannelSize)
	b[0], b[1], b[3] = 1, 1, byte(StatusSealed)
	b[12] = 0x40 // deposit 64
	b[20] = 0x10 // settled 16
	c, err := DecodeChannel(b)
	if err != nil || c.Status != StatusSealed || c.Deposit != 64 || c.Settled != 16 || c.Refundable() != 48 {
		t.Fatalf("%v %+v", err, c)
	}
	if _, err := DecodeChannel(b[:10]); err == nil {
		t.Fatal("short account accepted")
	}
}

func TestPreimageRefusesBadShares(t *testing.T) {
	a := solana.MustPublicKey("FvZ2jvMmVveZYNvsQEQbpkPnHtUuBWburtHAYAWW4SLg")
	if _, err := preimage([]Entry{{a, 6000}, {a, 1000}}); err == nil {
		t.Fatal("duplicate recipient accepted")
	}
	b := solana.MustPublicKey("2thyaZ4skNqYyMdR4HSkkQ1VPUCnzi884AahjmXy6fXu")
	if _, err := preimage([]Entry{{a, 6000}, {b, 5000}}); err == nil {
		t.Fatal("over 100% accepted")
	}
	p, err := preimage([]Entry{{a, 10_000}})
	if err != nil || len(p) != 4+34 {
		t.Fatalf("%v %d", err, len(p))
	}
}

func uptoTerms(t *testing.T, feePayer, payTo solana.PublicKey) UptoTerms {
	t.Helper()
	extra := `{"feePayer":"` + feePayer.String() + `","receiverAuthorizer":"` + feePayer.String() + `","withdrawDelay":900,"tokenProgram":"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"}`
	terms, err := ParseUptoTerms(x402.Requirements{Scheme: "upto", Network: "solana-devnet", Amount: "50000",
		Asset: "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU", PayTo: payTo.String(), MaxTimeoutSeconds: 120, Extra: []byte(extra)})
	if err != nil {
		t.Fatal(err)
	}
	return terms
}

func TestUptoOpenBuildsWhatTheProviderAccepts(t *testing.T) {
	payer, _ := solana.NewKeypair()
	sponsor, _ := solana.NewKeypair()
	cold, _ := solana.NewKeypair()
	terms := uptoTerms(t, sponsor.PublicKey(), cold.PublicKey())
	if terms.MaxAmount != 50_000 || terms.WithdrawDelay != 900 {
		t.Fatalf("terms: %+v", terms)
	}
	_, p, err := BuildUptoOpen(terms, payer, [32]byte{1}, 508_000_000, time.Unix(1_800_000_000, 0), 0)
	if err != nil {
		t.Fatal(err)
	}
	tx, ch, err := VerifyUptoOpen(terms, p)
	if err != nil {
		t.Fatalf("the provider must accept the client's open: %v", err)
	}
	if ch.String() != p.ChannelID || tx.Message.AccountKeys[0] != sponsor.PublicKey() {
		t.Fatal("wrong channel or fee payer")
	}
	// A client that escrows less than the ceiling, or names another channel,
	// is refused.
	bad := p
	bad.MaxAmount, bad.Deposit = "1", "1"
	if _, _, err := VerifyUptoOpen(terms, bad); err == nil {
		t.Fatal("a smaller deposit was accepted")
	}
	bad = p
	bad.ChannelID = cold.PublicKey().String()
	if _, _, err := VerifyUptoOpen(terms, bad); err == nil {
		t.Fatal("a mismatched channel was accepted")
	}
	// The settlement transaction carries the voucher's precompile right before
	// settle_and_seal, then distribute.
	v := SignVoucher(sponsor, ch, 12_000, time.Unix(p.ExpiresAt, 0))
	msg, err := SettleUptoTx("solana-devnet", terms, payer.PublicKey(), ch, &v, [32]byte{2})
	if err != nil {
		t.Fatal(err)
	}
	progs := []solana.PublicKey{}
	for _, ci := range msg.Instructions {
		progs = append(progs, msg.ProgramOf(ci))
	}
	if len(progs) != 5 || progs[2] != solana.Ed25519Program || progs[3] != ProgramID || progs[4] != ProgramID {
		t.Fatalf("settlement layout: %v", progs)
	}
}

func TestUptoRefusesAnOptionWithoutItsTerms(t *testing.T) {
	_, err := ParseUptoTerms(x402.Requirements{Scheme: "upto", Amount: "1", Asset: "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU",
		PayTo: "FvZ2jvMmVveZYNvsQEQbpkPnHtUuBWburtHAYAWW4SLg", Extra: []byte(`{"feePayer":"FvZ2jvMmVveZYNvsQEQbpkPnHtUuBWburtHAYAWW4SLg"}`)})
	if err == nil {
		t.Fatal("no receiver authorizer or withdraw delay, yet accepted")
	}
}
