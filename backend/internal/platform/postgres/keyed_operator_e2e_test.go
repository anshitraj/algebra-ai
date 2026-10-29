package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/providers/keyed"
)

type repoUsers struct{ r *AccountRepo }

func (u repoUsers) UserByEmail(ctx context.Context, email string) (*account.User, error) {
	return u.r.GetUserByEmail(ctx, email)
}

func keyedExec(f *econFixture, keys *app.ToolKeys, tools *keyed.Tools) *app.ExecutionService {
	f.svc.RegisterRail(keyed.NewRail())
	exec := app.NewExecutionService(f.svc, NewExecutionRepo(f.db))
	exec.RegisterRunner(keyed.NewRunner(keyed.Config{
		HTTP: safehttp.New(safehttp.Options{AllowLoopback: true, Timeout: 5 * time.Second}),
		Keys: keys, Passes: NewSpendPassRepo(f.db), Tools: tools,
	}))
	return exec
}

// The operator's key from the environment, end to end against real Postgres:
// an account at the operator's address (verified, no password) is offered the
// vendor's tools and the vendor is called with the operator's key; anyone else
// gets nothing, and an agent that names the operator's candidate anyway is
// refused before the vendor is reached.
func TestKeyed_TheOperatorsKeyIsForTheOperatorsAccountsOnly(t *testing.T) {
	op := newEconFixture(t, 1, 10_000_000)
	stranger := newEconFixture(t, 1, 10_000_000)
	ctx := context.Background()
	const secret = "exa-operator-0123456789abcd"

	var mu sync.Mutex
	var seen []string
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("x-api-key"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"requestId":"req_op","results":[{"title":"Agents","url":"https://a.example","text":"about agents"}],"costDollars":{"total":0.007}}`))
	}))
	t.Cleanup(vendor.Close)
	requests := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}

	tools := keyed.NewTools(map[string]string{"exa/web.search": vendor.URL})
	enc, err := privacy.NewAESGCMEncryptor(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	var vendors []app.ToolVendor
	for _, v := range tools.Vendors() {
		vendors = append(vendors, app.ToolVendor{ID: v.ID, Name: v.Name})
	}
	keys := app.NewToolKeys(NewToolKeyRepo(op.db), enc, vendors, tools.Candidates, nil)

	accounts := NewAccountRepo(op.db)
	me, err := accounts.GetUser(ctx, op.userID)
	if err != nil {
		t.Fatal(err)
	}
	// The address is the operator's, and the person has proved it.
	if err := accounts.MarkEmailVerified(ctx, op.userID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := keys.SetOperatorKeys(map[string]string{"exa": secret}, app.NewOperatorEmails([]string{me.Email}, repoUsers{accounts})); err != nil {
		t.Fatal(err)
	}

	execOp, execStranger := keyedExec(op, keys, tools), keyedExec(stranger, keys, tools)
	resolver := app.CandidateResolver{Keyed: keys}
	spec := func(window string) econ.Spec {
		s := econ.Spec{Capability: "web.search", Input: json.RawMessage(`{"query":"agents"}`), Window: window, Currency: "USDC", BudgetMaxMinor: 50_000}
		s.Constraints.AllowedNetworks = []string{chain.SolanaDevnet}
		return s
	}

	if cands, _ := resolver.ResolveFor(ctx, stranger.userID, "web.search", nil, nil); len(cands) != 0 {
		t.Fatalf("a stranger is offered the operator's tools: %+v", cands)
	}
	list, err := keys.List(ctx, stranger.userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range list {
		if v.ServerKey || v.Saved {
			t.Fatalf("a stranger sees a server key: %+v", v)
		}
	}

	cands, _ := resolver.ResolveFor(ctx, op.userID, "web.search", nil, nil)
	if len(cands) != 1 || cands[0].Provider != "keyed:exa" {
		t.Fatalf("the operator's account is offered Exa's search: %+v", cands)
	}
	if list, _ = keys.List(ctx, op.userID); len(list) == 0 {
		t.Fatal("no listing")
	}
	for _, v := range list {
		if v.ID == "exa" && (!v.ServerKey || v.Saved) {
			t.Fatalf("exa is the server's key for the operator, nothing saved: %+v", v)
		}
	}

	res, err := execOp.Do(ctx, app.DoRequest{AgentID: op.agents[0], Spec: spec("w1"), Candidates: cands})
	if err != nil || !res.Report.Delivered {
		t.Fatalf("delivered: %+v %v", res, err)
	}
	if got := requests(); len(got) != 1 || got[0] != secret {
		t.Fatalf("the vendor was called once, with the operator's key: %q", got)
	}
	in := res.Report.Intent
	if in.State != econ.StateCommitted || in.CommittedMinor != 7_000 {
		t.Fatalf("committed for what Exa reported: state %s committed %d", in.State, in.CommittedMinor)
	}
	var committed *econ.Reservation
	for i := range in.Reservations {
		if in.Reservations[i].State == econ.ReservationCommitted {
			committed = &in.Reservations[i]
		}
	}
	if committed == nil || committed.Rail != "keyed" || committed.Evidence.Network != chain.Keyed || !strings.HasPrefix(committed.Evidence.Transaction, "billed:req_op") {
		t.Fatalf("evidence: %+v", committed)
	}
	if strings.Contains(string(res.Report.Final().Body), secret) {
		t.Fatal("the key must never come back in an answer")
	}

	// A stranger's agent that names the operator's candidate itself: the key is
	// looked up for the pass's owner, finds none, and nothing is called.
	res, err = execStranger.Do(ctx, app.DoRequest{AgentID: stranger.agents[0], Spec: spec("w2"), Candidates: cands})
	if err == nil && res != nil && res.Report != nil && res.Report.Delivered {
		t.Fatalf("a stranger used the operator's key: %+v", res.Report)
	}
	if got := requests(); len(got) != 1 {
		t.Fatalf("the vendor was reached for a stranger: %q", got)
	}
}
