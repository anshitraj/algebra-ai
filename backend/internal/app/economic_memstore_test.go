package app

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// memEconStore is a transactional in-memory EconStore: Atomically stages
// every write and applies it only when fn succeeds, under one lock — the
// same all-or-nothing, serialized semantics the Postgres store gets from a
// transaction with row locks.
type memEconStore struct {
	mu       sync.Mutex
	intents  map[string]*econ.Intent
	rsv      map[string]*econ.Reservation
	events   []EconEvent
	receipts map[string]string
}

func newMemEconStore() *memEconStore {
	return &memEconStore{intents: map[string]*econ.Intent{}, rsv: map[string]*econ.Reservation{}, receipts: map[string]string{}}
}

func (m *memEconStore) CreateIntent(_ context.Context, in *econ.Intent) (*econ.Intent, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cur := range m.intents {
		if cur.PrincipalID == in.PrincipalID && cur.EffectKey == in.EffectKey {
			cp := *cur
			return &cp, false, nil
		}
	}
	cp := *in
	m.intents[in.ID] = &cp
	out := cp
	return &out, true, nil
}

func (m *memEconStore) GetIntent(_ context.Context, id string) (*econ.Intent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.intents[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *in
	return &cp, nil
}

func (m *memEconStore) ListIntents(_ context.Context, principalID string, limit int) ([]econ.Intent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []econ.Intent
	for _, in := range m.intents {
		if in.PrincipalID == principalID {
			out = append(out, *in)
		}
	}
	return out, nil
}

func (m *memEconStore) Reservations(_ context.Context, intentID string) ([]econ.Reservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []econ.Reservation
	for _, r := range m.rsv {
		if r.IntentID == intentID {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Attempt < out[j].Attempt })
	return out, nil
}

func (m *memEconStore) Events(_ context.Context, intentID string) ([]EconEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EconEvent
	for _, e := range m.events {
		if e.IntentID == intentID {
			out = append(out, e)
		}
	}
	return out, nil
}

type memUnit struct {
	m       *memEconStore
	intent  *econ.Intent
	version int64
	rsv     map[string]*econ.Reservation
	events  []EconEvent
	dirty   bool
}

func (m *memEconStore) Atomically(_ context.Context, intentID, _ string, fn func(u EconUnit) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.intents[intentID]
	if !ok {
		return shared.ErrNotFound
	}
	cp := *in
	u := &memUnit{m: m, intent: &cp, version: in.Version, rsv: map[string]*econ.Reservation{}}
	if err := fn(u); err != nil {
		return err // nothing staged is applied
	}
	if u.dirty {
		stored := *u.intent
		m.intents[intentID] = &stored
	}
	for id, r := range u.rsv {
		cp := *r
		m.rsv[id] = &cp
	}
	m.events = append(m.events, u.events...)
	return nil
}

func (u *memUnit) Intent() *econ.Intent { return u.intent }

// SaveIntent bumps the version like the Postgres CAS; under the store's
// single lock no other writer can interleave.
func (u *memUnit) SaveIntent(in *econ.Intent) error {
	u.version++
	in.Version = u.version
	u.intent, u.dirty = in, true
	return nil
}

func (u *memUnit) get(id string) (*econ.Reservation, bool) {
	if r, ok := u.rsv[id]; ok {
		return r, true
	}
	r, ok := u.m.rsv[id]
	return r, ok
}

func (u *memUnit) Reservation(id string) (*econ.Reservation, error) {
	r, ok := u.get(id)
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (u *memUnit) InsertReservation(r *econ.Reservation) error {
	all := map[string]*econ.Reservation{}
	for id, x := range u.m.rsv {
		all[id] = x
	}
	for id, x := range u.rsv {
		all[id] = x
	}
	for _, x := range all {
		if x.IntentID == r.IntentID && x.State.Live() {
			return ErrLiveReservationExists // the partial unique index
		}
	}
	cp := *r
	u.rsv[r.ID] = &cp
	return nil
}

func (u *memUnit) SaveReservation(r *econ.Reservation) error {
	cp := *r
	u.rsv[r.ID] = &cp
	return nil
}

func (u *memUnit) PassExposure(passID string, since time.Time) (int64, error) {
	all := map[string]*econ.Reservation{}
	for id, x := range u.m.rsv {
		all[id] = x
	}
	for id, x := range u.rsv {
		all[id] = x
	}
	var total int64
	for _, x := range all {
		if x.ExecutorPassID != passID {
			continue
		}
		if x.State.Live() || (x.State == econ.ReservationCommitted && x.FinishedAt != nil && !x.FinishedAt.Before(since)) {
			total += x.HoldMinor
		}
	}
	return total, nil
}

func (u *memUnit) AppendEvent(e EconEvent) error {
	if err := ValidateEvent(e); err != nil {
		return err
	}
	u.events = append(u.events, e)
	return nil
}

func (m *memEconStore) filter(pred func(r *econ.Reservation) bool) []econ.Reservation {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []econ.Reservation
	for _, r := range m.rsv {
		if pred(r) {
			out = append(out, *r)
		}
	}
	return out
}

func (m *memEconStore) ExpiredLeases(_ context.Context, now time.Time, _ int) ([]econ.Reservation, error) {
	return m.filter(func(r *econ.Reservation) bool {
		return r.State == econ.ReservationReserved && !now.Before(r.LeaseExpiresAt)
	}), nil
}

func (m *memEconStore) OverdueExecutions(_ context.Context, now time.Time, _ int) ([]econ.Reservation, error) {
	return m.filter(func(r *econ.Reservation) bool {
		return r.State == econ.ReservationExecuting && r.ExecutionDeadline != nil && !now.Before(*r.ExecutionDeadline)
	}), nil
}

func (m *memEconStore) Unresolved(_ context.Context, _ int) ([]econ.Reservation, error) {
	return m.filter(func(r *econ.Reservation) bool {
		return slices.Contains([]econ.ReservationState{econ.ReservationUnknown, econ.ReservationReconciling}, r.State)
	}), nil
}

func (m *memEconStore) ExpiredIntents(_ context.Context, now time.Time, _ int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for id, in := range m.intents {
		if (in.State == econ.StateOpen || in.State == econ.StateAwaitingApproval) && in.Expired(now) {
			out = append(out, id)
		}
	}
	return out, nil
}

func (m *memEconStore) SaveReceipt(_ context.Context, intentID, _ string, jws string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receipts[intentID] = jws
	return nil
}

func (m *memEconStore) GetReceipt(_ context.Context, intentID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.receipts[intentID]; ok {
		return j, nil
	}
	return "", shared.ErrNotFound
}

func (m *memEconStore) Stats(_ context.Context, principalID string, _ time.Time) (*EconStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := &EconStats{}
	for _, in := range m.intents {
		if in.PrincipalID != principalID {
			continue
		}
		st.Intents++
		st.DuplicateAttemptsBlock += in.BlockedAttempts
		st.Attempts += in.Attempts
		if in.State == econ.StateCommitted {
			st.Committed++
			st.SpentMinor += in.CommittedMinor
		}
	}
	return st, nil
}
