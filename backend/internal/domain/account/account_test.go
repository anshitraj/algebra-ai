package account

import (
	"strings"
	"testing"
	"time"
)

func TestHashAndVerifyPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("unexpected hash format: %s", h)
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Error("correct password rejected")
	}
	if VerifyPassword(h, "correct horse battery stapler") {
		t.Error("wrong password accepted")
	}
	if VerifyPassword("not-a-hash", "x") {
		t.Error("garbage hash accepted")
	}
}

func TestHashPassword_SaltsEveryHash(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Error("two hashes of the same password must differ (random salt)")
	}
}

func TestVerifyPasswordOrDummy_EmptyHashNeverMatches(t *testing.T) {
	if VerifyPasswordOrDummy("", "algebra-timing-equalizer") {
		t.Error("an account with no password must never match, even the dummy's own input")
	}
}

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  Ada@Example.COM ":   "ada@example.com",
		"a.b+c@sub.example.in": "a.b+c@sub.example.in",
	}
	for in, want := range cases {
		got, err := NormalizeEmail(in)
		if err != nil || got != want {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "nope", "Ada <ada@example.com>", "a@b", "a@@b.com"} {
		if _, err := NormalizeEmail(bad); err == nil {
			t.Errorf("NormalizeEmail(%q) should fail", bad)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword("short") == nil {
		t.Error("7-char password accepted")
	}
	if ValidatePassword("longenough") != nil {
		t.Error("10-char password rejected")
	}
	if ValidatePassword(strings.Repeat("x", MaxPasswordLength+1)) == nil {
		t.Error("over-long password accepted")
	}
}

func TestSessionTokens_HashRoundTrip(t *testing.T) {
	raw, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "alg_sess_") {
		t.Errorf("token missing prefix: %s", raw)
	}
	if HashToken(raw) != hash {
		t.Error("hash mismatch")
	}
}

func TestSessionActive(t *testing.T) {
	now := time.Now()
	s := Session{ExpiresAt: now.Add(time.Hour)}
	if !s.Active(now) {
		t.Error("fresh session inactive")
	}
	if s.Active(now.Add(2 * time.Hour)) {
		t.Error("expired session active")
	}
	s.RevokedAt = &now
	if s.Active(now) {
		t.Error("revoked session active")
	}
}

func TestOAuthProfileValidate(t *testing.T) {
	ok := OAuthProfile{Provider: "github", ProviderUserID: "1", Email: "a@b.co", EmailVerified: true}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid profile rejected: %v", err)
	}
	unverified := ok
	unverified.EmailVerified = false
	if unverified.Validate() == nil {
		t.Error("unverified email accepted — would allow account takeover via email linking")
	}
}

func TestGuardrails_NormalizeAndToRules(t *testing.T) {
	g, err := Guardrails{
		MaxPerDayMinorUnits:         500000,
		MaxPerPurchaseMinorUnits:    900000, // above daily → clamped
		ApprovalThresholdMinorUnits: 0,      // always ask
		BlockedCategories:           []string{"Gift_Cards", "alcohol", "alcohol"},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if g.Currency != "INR" || g.MaxPerPurchaseMinorUnits != 500000 {
		t.Errorf("normalize: %+v", g)
	}
	if len(g.BlockedCategories) != 2 {
		t.Errorf("categories not deduped/lowercased: %v", g.BlockedCategories)
	}
	r := g.ToRules()
	if r.ApprovalThresholdMinorUnits != 1 {
		t.Errorf("always-ask must map to threshold 1, got %d", r.ApprovalThresholdMinorUnits)
	}
	if len(r.AllowedPaymentProfiles) == 0 {
		t.Error("payment alias allow-list must be inherited from defaults, not dropped")
	}
}

func TestGuardrails_RejectsUnknownCategoryAndOverCeiling(t *testing.T) {
	if _, err := (Guardrails{MaxPerDayMinorUnits: 1000, BlockedCategories: []string{"giftcards"}}).Normalize(); err == nil {
		t.Error("typo category accepted")
	}
	if _, err := (Guardrails{MaxPerDayMinorUnits: PlatformMaxPerDayMinorUnits + 1}).Normalize(); err == nil {
		t.Error("daily limit above platform ceiling accepted")
	}
}
