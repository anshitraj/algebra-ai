package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/providers/keyed"
)

// A web tool called with the person's own key, end to end against real
// Postgres: the key is found for the pass's owner, the vendor is called with
// it, the call settles for the cost the vendor reported, it shows in the
// agent's P&L, and a call made after the key is removed is refused before
// anything is recorded as spent.
func TestKeyed_EndToEndWithTheOwnersKey(t *testing.T) {
	f := newEconFixture(t, 1, 10_000_000)
	ctx := context.Background()
	const secret = "exa-live-0123456789abcd"

	var gotKey string
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"requestId":"req_77","results":[{"title":"Agents","url":"https://a.example","text":"about agents"}],"costDollars":{"total":0.007}}`))
	}))
	t.Cleanup(vendor.Close)

	tools := keyed.NewTools(map[string]string{"exa/web.search": vendor.URL})
	enc, err := privacy.NewAESGCMEncryptor(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	var vendors []app.ToolVendor
	for _, v := range tools.Vendors() {
		vendors = append(vendors, app.ToolVendor{ID: v.ID, Name: v.Name})
	}
	keys := app.NewToolKeys(NewToolKeyRepo(f.db), enc, vendors, tools.Candidates, nil)
	f.svc.RegisterRail(keyed.NewRail())
	exec := app.NewExecutionService(f.svc, NewExecutionRepo(f.db))
	exec.RegisterRunner(keyed.NewRunner(keyed.Config{
		HTTP: safehttp.New(safehttp.Options{AllowLoopback: true, Timeout: 5 * time.Second}),
		Keys: keys, Passes: NewSpendPassRepo(f.db), Tools: tools,
	}))
	resolver := app.CandidateResolver{Keyed: keys}
	spec := func(window string) econ.Spec {
		s := econ.Spec{Capability: "web.search", Input: json.RawMessage(`{"query":"agents"}`), Window: window, Currency: "USDC", BudgetMaxMinor: 50_000}
		s.Constraints.AllowedNetworks = []string{chain.SolanaDevnet} // the Testnet environment: keyed is no chain
		return s
	}

	// No key saved: the person has no keyed tools to route to.
	if cands, _ := resolver.ResolveFor(ctx, f.userID, "web.search", nil, nil); len(cands) != 0 {
		t.Fatalf("no key, no tools: %+v", cands)
	}
	if err := keys.Save(ctx, f.userID, "exa", secret); err != nil {
		t.Fatal(err)
	}
	cands, _ := resolver.ResolveFor(ctx, f.userID, "web.search", nil, nil)
	if len(cands) != 1 || cands[0].Provider != "keyed:exa" {
		t.Fatalf("the saved key brings Exa's search: %+v", cands)
	}

	res, err := exec.Do(ctx, app.DoRequest{AgentID: f.agents[0], Spec: spec("w1"), Candidates: cands})
	if err != nil || !res.Report.Delivered {
		t.Fatalf("delivered: %+v %v", res, err)
	}
	if gotKey != secret {
		t.Errorf("the vendor was called with the person's key: %q", gotKey)
	}
	fin := res.Report.Final()
	if fin == nil {
		t.Fatal("no attempt was reported")
	}
	in := res.Report.Intent
	if in.State != econ.StateCommitted || in.CommittedMinor != 7_000 || in.Fulfillment != econ.FulfillmentFulfilled {
		t.Fatalf("committed for what Exa reported, 0.007 USDC: state %s committed %d %s", in.State, in.CommittedMinor, in.Fulfillment)
	}
	var committed *econ.Reservation
	for i := range in.Reservations {
		if in.Reservations[i].State == econ.ReservationCommitted {
			committed = &in.Reservations[i]
		}
	}
	if committed == nil || committed.Rail != "keyed" || committed.Evidence.Network != chain.Keyed || committed.HoldMinor != 7_000 || !strings.HasPrefix(committed.Evidence.Transaction, "billed:req_77") {
		t.Fatalf("evidence: %+v", committed)
	}
	if !strings.Contains(string(fin.Body), "about agents") {
		t.Errorf("the normalized answer comes back: %s", fin.Body)
	}

	// It is in the agent's money, like any payment.
	pass, err := NewSpendPassRepo(f.db).GetByAgent(ctx, f.agents[0])
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.repo.PassPnL(ctx, pass.ID, time.Now().Add(-time.Hour))
	if err != nil || m.PaidMinor != 7_000 || m.Accepted != 1 {
		t.Fatalf("P&L: %+v %v", m, err)
	}

	// The key is removed after the tools were resolved: the call is refused
	// before any authority is released, and nothing is spent.
	if err := keys.Remove(ctx, f.userID, "exa"); err != nil {
		t.Fatal(err)
	}
	gotKey = ""
	res, err = exec.Do(ctx, app.DoRequest{AgentID: f.agents[0], Spec: spec("w2"), Candidates: cands})
	if err == nil && res.Report != nil && res.Report.Delivered {
		t.Fatalf("no key, no call: %+v", res.Report)
	}
	if gotKey != "" {
		t.Error("the vendor was reached without a key")
	}
	if m, _ = f.repo.PassPnL(ctx, pass.ID, time.Now().Add(-time.Hour)); m.PaidMinor != 7_000 {
		t.Errorf("nothing more was spent: %+v", m)
	}
}
