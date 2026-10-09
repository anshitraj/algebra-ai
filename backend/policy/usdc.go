package policy

import (
	"fmt"
	"strings"
)

// usdcAtOrAbove compares two USDC decimal strings without importing a
// bignum/decimal package for one comparison — both values come from trusted
// internal sources (Algebra's own resolved amount, Algebra's own configured
// Rules), not user-supplied free text.
func usdcAtOrAbove(amount, threshold string) (bool, error) {
	a, err := parseUSDCMicros(amount)
	if err != nil {
		return false, err
	}
	t, err := parseUSDCMicros(threshold)
	if err != nil {
		return false, err
	}
	return a >= t, nil
}

// usdcAbove is usdcAtOrAbove's strict counterpart, for hard caps (a max is a
// ceiling — exactly at the max is still allowed) as opposed to a
// confirmation threshold (at the threshold already requires approval).
func usdcAbove(amount, limit string) (bool, error) {
	a, err := parseUSDCMicros(amount)
	if err != nil {
		return false, err
	}
	l, err := parseUSDCMicros(limit)
	if err != nil {
		return false, err
	}
	return a > l, nil
}

// parseUSDCMicros parses a decimal USDC string (e.g. "10.50") into an
// integer count of micro-USDC (1e-6), giving exact comparisons without
// floating point.
func parseUSDCMicros(s string) (int64, error) {
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	for len(frac) < 6 {
		frac += "0"
	}
	if len(frac) > 6 {
		frac = frac[:6]
	}
	var wholeVal, fracVal int64
	if _, err := fmt.Sscanf(whole, "%d", &wholeVal); err != nil {
		return 0, fmt.Errorf("policy: invalid USDC amount %q: %w", s, err)
	}
	if frac != "" {
		if _, err := fmt.Sscanf(frac, "%d", &fracVal); err != nil {
			return 0, fmt.Errorf("policy: invalid USDC amount %q: %w", s, err)
		}
	}
	total := wholeVal*1_000_000 + fracVal
	if neg {
		total = -total
	}
	return total, nil
}
