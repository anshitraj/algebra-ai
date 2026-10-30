package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
	"github.com/project-algebra/algebra/providers/keyed"
)

// A person's TinyFish key, held only in this test process's environment
// (KEYED_LIVE_TINYFISH_KEY) and never saved: envKeys stands in for
// app.ToolKeys, and everything else on the path is the real one (the router,
// the coordinator, the keyed rail and runner, Postgres) against the real
// TinyFish API.
//
//	KEYED_LIVE_TINYFISH_KEY=... DATABASE_URL=... go test ./internal/platform/postgres -run LiveTinyFish -v -count=1
//
// It makes one free Search and one free Fetch, and shows that an Agent run is
// held (over budget, then waiting for the person) without a request leaving for
// TinyFish. It never runs an Agent task, which costs credits: the HTTP client
// below refuses the Agent host, so a route that did reach it would fail here
// instead of being billed.
type envKeys struct {
	key   string
	tools *keyed.Tools
}

func (e envKeys) Key(_ context.Context, _, vendor string) (string, error) {
	if vendor != keyed.TinyFish {
		return "", shared.ErrNotFound
	}
	return e.key, nil
}

func (e envKeys) CandidatesFor(_ context.Context, _, capability string) []routing.Candidate {
	var out []routing.Candidate
	for _, c := range e.tools.Candidates(keyed.TinyFish) {
		if c.Capability == capability {
			out = append(out, c)
		}
	}
	return out
}

// guardedHTTP counts requests by host and refuses TinyFish's Agent host.
type guardedHTTP struct {
	inner keyed.HTTPDoer
	mu    sync.Mutex
	n     map[string]int
}

const agentHost = "agent.tinyfish.ai"

func (h *guardedHTTP) Do(req *http.Request) (*safehttp.Response, error) {
	h.mu.Lock()
	h.n[req.URL.Host]++
	h.mu.Unlock()
	if req.URL.Host == agentHost {
		return nil, errors.New("this live test never runs an Agent task: it costs credits")
	}
	return h.inner.Do(req)
}

func (h *guardedHTTP) count(host string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n[host]
}

func TestKeyed_LiveTinyFishThroughAlgebra(t *testing.T) {
	key := os.Getenv("KEYED_LIVE_TINYFISH_KEY")
	if key == "" {
		t.Skip("KEYED_LIVE_TINYFISH_KEY not set")
	}
	f := newEconFixture(t, 1, 10_000_000)
	ctx := context.Background()
	tools := keyed.NewTools(nil)
	keys := envKeys{key: key, tools: tools}
	f.svc.RegisterRail(keyed.NewRail())
	exec := app.NewExecutionService(f.svc, NewExecutionRepo(f.db))
	doer := &guardedHTTP{inner: safehttp.New(safehttp.Options{Timeout: 120 * time.Second, MaxBody: 4 << 20}), n: map[string]int{}}
	exec.RegisterRunner(keyed.NewRunner(keyed.Config{HTTP: doer, Keys: keys, Passes: NewSpendPassRepo(f.db), Tools: tools}))
	resolver := app.CandidateResolver{Keyed: keys}

	do := func(capability, input, window string, budget int64) (*app.DoResult, error) {
		cands, _ := resolver.ResolveFor(ctx, f.userID, capability, nil, nil)
		if len(cands) != 1 || cands[0].Provider != "keyed:tinyfish" {
			t.Fatalf("the person's key brings TinyFish's %s and nothing else: %+v", capability, cands)
		}
		s := econ.Spec{Capability: capability, Input: json.RawMessage(input), Window: window, Currency: "USDC", BudgetMaxMinor: budget}
		s.Constraints.AllowedNetworks = []string{chain.SolanaDevnet} // the Testnet environment: keyed is no chain
		return exec.Do(ctx, app.DoRequest{AgentID: f.agents[0], Spec: s, Candidates: cands})
	}

	// An Agent run is quoted at about twenty steps (0.30 USDC). Asked with a
	// ceiling below that, it is refused as over budget. Asked with a higher one
	// it is more than a pass lets a provider it has never paid take, so it waits
	// for the person. This runs first: once any call has cost money, TinyFish
	// is a known provider and the second case no longer applies.
	const agentInput = `{"url":"https://example.com","goal":"Report the page's main heading"}`
	res, err := do("web.agent", agentInput, "live-agent-low", 50_000)
	var nr *app.NoRoute
	if !errors.As(err, &nr) || len(nr.Rejected) != 1 || nr.Rejected[0].Code != routing.RejectOverBudget {
		t.Fatalf("an agent run with a ceiling of 0.05 is over budget: %v %+v", err, res)
	}
	t.Logf("agent run, ceiling 0.05: left out, %s: %s", nr.Rejected[0].Code, nr.Rejected[0].Detail)
	res, err = do("web.agent", agentInput, "live-agent-high", 400_000)
	var rej *app.ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != app.RejectApproval || rej.State != econ.StateAwaitingApproval {
		t.Fatalf("the first agent run waits for the person: %v %+v", err, res)
	}
	t.Logf("agent run, ceiling 0.40: held for the person (%s, intent %s), reasons %v", rej.Reason, rej.State, rej.ReasonCodes)
	if n := doer.count(agentHost); n != 0 {
		t.Fatalf("a held agent run reached TinyFish %d times", n)
	}

	for _, c := range []struct{ capability, input, window, host string }{
		{"web.search", `{"query":"solana x402 agent payments"}`, "live-search", "api.search.tinyfish.ai"},
		{"web.scrape", `{"url":"https://example.com"}`, "live-fetch", "api.fetch.tinyfish.ai"},
	} {
		res, err := do(c.capability, c.input, c.window, 50_000)
		if err != nil || res == nil || !res.Report.Delivered {
			t.Fatalf("%s was not delivered: %v %+v", c.capability, err, res)
		}
		in := res.Report.Intent
		var rsv *econ.Reservation
		for i := range in.Reservations {
			if in.Reservations[i].State == econ.ReservationCommitted {
				rsv = &in.Reservations[i]
			}
		}
		if in.State != econ.StateCommitted || in.CommittedMinor != 0 || rsv == nil || rsv.Rail != "keyed" || rsv.ProviderID != "keyed:tinyfish" ||
			rsv.Evidence.Network != chain.Keyed || rsv.HoldMinor != 0 || !strings.HasPrefix(rsv.Evidence.Transaction, "billed:") {
			t.Fatalf("%s should settle on the keyed rail at 0: state %s committed %d, attempt %+v", c.capability, in.State, in.CommittedMinor, rsv)
		}
		if n := doer.count(c.host); n != 1 {
			t.Errorf("%s: %d requests reached %s", c.capability, n, c.host)
		}
		fin := res.Report.Final()
		if strings.Contains(string(fin.Body), key) {
			t.Fatal("the key must never come back in an answer")
		}
		t.Logf("%s: delivered by %s on the %q rail, committed %d, %d bytes back, %dms", c.capability, rsv.ProviderID, rsv.Rail, in.CommittedMinor, len(fin.Body), fin.Result.LatencyMS)
	}

	// Both are in the agent's money as accepted answers that cost nothing.
	pass, err := NewSpendPassRepo(f.db).GetByAgent(ctx, f.agents[0])
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.repo.PassPnL(ctx, pass.ID, time.Now().Add(-time.Hour))
	if err != nil || m.Accepted != 2 || m.PaidMinor != 0 {
		t.Fatalf("P&L: two accepted answers, nothing paid: %+v %v", m, err)
	}
	if n := doer.count(agentHost); n != 0 {
		t.Fatalf("the agent host was reached %d times", n)
	}
}
