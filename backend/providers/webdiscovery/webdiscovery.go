// Package webdiscovery finds paid x402 endpoints on the open web, for work no
// catalog lists, by asking a model that can search the web.
//
// It is deliberately thin. What a model reads and writes is untrusted: it can
// invent a URL (observed with Gemini on product links) and it can be steered by
// the page it read. So this package takes from the model one thing, the address
// of an endpoint, and shape-checks it (public https, no credentials, bounded).
// It does not decide that the address is real or that its owner is honest:
// the caller asks the address what it charges with a free unpaid request
// (app.ExecutionService.DiscoverWeb) and only an endpoint that answers with
// x402 terms is offered, and then only as an unverified web find that the
// person's Spend Pass has the last word on.
package webdiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/providers/catalog"
)

// Grounder runs one prompt through a model with web search and returns its text.
// connectors/websearch.Gemini is one.
type Grounder interface {
	GroundedText(ctx context.Context, prompt string) (string, error)
}

// Finder is the app.WebFinder backed by a Grounder.
type Finder struct{ g Grounder }

var _ app.WebFinder = (*Finder)(nil)

// New builds a Finder.
func New(g Grounder) *Finder { return &Finder{g: g} }

// Find asks the model for endpoints that do the work in want.
func (f *Finder) Find(ctx context.Context, want string, limit int) ([]app.WebFound, error) {
	if limit <= 0 || limit > app.MaxWebFinds {
		limit = app.MaxWebFinds
	}
	text, err := f.g.GroundedText(ctx, prompt(want, limit))
	if err != nil {
		return nil, err
	}
	return parse(text, limit), nil
}

func prompt(want string, limit int) string {
	return fmt.Sprintf(`Search the web for HTTP API endpoints that charge per call with the x402 payment protocol (they answer HTTP 402 Payment Required until paid, in USDC on Solana) and can do this: %s

List up to %d distinct endpoints as a JSON array and nothing else:
[{"name":"","url":"","method":"GET","description":""}]
name: the service's name. url: the exact endpoint URL that answers HTTP 402 when called without payment. method: GET or POST. description: one line on what it does.
Only include an endpoint whose exact URL appears on a page you found. Never guess or construct a URL. Prefer services whose documentation or listing says they accept x402 payments on Solana. Skip free APIs and anything that needs an account or an API key.`, want, limit)
}

var jsonArray = regexp.MustCompile(`(?s)\[.*\]`)

// groundingHosts serve the redirect links a grounded search hands the model.
// They are links to pages, never the endpoint itself.
var groundingHosts = []string{"vertexaisearch.cloud.google.com", "google.com", "www.google.com"}

type rawFind struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Method      string `json:"method"`
	Description string `json:"description"`
}

// parse reads the model's answer. Anything that isn't a plausible public https
// endpoint is dropped, one bad entry never sinks the rest, and the same
// endpoint twice is one.
func parse(text string, limit int) []app.WebFound {
	m := jsonArray.FindString(text)
	if m == "" {
		return nil
	}
	var raws []rawFind
	if json.Unmarshal([]byte(m), &raws) != nil {
		return nil
	}
	var out []app.WebFound
	seen := map[string]bool{}
	for _, r := range raws {
		raw := strings.TrimSpace(r.URL)
		u, err := url.Parse(raw)
		if err != nil || len(raw) > 2048 || !catalog.PublicHTTPS(u) || u.Fragment != "" {
			continue
		}
		if isGroundingHost(u.Hostname()) {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(r.Method))
		if method != "GET" && method != "POST" {
			method = "GET"
		}
		name := catalog.CleanText(r.Name, catalog.MaxTitle)
		if name == "" {
			name = strings.ToLower(u.Hostname())
		}
		key := method + " " + u.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, app.WebFound{
			Name: name, URL: u.String(), Method: method, Description: catalog.CleanText(r.Description, catalog.MaxDescription),
		})
		if len(out) == limit {
			break
		}
	}
	return out
}

func isGroundingHost(host string) bool {
	host = strings.ToLower(host)
	for _, h := range groundingHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}
