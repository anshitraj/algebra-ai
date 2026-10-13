package app

import (
	"context"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

// stallRail is a rail whose usage-based payment is stuck: the provider took the
// channel and never settled it.
type stallRail struct {
	status    SettlementStatus
	calls     int
	notBefore time.Time
	ev        econ.Evidence
}

func (s *stallRail) Name() string { return "x402-solana-devnet" }
func (s *stallRail) Settlement(context.Context, econ.Evidence) (Settlement, error) {
	return Settlement{Status: s.status}, nil
}
func (s *stallRail) RecoverEscrow(_ context.Context, ev econ.Evidence, notBefore time.Time) (string, error) {
	s.calls++
	s.ev, s.notBefore = ev, notBefore
	return "requested the channel's close", nil
}

type oneReservation econ.Reservation

func (o oneReservation) Reservations(context.Context, string) ([]econ.Reservation, error) {
	return []econ.Reservation{econ.Reservation(o)}, nil
}

func recoveryRig(t *testing.T, status SettlementStatus, channel string, deadline time.Time) (*OnchainPassService, *OnchainNetwork, *stallRail) {
	t.Helper()
	rail := &stallRail{status: status}
	n := testNetwork(t, true)
	n.Rail = rail
	rv := oneReservation{ID: "r1", IntentID: "i1", State: econ.ReservationUnknown, ExecutionDeadline: &deadline,
		Evidence: econ.Evidence{AuthorityIssued: true, Channel: channel}}
	svc := NewOnchainPassService(newMemOnchainStore(), nil, nil, rv, []*OnchainNetwork{n})
	return svc, n, rail
}

var stuckPull = &OnchainPull{ReservationID: "r1", IntentID: "i1", AmountMinor: 50_000}

// A pull is refunded once its attempt's spend is final. A provider that never
// settles its channel would keep that from ever happening, so the payer takes
// the program's way out, starting a while after the attempt's deadline.
func TestAStuckChannelIsRecoveredAfterTheAttemptIsLongOver(t *testing.T) {
	deadline := time.Now().Add(-time.Hour)
	svc, n, rail := recoveryRig(t, SettlementPending, "FakeChannelAddress111", deadline)
	svc.RecoverAfter = 10 * time.Minute

	used, final, err := svc.used(context.Background(), n, stuckPull)
	if err != nil || final || used != 0 {
		t.Fatalf("a stuck channel is not final yet: %d %v %v", used, final, err)
	}
	if rail.calls != 1 || rail.ev.Channel != "FakeChannelAddress111" {
		t.Fatalf("the rail should be asked to recover the channel once: %+v", rail)
	}
	if !rail.notBefore.Equal(deadline.Add(10 * time.Minute)) {
		t.Fatalf("the close may start 10 minutes after the deadline, not %v", rail.notBefore)
	}
}

func TestNothingIsRecoveredWhenThePaymentIsFinalOrUsedNoChannel(t *testing.T) {
	deadline := time.Now().Add(-time.Hour)
	for name, c := range map[string]struct {
		status  SettlementStatus
		channel string
	}{
		"settled":     {SettlementSettled, "FakeChannelAddress111"},
		"not settled": {SettlementNotSettled, "FakeChannelAddress111"},
		"no channel":  {SettlementPending, ""},
	} {
		t.Run(name, func(t *testing.T) {
			svc, n, rail := recoveryRig(t, c.status, c.channel, deadline)
			_, _, _ = svc.used(context.Background(), n, stuckPull)
			if rail.calls != 0 {
				t.Fatalf("nothing to recover, yet the rail was asked %d times", rail.calls)
			}
		})
	}
}
