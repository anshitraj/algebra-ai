package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type fakeFinder struct {
	found []WebFound
	err   error
	wants []string
}

func (f *fakeFinder) Find(_ context.Context, want string, _ int) ([]WebFound, error) {
	f.wants = append(f.wants, want)
	return f.found, f.err
}

type fakeLimiter struct {
	allow bool
	keys  []string
}

func (l *fakeLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, time.Duration, error) {
	l.keys = append(l.keys, key)
	return l.allow, 20 * time.Minute, nil
}

func webRig(t *testing.T, found ...WebFound) (*execRig, *fakeFinder) {
	t.Helper()
	rig := newExecRig(t)
	f := &fakeFinder{found: found}
	rig.exec.SetWebFinder(f, nil)
	return rig, f
}

func TestWebDiscovery_ProbesEachFindForFreeAndSaysWhichOnesAnswerLikeX402(t *testing.T) {
	rig, finder := webRig(t,
		WebFound{Name: "Pricey", URL: "https://pricey.example.com/risk", Method: "POST", Description: "dear"},
		WebFound{Name: "Cheap", URL: "https://cheap.example.com/risk", Method: "POST"},
		WebFound{Name: "Dead", URL: "https://dead.example.com/risk", Method: "GET"},
	)
	rig.runner.costs["pricey.example.com"] = 9_000
	rig.runner.costs["cheap.example.com"] = 1_000
	rig.runner.quoteErrs["dead.example.com"] = errors.New("x402: dead.example.com answered 404 to an unpaid request")

	got, err := rig.exec.DiscoverWeb(context.Background(), WebDiscoveryRequest{AgentID: "agent_1", Capability: "solana.token-risk"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Endpoints) != 3 {
		t.Fatalf("endpoints: %+v", got.Endpoints)
	}
	// Usable first, cheapest first; what didn't answer last, with its reason.
	order := []string{got.Endpoints[0].Name, got.Endpoints[1].Name, got.Endpoints[2].Name}
	if strings.Join(order, ",") != "Cheap,Pricey,Dead" {
		t.Errorf("order: %v", order)
	}
	cheap, dead := got.Endpoints[0], got.Endpoints[2]
	if !cheap.Verified || cheap.PriceMinor != 1_000 || cheap.Network != "sandbox" {
		t.Errorf("cheap: %+v", cheap)
	}
	if cheap.Candidate.Provider != "cheap.example.com" || cheap.Candidate.Endpoint != "https://cheap.example.com/risk" || cheap.Candidate.Method != "POST" || cheap.Candidate.Network != "sandbox" {
		t.Errorf("what to pass to execute: %+v", cheap.Candidate)
	}
	if dead.Verified || !strings.Contains(dead.Reason, "404") {
		t.Errorf("dead: %+v", dead)
	}
	if !strings.Contains(got.Wanted, "Token risk check") || len(finder.wants) != 1 {
		t.Errorf("a class is searched for by what it is: %q (%d searches)", got.Wanted, len(finder.wants))
	}
}

func TestWebDiscovery_ProbesWithTheClassSampleNeverTheAgentsInput(t *testing.T) {
	rig, _ := webRig(t, WebFound{Name: "Alpha", URL: "https://alpha.example.com/risk", Method: "POST"})
	class, _ := routing.ClassByID("solana.token-risk")

	if _, err := rig.exec.DiscoverWeb(context.Background(), WebDiscoveryRequest{AgentID: "agent_1", Capability: "solana.token-risk"}); err != nil {
		t.Fatal(err)
	}
	rig.runner.mu.Lock()
	sent := rig.runner.quoteInputs["alpha.example.com"]
	rig.runner.mu.Unlock()
	if string(sent) != string(class.Sample) {
		t.Errorf("an endpoint nobody chose learns the sample, nothing of what the agent wants: %s", sent)
	}

	// Without a class there is no sample, and the probe carries nothing.
	rig, _ = webRig(t, WebFound{Name: "Alpha", URL: "https://alpha.example.com/risk", Method: "POST"})
	if _, err := rig.exec.DiscoverWeb(context.Background(), WebDiscoveryRequest{AgentID: "agent_1", Query: "a service that screens wallets for sanctions"}); err != nil {
		t.Fatal(err)
	}
	rig.runner.mu.Lock()
	sent = rig.runner.quoteInputs["alpha.example.com"]
	rig.runner.mu.Unlock()
	if string(sent) != "{}" {
		t.Errorf("an empty probe: %s", sent)
	}
}

func TestWebDiscovery_NeedsToKnowWhatToLookFor(t *testing.T) {
	rig, finder := webRig(t)
	for name, req := range map[string]WebDiscoveryRequest{
		"nothing":                        {AgentID: "agent_1"},
		"a provider's own capability":    {AgentID: "agent_1", Capability: "birdeye.data.get.x402-defi-price"},
		"a capability that isn't a name": {AgentID: "agent_1", Capability: "Not A Name!"},
	} {
		if _, err := rig.exec.DiscoverWeb(context.Background(), req); !errors.Is(err, shared.ErrConflict) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(finder.wants) != 0 {
		t.Errorf("nothing was searched for: %v", finder.wants)
	}
	// In words it is enough.
	if _, err := rig.exec.DiscoverWeb(context.Background(), WebDiscoveryRequest{AgentID: "agent_1", Capability: "birdeye.data.get.x402-defi-price", Query: "  current   price of a\nSolana token "}); err != nil {
		t.Fatal(err)
	}
	if len(finder.wants) != 1 || finder.wants[0] != "current price of a Solana token" {
		t.Errorf("the query is tidied: %q", finder.wants)
	}
}

func TestWebDiscovery_IsOffWithoutAFinderAndForAnAgentWithoutAPass(t *testing.T) {
	rig := newExecRig(t)
	if rig.exec.WebDiscoveryEnabled() {
		t.Error("no finder, no web discovery")
	}
	if _, err := rig.exec.DiscoverWeb(context.Background(), WebDiscoveryRequest{AgentID: "agent_1", Query: "x"}); !errors.Is(err, shared.ErrNotImplemented) || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Errorf("off, and says why: %v", err)
	}

	rig, finder := webRig(t)
	if _, err := rig.exec.DiscoverWeb(context.Background(), WebDiscoveryRequest{AgentID: "agent_nobody", Query: "x"}); !errors.Is(err, shared.ErrUnauthorized) {
		t.Errorf("an agent with no Spend Pass can't spend the server's searches: %v", err)
	}
	if len(finder.wants) != 0 {
		t.Error("and nothing was searched")
	}
}

func TestWebDiscovery_EachAgentIsHeldToAFewSearchesAnHour(t *testing.T) {
	rig, finder := webRig(t)
	lim := &fakeLimiter{allow: false}
	rig.exec.SetWebFinder(finder, lim)

	_, err := rig.exec.DiscoverWeb(context.Background(), WebDiscoveryRequest{AgentID: "agent_1", Query: "x"})
	var rl *RateLimited
	if !errors.As(err, &rl) || rl.Retry != 20*time.Minute || !strings.Contains(err.Error(), "21 minutes") {
		t.Fatalf("held back, and told for how long: %v", err)
	}
	if len(lim.keys) != 1 || lim.keys[0] != "webdiscovery:agent_1" || len(finder.wants) != 0 {
		t.Errorf("counted per agent, before the model is asked: %v %v", lim.keys, finder.wants)
	}
	lim.allow = true
	if _, err := rig.exec.DiscoverWeb(context.Background(), WebDiscoveryRequest{AgentID: "agent_1", Query: "x"}); err != nil {
		t.Errorf("within the limit: %v", err)
	}
}

func TestWebDiscovery_ASearchThatFailsIsAnErrorNotAnEmptyAnswer(t *testing.T) {
	rig, finder := webRig(t)
	finder.err = errors.New("Gemini rate limit reached")
	if _, err := rig.exec.DiscoverWeb(context.Background(), WebDiscoveryRequest{AgentID: "agent_1", Query: "x"}); err == nil || !strings.Contains(err.Error(), "web search failed") {
		t.Errorf("%v", err)
	}
}

func TestWebDiscovery_FindsThatCantBeProbedAreReportedNotDropped(t *testing.T) {
	rig, _ := webRig(t, WebFound{Name: "Odd", URL: "https://odd.example.com/x", Method: "TRACE"})
	got, err := rig.exec.DiscoverWeb(context.Background(), WebDiscoveryRequest{AgentID: "agent_1", Query: "x"})
	if err != nil || len(got.Endpoints) != 1 || got.Endpoints[0].Verified || got.Endpoints[0].Reason == "" {
		t.Fatalf("%v %+v", err, got)
	}
}
