package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// HealthStatus is what an unpaid probe of an endpoint found.
type HealthStatus string

const (
	// HealthUp: it answered with payment terms Algebra can pay (an x402 402),
	// or answered for free.
	HealthUp HealthStatus = "up"
	// HealthInputRejected: it answered 4xx to the class's sample input, so
	// it doesn't take that input the way the class gives it.
	HealthInputRejected HealthStatus = "input_rejected"
	// HealthUnpayable: it asked for payment Algebra can't make (another
	// chain, another token, another scheme).
	HealthUnpayable HealthStatus = "unpayable"
	// HealthDown: it failed (5xx), timed out or couldn't be reached.
	HealthDown HealthStatus = "down"
)

// EndpointHealth is the latest probe of one candidate and its running record.
type EndpointHealth struct {
	CandidateID string       `json:"candidate_id"`
	Provider    string       `json:"provider"`
	Capability  string       `json:"capability"`
	Endpoint    string       `json:"endpoint"`
	Network     string       `json:"network,omitempty"`
	Status      HealthStatus `json:"status"`
	HTTPStatus  int          `json:"http_status,omitempty"`
	LatencyMS   int          `json:"latency_ms"`
	// ListedPriceMinor is what the catalog advertises; LivePriceMinor what the
	// endpoint's 402 asked for just now. Both micro-USDC; zero when unknown.
	ListedPriceMinor int64 `json:"listed_price_minor,omitempty"`
	LivePriceMinor   int64 `json:"live_price_minor,omitempty"`
	// Overcharges: the live price is more than the listed one (beyond
	// rounding). Algebra refuses to pay such an endpoint.
	Overcharges bool `json:"overcharges,omitempty"`
	Checks      int  `json:"checks"`
	Ups         int  `json:"ups"`
	// Failures counts consecutive probes that weren't up.
	Failures  int       `json:"consecutive_failures"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Uptime is the share of probes that found the endpoint up.
func (h EndpointHealth) Uptime() float64 {
	if h.Checks == 0 {
		return 0
	}
	return float64(h.Ups) / float64(h.Checks)
}

// HealthStore keeps the latest probe of each candidate.
type HealthStore interface {
	SaveHealth(ctx context.Context, h EndpointHealth) error
	// HealthFor returns the records of the given candidates, by candidate ID.
	HealthFor(ctx context.Context, candidateIDs []string) (map[string]EndpointHealth, error)
	// HealthForCapability lists one capability's records, newest first.
	HealthForCapability(ctx context.Context, capability string) ([]EndpointHealth, error)
}

// MemHealthStore is a HealthStore in memory, for tests and for a server with
// no database.
type MemHealthStore struct {
	mu   sync.Mutex
	recs map[string]EndpointHealth
}

func NewMemHealthStore() *MemHealthStore { return &MemHealthStore{recs: map[string]EndpointHealth{}} }

func (m *MemHealthStore) SaveHealth(_ context.Context, h EndpointHealth) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs[h.CandidateID] = h
	return nil
}

func (m *MemHealthStore) HealthFor(_ context.Context, ids []string) (map[string]EndpointHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]EndpointHealth{}
	for _, id := range ids {
		if h, ok := m.recs[id]; ok {
			out[id] = h
		}
	}
	return out, nil
}

func (m *MemHealthStore) HealthForCapability(_ context.Context, capability string) ([]EndpointHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EndpointHealth
	for _, h := range m.recs {
		if h.Capability == capability {
			out = append(out, h)
		}
	}
	slices.SortFunc(out, func(a, b EndpointHealth) int { return b.CheckedAt.Compare(a.CheckedAt) })
	return out, nil
}

// HealthService probes candidates the way the router prices them, an unpaid
// request with the class's sample input, without ever paying: is it there,
// how fast does it answer, and does it ask what its catalog says it charges.
// A probe moves no money, so it can run as often as is polite.
type HealthService struct {
	exec    *ExecutionService
	store   HealthStore
	classes ClassSource
	log     *slog.Logger
	now     func() time.Time

	// Interval between sweeps; Concurrency and Timeout per sweep.
	Interval    time.Duration
	Concurrency int
	Timeout     time.Duration
}

// NewHealthService builds the prober. classes may be nil (then only explicit
// probes run).
func NewHealthService(exec *ExecutionService, store HealthStore, classes ClassSource) *HealthService {
	return &HealthService{exec: exec, store: store, classes: classes, log: slog.Default(), now: time.Now,
		Interval: 15 * time.Minute, Concurrency: 6, Timeout: 12 * time.Second}
}

// Stale is how long a probe stays meaningful for routing.
const healthStale = 2 * time.Hour

// downAfter: this many consecutive failed probes and the router stops even
// asking the endpoint for a price.
const downAfter = 2

// Run sweeps every class on Interval until ctx ends. An Interval of zero turns
// the sweeps off; explicit probes still run.
func (h *HealthService) Run(ctx context.Context) {
	if h.Interval <= 0 {
		return
	}
	t := time.NewTicker(h.Interval)
	defer t.Stop()
	for {
		if h.classes != nil {
			for _, c := range routing.Classes() {
				if ctx.Err() != nil {
					return
				}
				if _, err := h.ProbeClass(ctx, c.ID); err != nil {
					h.log.Warn("health: sweeping a class failed", "class", c.ID, "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ProbeClass probes every routable candidate of a class (and the operator's
// own providers of it) and returns the fresh records.
func (h *HealthService) ProbeClass(ctx context.Context, classID string) ([]EndpointHealth, error) {
	class, ok := routing.ClassByID(classID)
	if !ok {
		return nil, fmt.Errorf("unknown class %q", classID)
	}
	var cands []routing.Candidate
	if h.classes != nil {
		cs, err := h.classes.ClassCandidates(ctx, class.ID)
		if err != nil {
			return nil, err
		}
		cands = cs
	}
	return h.Probe(ctx, cands, class.Sample), nil
}

// Probe probes candidates with one input, a few at a time.
func (h *HealthService) Probe(ctx context.Context, cands []routing.Candidate, input []byte) []EndpointHealth {
	out := make([]EndpointHealth, len(cands))
	sem := make(chan struct{}, max(h.Concurrency, 1))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = h.probeOne(ctx, c, input)
		}()
	}
	wg.Wait()
	return out
}

var answeredRE = regexp.MustCompile(`answered (\d{3})`)

func (h *HealthService) probeOne(ctx context.Context, c routing.Candidate, input []byte) EndpointHealth {
	rec := EndpointHealth{
		CandidateID: c.ID, Provider: c.Provider, Capability: c.Capability, Endpoint: c.Endpoint, Network: c.Network,
		ListedPriceMinor: c.PriceMinor,
	}
	if prev, err := h.store.HealthFor(ctx, []string{c.ID}); err == nil {
		if p, ok := prev[c.ID]; ok {
			rec.Checks, rec.Ups, rec.Failures = p.Checks, p.Ups, p.Failures
		}
	}
	pctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	start := h.now()
	q, err := func() (q routing.Quote, err error) {
		defer func() {
			if p := recover(); p != nil {
				err = errors.New("the runner crashed")
			}
		}()
		return h.exec.priceOnly(pctx, c, input)
	}()
	rec.LatencyMS = int(h.now().Sub(start) / time.Millisecond)
	rec.CheckedAt = h.now().UTC()
	rec.Checks++
	switch {
	case err == nil:
		rec.Status, rec.HTTPStatus = HealthUp, 402
		rec.LivePriceMinor = q.Cost.ProviderMinor
		rec.Overcharges = rec.ListedPriceMinor > 0 && rec.LivePriceMinor*100 > rec.ListedPriceMinor*105
	case strings.Contains(err.Error(), "answered without asking for payment"):
		rec.Status, rec.HTTPStatus = HealthUp, 200
	case strings.Contains(err.Error(), "offers no payment option"):
		rec.Status, rec.HTTPStatus = HealthUnpayable, 402
	default:
		rec.Status = HealthDown
		if m := answeredRE.FindStringSubmatch(err.Error()); m != nil {
			rec.HTTPStatus, _ = strconv.Atoi(m[1])
			if rec.HTTPStatus >= 400 && rec.HTTPStatus < 500 {
				rec.Status = HealthInputRejected
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			rec.Error = "timed out"
		}
	}
	if err != nil && rec.Error == "" {
		rec.Error = briefly(err.Error())
	}
	if rec.Status == HealthUp {
		rec.Ups++
		rec.Failures = 0
	} else {
		rec.Failures++
	}
	if err := h.store.SaveHealth(context.WithoutCancel(ctx), rec); err != nil {
		h.log.Warn("health: saving a probe failed", "candidate", c.ID, "err", err)
	}
	return rec
}

// ForClass lists a class's latest probes.
func (h *HealthService) ForClass(ctx context.Context, classID string) []EndpointHealth {
	recs, err := h.store.HealthForCapability(ctx, classID)
	if err != nil {
		return []EndpointHealth{}
	}
	if recs == nil {
		recs = []EndpointHealth{}
	}
	return recs
}

// Known returns the fresh probes of the given candidates.
func (h *HealthService) Known(ctx context.Context, cands []routing.Candidate) map[string]EndpointHealth {
	if h == nil || len(cands) == 0 {
		return nil
	}
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.ID
	}
	recs, err := h.store.HealthFor(ctx, ids)
	if err != nil {
		return nil
	}
	cutoff := h.now().Add(-healthStale)
	for id, r := range recs {
		if r.CheckedAt.Before(cutoff) {
			delete(recs, id)
		}
	}
	return recs
}
