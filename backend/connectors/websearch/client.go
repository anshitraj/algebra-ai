// Package websearch is a general web-search fallback for when no connected
// merchant connector can find a product — backed by Google's official
// Programmable Search Engine / Custom Search JSON API
// (https://developers.google.com/custom-search/v1/overview):
//
//	GET https://www.googleapis.com/customsearch/v1?key=API_KEY&cx=CX&q=QUERY&num=N
//
// This is not a merchant.Connector and never will be: results are arbitrary
// public web pages with a title/snippet/link, not a priced, buyable item
// from one storefront Algebra has an integration with. It has no cart, no
// checkout, no order — the same non-custodial shape as a merchant's
// HandoffURL, just not scoped to one merchant's own search page. Algebra
// never fetches a result URL itself; it only ever hands the link to an
// agent/user (still filtered through
// internal/domain/merchant.ValidatePublicHTTPSURL for SSRF safety, since a
// web-search result can point at any public domain, not one an operator
// allowlisted).
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/connectors/sanitize"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	wsdomain "github.com/project-algebra/algebra/internal/domain/websearch"
)

const (
	Name           = "web_search"
	DefaultAPIBase = "https://www.googleapis.com/customsearch/v1"
	DocsURL        = "https://developers.google.com/custom-search/v1/overview"

	maxResults   = 10 // Custom Search JSON API's own per-request cap
	maxBodyBytes = 4 << 20
)

// Config holds a Google Custom Search JSON API credential: an API key from
// Google Cloud Console and the ID ("cx") of a Programmable Search Engine
// (https://programmablesearchengine.google.com/) configured to search the
// whole web.
type Config struct {
	APIKey         string
	SearchEngineID string
	// APIBase overrides the published default; must be https (loopback http
	// is accepted for tests).
	APIBase    string
	HTTPClient *http.Client
}

type Client struct {
	cfg       Config
	http      *http.Client
	configErr string
}

func New(cfg Config) *Client {
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}
	cfg.APIBase = strings.TrimSuffix(cfg.APIBase, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	client := *hc
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	c := &Client{cfg: cfg, http: &client}
	switch {
	case cfg.APIKey == "" || cfg.SearchEngineID == "":
		c.configErr = "Set GOOGLE_SEARCH_API_KEY and GOOGLE_SEARCH_ENGINE_ID (a Google Cloud API key and a Programmable Search Engine ID configured to search the whole web) to enable the general web-search fallback."
	case !safeBaseURL(cfg.APIBase):
		c.configErr = "Google Custom Search API base URL must be https."
	}
	return c
}

func safeBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))
}

// Configured reports whether an API key and search engine ID are set.
func (c *Client) Configured() bool { return c.configErr == "" }

// ConfigError explains what's missing when Configured is false.
func (c *Client) ConfigError() string { return c.configErr }

type searchResponse struct {
	Items []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
	} `json:"items"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Search runs a Custom Search JSON API query and returns up to limit
// results. A result whose link fails
// internal/domain/merchant.ValidatePublicHTTPSURL (not https, or resolves
// to a loopback/private/link-local address) is dropped rather than passed
// to an agent.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]wsdomain.Result, error) {
	if !c.Configured() {
		return nil, errors.New("websearch: " + c.configErr)
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("websearch: empty search query")
	}
	if limit <= 0 || limit > maxResults {
		limit = maxResults
	}
	endpoint := c.cfg.APIBase + "?" + url.Values{
		"key": {c.cfg.APIKey},
		"cx":  {c.cfg.SearchEngineID},
		"q":   {q},
		"num": {strconv.Itoa(limit)},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("websearch: building search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("websearch: search request failed: %w", err)
	}
	defer resp.Body.Close()
	var body searchResponse
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body)

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("websearch: Custom Search API rejected the credentials (HTTP %d)%s", resp.StatusCode, apiErrorSuffix(body))
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("websearch: Custom Search API rate limit reached")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("websearch: Custom Search API returned HTTP %d%s", resp.StatusCode, apiErrorSuffix(body))
	case decodeErr != nil:
		return nil, fmt.Errorf("websearch: decoding search response: %w", decodeErr)
	}

	out := make([]wsdomain.Result, 0, len(body.Items))
	for _, it := range body.Items {
		if len(out) >= limit {
			break
		}
		if it.Title == "" || it.Link == "" {
			continue
		}
		if _, err := merchant.ValidatePublicHTTPSURL(it.Link); err != nil {
			continue // drop, don't hand an agent an unsafe link
		}
		out = append(out, wsdomain.Result{
			Title:   sanitize.Text(it.Title, 200),
			Snippet: sanitize.Text(it.Snippet, 400),
			URL:     it.Link,
		})
	}
	return out, nil
}

func apiErrorSuffix(body searchResponse) string {
	if body.Error == nil || body.Error.Message == "" {
		return ""
	}
	return ": " + sanitize.Text(body.Error.Message, 200)
}
