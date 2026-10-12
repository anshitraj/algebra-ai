package intel

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 12, 7, 30, 0, 0, time.UTC)

func listing(price int64, payTo string) Listed {
	return Listed{Source: "fixture", ProviderID: "fixture:acme", ProviderName: "Acme", Capability: "acme.get.price",
		Method: "GET", URL: "https://acme.example/price", Network: "solana-devnet", PriceMinor: price, PayTo: payTo, Callable: true}
}

func kinds(cs []Change) map[ChangeKind]Change {
	out := map[ChangeKind]Change{}
	for _, c := range cs {
		out[c.Kind] = c
	}
	return out
}

func TestMerge_NewEndpointIsUnverifiedWithItsPayeePinned(t *testing.T) {
	save, changes := Merge(nil, []Listed{listing(1000, "PAYEE_A")}, t0)
	if len(save) != 1 || len(changes) != 1 || changes[0].Kind != ChangeAdded {
		t.Fatalf("save=%v changes=%v", save, changes)
	}
	e := save[0]
	if e.Trust != TrustUnverified || e.PinnedPayTo != "PAYEE_A" || e.Status != StatusActive || !e.FirstSeenAt.Equal(t0) {
		t.Fatalf("new endpoint: %+v", e)
	}
	if l := e.Levels(); !l.Listed || l.Quoted || l.Tested || l.Verified || !l.Executable {
		t.Fatalf("a listing is listed and executable, nothing more: %+v", l)
	}
}

func TestMerge_ListingPayeeChangeBlocks(t *testing.T) {
	prev, _ := Merge(nil, []Listed{listing(1000, "PAYEE_A")}, t0)
	save, changes := Merge(prev, []Listed{listing(1000, "ATTACKER")}, t0.Add(6*time.Hour))
	k := kinds(changes)
	if c, ok := k[ChangePayeeChanged]; !ok || !c.Suspicious {
		t.Fatalf("payee change must be flagged suspicious: %v", changes)
	}
	if _, ok := k[ChangeTrust]; !ok {
		t.Fatalf("trust change must be recorded: %v", changes)
	}
	e := save[0]
	if e.Trust != TrustBlocked || e.TrustReason != ReasonPayeeChanged || e.PinnedPayTo != "PAYEE_A" || e.PayTo != "ATTACKER" {
		t.Fatalf("blocked with the old payee still pinned: %+v", e)
	}
	if code, _ := e.Check("ATTACKER"); code != ReasonBlocked {
		t.Fatalf("a blocked endpoint is never paid: %q", code)
	}
	if e.Levels().Executable {
		t.Fatal("a blocked endpoint is not executable")
	}
}

func TestMerge_PriceRemovalAndRestore(t *testing.T) {
	prev, _ := Merge(nil, []Listed{listing(1000, "PAYEE_A")}, t0)
	save, changes := Merge(prev, []Listed{listing(5000, "PAYEE_A")}, t0.Add(time.Hour))
	if c := kinds(changes)[ChangePriceChanged]; c.Kind == "" || !c.Suspicious {
		t.Fatalf("a fivefold price rise is suspicious: %v", changes)
	}
	if !save[0].LastChangedAt.Equal(t0.Add(time.Hour)) || save[0].ListedPriceMinor != 5000 {
		t.Fatalf("price saved: %+v", save[0])
	}
	// The catalog answers without it: removed.
	save, changes = Merge(save, nil, t0.Add(2*time.Hour))
	if len(save) != 1 || save[0].Status != StatusRemoved || kinds(changes)[ChangeRemoved].Kind == "" {
		t.Fatalf("removed: %v %v", save, changes)
	}
	if save[0].Levels().Listed {
		t.Fatal("a removed endpoint is not listed")
	}
	// Removing again records nothing new.
	if _, again := Merge(save, nil, t0.Add(3*time.Hour)); len(again) != 0 {
		t.Fatalf("already removed: %v", again)
	}
	save, changes = Merge(save, []Listed{listing(5000, "PAYEE_A")}, t0.Add(4*time.Hour))
	if save[0].Status != StatusActive || kinds(changes)[ChangeRestored].Kind == "" {
		t.Fatalf("restored: %v %v", save, changes)
	}
}

func TestMerge_UnchangedListingRecordsNoChange(t *testing.T) {
	prev, _ := Merge(nil, []Listed{listing(1000, "PAYEE_A"), listing(1000, "PAYEE_A")}, t0)
	if len(prev) != 1 {
		t.Fatalf("duplicates in one listing are one endpoint: %d", len(prev))
	}
	save, changes := Merge(prev, []Listed{listing(1000, "PAYEE_A")}, t0.Add(6*time.Hour))
	if len(changes) != 0 || !save[0].LastChangedAt.Equal(t0) || !save[0].LastSeenAt.Equal(t0.Add(6*time.Hour)) {
		t.Fatalf("unchanged: %v %+v", changes, save[0])
	}
}

func TestEndpoint_DeliveryVerifiesOnlyAtThePinnedPayee(t *testing.T) {
	save, _ := Merge(nil, []Listed{listing(1000, "")}, t0)
	e := save[0]
	// Nothing listed a payee: the first paid call pins it and verifies.
	changes := e.Delivered("PAYEE_A", 120, t0)
	if e.PinnedPayTo != "PAYEE_A" || e.Trust != TrustVerified || len(changes) != 2 || e.LatencyMS != 120 {
		t.Fatalf("first delivery: %+v %v", e, changes)
	}
	if l := e.Levels(); !l.Tested || !l.Verified {
		t.Fatalf("levels: %+v", l)
	}
	// A quote to another payee is refused, and the refusal blocks it.
	if code, _ := e.Check("ATTACKER"); code != ReasonPayeeChanged {
		t.Fatalf("payee check: %q", code)
	}
	e.Flag("ATTACKER", t0.Add(time.Minute))
	if e.Trust != TrustBlocked || e.VerifiedAt != nil {
		t.Fatalf("flagged: %+v", e)
	}
	// A person unblocks it: the new payee is accepted and pinned.
	c, err := e.SetTrust(TrustUnverified, "rotated wallet, confirmed with the provider", "user_1", t0.Add(time.Hour))
	if err != nil || e.PinnedPayTo != "ATTACKER" || c.Actor != "user_1" || c.After["note"] == nil {
		t.Fatalf("unblock: %v %+v %+v", err, e, c)
	}
	if _, err := e.SetTrust("trusted", "", "user_1", t0); err == nil {
		t.Fatal("unknown trust state accepted")
	}
}

func TestCheck_PayeesThatAbbreviateAlikeAreShownInFull(t *testing.T) {
	save, _ := Merge(nil, []Listed{listing(1000, "sandbox-provider-shifty")}, t0)
	_, detail := save[0].Check("sandbox-attacker-shifty")
	if !strings.Contains(detail, "sandbox-attacker-shifty") || !strings.Contains(detail, "sandbox-provider-shifty") {
		t.Fatalf("detail: %q", detail)
	}
	save, _ = Merge(nil, []Listed{listing(1000, "7xKXtg2CW87d97TXJSDpbD5jBkheTqA83TZRuJosgAsU")}, t0)
	if _, detail := save[0].Check("9kEVg2noE8uubD6e1jMtEwumULEyNLgr7uYCXDVL4CLW"); !strings.Contains(detail, "9kEV…4CLW") || !strings.Contains(detail, "7xKX…gAsU") {
		t.Fatalf("detail: %q", detail)
	}
}

func TestEndpoint_UnreachableAndBack(t *testing.T) {
	save, _ := Merge(nil, []Listed{listing(1000, "PAYEE_A")}, t0)
	e := save[0]
	if len(e.Unreachable("dial tcp: timeout", t0)) != 1 || e.Status != StatusUnreachable || e.Levels().Executable {
		t.Fatalf("unreachable: %+v", e)
	}
	if len(e.Unreachable("again", t0)) != 0 {
		t.Fatal("unreachable twice records once")
	}
	if len(e.Quoted(t0.Add(time.Minute))) != 1 || e.Status != StatusActive || !e.Levels().Quoted {
		t.Fatalf("quoted again: %+v", e)
	}
}

func TestNextRun_SixHourSlotsUTC(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+1800)
	for _, c := range []struct{ at, want time.Time }{
		{time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)},
		{time.Date(2026, 10, 12, 5, 59, 59, 0, time.UTC), time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)},
		{time.Date(2026, 10, 12, 13, 1, 0, 0, time.UTC), time.Date(2026, 10, 12, 18, 0, 0, 0, time.UTC)},
		{time.Date(2026, 10, 12, 18, 0, 1, 0, time.UTC), time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)},
		{time.Date(2026, 10, 13, 2, 0, 0, 0, ist), time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)},
	} {
		if got := NextRun(c.at); !got.Equal(c.want) {
			t.Errorf("NextRun(%v) = %v, want %v", c.at, got, c.want)
		}
	}
}

func TestID_StableAndDistinct(t *testing.T) {
	a := ID("Paysh:Acme", "https://a.example/x", "solana")
	if a != ID("paysh:acme", "https://a.example/x", "SOLANA") {
		t.Fatal("provider and network are compared without case")
	}
	if a == ID("paysh:acme", "https://a.example/x", "solana-devnet") || a == ID("paysh:acme", "https://a.example/y", "solana") {
		t.Fatal("network and URL distinguish endpoints")
	}
}
