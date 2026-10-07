package wiring

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/project-algebra/algebra/connectors/amazon"
	"github.com/project-algebra/algebra/connectors/blinkit"
	"github.com/project-algebra/algebra/connectors/flipkart"
	"github.com/project-algebra/algebra/connectors/genericbrowser"
	"github.com/project-algebra/algebra/connectors/mock"
	"github.com/project-algebra/algebra/connectors/remotemcp"
	"github.com/project-algebra/algebra/connectors/swiggyinstamart"
	"github.com/project-algebra/algebra/connectors/zepto"
	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/platform/config"
)

const warmTimeout = 20 * time.Second

// buildConnectors registers every merchant connector cfg enables. A
// connector that still needs setup (API credentials, a linked account) is
// registered anyway: it reports all-false capabilities and a status saying
// exactly what is missing, so "which merchants can I use?" always gets an
// honest answer instead of a silently shorter list.
func buildConnectors(cfg config.MerchantsConfig, enc remotemcp.Encryptor) (*app.ConnectorRegistry, error) {
	cfg = cfg.WithDefaults()
	sessions := remotemcp.NewFileSessionStore(cfg.SessionDir, enc)
	mcpClient := func(name, endpoint string) (*remotemcp.Client, error) {
		return remotemcp.NewClient(remotemcp.Config{Merchant: name, Endpoint: endpoint, Store: sessions})
	}

	factories := map[string]func() (merchant.Connector, error){
		"mock": func() (merchant.Connector, error) { return mock.New(), nil },
		zepto.Name: func() (merchant.Connector, error) {
			client, err := mcpClient(zepto.Name, cfg.ZeptoMCPEndpoint)
			if err != nil {
				return nil, err
			}
			return zepto.New(client), nil
		},
		swiggyinstamart.Name: func() (merchant.Connector, error) {
			client, err := mcpClient(swiggyinstamart.Name, cfg.SwiggyInstamartMCPEndpoint)
			if err != nil {
				return nil, err
			}
			return swiggyinstamart.New(client), nil
		},
		amazon.Name: func() (merchant.Connector, error) {
			return amazon.New(amazon.Config{
				CredentialID:      cfg.AmazonCredentialID,
				CredentialSecret:  cfg.AmazonCredentialSecret,
				CredentialVersion: cfg.AmazonCredentialVersion,
				PartnerTag:        cfg.AmazonPartnerTag,
				Marketplace:       cfg.AmazonMarketplace,
			}), nil
		},
		flipkart.Name: func() (merchant.Connector, error) {
			return flipkart.New(flipkart.Config{AffiliateID: cfg.FlipkartAffiliateID, AffiliateToken: cfg.FlipkartAffiliateToken}), nil
		},
		"blinkit":         func() (merchant.Connector, error) { return blinkit.New(), nil },
		"generic-browser": func() (merchant.Connector, error) { return genericbrowser.New(), nil },
	}

	registry := app.NewConnectorRegistry()
	for _, name := range cfg.Enabled {
		if name == config.NoMerchants {
			continue // an API-only deployment: no shopping connector at all
		}
		build, ok := factories[name]
		if !ok {
			known := make([]string, 0, len(factories))
			for k := range factories {
				known = append(known, k)
			}
			sort.Strings(known)
			return nil, fmt.Errorf("wiring: ENABLED_MERCHANTS names unknown merchant %q (known: %s)", name, strings.Join(known, ", "))
		}
		c, err := build()
		if err != nil {
			return nil, fmt.Errorf("wiring: building %s connector: %w", name, err)
		}
		registry.Register(c)
	}
	return registry, nil
}

// warmConnectors runs each connector's readiness check (linked session,
// live tool contract) in the background, so a slow or unreachable merchant
// never delays startup. Until a check finishes, that connector simply
// reports no capabilities.
func warmConnectors(registry *app.ConnectorRegistry) {
	for _, c := range registry.List() {
		w, ok := c.(merchant.Warmer)
		if !ok {
			continue
		}
		name := c.Name()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), warmTimeout)
			defer cancel()
			if err := w.Warm(ctx); err != nil {
				log.Printf("wiring: merchant %s is not ready: %v", name, err)
			}
		}()
	}
}
