package solana

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func TestBase58Vectors(t *testing.T) {
	for in, want := range map[string]string{
		"48656c6c6f20576f726c6421": "2NEpo7TZRRrLZSi2U", // "Hello World!"
		"000001":                   "112",               // leading zeros are leading ones
		"00":                       "1",
		"":                         "",
		"ff":                       "5Q",
	} {
		b, _ := hex.DecodeString(in)
		if got := EncodeBase58(b); got != want {
			t.Errorf("EncodeBase58(%s) = %q, want %q", in, got, want)
		}
		if want == "" {
			continue
		}
		back, err := DecodeBase58(want)
		if err != nil || !bytes.Equal(back, b) {
			t.Errorf("DecodeBase58(%q) = %x, %v; want %s", want, back, err, in)
		}
	}
	for _, bad := range []string{"", "0OIl", "abc!", "hello world"} {
		if _, err := DecodeBase58(bad); err == nil {
			t.Errorf("DecodeBase58(%q) must fail", bad)
		}
	}
	// Round trip over random lengths, including leading zeros.
	for i := 1; i < 200; i++ {
		b := make([]byte, i%40+1)
		_, _ = rand.Read(b)
		if i%3 == 0 && len(b) > 2 {
			b[0], b[1] = 0, 0
		}
		got, err := DecodeBase58(EncodeBase58(b))
		if err != nil || !bytes.Equal(got, b) {
			t.Fatalf("round trip %x -> %q -> %x (%v)", b, EncodeBase58(b), got, err)
		}
	}
}

func TestKnownAddresses(t *testing.T) {
	if got := SystemProgram.String(); got != "11111111111111111111111111111111" {
		t.Errorf("system program = %s", got)
	}
	// spl_token::id() as the byte array Rust code carries.
	want := [32]byte{6, 221, 246, 225, 215, 101, 161, 147, 217, 203, 225, 70, 206, 235, 121, 172, 28, 180, 133, 237, 95, 91, 55, 145, 58, 140, 245, 133, 126, 255, 0, 169}
	if TokenProgram != PublicKey(want) {
		t.Errorf("token program bytes = %v", [32]byte(TokenProgram))
	}
	for _, bad := range []string{"", "abc", "11111111111111111111111111111", strings.Repeat("1", 40)} {
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("ParsePublicKey(%q) must fail", bad)
		}
	}
	pk, err := ParsePublicKey(" " + TokenProgram.String() + " ")
	if err != nil || pk != TokenProgram {
		t.Errorf("whitespace is trimmed: %v %v", pk, err)
	}
}

func TestIsOnCurve(t *testing.T) {
	// Every real ed25519 public key is a point on the curve.
	for i := 0; i < 500; i++ {
		pub, _, _ := ed25519.GenerateKey(rand.Reader)
		var b [32]byte
		copy(b[:], pub)
		if !IsOnCurve(b) {
			t.Fatalf("a real public key must be on the curve: %x", b)
		}
	}
	// The ed25519 base point.
	base, _ := hex.DecodeString("5866666666666666666666666666666666666666666666666666666666666666")
	var bp [32]byte
	copy(bp[:], base)
	if !IsOnCurve(bp) {
		t.Error("the base point is on the curve")
	}
	// Random bytes are valid points about half the time.
	on := 0
	for i := 0; i < 2000; i++ {
		var b [32]byte
		_, _ = rand.Read(b[:])
		if IsOnCurve(b) {
			on++
		}
	}
	if on < 850 || on > 1150 {
		t.Errorf("about half of random 32-byte strings are on the curve, got %d/2000", on)
	}
}

func TestProgramAddresses(t *testing.T) {
	owner := MustPublicKey("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU") // any valid address
	mint := MustPublicKey("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")
	a, err := AssociatedTokenAddress(owner, mint, TokenProgram)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := AssociatedTokenAddress(owner, mint, TokenProgram)
	if a != b {
		t.Error("derivation is deterministic")
	}
	if IsOnCurve(a) {
		t.Error("a program derived address is never on the curve")
	}
	other, _ := AssociatedTokenAddress(mint, owner, TokenProgram)
	if other == a {
		t.Error("owner and mint are not interchangeable")
	}
	t2022, _ := AssociatedTokenAddress(owner, mint, Token2022Program)
	if t2022 == a {
		t.Error("the token program is part of the derivation")
	}
	// The canonical bump is the highest that works.
	pda, bump, err := FindProgramAddress([][]byte{owner[:], TokenProgram[:], mint[:]}, AssociatedTokenProgram)
	if err != nil || pda != a {
		t.Fatalf("find: %v %v", pda, err)
	}
	if _, err := CreateProgramAddress([][]byte{owner[:], TokenProgram[:], mint[:], {bump}}, AssociatedTokenProgram); err != nil {
		t.Errorf("the reported bump must reproduce the address: %v", err)
	}
	for b := 255; uint8(b) > bump; b-- {
		if _, err := CreateProgramAddress([][]byte{owner[:], TokenProgram[:], mint[:], {byte(b)}}, AssociatedTokenProgram); err == nil {
			t.Errorf("bump %d is higher than %d and also valid: not canonical", b, bump)
		}
	}
	if _, err := CreateProgramAddress([][]byte{make([]byte, 33)}, AssociatedTokenProgram); err == nil {
		t.Error("a seed over 32 bytes is refused")
	}
	if _, err := CreateProgramAddress(make([][]byte, 17), AssociatedTokenProgram); err == nil {
		t.Error("more than 16 seeds are refused")
	}
}

func TestKeypair(t *testing.T) {
	kp, err := NewKeypair()
	if err != nil {
		t.Fatal(err)
	}
	// Both text forms Solana tooling produces round trip to the same key.
	raw := append(append([]byte{}, kp.priv.Seed()...), kp.priv[32:]...)
	asBase58 := EncodeBase58(raw)
	nums := make([]string, len(raw))
	for i, b := range raw {
		nums[i] = fmt.Sprint(b)
	}
	asJSON := "[" + strings.Join(nums, ",") + "]"
	for name, text := range map[string]string{"base58": asBase58, "json": asJSON, "padded": "  " + asJSON + "\n"} {
		got, err := ParseKeypair(text)
		if err != nil || got.PublicKey() != kp.PublicKey() {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A key whose halves don't match, or the wrong size, is refused.
	bad := append([]byte{}, raw...)
	bad[40] ^= 0xff
	for name, text := range map[string]string{
		"mismatched halves": EncodeBase58(bad), "short": EncodeBase58(raw[:32]), "junk": "not a key", "empty": "",
		"out of range": "[1,2,300]", "not numbers": `["a"]`,
	} {
		if _, err := ParseKeypair(text); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	// The secret never prints, however it is formatted.
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%d"} {
		out := fmt.Sprintf(verb, kp)
		if strings.Contains(out, asBase58) || strings.Contains(out, hex.EncodeToString(raw)) || strings.Contains(out, fmt.Sprint(raw[:8])) {
			t.Errorf("%s leaked the secret: %s", verb, out)
		}
	}
	if out := fmt.Sprintf("%+v", struct{ K *Keypair }{kp}); strings.Contains(out, fmt.Sprint(raw[:8])) {
		t.Errorf("a keypair inside a struct leaked: %s", out)
	}
	if _, err := kp.MarshalJSON(); err == nil {
		t.Error("a keypair can't be marshalled")
	}
	// The one deliberate export round-trips, and is the only way out.
	back, err := ParseKeypair(kp.ExportKeygenJSON())
	if err != nil || back.PublicKey() != kp.PublicKey() {
		t.Errorf("export then parse: %v", err)
	}
	sig := kp.Sign([]byte("hello"))
	if !ed25519.Verify(kp.priv.Public().(ed25519.PublicKey), []byte("hello"), sig[:]) {
		t.Error("signature must verify")
	}
}

func TestInstructionEncodings(t *testing.T) {
	limit := SetComputeUnitLimit(60_000)
	if limit.ProgramID != ComputeBudgetProgram || !bytes.Equal(limit.Data, []byte{2, 0x60, 0xea, 0, 0}) {
		t.Errorf("SetComputeUnitLimit: %v", limit.Data)
	}
	price := SetComputeUnitPrice(1)
	if !bytes.Equal(price.Data, []byte{3, 1, 0, 0, 0, 0, 0, 0, 0}) {
		t.Errorf("SetComputeUnitPrice: %v", price.Data)
	}
	src, mint, dst, auth := PublicKey{1}, PublicKey{2}, PublicKey{3}, PublicKey{4}
	tc := TransferChecked(TokenProgram, src, mint, dst, auth, 5_000, 6)
	want := make([]byte, 10)
	want[0] = 12
	binary.LittleEndian.PutUint64(want[1:], 5_000)
	want[9] = 6
	if !bytes.Equal(tc.Data, want) {
		t.Errorf("TransferChecked data: %v", tc.Data)
	}
	if len(tc.Accounts) != 4 || tc.Accounts[0] != (AccountMeta{Pubkey: src, IsWritable: true}) || tc.Accounts[1] != (AccountMeta{Pubkey: mint}) ||
		tc.Accounts[2] != (AccountMeta{Pubkey: dst, IsWritable: true}) || tc.Accounts[3] != (AccountMeta{Pubkey: auth, IsSigner: true}) {
		t.Errorf("TransferChecked accounts (source, mint, destination, authority): %+v", tc.Accounts)
	}
	m, err := Memo("nonce")
	if err != nil || m.ProgramID != MemoProgram || string(m.Data) != "nonce" || len(m.Accounts) != 0 {
		t.Errorf("memo: %+v %v", m, err)
	}
	if _, err := Memo(""); err == nil {
		t.Error("an empty memo is refused")
	}
	if _, err := Memo(strings.Repeat("a", 567)); err == nil {
		t.Error("an oversized memo is refused")
	}
}

func TestShortvec(t *testing.T) {
	for n, want := range map[int][]byte{0: {0}, 1: {1}, 127: {0x7f}, 128: {0x80, 1}, 255: {0xff, 1}, 16384: {0x80, 0x80, 1}} {
		if got := shortvec(nil, n); !bytes.Equal(got, want) {
			t.Errorf("shortvec(%d) = %v, want %v", n, got, want)
		}
		r := &reader{b: want}
		if got := r.shortvec(); got != n || r.err != nil {
			t.Errorf("read shortvec(%v) = %d, %v; want %d", want, got, r.err, n)
		}
	}
}

// buildPayment builds the transaction an x402 payment is: compute budget,
// transfer, memo, with the sponsor as fee payer and only the payer signing.
func buildPayment(t *testing.T) (*Transaction, *Keypair, PublicKey, PublicKey) {
	t.Helper()
	payer, _ := NewKeypair()
	sponsor, _ := NewKeypair()
	mint := MustPublicKey("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")
	payee := MustPublicKey("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU")
	src, _ := AssociatedTokenAddress(payer.PublicKey(), mint, TokenProgram)
	dst, _ := AssociatedTokenAddress(payee, mint, TokenProgram)
	memo, _ := Memo("0123456789abcdef0123456789abcdef")
	var bh [32]byte
	_, _ = rand.Read(bh[:])
	msg, err := CompileV0(sponsor.PublicKey(), []Instruction{
		SetComputeUnitLimit(60_000), SetComputeUnitPrice(1),
		TransferChecked(TokenProgram, src, mint, dst, payer.PublicKey(), 5_000, 6), memo,
	}, bh)
	if err != nil {
		t.Fatal(err)
	}
	tx := NewTransaction(msg)
	if err := tx.PartialSign(payer); err != nil {
		t.Fatal(err)
	}
	return tx, payer, sponsor.PublicKey(), dst
}

func TestPaymentTransactionLayout(t *testing.T) {
	tx, payer, sponsor, dst := buildPayment(t)
	m := tx.Message
	// The sponsor pays and is first; the payer is the only other signer, read-only.
	if m.AccountKeys[0] != sponsor || m.AccountKeys[1] != payer.PublicKey() {
		t.Fatalf("fee payer first, then the payer: %v", m.AccountKeys[:2])
	}
	if m.NumRequiredSignatures != 2 || m.NumReadonlySignedAccounts != 1 {
		t.Errorf("header: %d signers (%d read-only)", m.NumRequiredSignatures, m.NumReadonlySignedAccounts)
	}
	// source and destination token accounts are writable non-signers; mint and the three programs are read-only.
	if m.NumReadonlyUnsignedAccounts != 4 || len(m.AccountKeys) != 8 {
		t.Errorf("header: %d read-only unsigned of %d accounts", m.NumReadonlyUnsignedAccounts, len(m.AccountKeys))
	}
	if m.AccountKeys[3] != dst {
		t.Errorf("writable non-signers come before read-only ones: %v", m.AccountKeys)
	}
	// Four instructions in the order the x402 spec requires.
	progs := make([]PublicKey, len(m.Instructions))
	for i, in := range m.Instructions {
		progs[i] = m.ProgramOf(in)
	}
	if len(progs) != 4 || progs[0] != ComputeBudgetProgram || progs[1] != ComputeBudgetProgram || progs[2] != TokenProgram || progs[3] != MemoProgram {
		t.Errorf("instruction programs: %v", progs)
	}
	// The sponsor appears in no instruction's accounts: that is what stops it being drained.
	for _, in := range m.Instructions {
		for _, a := range m.InstructionAccounts(in) {
			if a == sponsor {
				t.Error("the fee payer must not appear in any instruction")
			}
		}
	}
	// Only the payer has signed, and that signature is valid; the sponsor's slot is empty.
	if sig, signed := tx.SignerSignature(sponsor); signed || sig != [64]byte{} {
		t.Error("the sponsor's slot must be empty")
	}
	if !tx.VerifySignature(payer.PublicKey()) {
		t.Error("the payer's signature must verify")
	}
	// A different message doesn't verify under the same signature.
	tampered, _ := DecodeTransaction(tx.Serialize())
	tampered.Message.Instructions[2].Data[1]++ // change the amount
	if tampered.VerifySignature(payer.PublicKey()) {
		t.Error("changing the amount must invalidate the payer's signature")
	}
}

func TestTransactionRoundTrip(t *testing.T) {
	tx, _, _, _ := buildPayment(t)
	raw := tx.Serialize()
	back, err := DecodeTransactionBase64(tx.Base64())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back.Serialize(), raw) {
		t.Error("decode then encode must reproduce the bytes")
	}
	if raw[1+2*64] != 0x80 {
		t.Errorf("a v0 message starts with 0x80 after the signatures, got %#x", raw[1+2*64])
	}
	for name, mutate := range map[string]func([]byte) []byte{
		"truncated": func(b []byte) []byte { return b[:len(b)-3] },
		"trailing":  func(b []byte) []byte { return append(append([]byte{}, b...), 0) },
		"empty":     func(b []byte) []byte { return nil },
		"bad count": func(b []byte) []byte { c := append([]byte{}, b...); c[0] = 3; return c },
	} {
		if _, err := DecodeTransaction(mutate(raw)); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if _, err := DecodeTransactionBase64("%%%"); err == nil {
		t.Error("non-base64 must be refused")
	}
	// A message with address lookup tables can't be read without them.
	withLookup := append(append([]byte{}, raw[:len(raw)-1]...), 1, 0, 0, 0)
	if _, err := DecodeTransaction(withLookup); err == nil {
		t.Error("lookup tables are refused")
	}
	// A signer that isn't in the message can't sign.
	other, _ := NewKeypair()
	if err := tx.PartialSign(other); err == nil {
		t.Error("only a required signer can sign")
	}
}

func TestCompileMergesAccountFlags(t *testing.T) {
	a, b := PublicKey{1}, PublicKey{2}
	fee := PublicKey{9}
	prog := PublicKey{7}
	msg, err := CompileV0(fee, []Instruction{
		{ProgramID: prog, Accounts: []AccountMeta{{Pubkey: a}, {Pubkey: b, IsSigner: true}}},
		{ProgramID: prog, Accounts: []AccountMeta{{Pubkey: a, IsWritable: true}}},
	}, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	// a is writable (merged), b a read-only signer, prog read-only.
	want := []PublicKey{fee, b, a, prog}
	for i, k := range want {
		if msg.AccountKeys[i] != k {
			t.Fatalf("order: got %v, want %v", msg.AccountKeys, want)
		}
	}
	if msg.NumRequiredSignatures != 2 || msg.NumReadonlySignedAccounts != 1 || msg.NumReadonlyUnsignedAccounts != 1 {
		t.Errorf("header: %+v", msg)
	}
}
