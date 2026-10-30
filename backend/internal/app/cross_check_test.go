package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// askOnly pays one named provider for the question doSpec asks, in its own window,
// and returns the intent. mutate changes the request first; discard asks for the
// answer not to be kept.
func (r *execRig) askOnly(t *testing.T, provider, window string, discard bool, mutate func(*econ.Spec)) string {
	t.Helper()
	spec := doSpec(window)
	spec.ProviderPolicy = econ.ProviderPolicy{Providers: []string{provider}}
	if mutate != nil {
		mutate(&spec)
	}
	got, err := r.exec.Do(context.Background(), DoRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{cand(provider)}, DiscardResult: discard, Fresh: true})
	if err != nil || got == nil || !got.Report.Delivered {
		t.Fatalf("paying %s: %+v %v", provider, got, err)
	}
	return got.Intent.ID
}

func TestCrossCheck_ComparesWhatSeveralProvidersAnsweredAndRecordsIt(t *testing.T) {
	rig, _ := keepingRig(t)
	ctx := context.Background()
	rig.runner.behaviors["alpha"] = rig.delivers(`{"mint":"SOL","risk_score":12,"provider":"alpha","sandbox":true}`)
	rig.runner.behaviors["beta"] = rig.delivers(`{"mint":"SOL","risk_score":12.1,"provider":"beta","sandbox":true}`)
	rig.runner.behaviors["gamma"] = rig.delivers(`{"mint":"SOL","risk_score":80,"provider":"gamma","sandbox":true}`)
	a, b, c := rig.askOnly(t, "alpha", "wa", false, nil), rig.askOnly(t, "beta", "wb", false, nil), rig.askOnly(t, "gamma", "wc", false, nil)

	got, err := rig.exec.CrossCheck(ctx, "user_1", []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	// What names the seller and what repeats the question is not the answer: one field is compared.
	if got.Verdict != routing.CheckAgree || got.Compared != 1 || got.Capability != "solana.token-risk" || !got.ValuesAreUntrusted {
		t.Fatalf("two providers a whisker apart agree on the one field that is an answer: %+v", got)
	}
	if len(got.Answers) != 2 || got.Answers[0] != (CheckedAnswer{IntentID: a, Provider: "alpha", PaidMinor: 3_000}) || got.Answers[1].Provider != "beta" {
		t.Errorf("who was compared, and what each cost: %+v", got.Answers)
	}

	three, err := rig.exec.CrossCheck(ctx, "user_1", []string{a, b, c})
	if err != nil || three.Verdict != routing.CheckDiffer || len(three.Outliers) != 1 || three.Outliers[0] != "gamma" {
		t.Fatalf("one of three is alone: %+v %v", three, err)
	}
	if d := three.Differences[0]; d.Path != "risk_score" || d.Values["gamma"] != "80" || d.Outlier != "gamma" {
		t.Errorf("the difference: %+v", d)
	}

	// It is on every intent's record, with whom it was checked against.
	v, _ := rig.econRig.svc.View(ctx, c, true)
	i := slices.IndexFunc(v.Events, func(e EconEvent) bool { return e.Event == "result.crosschecked" })
	if i < 0 || v.Events[i].Data["verdict"] != "differ" {
		t.Errorf("the check is on the intent's record: %+v", v.Events)
	}
	// And nothing was run or paid for it.
	if rig.runner.ran("alpha") != 1 || rig.runner.ran("beta") != 1 || rig.runner.ran("gamma") != 1 {
		t.Errorf("a check calls nobody: %d %d %d", rig.runner.ran("alpha"), rig.runner.ran("beta"), rig.runner.ran("gamma"))
	}
}

func TestCrossCheck_RefusesWhatCannotBeACheck(t *testing.T) {
	rig, _ := keepingRig(t)
	ctx := context.Background()
	for _, p := range []string{"alpha", "beta", "gamma"} {
		rig.runner.behaviors[p] = rig.delivers(goodBody)
	}
	a, b := rig.askOnly(t, "alpha", "wa", false, nil), rig.askOnly(t, "beta", "wb", false, nil)
	aAgain := rig.askOnly(t, "alpha", "wa2", false, nil) // alpha again, paid for anew
	other := rig.askOnly(t, "gamma", "wg", false, func(s *econ.Spec) { s.Input = []byte(`{"mint":"BONK"}`) })
	private := rig.askOnly(t, "gamma", "wp", true, nil) // the answer was not to be kept
	open := rig.intent(t, "agent_1", 50_000).ID         // never paid for

	cases := map[string]struct {
		ids  []string
		who  string
		want string
	}{
		"one intent":             {[]string{a}, "user_1", "between 2 and 5"},
		"the same one twice":     {[]string{a, a}, "user_1", "between 2 and 5"},
		"six":                    {[]string{a, b, aAgain, other, private, open}, "user_1", "between 2 and 5"},
		"one provider twice":     {[]string{a, aAgain}, "user_1", "different providers"},
		"two questions":          {[]string{a, other}, "user_1", "different question"},
		"an answer not kept":     {[]string{a, private}, "user_1", "not kept"},
		"an intent never paid":   {[]string{a, open}, "user_1", "was not paid for"},
		"somebody else's intent": {[]string{a, b}, "user_2", ""},
	}
	for name, tc := range cases {
		_, err := rig.exec.CrossCheck(ctx, tc.who, tc.ids)
		switch {
		case tc.who == "user_2":
			if !errors.Is(err, shared.ErrNotFound) {
				t.Errorf("%s: another person's intent is not found, whatever it holds: %v", name, err)
			}
		case !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), tc.want):
			t.Errorf("%s: want a refusal saying %q, got %v", name, tc.want, err)
		}
	}
}

func TestCrossCheck_NeedsAnswersToBeKeptAtAll(t *testing.T) {
	rig := newExecRig(t) // no result vault: nothing is kept
	if _, err := rig.exec.CrossCheck(context.Background(), "user_1", []string{"a", "b"}); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "keeps no answers") {
		t.Fatalf("%v", err)
	}
}

func TestCrossCheck_AnswersThatCannotBeComparedAreUnclearNotAgreement(t *testing.T) {
	rig, _ := keepingRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(`{"mint":"SOL","sandbox":true}`)
	rig.runner.behaviors["beta"] = rig.delivers(`{"mint":"SOL","note":"hello"}`)
	a, b := rig.askOnly(t, "alpha", "wa", false, nil), rig.askOnly(t, "beta", "wb", false, nil)
	got, err := rig.exec.CrossCheck(context.Background(), "user_1", []string{a, b})
	if err != nil || got.Verdict != routing.CheckUnclear || got.Compared != 0 {
		t.Fatalf("no field is an answer in both: %+v %v", got, err)
	}
}
