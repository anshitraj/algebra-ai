package catalog

import (
	"context"
	"fmt"
	"sync"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/intel"
)

// Catalogs returns the catalogs, in order.
func (m *Multi) Catalogs() []Source { return append([]Source(nil), m.sources...) }

// RegistrySource reads one catalog for the provider registry
// (app.IntelService): every provider's endpoints, one row per network each
// can be paid on.
type RegistrySource struct {
	Source Source
	// Workers is how many providers' details are read at once.
	Workers int
}

func (r RegistrySource) Name() string { return r.Source.Name() }

// Snapshot lists every endpoint of every provider. A provider whose details
// can't be read leaves the snapshot stale (so nothing is taken as removed);
// if most can't be read, the whole read fails and the registry keeps its
// last good copy.
func (r RegistrySource) Snapshot(ctx context.Context) ([]intel.Listed, bool, error) {
	l, err := r.Source.List(ctx, Filter{})
	if err != nil {
		return nil, false, err
	}
	workers := r.Workers
	if workers <= 0 {
		workers = 6
	}
	var (
		mu     sync.Mutex
		out    []intel.Listed
		failed int
		wg     sync.WaitGroup
		next   = make(chan Provider)
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range next {
				d, err := r.Source.Detail(ctx, p.ID)
				mu.Lock()
				if err != nil {
					failed++
				} else {
					out = append(out, listedOf(r.Source.Name(), d)...)
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range l.Providers {
		if ctx.Err() != nil {
			break
		}
		next <- p
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if n := len(l.Providers); n > 0 && failed*2 > n {
		return nil, false, fmt.Errorf("%s: %d of %d providers' details could not be read", r.Source.Name(), failed, n)
	}
	return out, l.Stale || failed > 0, nil
}

// listedOf flattens a provider's endpoints into registry rows: one per
// network it can be paid on (one with no network when the catalog names
// none).
func listedOf(source string, d *Detail) []intel.Listed {
	protocol, billable := "x402", d.Billing == ""
	if !billable {
		protocol = d.Billing
	}
	var out []intel.Listed
	for _, e := range d.Endpoints {
		pays := e.Payments
		if len(pays) == 0 {
			pays = []Payment{{Network: e.Network, PriceMinor: e.PriceMinor, PayTo: e.PayTo}}
		}
		for _, pay := range pays {
			out = append(out, intel.Listed{
				Source: source, ProviderID: d.ID, ProviderName: d.Name, Capability: e.Capability, Method: e.Method,
				URL: e.URL, Network: pay.Network, Protocol: protocol, Asset: Currency, PriceMinor: pay.PriceMinor,
				PayTo: pay.PayTo, Callable: e.Callable && billable && (pay.Network == "" || chain.Payable(pay.Network)),
			})
		}
	}
	return out
}
