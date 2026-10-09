package app

import (
	"context"
	"fmt"

	"github.com/project-algebra/algebra/policy"
)

// UserRulesSource supplies a user's own policy.Rules (their guardrails),
// or nil when they've set none. AccountService implements it.
type UserRulesSource interface {
	RulesFor(ctx context.Context, userID string) (*policy.Rules, error)
}

// providerFor returns the policy provider for userID: a fresh
// policy.LocalProvider over their own rules when they have any, otherwise
// the platform default. Built per evaluation, matching the public policy
// package's stateless-per-request construction — no cache that could serve
// a stale limit after the user tightens it.
func providerFor(ctx context.Context, src UserRulesSource, fallback policy.Provider, userID string) (policy.Provider, error) {
	if src == nil || userID == "" {
		return fallback, nil
	}
	rules, err := src.RulesFor(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("app: loading user guardrails: %w", err)
	}
	if rules == nil {
		return fallback, nil
	}
	return policy.NewLocalProvider(*rules), nil
}
