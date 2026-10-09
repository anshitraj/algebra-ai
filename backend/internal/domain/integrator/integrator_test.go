package integrator

import (
	"testing"
	"time"
)

func TestIsRevoked(t *testing.T) {
	i := &Integrator{ID: "integrator_1", Name: "Kite"}
	if i.IsRevoked() {
		t.Error("expected a fresh integrator to not be revoked")
	}
	now := time.Now()
	i.RevokedAt = &now
	if !i.IsRevoked() {
		t.Error("expected IsRevoked to be true once RevokedAt is set")
	}
}
