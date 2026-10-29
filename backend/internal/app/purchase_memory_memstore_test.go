package app

import (
	"context"
	"sort"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

// RecentPurchases: the memory store's purchase memory, the same query as
// postgres.EconRepo.RecentPurchases.
func (m *memEconStore) RecentPurchases(_ context.Context, principalID, capability, inputHash string, since time.Time, limit int) ([]RememberedPurchase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []RememberedPurchase{}
	for _, in := range m.intents {
		if in.PrincipalID != principalID || in.Capability != capability || in.InputHash != inputHash ||
			in.State != econ.StateCommitted || in.Fulfillment != econ.FulfillmentFulfilled {
			continue
		}
		for _, r := range m.rsv {
			if r.IntentID != in.ID || r.State != econ.ReservationCommitted || r.FinishedAt == nil || r.FinishedAt.Before(since) {
				continue
			}
			out = append(out, RememberedPurchase{IntentID: in.ID, ReservationID: r.ID, ProviderID: r.ProviderID, ProjectID: in.ProjectID,
				CommittedMinor: in.CommittedMinor, Networks: in.Constraints.AllowedNetworks, At: *r.FinishedAt})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].At.After(out[b].At) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

var _ PurchaseMemory = (*memEconStore)(nil)
