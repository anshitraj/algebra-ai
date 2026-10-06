package app

import (
	"context"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// The memory store's half of the pass controls: velocity counts and whether
// a provider was ever paid, read the way the Postgres unit reads them.

func (u *memUnit) allReservations() map[string]*econ.Reservation {
	all := map[string]*econ.Reservation{}
	for id, x := range u.m.rsv {
		all[id] = x
	}
	for id, x := range u.rsv {
		all[id] = x
	}
	return all
}

func (u *memUnit) PassAttemptsSince(passID, provider string, since time.Time) (int, error) {
	n := 0
	for _, x := range u.allReservations() {
		if x.ExecutorPassID == passID && !x.CreatedAt.Before(since) && (provider == "" || x.ProviderID == provider) {
			n++
		}
	}
	return n, nil
}

func (u *memUnit) ProviderPaid(principalID, provider string) (bool, error) {
	for _, x := range u.allReservations() {
		if x.ProviderID != provider || x.State != econ.ReservationCommitted {
			continue
		}
		in := u.m.intents[x.IntentID]
		if x.IntentID == u.intent.ID {
			in = u.intent
		}
		if in != nil && in.PrincipalID == principalID {
			return true, nil
		}
	}
	return false, nil
}

// The fake pass store's kill switch and controls.

func (f *fakePassStore) SetFrozen(_ context.Context, id string, at *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.passes[id]
	if !ok || p.RevokedAt != nil {
		return shared.ErrNotFound
	}
	p.FrozenAt = at
	return nil
}

func (f *fakePassStore) SetFrozenAll(_ context.Context, userID string, at *time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.passes {
		if p.UserID != userID || p.RevokedAt != nil || (p.FrozenAt != nil) == (at != nil) {
			continue
		}
		p.FrozenAt = at
		n++
	}
	return n, nil
}

func (f *fakePassStore) SetControls(_ context.Context, id string, c spendpass.Controls) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.passes[id]
	if !ok || p.RevokedAt != nil {
		return shared.ErrNotFound
	}
	p.Controls = c
	return nil
}

// The memory store as a PolicyReader, for the dry run.

func (m *memEconStore) PassSpend(_ context.Context, passID string, since time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, x := range m.rsv {
		if x.ExecutorPassID == passID && (x.State.Live() || (x.State == econ.ReservationCommitted && x.FinishedAt != nil && !x.FinishedAt.Before(since))) {
			total += x.HoldMinor
		}
	}
	return total, nil
}

func (m *memEconStore) PassAttemptsRecent(_ context.Context, passID, provider string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, x := range m.rsv {
		if x.ExecutorPassID == passID && !x.CreatedAt.Before(since) && (provider == "" || x.ProviderID == provider) {
			n++
		}
	}
	return n, nil
}

func (m *memEconStore) ProviderPaidBy(_ context.Context, principalID, provider string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.rsv {
		if in := m.intents[x.IntentID]; in != nil && x.ProviderID == provider && x.State == econ.ReservationCommitted && in.PrincipalID == principalID {
			return true, nil
		}
	}
	return false, nil
}
