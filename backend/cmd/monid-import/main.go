// Command monid-import builds providers/monid/snapshot.json, Algebra's listing
// of Monid's tools, from Monid's open-source connector repository
// (github.com/monid-ai/monid), where every provider and endpoint Monid serves
// is declared.
//
//	git clone --depth 1 https://github.com/monid-ai/monid /tmp/monid
//	go run ./cmd/monid-import -repo /tmp/monid -out providers/monid/snapshot.json
//
// The connectors are TypeScript, read here by pattern, not executed: names,
// summaries, categories, the HTTP method and path each endpoint is run by,
// and its usage model. A price is kept only where the provider bills in US
// dollars; the others bill in vendor credits that Monid converts on its side,
// so their price is left unknown rather than guessed (Monid's live discover
// API states it, with a key).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type endpoint struct {
	Path        string   `json:"path"`
	Method      string   `json:"method"`
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Categories  []string `json:"categories,omitempty"`
	PriceKind   string   `json:"price_kind"`
	PriceUSD    float64  `json:"price_usd,omitempty"`
	PriceUnit   string   `json:"price_unit,omitempty"`
	ConnectorID string   `json:"connector_id"`
}

type provider struct {
	Slug       string     `json:"slug"`
	Name       string     `json:"name"`
	Summary    string     `json:"summary"`
	Homepage   string     `json:"homepage,omitempty"`
	Categories []string   `json:"categories,omitempty"`
	CreditUnit string     `json:"credit_unit,omitempty"`
	Endpoints  []endpoint `json:"endpoints"`
}

type snapshot struct {
	Source    string     `json:"source"`
	Generated time.Time  `json:"generated_at"`
	Providers []provider `json:"providers"`
}

var (
	nameRE     = regexp.MustCompile(`(?m)^\s*name:\s*"([a-z0-9_-]+)"`)
	displayRE  = regexp.MustCompile(`displayName:\s*"([^"]+)"`)
	summaryRE  = regexp.MustCompile(`summary:\s*\n?\s*"([^"]+)"`)
	homeRE     = regexp.MustCompile(`homepageUrl:\s*"([^"]+)"`)
	catsRE     = regexp.MustCompile(`categories:\s*\[([^\]]*)\]`)
	quotedRE   = regexp.MustCompile(`"([^"]+)"`)
	creditRE   = regexp.MustCompile(`credits:\s*\{[^}]*?label:\s*"([^"]+)"`)
	requestRE  = regexp.MustCompile(`request:\s*\{([^}]*)\}`)
	methodRE   = regexp.MustCompile(`method:\s*"([A-Z]+)"`)
	pathRE     = regexp.MustCompile(`path:\s*"([^"]+)"`)
	kindRE     = regexp.MustCompile(`kind:\s*UsageModelKind\.([A-Z_]+)`)
	amountRE   = regexp.MustCompile(`amount:\s*([0-9][0-9_.eE-]*)`)
	unitRE     = regexp.MustCompile(`unit:\s*Unit\.([A-Z_]+)`)
	partKindRE = regexp.MustCompile(`kind:\s*UsageModelKind\.(PER_CALL|PER_UNIT)`)
	// endpointRE is an endpoint's public path when it differs from the
	// vendor's request path ("/apidojo/tweet-scraper").
	endpointRE = regexp.MustCompile(`(?m)^\s*endpoint:\s*"([^"]+)"`)
)

// published reads connectors/ids.lock.json: the endpoint IDs Monid serves,
// "provider#path". Only those are listed.
func published(repo string) map[string]bool {
	b, err := os.ReadFile(filepath.Join(repo, "connectors", "ids.lock.json"))
	if err != nil {
		return nil
	}
	var lock struct {
		Endpoints []string `json:"endpoints"`
	}
	if json.Unmarshal(b, &lock) != nil {
		return nil
	}
	out := make(map[string]bool, len(lock.Endpoints))
	for _, id := range lock.Endpoints {
		out[id] = true
	}
	return out
}

func first(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func cats(s string) []string {
	m := catsRE.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	var out []string
	for _, q := range quotedRE.FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	return out
}

func main() {
	repo := flag.String("repo", "", "a clone of github.com/monid-ai/monid")
	out := flag.String("out", "providers/monid/snapshot.json", "where to write the snapshot")
	flag.Parse()
	if *repo == "" {
		log.Fatal("monid-import: -repo is required")
	}
	dirs, err := os.ReadDir(filepath.Join(*repo, "connectors"))
	if err != nil {
		log.Fatal(err)
	}
	snap := snapshot{Source: "github.com/monid-ai/monid", Generated: time.Now().UTC().Truncate(time.Second)}
	lock := published(*repo)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		root := filepath.Join(*repo, "connectors", d.Name())
		b, err := os.ReadFile(filepath.Join(root, "provider.ts"))
		if err != nil {
			continue
		}
		src := string(b)
		p := provider{
			Slug: first(nameRE, src), Name: first(displayRE, src), Summary: first(summaryRE, src),
			Homepage: first(homeRE, src), Categories: cats(src), CreditUnit: first(creditRE, src),
		}
		if p.Slug == "" {
			p.Slug = d.Name()
		}
		if p.Name == "" {
			p.Name = p.Slug
		}
		usd := p.CreditUnit == "" || strings.EqualFold(p.CreditUnit, "US dollars")
		_ = filepath.WalkDir(filepath.Join(root, "endpoints"), func(path string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() || e.Name() != "endpoint.ts" {
				return nil
			}
			eb, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			es := string(eb)
			rel, _ := filepath.Rel(filepath.Join(root, "endpoints"), filepath.Dir(path))
			ep := endpoint{
				Name: first(displayRE, es), Summary: first(summaryRE, es), Categories: cats(es),
			}
			if req := requestRE.FindStringSubmatch(es); req != nil {
				ep.Method, ep.Path = first(methodRE, req[1]), first(pathRE, req[1])
			}
			if pub := first(endpointRE, es); pub != "" {
				ep.Path = pub
			}
			if ep.Path == "" {
				ep.Path = "/" + filepath.ToSlash(rel)
			}
			ep.Path = "/" + strings.Trim(ep.Path, "/")
			ep.ConnectorID = p.Slug + "#" + strings.TrimPrefix(ep.Path, "/")
			if lock != nil && !lock[ep.ConnectorID] {
				return nil // not a published endpoint
			}
			if ep.Method == "" {
				ep.Method = "POST"
			}
			ep.PriceKind = first(kindRE, es)
			if ep.PriceKind == "FREE" {
				ep.PriceUSD = 0
			} else if usd {
				// The first billed component is the headline price: the flat
				// call fee of a composite, or the per-unit rate.
				if amt := first(amountRE, es); amt != "" {
					if v, err := strconv.ParseFloat(strings.ReplaceAll(amt, "_", ""), 64); err == nil {
						ep.PriceUSD = v
						if k := first(partKindRE, es); k == "PER_UNIT" {
							ep.PriceUnit = strings.ToLower(first(unitRE, es))
						}
					}
				}
			}
			if ep.Name == "" {
				ep.Name = ep.Path
			}
			p.Endpoints = append(p.Endpoints, ep)
			return nil
		})
		slices.SortFunc(p.Endpoints, func(a, b endpoint) int { return strings.Compare(a.ConnectorID, b.ConnectorID) })
		if len(p.Endpoints) > 0 {
			snap.Providers = append(snap.Providers, p)
		}
	}
	slices.SortFunc(snap.Providers, func(a, b provider) int { return strings.Compare(a.Slug, b.Slug) })
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	n, priced := 0, 0
	for _, p := range snap.Providers {
		n += len(p.Endpoints)
		for _, e := range p.Endpoints {
			if e.PriceUSD > 0 {
				priced++
			}
		}
	}
	fmt.Printf("monid-import: %d providers, %d endpoints (%d with a USD price) -> %s\n", len(snap.Providers), n, priced, *out)
}
