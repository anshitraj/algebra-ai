package receipt

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func testSigner(t *testing.T, seed byte) *Signer {
	t.Helper()
	s, err := NewSigner(bytes.Repeat([]byte{seed}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sample() Claims {
	return Claims{
		Issuer: "https://algebra.example", ID: "rcpt_1", IssuedAt: 1790000000, Subject: "person_x",
		Agent:    Agent{ID: "agent_1", Name: "Claude — groceries", Client: "spend-pass:claude"},
		Pass:     "pass_1",
		Merchant: "demo_checkout", MerchantOrderID: "DEMO-ABCD2345",
		Amount:        Money{MinorUnits: 49900, Currency: "INR"},
		Items:         []Item{{Name: "HP X200 Wireless Mouse", Quantity: 1, UnitMinorUnits: 45900}},
		ItemsHash:     "abc123",
		Authorization: Authorization{Method: MethodHuman, ApprovedAt: 1790000000, PolicyVersion: "v1+pass:pass_1"},
		Test:          true,
	}
}

func TestSignAndVerify(t *testing.T) {
	s := testSigner(t, 7)
	jws, err := s.Sign(sample())
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(jws, s.JWKS())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Merchant != "demo_checkout" || got.Amount.MinorUnits != 49900 || got.Authorization.Method != MethodHuman || !got.Test {
		t.Fatalf("claims round-trip wrong: %+v", got)
	}
}

func TestVerifyRejectsTamperingAndOtherKeys(t *testing.T) {
	s := testSigner(t, 7)
	jws, _ := s.Sign(sample())
	parts := strings.Split(jws, ".")

	// Change the amount in the payload, keep the signature.
	forged := sample()
	forged.Amount.MinorUnits = 1
	other, _ := s.Sign(forged)
	tampered := parts[0] + "." + strings.Split(other, ".")[1] + "." + parts[2]

	for name, bad := range map[string]string{
		"tampered payload": tampered,
		"truncated":        parts[0] + "." + parts[1],
		"garbage":          "hello",
		"empty":            "",
	} {
		if _, err := Verify(bad, s.JWKS()); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: expected ErrInvalid, got %v", name, err)
		}
	}
	if _, err := Verify(jws, testSigner(t, 9).JWKS()); !errors.Is(err, ErrInvalid) {
		t.Errorf("a receipt must not verify against another deployment's keys, got %v", err)
	}
}

func TestSignerIsDeterministicAndPseudonymsAreStable(t *testing.T) {
	a, b := testSigner(t, 7), testSigner(t, 7)
	if a.JWKS().Keys[0].X != b.JWKS().Keys[0].X {
		t.Fatal("the same master key must give the same receipt key, or old receipts stop verifying")
	}
	if a.Pseudonym("user_1") != b.Pseudonym("user_1") || a.Pseudonym("user_1") == a.Pseudonym("user_2") {
		t.Fatal("pseudonyms must be stable per person and differ between people")
	}
	if strings.Contains(a.Pseudonym("user_1"), "user_1") {
		t.Fatal("a pseudonym must not contain the user ID")
	}
	if _, err := NewSigner([]byte("short")); err == nil {
		t.Fatal("a short master key must be refused")
	}
}
