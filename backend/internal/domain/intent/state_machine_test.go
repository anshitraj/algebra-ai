package intent

import (
	"errors"
	"testing"
	"time"
)

func TestCanTransition_HappyPath(t *testing.T) {
	path := []State{
		StateDraft, StateDiscovering, StateQuoted, StatePolicyCheck,
		StateApprovalRequired, StateApproved, StateExecuting, StateSucceeded,
	}
	for i := 0; i < len(path)-1; i++ {
		from, to := path[i], path[i+1]
		if !CanTransition(from, to) {
			t.Errorf("expected %s -> %s to be legal", from, to)
		}
	}
}

func TestCanTransition_PolicyAllowSkipsApproval(t *testing.T) {
	if !CanTransition(StatePolicyCheck, StateApproved) {
		t.Error("policy ALLOW must be able to move POLICY_CHECK -> APPROVED directly")
	}
}

func TestCanTransition_RejectsIllegalJumps(t *testing.T) {
	cases := []struct{ from, to State }{
		{StateDraft, StateSucceeded},
		{StateDraft, StateExecuting},
		{StateApprovalRequired, StateExecuting},
		{StateSucceeded, StateDraft},
		{StatePolicyRejected, StateApproved},
		{StateQuoted, StateExecuting},
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
	terminal := []State{StatePolicyRejected, StateSucceeded, StateFailed, StateCancelled, StateExpired, StatePartiallyCompleted}
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
	// Every non-terminal state must have at least one outbound edge, and
	// every state must be reachable from DRAFT in the adjacency map (a
	// dangling state is either a typo or a forgotten transition).
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
	pi := New("pi_1", "user_1", "agent_1", nil, Constraints{MaxTotalMinorUnits: 40000, Currency: "INR"}, now)

	later := now.Add(time.Minute)
	prev, err := pi.ApplyTransition(StateDiscovering, later)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prev != StateDraft {
		t.Errorf("expected previous state DRAFT, got %s", prev)
	}
	if pi.Status != StateDiscovering {
		t.Errorf("expected status DISCOVERING, got %s", pi.Status)
	}
	if !pi.UpdatedAt.Equal(later) {
		t.Errorf("expected UpdatedAt to be bumped to %v, got %v", later, pi.UpdatedAt)
	}

	if _, err := pi.ApplyTransition(StateSucceeded, later); err == nil {
		t.Fatal("expected illegal transition error, intent state must be unchanged")
	}
	if pi.Status != StateDiscovering {
		t.Errorf("status must not change on failed transition, got %s", pi.Status)
	}
}
