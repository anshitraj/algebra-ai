package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type memToolKeys struct {
	mu   sync.Mutex
	rows map[string]ToolKeyRecord
}

func newMemToolKeys() *memToolKeys { return &memToolKeys{rows: map[string]ToolKeyRecord{}} }

func (m *memToolKeys) PutToolKey(_ context.Context, userID string, k ToolKeyRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[userID+"|"+k.Vendor] = k
	return nil
}

func (m *memToolKeys) ToolKey(_ context.Context, userID, vendor string) (*ToolKeyRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.rows[userID+"|"+vendor]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return &k, nil
}

func (m *memToolKeys) ToolKeys(_ context.Context, userID string) ([]ToolKeyRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ToolKeyRecord
	for id, k := range m.rows {
		if strings.HasPrefix(id, userID+"|") {
			k.Sealed, k.Nonce = nil, nil
			out = append(out, k)
		}
	}
	return out, nil
}

func (m *memToolKeys) DeleteToolKey(_ context.Context, userID, vendor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, userID+"|"+vendor)
	return nil
}

type modeOf map[string]account.Mode

func (m modeOf) UserMode(_ context.Context, id string) (account.Mode, error) { return m[id], nil }

func testToolKeys(t *testing.T, modes AccountModes) (*ToolKeys, *memToolKeys) {
	t.Helper()
	enc, err := privacy.NewAESGCMEncryptor(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := newMemToolKeys()
	vendors := []ToolVendor{{ID: "acme", Name: "Acme"}, {ID: "zed", Name: "Zed"}}
	offers := func(vendor string) []routing.Candidate {
		return []routing.Candidate{{ID: "cand_" + vendor, Capability: "web.search", Provider: "keyed:" + vendor}}
	}
	return NewToolKeys(store, enc, vendors, offers, modes), store
}

func TestToolKeys_SavedSealedAndNeverShown(t *testing.T) {
	ctx := context.Background()
	keys, store := testToolKeys(t, nil)
	const secret = "sk-live-0123456789abcdef"
	if err := keys.Save(ctx, "user_1", "acme", secret); err != nil {
		t.Fatal(err)
	}
	row := store.rows["user_1|acme"]
	if bytes.Contains(row.Sealed, []byte(secret)) || bytes.Contains(row.Sealed, []byte("0123456789")) {
		t.Fatal("the key is stored sealed, not in the clear")
	}
	if got, err := keys.Key(ctx, "user_1", "acme"); err != nil || got != secret {
		t.Fatalf("the owner's key opens: %q %v", got, err)
	}
	list, err := keys.List(ctx, "user_1")
	if err != nil || len(list) != 2 || !list[0].Saved || list[0].Hint != "cdef" || list[1].Saved {
		t.Fatalf("acme is saved, zed is not: %+v %v", list, err)
	}

	// A row copied to another account, or under another vendor, opens to nothing.
	store.rows["user_2|acme"] = row
	if _, err := keys.Key(ctx, "user_2", "acme"); err == nil {
		t.Error("a sealed key is bound to its owner")
	}
	store.rows["user_1|zed"] = row
	if _, err := keys.Key(ctx, "user_1", "zed"); err == nil {
		t.Error("a sealed key is bound to its vendor")
	}
	if _, err := keys.Key(ctx, "user_3", "acme"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("no key saved: %v", err)
	}

	if err := keys.Remove(ctx, "user_1", "acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Key(ctx, "user_1", "acme"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("removed: %v", err)
	}
}

func TestToolKeys_RefusesWhatIsNotAKey(t *testing.T) {
	ctx := context.Background()
	keys, _ := testToolKeys(t, modeOf{"demo": account.ModeDemo})
	for name, c := range map[string]struct{ vendor, key string }{
		"unknown vendor": {"nope", "sk-live-0123456789"},
		"too short":      {"acme", "abc"},
		"has a space":    {"acme", "sk live 0123456789"},
		"has a newline":  {"acme", "sk-live-01234\n56789"},
		"too long":       {"acme", strings.Repeat("k", 300)},
	} {
		if err := keys.Save(ctx, "user_1", c.vendor, c.key); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := keys.Save(ctx, "demo", "acme", "sk-live-0123456789"); !errors.Is(err, shared.ErrUnauthorized) {
		t.Errorf("a demo account cannot save keys: %v", err)
	}
}

func TestToolKeys_OnlyTheOwnersToolsAreOffered(t *testing.T) {
	ctx := context.Background()
	keys, _ := testToolKeys(t, nil)
	if err := keys.Save(ctx, "user_1", "acme", "sk-live-0123456789"); err != nil {
		t.Fatal(err)
	}
	if got := keys.CandidatesFor(ctx, "user_1", "web.search"); len(got) != 1 || got[0].Provider != "keyed:acme" {
		t.Fatalf("the owner has acme's tool: %+v", got)
	}
	if got := keys.CandidatesFor(ctx, "user_1", "web.scrape"); len(got) != 0 {
		t.Errorf("acme does no scraping: %+v", got)
	}
	if got := keys.CandidatesFor(ctx, "user_2", "web.search"); len(got) != 0 {
		t.Errorf("another account has none: %+v", got)
	}

	resolver := CandidateResolver{Configured: map[string][]routing.Candidate{
		"sellers": {{ID: "cand_paid", Capability: "web.search", Provider: "sellers"}},
	}, Keyed: keys}
	ids := func(cs []routing.Candidate) string {
		var s []string
		for _, c := range cs {
			s = append(s, c.ID)
		}
		return strings.Join(s, ",")
	}
	got, _ := resolver.ResolveFor(ctx, "user_1", "web.search", nil, nil)
	if ids(got) != "cand_acme,cand_paid" {
		t.Errorf("their own tool joins the field, first: %s", ids(got))
	}
	got, _ = resolver.ResolveFor(ctx, "user_2", "web.search", nil, nil)
	if ids(got) != "cand_paid" {
		t.Errorf("nobody else's key is used: %s", ids(got))
	}
	got, rej := resolver.ResolveFor(ctx, "user_1", "web.search", []string{"keyed:acme"}, nil)
	if ids(got) != "cand_acme" || len(rej) != 0 {
		t.Errorf("named, it is the only one: %s %+v", ids(got), rej)
	}
	got, _ = resolver.ResolveFor(ctx, "user_1", "web.search", []string{"sellers"}, nil)
	if ids(got) != "cand_paid" {
		t.Errorf("naming another seller leaves the keyed tool out: %s", ids(got))
	}
	got, _ = resolver.ResolveFor(ctx, "user_1", "web.search", nil, []CandidateInput{{Endpoint: "https://found.example/x"}})
	for _, c := range got {
		if c.Provider == "keyed:acme" {
			t.Errorf("an endpoint the agent found itself does not bring the keyed tool along: %s", ids(got))
		}
	}
}

// A vendor-billed call goes through the same coordinator as a paid one: it
// settles for what the vendor charged, ignores the network limit (no chain is
// used), and a call that was not billed costs nothing.

type billedRail struct{}

func (billedRail) Name() string { return "keyed" }
func (billedRail) Authorize(_ context.Context, r *econ.Reservation, _ PaymentRequest) (*PaymentAuthority, error) {
	return &PaymentAuthority{Value: "keyed_" + r.ID, Evidence: econ.Evidence{
		Rail: "keyed", Protocol: "api-key", Scheme: "billed", Network: chain.Keyed, Asset: "USD", PaymentID: "keyed_" + r.ID, PayTo: "acme",
	}}, nil
}
func (billedRail) Settlement(_ context.Context, ev econ.Evidence) (Settlement, error) {
	if !strings.HasPrefix(ev.Transaction, "billed:") {
		return Settlement{Status: SettlementNotSettled, Detail: "no charge reported"}, nil
	}
	return Settlement{Status: SettlementSettled, AmountMinor: ev.AmountMinor, Transaction: ev.Transaction, Network: chain.Keyed, Asset: "USD", PayTo: ev.PayTo}, nil
}

type billedRunner struct {
	listed  int64
	charged int64
	fail    bool
}

func (billedRunner) Type() routing.ExecutionType { return routing.ExecHTTPAPI }
func (billedRunner) Rail(routing.Quote) (string, error) {
	return "keyed", nil
}
func (b billedRunner) Quote(_ context.Context, _ routing.Candidate, _ json.RawMessage) (routing.Quote, error) {
	return routing.Quote{Cost: routing.Cost{ProviderMinor: b.listed}, Asset: "USDC", Network: chain.Keyed, PayTo: "acme", Semantics: econ.SemanticsMeteredCapture}, nil
}
func (b billedRunner) Run(ctx context.Context, call StepCall) StepObservation {
	if _, err := call.Pay(ctx, PaymentRequest{}); err != nil {
		return StepObservation{Class: routing.FailPayment, Message: err.Error()}
	}
	if b.fail {
		return StepObservation{AuthorityReleased: true, Class: routing.FailProvider, Message: "the vendor refused the API key"}
	}
	return StepObservation{
		AuthorityReleased: true, Delivered: true, HTTPStatus: 200, ContentType: "application/json", Body: []byte(`{"query":"x","results":[]}`),
		RequestHash: "sha256:request", SettleTx: "billed:op1", ProviderOperationID: "op1", ChargedMinor: b.charged,
	}
}

func keyedRig(t *testing.T, r billedRunner) *execRig {
	t.Helper()
	rig := newExecRig(t)
	rig.svc.RegisterRail(billedRail{})
	rig.exec.RegisterRunner(r)
	return rig
}

func keyedCand() routing.Candidate {
	return routing.Candidate{
		Capability: "solana.token-risk", Provider: "keyed:acme", ExecutionType: routing.ExecHTTPAPI,
		Endpoint: "https://api.acme.example/risk", Method: "POST", PriceMinor: 20_000, Asset: "USDC", Network: chain.Keyed,
		Sources: []routing.DiscoverySource{routing.SourceConfigured},
	}
}

func doKeyed(t *testing.T, rig *execRig, window string) (*DoResult, error) {
	t.Helper()
	spec := doSpec(window)
	spec.Constraints.AllowedNetworks = []string{chain.SolanaDevnet} // the environment's chains: keyed is no chain
	return rig.exec.Do(context.Background(), DoRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{keyedCand()}})
}

func committedAttempt(t *testing.T, res *DoResult) *econ.Reservation {
	t.Helper()
	for i := range res.Report.Intent.Reservations {
		if r := &res.Report.Intent.Reservations[i]; r.State == econ.ReservationCommitted {
			return r
		}
	}
	t.Fatalf("no committed attempt: %+v", res.Report.Intent)
	return nil
}

func TestKeyed_ASettledCallCostsWhatTheVendorCharged(t *testing.T) {
	rig := keyedRig(t, billedRunner{listed: 20_000, charged: 7_000})
	res, err := doKeyed(t, rig, "w1")
	if err != nil || !res.Report.Delivered {
		t.Fatalf("delivered: %+v %v", res.Report, err)
	}
	if r := committedAttempt(t, res); r.HoldMinor != 7_000 || r.Evidence.AmountMinor != 7_000 || r.Evidence.Network != chain.Keyed || r.Evidence.Transaction != "billed:op1" {
		t.Fatalf("the attempt is committed for what the vendor charged, not what was listed: %+v", r)
	}
	if res.Report.Intent.CommittedMinor != 7_000 {
		t.Errorf("the intent committed %d", res.Report.Intent.CommittedMinor)
	}
}

func TestKeyed_AFreeCallCommitsNothingNotTheWholeCeiling(t *testing.T) {
	rig := keyedRig(t, billedRunner{listed: 1, charged: 0})
	res, err := doKeyed(t, rig, "w2")
	if err != nil || !res.Report.Delivered {
		t.Fatalf("delivered: %+v %v", res.Report, err)
	}
	if r := committedAttempt(t, res); r.HoldMinor != 0 || r.Evidence.AmountMinor != 0 {
		t.Fatalf("a free call counts as none of the pass's money: %+v", r)
	}
	if res.Report.Intent.CommittedMinor != 0 {
		t.Errorf("committed %d, the intent's ceiling was 50000", res.Report.Intent.CommittedMinor)
	}
}

func TestKeyed_ACallTheVendorRefusedCostsNothing(t *testing.T) {
	rig := keyedRig(t, billedRunner{listed: 20_000, fail: true})
	res, err := doKeyed(t, rig, "w3")
	if err == nil && res != nil && res.Report != nil && res.Report.Delivered {
		t.Fatalf("a refused key delivers nothing: %+v", res.Report)
	}
	if res != nil && res.Report != nil && res.Report.Intent != nil && res.Report.Intent.CommittedMinor != 0 {
		t.Errorf("nothing was billed, so nothing is committed: %d", res.Report.Intent.CommittedMinor)
	}
}
