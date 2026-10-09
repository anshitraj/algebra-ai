package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ResultsConfig is how the answer to a paid call is kept, so that asking again
// for the same outcome returns it instead of "already committed" and nothing
// to show (see app.ResultVault). The answer is sealed at rest, and kept for a
// limited time.
type ResultsConfig struct {
	// Retention is how long an answer is kept: RESULT_RETENTION, a duration such
	// as "6h" or "48h". 24 hours by default; "off" (or "0") keeps nothing, and
	// a repeat request is then refused as already committed. At most 7 days.
	Retention time.Duration
	// MaxBytes is the largest answer kept (RESULT_MAX_BYTES), 1 MiB by default.
	// A larger answer is not kept, never truncated.
	MaxBytes int
}

const (
	defaultResultRetention = 24 * time.Hour
	maxResultRetention     = 7 * 24 * time.Hour
	defaultResultMaxBytes  = 1 << 20
	minResultMaxBytes      = 1 << 10
	maxResultMaxBytes      = 16 << 20
)

func loadResults(c *ResultsConfig) error {
	c.Retention, c.MaxBytes = defaultResultRetention, defaultResultMaxBytes
	if v := strings.TrimSpace(os.Getenv("RESULT_RETENTION")); v != "" {
		switch strings.ToLower(v) {
		case "off", "0", "none":
			c.Retention = 0
		default:
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 || d > maxResultRetention {
				return fmt.Errorf("config: RESULT_RETENTION must be a duration up to 168h such as \"24h\", or \"off\"")
			}
			c.Retention = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("RESULT_MAX_BYTES")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < minResultMaxBytes || n > maxResultMaxBytes {
			return fmt.Errorf("config: RESULT_MAX_BYTES must be a number of bytes between %d and %d", minResultMaxBytes, maxResultMaxBytes)
		}
		c.MaxBytes = n
	}
	return nil
}
