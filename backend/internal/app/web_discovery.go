package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Web discovery is the third place Algebra looks for a provider, after the
// operator's own and the catalogs: the open web, through a model that can
// search it. It finds the long tail nobody has listed, and everything it finds
// is unverified. A model can invent a URL, or be steered by the page it read,
// so it is never trusted with anything but the address of an endpoint, and the
// address is then checked the only way that means anything: by asking the
// endpoint, with a free unpaid request, what it charges.
//
// It never moves money and never chooses for the agent. It returns endpoints,
// each marked verified (it answered like an x402 resource, in USDC on a network
// Algebra can pay) or not, and the agent passes the ones it wants to
// algebra.execute as `candidates`, where the Spend Pass decides, exactly as for
// an endpoint the agent found itself.

// WebFound is an endpoint the open web says does some work. Nothing about it is
// checked yet.
type WebFound struct {
	Name, URL, Method, Description string
}

// WebFinder searches the open web for endpoints that do the work in want.
type WebFinder interface {
	Find(ctx context.Context, want string, limit int) ([]WebFound, error)
}

const (
	// MaxWebFinds bounds how many endpoints one search returns and probes.
	MaxWebFinds = 8
	// webProbeTimeout is how long one endpoint has to say what it charges.
	webProbeTimeout = 10 * time.Second
	// webSearchesPerHour bounds one agent's web searches: each costs a model call.
	webSearchesPerHour = 10
	maxWebWant         = 300
)

// RateLimited is returned when an agent has used up what it may do in a
// period. Retry says how long until it may try again.
type RateLimited struct {
	What  string
	Retry time.Duration
}

func (e *RateLimited) Error() string {
	return fmt.Sprintf("too many %s: try again in %d minutes", e.What, int(e.Retry.Minutes())+1)
}

// WebDiscoveryRequest is what an agent asks the open web for.
type WebDiscoveryRequest struct {
	AgentID string
	// Capability is a class ("token.price") or a capability name; Query says in
	// words what is wanted. One of them is needed.
	Capability string
	Query      string
	Limit      int
}

// WebEndpoint is one find and what the free probe made of it.
type WebEndpoint struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`
	Method      string `json:"method"`
	// Verified: it answered an unpaid request with x402 terms Algebra can pay
	// (USDC on a Solana cluster it has a rail for). Not verified says why.
	Verified   bool   `json:"verified"`
	Reason     string `json:"not_verified_reason,omitempty"`
	PriceMinor int64  `json:"price_minor,omitempty"`
	Network    string `json:"network,omitempty"`
	// Candidate is what to pass in algebra.execute's `candidates` to use it.
	Candidate CandidateInput `json:"candidate"`
}

// WebDiscovery is the answer to a web search.
type WebDiscovery struct {
	Wanted    string        `json:"wanted"`
	Endpoints []WebEndpoint `json:"endpoints"`
}

// SetWebFinder turns web discovery on. limiter may be nil (no rate limit).
func (s *ExecutionService) SetWebFinder(f WebFinder, limiter RateLimiter) {
	s.web, s.webLimiter = f, limiter
}

// WebDiscoveryEnabled reports whether this server can search the web.
func (s *ExecutionService) WebDiscoveryEnabled() bool { return s.web != nil }

// DiscoverWeb searches the open web for endpoints that do the work asked for,
// then asks each one what it charges. The probe sends the class's sample input
// (or nothing), never the agent's real input: nobody the agent hasn't chosen
// learns what it wants to buy.
func (s *ExecutionService) DiscoverWeb(ctx context.Context, req WebDiscoveryRequest) (*WebDiscovery, error) {
	if s.web == nil {
		return nil, fmt.Errorf("%w: web discovery needs a Gemini key (GEMINI_API_KEY) on this server", shared.ErrNotImplemented)
	}
	if _, err := s.econ.executorPass(ctx, req.AgentID); err != nil {
		return nil, err
	}
	want, capability, sample, err := webWant(req)
	if err != nil {
		return nil, err
	}
	if s.webLimiter != nil {
		ok, retry, err := s.webLimiter.Allow(ctx, "webdiscovery:"+req.AgentID, webSearchesPerHour, time.Hour)
		if err == nil && !ok {
			return nil, &RateLimited{What: "web searches", Retry: retry}
		}
	}
	limit := req.Limit
	if limit <= 0 || limit > MaxWebFinds {
		limit = MaxWebFinds
	}
	found, err := s.web.Find(ctx, want, limit)
	if err != nil {
		s.log.Warn("execution: web discovery failed", "err", err)
		return nil, fmt.Errorf("the web search failed: %w", err)
	}
	if len(found) > limit {
		found = found[:limit]
	}

	out := &WebDiscovery{Wanted: want, Endpoints: make([]WebEndpoint, len(found))}
	var wg sync.WaitGroup
	for i, f := range found {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out.Endpoints[i] = s.probeWebFind(ctx, f, capability, sample)
		}()
	}
	wg.Wait()

	// What can be used first, cheapest first; the rest in the order found.
	slices.SortStableFunc(out.Endpoints, func(a, b WebEndpoint) int {
		switch {
		case a.Verified != b.Verified:
			if a.Verified {
				return -1
			}
			return 1
		case a.Verified && a.PriceMinor != b.PriceMinor:
			if a.PriceMinor < b.PriceMinor {
				return -1
			}
			return 1
		}
		return 0
	})
	s.log.Info("execution: web discovery", "agent", req.AgentID, "found", len(found), "verified", countVerified(out.Endpoints))
	return out, nil
}

func countVerified(es []WebEndpoint) int {
	n := 0
	for _, e := range es {
		if e.Verified {
			n++
		}
	}
	return n
}

// webWant words what to search for, and returns the capability and sample input
// to probe the finds with.
func webWant(req WebDiscoveryRequest) (want, capability string, sample json.RawMessage, err error) {
	capability = "web.discovered"
	sample = json.RawMessage(`{}`)
	query := clean(req.Query, maxWebWant)
	if c := strings.TrimSpace(req.Capability); c != "" {
		norm, err := econ.NormalizeCapability(c)
		if err != nil {
			return "", "", nil, fmt.Errorf("%w: %s", shared.ErrConflict, err)
		}
		if class, ok := routing.ClassByID(norm); ok {
			want = class.Title + ": " + class.Description
			capability = class.ID
			if len(class.Sample) > 0 {
				sample = class.Sample
			}
		} else if query == "" {
			// A provider's own capability name says little; ask in words.
			return "", "", nil, fmt.Errorf("%w: say in `query` what you want done, or use a class from algebra.classes", shared.ErrConflict)
		}
	}
	if query != "" {
		if want != "" {
			want += " (" + query + ")"
		} else {
			want = query
		}
	}
	if want == "" {
		return "", "", nil, fmt.Errorf("%w: say what you want done, in `query` or as a class", shared.ErrConflict)
	}
	return clean(want, maxWebWant), capability, sample, nil
}

// clean is text safe to put in a prompt and to show: one line, bounded.
func clean(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = strings.ToValidUTF8(s[:max], "")
	}
	return s
}

// probeWebFind asks one find what it charges. It never fails the search: a find
// that can't be priced is returned, marked not verified, with the reason.
func (s *ExecutionService) probeWebFind(ctx context.Context, f WebFound, capability string, sample json.RawMessage) WebEndpoint {
	e := WebEndpoint{Name: f.Name, Description: f.Description, URL: f.URL, Method: f.Method}
	host := ""
	if u, err := url.Parse(f.URL); err == nil {
		host = u.Hostname()
	}
	e.Candidate = CandidateInput{Provider: host, Name: f.Name, Endpoint: f.URL, Method: f.Method}
	cand, err := routing.Candidate{
		Capability: capability, Provider: host, Name: f.Name, ExecutionType: routing.ExecX402,
		Endpoint: f.URL, Method: f.Method, Sources: []routing.DiscoverySource{routing.SourceWeb},
	}.Normalize()
	if err != nil {
		e.Reason = briefly(err.Error())
		return e
	}
	pctx, cancel := context.WithTimeout(ctx, webProbeTimeout)
	defer cancel()
	q, err := s.priceOnly(pctx, cand, sample)
	if err != nil {
		e.Reason = briefly(err.Error())
		return e
	}
	e.Verified, e.PriceMinor, e.Network = true, q.Cost.Total(), q.Network
	e.Candidate.Network = q.Network
	return e
}
