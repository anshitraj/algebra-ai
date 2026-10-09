package paymentintent

import (
	"errors"
	"testing"
	"time"
)

func TestCanTransition_HappyPath_AutoAllow(t *testing.T) {
	path := []State{
		StateDraft, StatePolicyEvaluating, StateAuthorized,
		StateCredentialPreparing, StateReadyToExecute, StateProcessing, StateSucceeded,
	}
	for i := 0; i < len(path)-1; i++ {
		from, to := path[i], path[i+1]
		if !CanTransition(from, to) {
			t.Errorf("expected %s -> %s to be legal", from, to)
		}
	}
}

func TestCanTransition_ApprovalRequiredPath(t *testing.T) {
	if !CanTransition(StatePolicyEvaluating, StateApprovalRequired) {
		t.Error("policy REQUIRE_APPROVAL must move POLICY_EVALUATING -> APPROVAL_REQUIRED")
	}
	if !CanTransition(StateApprovalRequired, StateAuthorized) {
		t.Error("a granted approval must move APPROVAL_REQUIRED -> AUTHORIZED")
	}
}

func TestCanTransition_Denied(t *testing.T) {
	if !CanTransition(StatePolicyEvaluating, StateDenied) {
		t.Error("policy DENY must move POLICY_EVALUATING -> DENIED")
	}
	if len(transitions[StateDenied]) != 0 {
		t.Error("DENIED must be terminal — nothing can override a policy denial")
	}
}

func TestCanTransition_RejectsIllegalJumps(t *testing.T) {
	cases := []struct{ from, to State }{
		{StateDraft, StateSucceeded},
		{StateDraft, StateProcessing},
		{StateApprovalRequired, StateProcessing},
		{StateSucceeded, StateDraft},
		{StateDenied, StateAuthorized},
		{StatePolicyEvaluating, StateProcessing},
	}
	for _, c := range cases {
		if CanTransition(c.from, c.to) {
			t.Errorf("expected %s -> %s to be illegal", c.from, c.to)
		}
	}
}

func TestTransition_ReturnsTypedErrorOnIllegalJump(t *testing.T) {
	_, err := Transition(StateDraft, StateSucceeded)
	if err == nil {
		t.Fatal("expected error")
	}
	var illegal *ErrIllegalTransition
	if !errors.As(err, &illegal) {
		t.Fatalf("expected *ErrIllegalTransition, got %T: %v", err, err)
	}
	if illegal.From != StateDraft || illegal.To != StateSucceeded {
		t.Errorf("unexpected error fields: %+v", illegal)
	}
}

func TestTransition_UnknownState(t *testing.T) {
	if _, err := Transition(State("NOT_A_STATE"), StateDraft); err == nil {
		t.Fatal("expected error for unknown source state")
	}
	if _, err := Transition(StateDraft, State("NOT_A_STATE")); err == nil {
		t.Fatal("expected error for unknown target state")
	}
}

func TestTerminalStates(t *testing.T) {
	terminal := []State{StateDenied, StateSucceeded, StateFailed, StateExpired, StateRevoked, StateCancelled}
	for _, s := range terminal {
		if !s.Terminal() {
			t.Errorf("expected %s to be terminal", s)
		}
		if len(transitions[s]) != 0 {
			t.Errorf("terminal state %s must have no outbound transitions, got %v", s, transitions[s])
		}
	}
	if StateDraft.Terminal() {
		t.Error("DRAFT must not be terminal")
	}
}

func TestNoStateIsAnIsland(t *testing.T) {
	reachable := map[State]bool{StateDraft: true}
	queue := []State{StateDraft}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, next := range transitions[s] {
			if !reachable[next] {
				reachable[next] = true
				queue = append(queue, next)
			}
		}
	}
	for s := range transitions {
		if !reachable[s] {
			t.Errorf("state %s is not reachable from DRAFT", s)
		}
	}
}

func TestApplyTransition_UpdatesStatusAndTimestamp(t *testing.T) {
	now := time.Now()
	p := New("pay_1", "tenant_1", "user_1", "agent_1", "Acme Co", 5000, "USD", "payment:personal", now)

	later := now.Add(time.Minute)
	prev, err := p.ApplyTransition(StatePolicyEvaluating, later)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prev != StateDraft {
		t.Errorf("expected previous state DRAFT, got %s", prev)
	}
	if p.Status != StatePolicyEvaluating {
		t.Errorf("expected status POLICY_EVALUATING, got %s", p.Status)
	}
	if !p.UpdatedAt.Equal(later) {
		t.Errorf("expected UpdatedAt to be bumped to %v, got %v", later, p.UpdatedAt)
	}

	if _, err := p.ApplyTransition(StateSucceeded, later); err == nil {
		t.Fatal("expected illegal transition error, payment intent state must be unchanged")
	}
	if p.Status != StatePolicyEvaluating {
		t.Errorf("status must not change on failed transition, got %s", p.Status)
	}
}
