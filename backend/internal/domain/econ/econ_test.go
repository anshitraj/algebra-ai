package econ

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCanonicalizeIsDeterministic(t *testing.T) {
	a, err := Canonicalize(json.RawMessage(`{"token":"SOL", "window":{"b":2,"a":1.0}, "tags":["x","<y>"]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Canonicalize(json.RawMessage("{\n \"tags\":[\"x\",\"<y>\"],\"window\":{\"a\":1,\"b\":2.00},\"token\":\"SOL\"}"))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("same value, different canonical form:\n%s\n%s", a, b)
	}
	if want := `{"tags":["x","<y>"],"token":"SOL","window":{"a":1,"b":2}}`; string(a) != want {
		t.Errorf("canonical = %s, want %s", a, want)
	}
	if _, err := Canonicalize(json.RawMessage(`{"a":1} {"b":2}`)); err == nil {
		t.Error("two JSON values must be refused")
	}
	if c, _ := Canonicalize(nil); string(c) != "{}" {
		t.Errorf("empty input canonicalizes to {}, got %s", c)
	}
}

func TestSameOutcomeSameEffectKey(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	spec := Spec{Capability: "Solana.Token-Risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: "2026-09-29T10", Currency: "usdc", BudgetMaxMinor: 50_000}
	a, err := New("eint_a", "user_1", "pass_1", "agent_a", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	spec.Input = json.RawMessage(` { "mint" : "SOL" } `)
	b, err := New("eint_b", "user_1", "pass_1", "agent_b", spec, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if a.EffectKey != b.EffectKey {
		t.Fatalf("two agents asking for the same outcome must get the same effect key: %s vs %s", a.EffectKey, b.EffectKey)
	}
	if !strings.HasPrefix(a.EffectKey, "solana.token-risk:") || !strings.HasSuffix(a.EffectKey, ":2026-09-29T10:1") {
		t.Errorf("effect key shape: %s", a.EffectKey)
	}
	spec.Input = json.RawMessage(`{"mint":"JUP"}`)
	c, _ := New("eint_c", "user_1", "pass_1", "agent_c", spec, now)
	if c.EffectKey == a.EffectKey {
		t.Error("a different input is a different outcome")
	}
	if a.Currency != "USDC" || a.Quantity != 1 || a.State != StateOpen || a.Commitment != CommitmentNone {
		t.Errorf("defaults wrong: %+v", a)
	}
}

func TestIntentHashDetectsTampering(t *testing.T) {
	in, err := New("eint_x", "user_1", "pass_1", "agent_a", Spec{Capability: "company-report", Input: json.RawMessage(`{"company":"NVIDIA","period":"FY2026-Q2"}`), BudgetMaxMinor: 2_000_000}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if in.Hash() != in.IntentHash {
		t.Fatal("hash must be reproducible")
	}
	in.BudgetMaxMinor = 9_000_000
	if in.Hash() == in.IntentHash {
		t.Error("changing the budget must change the intent hash")
	}
}

func TestSpecValidation(t *testing.T) {
	now := time.Now()
	bad := []Spec{
		{Capability: "x", BudgetMaxMinor: 1},
		{Capability: "Has Spaces", BudgetMaxMinor: 1},
		{Capability: "ok.cap", BudgetMaxMinor: 0},
		{Capability: "ok.cap", BudgetMaxMinor: 1, Window: "no spaces allowed"},
		{Capability: "ok.cap", BudgetMaxMinor: 1, TTL: 30 * 24 * time.Hour},
		{Capability: "ok.cap", BudgetMaxMinor: 1, Input: json.RawMessage(`not json`)},
	}
	for i, s := range bad {
		if _, err := New("id", "u", "p", "a", s, now); err == nil {
			t.Errorf("spec %d should be refused: %+v", i, s)
		}
	}
}

func TestUnknownIsNeverFailedAndNeverSilentlyRetried(t *testing.T) {
	// EXECUTING may not go back to OPEN except through proof (handled by the
	// service); UNKNOWN can only go to RECONCILING, and RECONCILING only to
	// proof: COMMITTED or OPEN.
	if CanTransition(StateUnknown, StateOpen) || CanTransition(StateUnknown, StateCommitted) {
		t.Error("UNKNOWN must pass through RECONCILING")
	}
	if !StateExecuting.Blocked() || !StateUnknown.Blocked() || !StateReconciling.Blocked() {
		t.Error("an attempt that may have committed blocks new commitments")
	}
	if CanTransition(StateCommitted, StateOpen) || !StateCommitted.Terminal() {
		t.Error("a committed intent never reopens")
	}
	if CanTransition(StateExecuting, StateCancelled) || CanTransition(StateUnknown, StateExpired) {
		t.Error("expiry or cancellation must not paper over money that may be moving")
	}
	if CanReservationTransition(ReservationUnknown, ReservationReleased) {
		t.Error("an UNKNOWN reservation is released only after reconciliation")
	}
	r := &Reservation{State: ReservationExecuting}
	if err := r.Transition(ReservationUnknown, time.Now()); err != nil || r.FinishedAt != nil {
		t.Errorf("UNKNOWN is still live: %v %+v", err, r)
	}
	if err := r.Transition(ReservationCommitted, time.Now()); err == nil {
		t.Error("UNKNOWN → COMMITTED must go through RECONCILING")
	}
}
