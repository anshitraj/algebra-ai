package paysh

import (
	"net/url"
	"slices"
	"strings"

	"github.com/project-algebra/algebra/providers/catalog"
)

// endpointRow is one row of the endpoint table on a provider's page, as
// published.
type endpointRow struct {
	method, path, pricing, description string
}

var httpMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// parseEndpointTable reads the "Endpoint Table" of a provider's markdown page:
//
//	| Method | Path | Pricing | Description |
//	| --- | --- | --- | --- |
//	| POST | v1/images:annotate | $0.0015/requests | Run image detection. |
//
// A row that isn't four cells is skipped, and so is the header. Whether a row's
// method and path are usable is decided later, in buildEndpoints.
func parseEndpointTable(md string) []endpointRow {
	_, after, found := strings.Cut(md, "## Endpoint Table")
	if !found {
		return nil
	}
	var rows []endpointRow
	started := false
	for _, line := range strings.Split(after, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			if started || strings.HasPrefix(line, "## ") {
				break
			}
			continue
		}
		started = true
		cells := splitRow(line)
		if len(cells) != 4 || strings.EqualFold(cells[0], "method") || strings.Trim(cells[0], "-: ") == "" {
			continue
		}
		rows = append(rows, endpointRow{
			method: strings.ToUpper(cells[0]), path: strings.TrimLeft(strings.Trim(cells[1], "` "), "/"),
			pricing: cells[2], description: cells[3],
		})
		if len(rows) >= maxEndpoints {
			break
		}
	}
	return rows
}

// splitRow splits a markdown table row on its unescaped pipes.
func splitRow(line string) []string {
	line = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "|"), "|")
	parts := strings.Split(strings.ReplaceAll(line, `\|`, "\x00"), "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(strings.ReplaceAll(p, "\x00", "|"))
	}
	return parts
}

// buildEndpoints turns table rows into endpoints, dropping any whose method or
// path can't be trusted. An endpoint with {parameters} in its path is listed
// but not callable: the runner calls a concrete URL.
func buildEndpoints(p Provider, rows []endpointRow) []Endpoint {
	out := make([]Endpoint, 0, len(rows))
	for _, r := range rows {
		if !slices.Contains(httpMethods, r.method) || r.path == "" || len(r.path) > maxPathLen ||
			!pathRE.MatchString(r.path) || strings.Contains(r.path, "..") {
			continue
		}
		e := Endpoint{
			Capability: capabilityID(p.FQN, r.method, r.path), Method: r.method, Path: r.path,
			URL: p.ServiceURL + "/" + r.path, Pricing: cleanText(r.pricing, 60), Description: cleanText(r.description, maxDescription),
		}
		price := strings.ToLower(e.Pricing)
		switch {
		case price == "free":
			e.Free = true
		case strings.HasPrefix(price, "$"):
			lit, _, _ := strings.Cut(price[1:], "/")
			if v, ok := microUSDC(lit); ok {
				e.PriceMinor, e.Free = v, v == 0
			}
		}
		if strings.ContainsAny(r.path, "{}") {
			e.Reason = "the path has parameters, which Algebra can't fill in yet"
		} else if u, err := url.Parse(e.URL); err != nil || u.Host == "" {
			e.Reason = "the endpoint isn't a valid URL"
		} else {
			e.Callable = true
		}
		out = append(out, e)
	}
	return out
}

// capabilityID names an endpoint from its provider, method and path:
// "birdeye.data.get.x402-defi-price" (see catalog.CapabilityID).
func capabilityID(fqn, method, path string) string {
	return catalog.CapabilityID(strings.ReplaceAll(fqn, "/", "."), method, path)
}
