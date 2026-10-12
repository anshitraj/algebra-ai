package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/intel"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Provider Intelligence: the registry of every paid endpoint the catalogs
// list (internal/domain/intel), refreshed every six hours by whichever API
// instance takes the job's lease, and the trust gate the router consults
// before it pays anybody.

// IntelJob is the scheduled job's name.
const IntelJob = "provider_intelligence"

// IntelSource is one catalog, as the registry reads it.
type IntelSource interface {
	Name() string
	// Snapshot lists every endpoint the catalog lists now. Stale reports
	// that the catalog served an older copy because it couldn't be reached:
	// then nothing it leaves out is taken as removed.
	Snapshot(ctx context.Context) (listed []intel.Listed, stale bool, err error)
}

// IntelFilter narrows the registry for the dashboard.
type IntelFilter struct {
	Source     string
	Network    string
	Protocol   string
	Trust      string
	Level      string // listed, quoted, tested, verified, executable
	Capability string
	Query      string
	NewSince   time.Time
	Sort       string // cheapest, fastest, newest, name
	Limit      int
	Offset     int
}

// ChangeFilter narrows the change history.
type ChangeFilter struct {
	EndpointID string
	Kind       string
	Suspicious bool
	Limit      int
}

// SourceRun is how one catalog answered one refresh.
type SourceRun struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	Stale      bool   `json:"stale,omitempty"`
	Error      string `json:"error,omitempty"`
	Attempts   int    `json:"attempts"`
	Seen       int    `json:"seen"`
	Added      int    `json:"added"`
	Changed    int    `json:"changed"`
	Removed    int    `json:"removed"`
	DurationMS int64  `json:"duration_ms"`
	// KeptLastGood: the catalog answered with nothing although the registry
	// holds its endpoints, so the answer was not believed.
	KeptLastGood bool `json:"kept_last_good,omitempty"`
}

// SyncRun is one refresh.
type SyncRun struct {
	ID         string      `json:"id"`
	Trigger    string      `json:"trigger"`
	Status     string      `json:"status"` // running, ok, partial, failed
	Sources    []SourceRun `json:"sources"`
	Seen       int         `json:"seen"`
	Added      int         `json:"added"`
	Changed    int         `json:"changed"`
	Removed    int         `json:"removed"`
	Error      string      `json:"error,omitempty"`
	StartedAt  time.Time   `json:"started_at"`
	FinishedAt *time.Time  `json:"finished_at,omitempty"`
}

// ScheduledJob is a job shared by every API instance.
type ScheduledJob struct {
	Name           string                      `json:"name"`
	Schedule       string                      `json:"schedule"`
	NextRunAt      time.Time                   `json:"next_run_at"`
	LeaseOwner     string                      `json:"lease_owner,omitempty"`
	LeaseUntil     *time.Time                  `json:"lease_until,omitempty"`
	LastStartedAt  *time.Time                  `json:"last_started_at,omitempty"`
	LastFinishedAt *time.Time                  `json:"last_finished_at,omitempty"`
	LastSuccessAt  *time.Time                  `json:"last_success_at,omitempty"`
	LastError      string                      `json:"last_error,omitempty"`
	Checkpoint     map[string]SourceCheckpoint `json:"checkpoint,omitempty"`
}

// SourceCheckpoint is the last time each catalog was read successfully.
type SourceCheckpoint struct {
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
}

// IntelSummary counts the registry.
type IntelSummary struct {
	Endpoints   int            `json:"endpoints"`
	Providers   int            `json:"providers"`
	ByTrust     map[string]int `json:"by_trust"`
	ByStatus    map[string]int `json:"by_status"`
	ByNetwork   map[string]int `json:"by_network"`
	BySource    map[string]int `json:"by_source"`
	Quoted      int            `json:"quoted"`
	Tested      int            `json:"tested"`
	Executable  int            `json:"executable"`
	NewThisWeek int            `json:"new_this_week"`
}

// IntelStore keeps the registry, its history, its runs and the job lease.
type IntelStore interface {
	EndpointsBySource(ctx context.Context, source string) ([]intel.Endpoint, error)
	Endpoint(ctx context.Context, id string) (*intel.Endpoint, error)
	EndpointsByID(ctx context.Context, ids []string) (map[string]intel.Endpoint, error)
	// SaveEndpoints writes endpoints and their changes in one transaction.
	SaveEndpoints(ctx context.Context, eps []intel.Endpoint, changes []intel.Change) error
	ListEndpoints(ctx context.Context, f IntelFilter) ([]intel.Endpoint, int, error)
	Changes(ctx context.Context, f ChangeFilter) ([]intel.Change, error)
	Summary(ctx context.Context, newSince time.Time) (IntelSummary, error)
	SaveRun(ctx context.Context, run SyncRun) error
	Runs(ctx context.Context, limit int) ([]SyncRun, error)
	// EnsureJob creates the job, due at next, unless it exists.
	EnsureJob(ctx context.Context, name, schedule string, next time.Time) error
	// ClaimJob takes the job's lease when it is due (or force) and nobody
	// holds an unexpired lease. Exactly one claimer wins.
	ClaimJob(ctx context.Context, name, owner string, now time.Time, lease time.Duration, force bool) (bool, error)
	// FinishJob releases the lease, schedules the next run and records the outcome.
	FinishJob(ctx context.Context, name, owner string, now, next time.Time, errText string, checkpoint map[string]SourceCheckpoint) error
	Job(ctx context.Context, name string) (*ScheduledJob, error)
}

// IntelService runs the registry.
type IntelService struct {
	store   IntelStore
	sources []IntelSource
	owner   string
	log     *slog.Logger
	now     func() time.Time

	// Tick is how often an instance looks whether the job is due; Lease how
	// long a claimed run may hold it; SourceTimeout and Retries bound each
	// catalog; StaleAfter is when a catalog not read since counts as stale.
	Tick          time.Duration
	Lease         time.Duration
	SourceTimeout time.Duration
	Retries       int
	Backoff       time.Duration
	StaleAfter    time.Duration
	// FirstRunDelay: a registry that has never run is refreshed this long
	// after start, not at the next six-hour slot.
	FirstRunDelay time.Duration

	mu      sync.Mutex
	running bool
}

// NewIntelService builds the registry over its catalogs.
func NewIntelService(store IntelStore, sources ...IntelSource) *IntelService {
	host, _ := os.Hostname()
	var b [4]byte
	_, _ = rand.Read(b[:])
	return &IntelService{
		store: store, sources: sources, owner: fmt.Sprintf("%s/%d/%s", host, os.Getpid(), hex.EncodeToString(b[:])),
		log: slog.Default(), now: time.Now,
		Tick: time.Minute, Lease: 30 * time.Minute, SourceTimeout: 4 * time.Minute, Retries: 3, Backoff: 5 * time.Second,
		StaleAfter: 13 * time.Hour, FirstRunDelay: time.Minute,
	}
}

// SetClock replaces the clock (tests).
func (s *IntelService) SetClock(now func() time.Time) { s.now = now }

// Sources names the catalogs it reads.
func (s *IntelService) Sources() []string {
	out := make([]string, len(s.sources))
	for i, src := range s.sources {
		out[i] = src.Name()
	}
	return out
}

// Run looks every Tick whether the refresh is due and runs it when this
// instance takes the lease. It returns when ctx ends.
func (s *IntelService) Run(ctx context.Context) {
	if err := s.store.EnsureJob(ctx, IntelJob, intel.Schedule, s.now().Add(s.FirstRunDelay)); err != nil {
		s.log.Warn("provider intelligence: the job could not be scheduled", "err", err)
	}
	t := time.NewTicker(s.Tick)
	defer t.Stop()
	for {
		if _, err := s.RunDue(ctx, "schedule", false); err != nil && !errors.Is(err, context.Canceled) {
			s.log.Warn("provider intelligence: refresh failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ErrNotDue: the job isn't due, or another instance holds it.
var ErrNotDue = errors.New("provider intelligence: not due, or running elsewhere")

// RunDue refreshes the registry if this instance can take the job's lease:
// when it is due, or at once when force (a person asked). It returns the run,
// or nil and no error when there was nothing to do.
func (s *IntelService) RunDue(ctx context.Context, trigger string, force bool) (*SyncRun, error) {
	now := s.now()
	if force {
		if err := s.store.EnsureJob(ctx, IntelJob, intel.Schedule, intel.NextRun(now)); err != nil {
			return nil, err
		}
	}
	won, err := s.store.ClaimJob(ctx, IntelJob, s.owner, now, s.Lease, force)
	if err != nil || !won {
		if force && err == nil {
			return nil, ErrNotDue
		}
		return nil, err
	}
	job, _ := s.store.Job(ctx, IntelJob)
	checkpoint := map[string]SourceCheckpoint{}
	if job != nil && job.Checkpoint != nil {
		checkpoint = job.Checkpoint
	}
	run := s.Sync(ctx, trigger, checkpoint)
	end := s.now()
	errText := ""
	if run.Status == "failed" {
		errText = run.Error
	}
	// The next run is the next six-hour slot, whatever happened: a failed
	// catalog is retried within this run, not by hammering it every minute.
	if err := s.store.FinishJob(context.WithoutCancel(ctx), IntelJob, s.owner, end, intel.NextRun(end), errText, checkpoint); err != nil {
		s.log.Warn("provider intelligence: recording the run failed", "err", err)
	}
	return run, nil
}

// Sync reads every catalog and applies what changed. A catalog that fails
// (after retries) or answers with nothing it used to list keeps its last
// good copy in the registry; the others are applied. checkpoint is updated
// in place with each catalog's outcome.
func (s *IntelService) Sync(ctx context.Context, trigger string, checkpoint map[string]SourceCheckpoint) *SyncRun {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return &SyncRun{Trigger: trigger, Status: "failed", Error: "a refresh is already running in this instance", StartedAt: s.now()}
	}
	s.running = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()

	run := &SyncRun{ID: newID("sync"), Trigger: trigger, Status: "running", StartedAt: s.now().UTC()}
	if err := s.store.SaveRun(ctx, *run); err != nil {
		s.log.Warn("provider intelligence: recording the run failed", "err", err)
	}
	ok := 0
	for _, src := range s.sources {
		sr := s.syncSource(ctx, src, run.ID)
		run.Sources = append(run.Sources, sr)
		run.Seen += sr.Seen
		run.Added += sr.Added
		run.Changed += sr.Changed
		run.Removed += sr.Removed
		now := s.now().UTC()
		cp := checkpoint[sr.Name]
		if sr.OK {
			ok++
			cp.LastSuccessAt, cp.LastError, cp.LastErrorAt = &now, "", nil
		} else {
			cp.LastError, cp.LastErrorAt = sr.Error, &now
		}
		if checkpoint != nil {
			checkpoint[sr.Name] = cp
		}
	}
	switch {
	case len(s.sources) == 0:
		run.Status, run.Error = "failed", "no catalogs are configured"
	case ok == len(s.sources):
		run.Status = "ok"
	case ok == 0:
		run.Status, run.Error = "failed", "no catalog could be read; the registry keeps its last good copy"
	default:
		run.Status = "partial"
	}
	end := s.now().UTC()
	run.FinishedAt = &end
	if err := s.store.SaveRun(context.WithoutCancel(ctx), *run); err != nil {
		s.log.Warn("provider intelligence: recording the run failed", "err", err)
	}
	s.log.Info("provider intelligence: refresh", "run", run.ID, "status", run.Status, "seen", run.Seen, "added", run.Added, "changed", run.Changed, "removed", run.Removed)
	return run
}

func (s *IntelService) syncSource(ctx context.Context, src IntelSource, runID string) SourceRun {
	sr := SourceRun{Name: src.Name()}
	start := s.now()
	defer func() { sr.DurationMS = s.now().Sub(start).Milliseconds() }()
	var listed []intel.Listed
	var stale bool
	var err error
	for attempt := 1; attempt <= max(1, s.Retries); attempt++ {
		sr.Attempts = attempt
		sctx, cancel := context.WithTimeout(ctx, s.SourceTimeout)
		listed, stale, err = src.Snapshot(sctx)
		cancel()
		if err == nil || ctx.Err() != nil {
			break
		}
		if attempt < s.Retries {
			select {
			case <-ctx.Done():
			case <-time.After(s.Backoff * time.Duration(1<<(attempt-1))):
			}
		}
	}
	if err != nil {
		sr.Error = briefly(err.Error())
		return sr
	}
	prev, err := s.store.EndpointsBySource(ctx, src.Name())
	if err != nil {
		sr.Error = briefly(err.Error())
		return sr
	}
	for i := range listed {
		listed[i].Source = src.Name()
	}
	active := 0
	for _, e := range prev {
		if e.Status != intel.StatusRemoved {
			active++
		}
	}
	if len(listed) == 0 && active > 0 {
		sr.Error, sr.KeptLastGood = "the catalog answered with nothing; its last good copy is kept", true
		return sr
	}
	if stale {
		// Only what it still lists is applied; what it leaves out is kept.
		sr.Stale = true
		keep := map[string]bool{}
		for _, l := range listed {
			keep[l.ID()] = true
		}
		var seen []intel.Endpoint
		for _, e := range prev {
			if keep[e.ID] {
				seen = append(seen, e)
			}
		}
		prev = seen
	}
	save, changes := intel.Merge(prev, listed, s.now().UTC())
	for i := range changes {
		changes[i].ID, changes[i].RunID = newID("pchg"), runID
		switch changes[i].Kind {
		case intel.ChangeAdded:
			sr.Added++
		case intel.ChangeRemoved:
			sr.Removed++
		default:
			sr.Changed++
		}
	}
	sr.Seen = len(listed)
	if err := s.store.SaveEndpoints(ctx, save, changes); err != nil {
		sr.Error = briefly(err.Error())
		return sr
	}
	sr.OK = true
	return sr
}

// IntelStatus is the registry's health: the job, the last runs, and each
// catalog's last successful read.
type IntelStatus struct {
	Schedule string              `json:"schedule"`
	Job      *ScheduledJob       `json:"job,omitempty"`
	Runs     []SyncRun           `json:"runs"`
	Sources  []IntelSourceStatus `json:"sources"`
	Summary  IntelSummary        `json:"summary"`
	// Discovery says what else feeds the registry (none of it authoritative).
	Discovery map[string]string `json:"discovery"`
}

// IntelSourceStatus is one catalog's freshness.
type IntelSourceStatus struct {
	Name          string     `json:"name"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	// Stale: not read successfully within StaleAfter; its endpoints are
	// shown from the last good copy.
	Stale bool `json:"stale"`
}

// Status reports the job, recent runs and each catalog's freshness.
func (s *IntelService) Status(ctx context.Context) (*IntelStatus, error) {
	job, err := s.store.Job(ctx, IntelJob)
	if err != nil && !errors.Is(err, shared.ErrNotFound) {
		return nil, err
	}
	runs, err := s.store.Runs(ctx, 10)
	if err != nil {
		return nil, err
	}
	now := s.now()
	sum, err := s.store.Summary(ctx, now.Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	out := &IntelStatus{Schedule: intel.Schedule + " (UTC)", Job: job, Runs: runs, Summary: sum,
		Discovery: map[string]string{"parallel_ai": "off: not built; the registry is fed by catalogs and Algebra's own calls only"}}
	for _, name := range s.Sources() {
		st := IntelSourceStatus{Name: name, Stale: true}
		if job != nil {
			if cp, ok := job.Checkpoint[name]; ok {
				st.LastSuccessAt, st.LastError = cp.LastSuccessAt, cp.LastError
				st.Stale = cp.LastSuccessAt == nil || now.Sub(*cp.LastSuccessAt) > s.StaleAfter
			}
		}
		out.Sources = append(out.Sources, st)
	}
	return out, nil
}

// Endpoints lists the registry.
func (s *IntelService) Endpoints(ctx context.Context, f IntelFilter) ([]intel.Endpoint, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Level == "new" {
		f.Level, f.NewSince = "", s.now().Add(-7*24*time.Hour)
	}
	return s.store.ListEndpoints(ctx, f)
}

// EndpointDetail is one endpoint with its history.
type EndpointDetail struct {
	intel.Endpoint
	Levels  intel.Levels   `json:"levels"`
	Changes []intel.Change `json:"changes"`
}

// Endpoint returns one endpoint and its history.
func (s *IntelService) Endpoint(ctx context.Context, id string) (*EndpointDetail, error) {
	e, err := s.store.Endpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	changes, err := s.store.Changes(ctx, ChangeFilter{EndpointID: id, Limit: 100})
	if err != nil {
		return nil, err
	}
	return &EndpointDetail{Endpoint: *e, Levels: e.Levels(), Changes: changes}, nil
}

// Changes lists what changed, newest first.
func (s *IntelService) Changes(ctx context.Context, f ChangeFilter) ([]intel.Change, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	return s.store.Changes(ctx, f)
}

// SetTrust is an operator's decision about an endpoint, recorded with who
// made it.
func (s *IntelService) SetTrust(ctx context.Context, id string, t intel.Trust, note, actor string) (*intel.Endpoint, error) {
	e, err := s.store.Endpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	c, err := e.SetTrust(t, note, actor, s.now().UTC())
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidRequest, err)
	}
	c.ID = newID("pchg")
	if err := s.store.SaveEndpoints(ctx, []intel.Endpoint{*e}, []intel.Change{c}); err != nil {
		return nil, err
	}
	return e, nil
}

// --- The trust gate: what the router asks before it pays. ---

// TrustRefused is the router refusing a provider for what the registry
// knows about it. Nothing was reserved or paid.
type TrustRefused struct {
	Code, Detail, Provider string
}

func (e *TrustRefused) Error() string {
	return fmt.Sprintf("%s refused (%s): %s", e.Provider, e.Code, e.Detail)
}

// known returns the registry rows of these candidates, by candidate ID.
func (s *IntelService) known(ctx context.Context, cands []routing.Candidate) map[string]intel.Endpoint {
	ids := make([]string, 0, len(cands))
	byID := map[string][]string{}
	for _, c := range cands {
		id := intel.ID(c.Provider, c.Endpoint, c.Network)
		ids = append(ids, id)
		byID[id] = append(byID[id], c.ID)
	}
	rows, err := s.store.EndpointsByID(ctx, ids)
	if err != nil {
		s.log.Warn("provider intelligence: reading trust failed; payee checks still run at payment", "err", err)
		return nil
	}
	out := map[string]intel.Endpoint{}
	for id, e := range rows {
		for _, cid := range byID[id] {
			out[cid] = e
		}
	}
	return out
}

// CheckQuote is the payment-time rule for a quote: a blocked endpoint, or a
// payee other than the pinned one, is refused, and a new payee blocks the
// endpoint. An endpoint the registry doesn't know passes: its payee is pinned
// when it first delivers.
func (s *IntelService) CheckQuote(ctx context.Context, q routing.Quote) *TrustRefused {
	if s == nil {
		return nil
	}
	id := intel.ID(q.Provider, q.Endpoint, q.Network)
	e, err := s.store.Endpoint(ctx, id)
	if err != nil {
		if !errors.Is(err, shared.ErrNotFound) {
			// Can't read the registry: refuse rather than pay blind.
			return &TrustRefused{Code: "TRUST_UNAVAILABLE", Provider: q.Provider, Detail: "the provider registry could not be read"}
		}
		return nil
	}
	code, detail := e.Check(q.PayTo)
	if code == "" {
		return nil
	}
	if code == intel.ReasonPayeeChanged {
		changes := e.Flag(q.PayTo, s.now().UTC())
		for i := range changes {
			changes[i].ID = newID("pchg")
		}
		if err := s.store.SaveEndpoints(context.WithoutCancel(ctx), []intel.Endpoint{*e}, changes); err != nil {
			s.log.Error("provider intelligence: recording a payee change failed", "endpoint", id, "err", err)
		}
	}
	return &TrustRefused{Code: code, Provider: q.Provider, Detail: detail}
}

// quotedEvery: a successful unpaid quote is written at most this often per endpoint.
const quotedEvery = 10 * time.Minute

// ObserveQuote records what an unpaid request found, for endpoints the
// registry knows: terms (quoted), or no answer at all (unreachable).
func (s *IntelService) ObserveQuote(ctx context.Context, c routing.Candidate, quoteErr error) {
	if s == nil {
		return
	}
	e, err := s.store.Endpoint(ctx, intel.ID(c.Provider, c.Endpoint, c.Network))
	if err != nil {
		return
	}
	now := s.now().UTC()
	var changes []intel.Change
	switch {
	case quoteErr == nil:
		if e.Status == intel.StatusActive && e.QuotedAt != nil && now.Sub(*e.QuotedAt) < quotedEvery {
			return
		}
		changes = e.Quoted(now)
	case strings.Contains(quoteErr.Error(), "reaching"):
		if changes = e.Unreachable(briefly(quoteErr.Error()), now); len(changes) == 0 {
			return
		}
	default:
		return
	}
	for i := range changes {
		changes[i].ID = newID("pchg")
	}
	if err := s.store.SaveEndpoints(context.WithoutCancel(ctx), []intel.Endpoint{*e}, changes); err != nil {
		s.log.Warn("provider intelligence: recording a quote failed", "err", err)
	}
}

// ObserveDelivery records a paid call that delivered: tested, its payee
// pinned if none was, and verified when it was paid at the pinned payee.
func (s *IntelService) ObserveDelivery(ctx context.Context, q routing.Quote, latencyMS int) {
	if s == nil {
		return
	}
	e, err := s.store.Endpoint(ctx, intel.ID(q.Provider, q.Endpoint, q.Network))
	if err != nil {
		return
	}
	changes := e.Delivered(q.PayTo, latencyMS, s.now().UTC())
	for i := range changes {
		changes[i].ID = newID("pchg")
	}
	if err := s.store.SaveEndpoints(context.WithoutCancel(ctx), []intel.Endpoint{*e}, changes); err != nil {
		s.log.Warn("provider intelligence: recording a delivery failed", "err", err)
	}
}
