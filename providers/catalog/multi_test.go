package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// fakeSource is a catalog with fixed providers, or a fixed error.
type fakeSource struct {
	name, prefix string
	providers    []Provider
	err          error
	caps         map[string][]routing.Candidate // capability -> candidates
}

func (f *fakeSource) Name() string   { return f.name }
func (f *fakeSource) Prefix() string { return f.prefix }

func (f *fakeSource) List(_ context.Context, flt Filter) (*Listing, error) {
	if f.err != nil {
		return nil, f.err
	}
	l := &Listing{Source: f.name, FetchedAt: time.Unix(100, 0), Categories: []Category{}}
	counts := map[string]int{}
	for _, p := range f.providers {
		counts[p.Category]++
		if flt.Matches(p) {
			l.Providers = append(l.Providers, p)
		}
	}
	for n, c := range counts {
		l.Categories = append(l.Categories, Category{Name: n, Count: c})
	}
	l.Total, l.Count = len(l.Providers), len(l.Providers)
	return l, nil
}

func (f *fakeSource) Detail(_ context.Context, id string) (*Detail, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, p := range f.providers {
		if p.ID == id || p.FQN == id {
			return &Detail{Provider: p}, nil
		}
	}
	return nil, ErrNotFound
}

func (f *fakeSource) CandidatesFor(_ context.Context, provider, capability string) ([]routing.Candidate, error) {
	return f.caps[capability], f.err
}

func (f *fakeSource) ForCapability(_ context.Context, capability string) ([]routing.Candidate, error) {
	if f.err != nil {
		return nil, f.err
	}
	if c, ok := f.caps[capability]; ok {
		return c, nil
	}
	return nil, ErrNotFound
}

func twoCatalogs() (*fakeSource, *fakeSource) {
	pay := &fakeSource{name: "pay.sh", prefix: "paysh:", providers: []Provider{
		{ID: "paysh:birdeye.data", FQN: "birdeye/data", Name: "Birdeye Data", Category: "finance", Source: "pay.sh"},
		{ID: "paysh:exa.search", FQN: "exa/search", Name: "Exa", Category: "search", Source: "pay.sh"},
	}, caps: map[string][]routing.Candidate{"birdeye.data.get.price": {{Provider: "paysh:birdeye.data"}}}}
	circle := &fakeSource{name: "circle", prefix: "circle:", providers: []Provider{
		{ID: "circle:allium", FQN: "allium", Name: "Allium", Category: "finance", Source: "circle"},
		{ID: "circle:birdeye", FQN: "birdeye", Name: "Birdeye", Category: "finance", Source: "circle"},
	}, caps: map[string][]routing.Candidate{"circle.birdeye.get.price": {{Provider: "circle:birdeye"}}}}
	return pay, circle
}

func TestMultiListMergesAndPages(t *testing.T) {
	pay, circle := twoCatalogs()
	m := NewMulti(pay, nil, circle)
	ctx := context.Background()

	l, err := m.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range l.Providers {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "circle:allium,circle:birdeye,paysh:birdeye.data,paysh:exa.search" || l.Total != 4 || l.Source != "" {
		t.Errorf("merged, sorted by name: %v", ids)
	}
	if len(l.Sources) != 2 || l.Sources[0].Name != "pay.sh" || l.Sources[0].Total != 2 || l.Sources[1].Total != 2 {
		t.Errorf("each catalog reported: %+v", l.Sources)
	}
	if l.Categories[0].Name != "finance" || l.Categories[0].Count != 3 {
		t.Errorf("categories summed across catalogs: %+v", l.Categories)
	}

	page, _ := m.List(ctx, Filter{Limit: 2, Offset: 1})
	if page.Total != 4 || page.Count != 2 || page.Providers[0].ID != "circle:birdeye" {
		t.Errorf("paging the merged list: %+v", page)
	}
	only, _ := m.List(ctx, Filter{Source: "CIRCLE", Query: "bird"})
	if only.Total != 1 || only.Providers[0].ID != "circle:birdeye" || len(only.Sources) != 1 {
		t.Errorf("one catalog, filtered: %+v", only)
	}
}

func TestMultiListSurvivesOneCatalogBeingDown(t *testing.T) {
	pay, circle := twoCatalogs()
	circle.err = ErrUnavailable
	l, err := NewMulti(pay, circle).List(context.Background(), Filter{})
	if err != nil || l.Total != 2 || l.Sources[1].Error == "" {
		t.Fatalf("the other catalog still lists, and the outage is reported: %v %+v", err, l)
	}
	pay.err = errors.New("down too")
	if _, err := NewMulti(pay, circle).List(context.Background(), Filter{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("all down: %v", err)
	}
}

func TestMultiLookups(t *testing.T) {
	pay, circle := twoCatalogs()
	m := NewMulti(pay, circle)
	ctx := context.Background()

	if !m.Owns("PaySh:x") || !m.Owns("circle:y") || m.Owns("acme") {
		t.Error("owns by prefix, ignoring case")
	}
	if d, err := m.Detail(ctx, "circle:birdeye"); err != nil || d.Source != "circle" {
		t.Errorf("by prefix: %v %+v", err, d)
	}
	if d, err := m.Detail(ctx, "allium"); err != nil || d.ID != "circle:allium" {
		t.Errorf("a catalog's own name, found in whichever catalog has it: %v %+v", err, d)
	}
	if _, err := m.Detail(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if c, err := m.ForCapability(ctx, "circle.birdeye.get.price"); err != nil || len(c) != 1 || c[0].Provider != "circle:birdeye" {
		t.Errorf("for capability: %v %+v", err, c)
	}
	if _, err := m.ForCapability(ctx, "solana.token-risk"); !errors.Is(err, ErrNotFound) {
		t.Errorf("nobody's capability: %v", err)
	}
	if _, err := m.CandidatesFor(ctx, "acme", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a provider no catalog owns: %v", err)
	}
}

func TestCapabilityIDAndSlug(t *testing.T) {
	if got := CapabilityID("circle.birdeye", "GET", "x402/defi/price"); got != "circle.birdeye.get.x402-defi-price" {
		t.Errorf("%q", got)
	}
	long := CapabilityID("circle.a-very-long-provider-name-for-testing", "POST", "v1/some/long/operation/path/here")
	if len(long) > 64 || !strings.HasPrefix(long, "circle.") {
		t.Errorf("%q", long)
	}
	if Slug("EMC2 AI", 48) != "emc2-ai" || Slug("  !! ", 48) != "" || Slug("Venice.ai — Private", 48) != "venice-ai-private" {
		t.Error("slugs")
	}
}
