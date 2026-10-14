package app

import (
	"context"
	"errors"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// webCand is an endpoint its agent found on the open web and supplied itself:
// no catalog lists it and nothing here has executed it.
func webCand(provider string) routing.Candidate {
	c := cand(provider)
	c.Sources = []routing.DiscoverySource{routing.SourceWeb}
	return c
}

// By default a pass holds the first payment to an endpoint nobody lists for its
// person's yes, while a provider the operator configured (or a catalog lists)
// is paid as before.
func TestWebFoundRule_DefaultWaitsForThePerson(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.econRig.svc.SetProviderTrust(fakeTrust{})
	// Only the new-provider cap is out of the way: the web-found rule is the default one.
	envPass(t, rig, "agent_w", "", spendpass.Controls{NewProviders: spendpass.NewProvidersAllow})
	rig.runner.behaviors["found"] = rig.delivers(goodBody)
	rig.runner.behaviors["listed"] = rig.delivers(goodBody)

	// the dry run says it would wait
	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_w", Spec: riskSpec("w1", "cheapest"), Candidates: []routing.Candidate{webCand("found")}, LiveQuotes: true})
	if err != nil || sim.Decision != "HOLD" {
		t.Fatalf("a web find would wait for a yes: %v %+v", err, sim)
	}

	// a configured provider next to it is paid without asking, and the web find is not
	res, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_w", Spec: riskSpec("w2", "cheapest"), Candidates: []routing.Candidate{webCand("found"), cand("listed")}})
	if err != nil || !res.Report.Delivered || rig.runner.ran("listed") != 1 || rig.runner.ran("found") != 0 {
		t.Fatalf("the listed provider runs and the web find does not: %v %+v", err, res)
	}

	// only the web find: held, nothing paid
	spec := riskSpec("w3", "cheapest")
	var rej *ReservationRejected
	_, err = rig.exec.Do(ctx, DoRequest{AgentID: "agent_w", Spec: spec, Candidates: []routing.Candidate{webCand("found")}})
	if !errors.As(err, &rej) || rej.Reason != RejectApproval || len(rej.ReasonCodes) == 0 || rej.ReasonCodes[0] != spendpass.ReasonWebFoundNeedsApproval {
		t.Fatalf("held for the person (with the web-found reason): %v", err)
	}
	if rig.runner.ran("found") != 0 {
		t.Fatal("nothing was paid while it waited")
	}

	// the person says yes to this request: it runs
	v, _, _ := rig.econRig.svc.CreateIntent(ctx, "agent_w", spec)
	if _, err := rig.econRig.svc.Approve(ctx, "user_1", v.ID); err != nil {
		t.Fatal(err)
	}
	res, err = rig.exec.Do(ctx, DoRequest{AgentID: "agent_w", Spec: spec, Candidates: []routing.Candidate{webCand("found")}})
	if err != nil || !res.Report.Delivered || rig.runner.ran("found") != 1 {
		t.Fatalf("after the yes it is paid: %v %+v", err, res)
	}
}

func TestWebFoundRule_AllowPaysLikeAnyProvider(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.econRig.svc.SetProviderTrust(fakeTrust{})
	envPass(t, rig, "agent_a", "", spendpass.Controls{NewProviders: spendpass.NewProvidersAllow, WebFoundProviders: spendpass.WebFoundAllow})
	rig.runner.behaviors["found"] = rig.delivers(goodBody)
	res, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_a", Spec: riskSpec("a1", "cheapest"), Candidates: []routing.Candidate{webCand("found")}})
	if err != nil || !res.Report.Delivered || rig.runner.ran("found") != 1 {
		t.Fatalf("a pass that allows web finds pays one: %v %+v", err, res)
	}
}

func TestWebFoundRule_DenyNeverPays(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.econRig.svc.SetProviderTrust(fakeTrust{})
	envPass(t, rig, "agent_d", "", spendpass.Controls{NewProviders: spendpass.NewProvidersAllow, WebFoundProviders: spendpass.WebFoundDeny})
	rig.runner.behaviors["found"] = rig.delivers(goodBody)
	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_d", Spec: riskSpec("d1", ""), Candidates: []routing.Candidate{webCand("found")}})
	if err != nil || sim.Decision != "BLOCK" {
		t.Fatalf("the dry run says no: %v %+v", err, sim)
	}
	_, err = rig.exec.Do(ctx, DoRequest{AgentID: "agent_d", Spec: riskSpec("d1", ""), Candidates: []routing.Candidate{webCand("found")}})
	var nr *NoRoute
	if !errors.As(err, &nr) || rig.runner.ran("found") != 0 {
		t.Fatalf("refused with nothing paid: %v", err)
	}
}

// An endpoint that has been executed here often enough to have a record is no
// longer "found only on the web", so the default does not hold it.
func TestWebFoundRule_AnEndpointWithARecordIsNotHeld(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.econRig.svc.SetProviderTrust(fakeTrust{})
	envPass(t, rig, "agent_o", "", spendpass.Controls{NewProviders: spendpass.NewProvidersAllow})
	rig.runner.behaviors["found"] = rig.delivers(goodBody)
	seedHistory(t, rig, "found", routing.ObservedMinCalls+1, 100)
	res, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_o", Spec: riskSpec("o1", "cheapest"), Candidates: []routing.Candidate{webCand("found")}})
	if err != nil || !res.Report.Delivered || rig.runner.ran("found") != 1 {
		t.Fatalf("a web find with a record is paid: %v %+v", err, res)
	}
}
