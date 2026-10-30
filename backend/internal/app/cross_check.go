package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Cross-checking what was paid for: several providers answered the same
// question, and their kept answers are compared (routing.CompareAnswers). It
// moves no money and asks nobody anything: it reads answers the person already
// paid for, from the result vault, and says whether they agree.

// MaxCrossCheck is how many providers' answers are compared at once.
const MaxCrossCheck = 5

// CheckedAnswer is one answer in a cross-check.
type CheckedAnswer struct {
	IntentID  string `json:"intent_id"`
	Provider  string `json:"provider"`
	PaidMinor int64  `json:"paid_minor"`
}

// CrossCheckReport is what the comparison found, and whose answers it was about.
type CrossCheckReport struct {
	Capability string          `json:"capability"`
	Answers    []CheckedAnswer `json:"answers"`
	routing.CrossCheck
	// ValuesAreUntrusted: the values in the differences are the providers' own words.
	ValuesAreUntrusted bool `json:"values_are_untrusted_provider_data"`
}

// CrossCheck compares the kept answers of intents the person paid for, each from
// a different provider, to the same question (the same work and the same input).
// It refuses what can't be a check: fewer than two or more than MaxCrossCheck
// intents, an intent that wasn't paid for or whose answer isn't kept any more,
// two answers to different questions, two from one provider. An intent that
// isn't theirs is not found.
func (s *ExecutionService) CrossCheck(ctx context.Context, principalID string, intentIDs []string) (*CrossCheckReport, error) {
	if !s.results.Enabled() {
		return nil, fmt.Errorf("%w: this server keeps no answers, so there is nothing to compare", ErrInvalidRequest)
	}
	var ids []string
	seen := map[string]bool{}
	for _, id := range intentIDs {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) < 2 || len(ids) > MaxCrossCheck {
		return nil, fmt.Errorf("%w: a cross-check compares between 2 and %d different intents, each answered by a different provider", ErrInvalidRequest, MaxCrossCheck)
	}

	rep := &CrossCheckReport{ValuesAreUntrusted: true}
	var (
		inputHash string
		inputKeys []string
		answers   []routing.Answer
	)
	byProvider := map[string]string{}
	for _, id := range ids {
		view, err := s.econ.ViewFor(ctx, principalID, id, false)
		if err != nil {
			return nil, err
		}
		var rsv *econ.Reservation
		for i := range view.Reservations {
			if view.Reservations[i].State == econ.ReservationCommitted {
				rsv = &view.Reservations[i]
			}
		}
		if view.State != econ.StateCommitted || rsv == nil {
			return nil, fmt.Errorf("%w: %s was not paid for, so it has no answer to compare", ErrInvalidRequest, id)
		}
		if rep.Capability == "" {
			rep.Capability, inputHash, inputKeys = view.Capability, view.InputHash, topLevelKeys(view.Input)
		} else if view.Capability != rep.Capability || view.InputHash != inputHash {
			return nil, fmt.Errorf("%w: %s asked a different question (another work or another input) from %s, so the two answers can't be checked against each other", ErrInvalidRequest, id, ids[0])
		}
		if other, dup := byProvider[rsv.ProviderID]; dup {
			return nil, fmt.Errorf("%w: %s and %s were both answered by %s: a check needs different providers", ErrInvalidRequest, other, id, rsv.ProviderID)
		}
		byProvider[rsv.ProviderID] = id
		kept, err := s.Result(ctx, principalID, id)
		if err != nil {
			if errors.Is(err, shared.ErrNotFound) {
				return nil, fmt.Errorf("%w: the answer to %s is not kept any more (it expired, or the request asked for it not to be kept)", ErrInvalidRequest, id)
			}
			return nil, err
		}
		answers = append(answers, routing.Answer{Provider: rsv.ProviderID, Body: kept.Body})
		rep.Answers = append(rep.Answers, CheckedAnswer{IntentID: id, Provider: rsv.ProviderID, PaidMinor: view.CommittedMinor})
	}

	rep.CrossCheck = routing.CompareAnswers(rep.Capability, inputKeys, answers)
	// On each intent's own record, with whom it was checked against.
	for _, a := range rep.Answers {
		s.event(ctx, a.IntentID, "result.crosschecked", map[string]any{
			"verdict": string(rep.Verdict), "providers": rep.Providers, "compared": rep.Compared,
			"differences": len(rep.Differences) + rep.MoreDifferences, "intents": ids,
		})
	}
	return rep, nil
}

// topLevelKeys are the parameter names of a request's input.
func topLevelKeys(raw json.RawMessage) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
