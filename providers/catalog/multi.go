package catalog

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// Multi serves several catalogs as one: a combined listing, and lookups
// routed by the provider ID's prefix. A catalog that can't be read leaves its
// providers out and is reported in the listing; it never hides the others.
type Multi struct {
	sources []Source
}

// NewMulti combines sources, in order of preference for lookups that could
// match more than one. Nil sources are skipped.
func NewMulti(sources ...Source) *Multi {
	m := &Multi{}
	for _, s := range sources {
		if s != nil {
			m.sources = append(m.sources, s)
		}
	}
	return m
}

// Sources names the catalogs, in order.
func (m *Multi) Sources() []string {
	out := make([]string, len(m.sources))
	for i, s := range m.sources {
		out[i] = s.Name()
	}
	return out
}

// Owns reports whether a provider name belongs to one of the catalogs.
func (m *Multi) Owns(provider string) bool {
	return m.byPrefix(provider) != nil
}

func (m *Multi) byPrefix(id string) Source {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, s := range m.sources {
		if strings.HasPrefix(id, strings.ToLower(s.Prefix())) {
			return s
		}
	}
	return nil
}

const maxPage = 200

// List merges the catalogs' listings, sorted by name, and pages the result.
func (m *Multi) List(ctx context.Context, f Filter) (*Listing, error) {
	type answer struct {
		l   *Listing
		err error
	}
	answers := make([]answer, len(m.sources))
	var wg sync.WaitGroup
	for i, s := range m.sources {
		if f.Source != "" && !strings.EqualFold(f.Source, s.Name()) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := s.List(ctx, Filter{Query: f.Query, Category: f.Category})
			answers[i] = answer{l, err}
		}()
	}
	wg.Wait()

	out := &Listing{Categories: []Category{}, Providers: []Provider{}}
	counts := map[string]int{}
	asked, answered := 0, 0
	var all []Provider
	for i, s := range m.sources {
		if f.Source != "" && !strings.EqualFold(f.Source, s.Name()) {
			continue
		}
		asked++
		a := answers[i]
		st := SourceStatus{Name: s.Name()}
		if a.err != nil {
			st.Error = "couldn't be read just now"
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			out.Sources = append(out.Sources, st)
			continue
		}
		answered++
		st.Total, st.GeneratedAt, st.FetchedAt, st.Stale = a.l.Total, a.l.GeneratedAt, a.l.FetchedAt, a.l.Stale
		out.Sources = append(out.Sources, st)
		out.Stale = out.Stale || a.l.Stale
		if a.l.GeneratedAt.After(out.GeneratedAt) {
			out.GeneratedAt = a.l.GeneratedAt
		}
		if out.FetchedAt.IsZero() || a.l.FetchedAt.Before(out.FetchedAt) {
			out.FetchedAt = a.l.FetchedAt
		}
		for _, c := range a.l.Categories {
			counts[c.Name] += c.Count
		}
		all = append(all, a.l.Providers...)
	}
	if asked > 0 && answered == 0 {
		return nil, ErrUnavailable
	}

	slices.SortStableFunc(all, func(a, b Provider) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	limit := f.Limit
	if limit <= 0 || limit > maxPage {
		limit = maxPage
	}
	off := max(f.Offset, 0)
	if off < len(all) {
		out.Providers = slices.Clone(all[off:min(off+limit, len(all))])
	}
	out.Total, out.Count = len(all), len(out.Providers)
	for name, n := range counts {
		out.Categories = append(out.Categories, Category{Name: name, Count: n})
	}
	slices.SortFunc(out.Categories, func(a, b Category) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

// Detail finds a provider by its ID, or by a catalog's own name for it, in
// the first catalog that has it.
func (m *Multi) Detail(ctx context.Context, id string) (*Detail, error) {
	if s := m.byPrefix(id); s != nil {
		return s.Detail(ctx, id)
	}
	var firstErr error
	for _, s := range m.sources {
		d, err := s.Detail(ctx, id)
		if err == nil {
			return d, nil
		}
		if !errors.Is(err, ErrNotFound) && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, ErrNotFound
}

// CandidatesFor routes to the catalog that owns the provider.
func (m *Multi) CandidatesFor(ctx context.Context, provider, capability string) ([]routing.Candidate, error) {
	s := m.byPrefix(provider)
	if s == nil {
		return nil, ErrNotFound
	}
	return s.CandidatesFor(ctx, provider, capability)
}

// ForCapability asks each catalog in turn and returns the first that knows
// the capability; ErrNotFound when none does.
func (m *Multi) ForCapability(ctx context.Context, capability string) ([]routing.Candidate, error) {
	var firstErr error
	for _, s := range m.sources {
		cands, err := s.ForCapability(ctx, capability)
		switch {
		case err == nil && len(cands) > 0:
			return cands, nil
		case err != nil && !errors.Is(err, ErrNotFound) && firstErr == nil:
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, ErrNotFound
}
