package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/chain"
)

// JupiterConfig is buying tokens with USDC through Jupiter (providers/jupiter).
// It moves value on Solana mainnet, so it is off unless turned on, and it uses
// the mainnet wallet the payment rail already has (SOLANA_MAINNET_KEYPAIR_FILE
// with SOLANA_ALLOW_MAINNET=yes). Jupiter has no devnet.
type JupiterConfig struct {
	// Enabled: JUPITER_SWAP_ENABLED=on.
	Enabled bool
	// APIKey is Jupiter's key (JUPITER_API_KEY, from portal.jup.ag); without one
	// the keyless tier is used, at a lower rate.
	APIKey string
	// BaseURL defaults to Jupiter's Swap API v2 (JUPITER_BASE_URL); https only.
	BaseURL string
	// MaxSwapMinor is the most one swap may spend, in micro-USDC
	// (JUPITER_MAX_SWAP_USDC, such as "5.00"): a ceiling in the rail itself,
	// whatever any policy says. Zero means the default, 5 USDC.
	MaxSwapMinor int64
	// MaxSlippageBps is the most slippage an agent may ask for
	// (JUPITER_MAX_SLIPPAGE_BPS). Zero means the default, 100 (1%).
	MaxSlippageBps int
}

func loadJupiter(c *JupiterConfig) error {
	c.Enabled = strings.EqualFold(strings.TrimSpace(os.Getenv("JUPITER_SWAP_ENABLED")), "on")
	c.APIKey = strings.TrimSpace(os.Getenv("JUPITER_API_KEY"))
	if c.BaseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("JUPITER_BASE_URL")), "/"); c.BaseURL != "" {
		u, err := url.Parse(c.BaseURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return fmt.Errorf("config: JUPITER_BASE_URL must be an https URL without credentials")
		}
	}
	if v := strings.TrimSpace(os.Getenv("JUPITER_MAX_SWAP_USDC")); v != "" {
		n, err := chain.ParseUnits(v, chain.USDCDecimals)
		if err != nil || n <= 0 {
			return fmt.Errorf("config: JUPITER_MAX_SWAP_USDC must be a positive amount of USDC like 5.00")
		}
		c.MaxSwapMinor = n
	}
	if v := strings.TrimSpace(os.Getenv("JUPITER_MAX_SLIPPAGE_BPS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 300 {
			return fmt.Errorf("config: JUPITER_MAX_SLIPPAGE_BPS must be between 1 and 300 (basis points: 100 is 1%%)")
		}
		c.MaxSlippageBps = n
	}
	return nil
}
