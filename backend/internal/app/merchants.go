package app

import (
	"sort"

	"github.com/project-algebra/algebra/internal/domain/merchant"
)

// MerchantInfo describes one merchant option for the console
// (GET /api/v1/merchants) and for agents (commerce.list_merchants). All of
// it comes from the connector itself — capabilities are computed, status is
// self-reported — so neither transport can drift into implying support that
// doesn't exist.
type MerchantInfo struct {
	Name         string                `json:"name"`
	Mode         string                `json:"mode"`
	Capabilities merchant.Capabilities `json:"capabilities"`
	Status       *merchant.Status      `json:"status,omitempty"`
}

// DescribeMerchants lists every registered connector, sorted by name.
func DescribeMerchants(registry *ConnectorRegistry) []MerchantInfo {
	connectors := registry.List()
	out := make([]MerchantInfo, 0, len(connectors))
	for _, c := range connectors {
		info := MerchantInfo{Name: c.Name(), Mode: string(c.Mode()), Capabilities: c.Capabilities()}
		if sr, ok := c.(merchant.StatusReporter); ok {
			st := sr.Status()
			info.Status = &st
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
